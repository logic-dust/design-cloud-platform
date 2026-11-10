package api

import (
	"context"
	"net/http"
	"time"

	"design-cloud-engine/internal/contract/schema"
	"design-cloud-engine/internal/model"
	"design-cloud-engine/internal/server/portal/httputil"
	"design-cloud-engine/internal/server/service/security"

	"github.com/labstack/echo/v5"
)

type banService interface {
	ListBannedRecords() map[string]any
	Unban(ctx context.Context, key string)
	ClearAllBans(ctx context.Context) int
	ManualBan(ctx context.Context, subject model.BanSubject, IP, username string, duration time.Duration, reason string) (security.BannedRecord, error)
}

// allowlistSettingsService 是封禁放行名单管理所需 settings 能力的窄接口。
type allowlistSettingsService interface {
	GetAuthSettings(ctx context.Context) (schema.AuthSettings, error)
	AddBanAllowlistEntries(ctx context.Context, entries []string) (schema.AuthSettings, error)
	RemoveBanAllowlistEntries(ctx context.Context, entries []string) (schema.AuthSettings, error)
}

type banHandler struct {
	security banService
	settings allowlistSettingsService
}

func newBanHandler(securitySvc banService, settingsSvc allowlistSettingsService) *banHandler {
	return &banHandler{security: securitySvc, settings: settingsSvc}
}

// route: /api/admin/security/bans
func (h *banHandler) adminBansAPI(g *echo.Group) {
	g.GET("", h.adminListBannedList)
	g.POST("", h.adminManualBan)
	g.DELETE("/:key", h.adminUnbanRecord)
	g.DELETE("", h.adminClearAllBans)
	g.GET("/allowlist", h.adminGetBanAllowlist)
	g.POST("/allowlist", h.adminAddBanAllowlist)
	g.DELETE("/allowlist", h.adminRemoveBanAllowlist)
}

// --- 获取所有封禁记录（管理员）
func (h *banHandler) adminListBannedList(c *echo.Context) error {
	return httputil.RespondData(c, h.security.ListBannedRecords())
}

// adminManualBanRequest 手动封禁请求体。Duration 单位为秒。
type adminManualBanRequest struct {
	Subject  string `json:"subject"` // IP | UserIP
	IP       string `json:"ip"`
	Username string `json:"username,omitempty"` // subject=UserIP 时必填
	Duration int    `json:"duration"`           // 封禁时长（秒），60 ~ 2592000
	Reason   string `json:"reason,omitempty"`
}

// --- 手动封禁（管理员）
// POST /api/admin/security/bans
func (h *banHandler) adminManualBan(c *echo.Context) error {
	var req adminManualBanRequest
	if err := c.Bind(&req); err != nil {
		return c.JSON(http.StatusBadRequest, httputil.Response{Message: err.Error()})
	}
	record, err := h.security.ManualBan(c.Request().Context(), model.BanSubject(req.Subject), req.IP, req.Username, time.Duration(req.Duration)*time.Second, req.Reason)
	if err != nil {
		return c.JSON(http.StatusBadRequest, httputil.Response{Message: err.Error()})
	}
	return httputil.RespondData(c, record)
}

// --- 解除指定封禁（管理员）
func (h *banHandler) adminUnbanRecord(c *echo.Context) error {
	h.security.Unban(c.Request().Context(), c.Param("key"))
	return c.NoContent(http.StatusNoContent)
}

// --- 清除所有封禁（管理员）
// DELETE /api/admin/security/bans
func (h *banHandler) adminClearAllBans(c *echo.Context) error {
	count := h.security.ClearAllBans(c.Request().Context())
	return httputil.RespondData(c, map[string]any{"count": count})
}

// --- 封禁放行名单（管理员）：AuthSettings.BanAllowlist 的独立增删查入口，
// 免去整域 settings PUT。放行名单条目合法性与持久化由 settings 服务域写路径统一处理。
// banAllowlistResponse 放行名单当前条目。BanAllowlist 为 nil 时规范化为空数组，
// 避免 JSON 序列化出 null。
type banAllowlistResponse struct {
	Entries []string `json:"entries"`
}

func newBanAllowlistResponse(entries []string) banAllowlistResponse {
	if entries == nil {
		entries = []string{}
	}
	return banAllowlistResponse{Entries: entries}
}

// GET /api/admin/security/bans/allowlist
func (h *banHandler) adminGetBanAllowlist(c *echo.Context) error {
	settings, err := h.settings.GetAuthSettings(c.Request().Context())
	if err != nil {
		return httputil.RespondError(c, err)
	}
	return httputil.RespondData(c, newBanAllowlistResponse(settings.BanAllowlist))
}

// banAllowlistAddRequest 追加放行名单条目请求体。
type banAllowlistAddRequest struct {
	Entries []string `json:"entries"` // 单个 IP 或 CIDR 前缀
}

// POST /api/admin/security/bans/allowlist — 追加条目（已存在则幂等跳过）。
// 非法条目由 settings 域写校验拒绝：contract.NewValidationError → 422，整次变更不生效。
func (h *banHandler) adminAddBanAllowlist(c *echo.Context) error {
	var req banAllowlistAddRequest
	if err := c.Bind(&req); err != nil {
		return c.JSON(http.StatusBadRequest, httputil.Response{Message: err.Error()})
	}
	if len(req.Entries) == 0 {
		return c.JSON(http.StatusBadRequest, httputil.Response{Message: "entries is required"})
	}
	updated, err := h.settings.AddBanAllowlistEntries(c.Request().Context(), req.Entries)
	if err != nil {
		return httputil.RespondError(c, err)
	}
	return httputil.RespondData(c, newBanAllowlistResponse(updated.BanAllowlist))
}

// DELETE /api/admin/security/bans/allowlist?entry=... — 移除单个条目（不存在则幂等跳过）。
// entry 为单个 IP 或 CIDR 前缀，含 `/` 时须做查询串 URL 编码（如 10.0.0.0%2F8）。
func (h *banHandler) adminRemoveBanAllowlist(c *echo.Context) error {
	entry := c.QueryParam("entry")
	if entry == "" {
		return c.JSON(http.StatusBadRequest, httputil.Response{Message: "query param entry is required"})
	}
	updated, err := h.settings.RemoveBanAllowlistEntries(c.Request().Context(), []string{entry})
	if err != nil {
		return httputil.RespondError(c, err)
	}
	return httputil.RespondData(c, newBanAllowlistResponse(updated.BanAllowlist))
}
