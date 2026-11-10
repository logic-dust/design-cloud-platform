package api

import (
	"context"
	"errors"
	"net/http"
	"time"

	"github.com/labstack/echo/v5"

	"design-cloud-engine/internal/model"
	"design-cloud-engine/internal/platform/config"
	"design-cloud-engine/internal/server/portal/httputil"
	"design-cloud-engine/internal/server/service/file"
	sessionsvc "design-cloud-engine/internal/server/service/session"
	userprovider "design-cloud-engine/internal/server/service/user/provider"
)

type fileLocationsLister interface {
	ListLocations(ctx context.Context, runUser string) ([]file.LocationInfo, error)
}

type sessionHandler struct {
	session *sessionsvc.Service
	file    fileLocationsLister
	cfg     *config.Config
}

func newSessionHandler(sessionSvc *sessionsvc.Service, fileSvc fileLocationsLister, cfg *config.Config) *sessionHandler {
	return &sessionHandler{session: sessionSvc, file: fileSvc, cfg: cfg}
}

// --- 用户登录，即创建session
// route: /api/sessions (POST, login)
func (h *sessionHandler) loginAPI(g *echo.Group) {
	g.POST("", h.login)
}

// route: /api/sessions
func (h *sessionHandler) sessionsAPI(g *echo.Group) {
	g.GET("", h.listSessions)
	g.GET("/current", h.getCurrentSession)
	g.POST("/current/renew", h.renewCurrentSession)
	g.POST("/batch-delete", h.bulkDeleteSessions)
	g.DELETE("/current", h.logout) // 删除当前登录的session（登出）
	g.DELETE("/:id", h.deleteSessionByID)
	g.DELETE("", h.logoutAll) // 删除用户的全部session（登出所有设备）
}

func (h *sessionHandler) login(c *echo.Context) error {
	info := new(struct {
		Username string `json:"username"`
		Password string `json:"password"`
		Client   struct {
			Platform   string `json:"platform"`
			OSVersion  string `json:"osVersion"`
			AppVersion string `json:"appVersion"`
			DeviceID   string `json:"deviceId"`
			Hostname   string `json:"hostname"`
		} `json:"client"`
	})
	if err := c.Bind(info); err != nil {
		return c.JSON(http.StatusBadRequest, httputil.Response{Message: err.Error()})
	}

	client := sessionsvc.LoginClient{
		IP:         c.RealIP(),
		Hostname:   info.Client.Hostname,
		Platform:   info.Client.Platform,
		OSVersion:  info.Client.OSVersion,
		AppVersion: info.Client.AppVersion,
		DeviceID:   info.Client.DeviceID,
	}
	data, err := h.session.Create(c.Request().Context(), info.Username, info.Password, client, h.cfg.Admin.EnableThreeRoles, h.file)

	if err != nil {
		statusCode := http.StatusInternalServerError
		switch {
		case errors.Is(err, sessionsvc.ErrAdminModeRestriction):
			statusCode = http.StatusForbidden
		case errors.Is(err, userprovider.ErrUserNotActive):
			statusCode = http.StatusForbidden
		case errors.Is(err, userprovider.ErrUserNotFound),
			errors.Is(err, userprovider.ErrUserPasswordIncorrect):
			statusCode = http.StatusUnauthorized
		case errors.Is(err, sessionsvc.ErrLoginBlocked):
			statusCode = http.StatusForbidden
		default:
		}

		if statusCode == http.StatusInternalServerError {
			return httputil.RespondError(c, err)
		}
		return c.JSON(statusCode, httputil.Response{Message: err.Error()})
	}

	return c.JSON(http.StatusCreated, httputil.Response{Data: data})
	// 如需要支持浏览器cookie机制，应改为 Set-Cookie（HttpOnly; Secure; SameSite=Strict）
}

func (h *sessionHandler) getCurrentSession(c *echo.Context) error {
	token := httputil.CurrentToken(c)
	ctx := c.Request().Context()
	s, err := h.session.GetByToken(ctx, token)
	if err != nil {
		return c.JSON(http.StatusNotFound, httputil.Response{Message: "session not found"})
	}
	multi, err := h.session.MultiSessionEnabled(ctx)
	if err != nil {
		return httputil.RespondError(c, err)
	}

	return c.JSON(http.StatusOK, httputil.Response{Data: sessionsvc.CurrentSession{
		ID:                  s.ID,
		Username:            s.Username,
		Token:               s.Token,
		Role:                s.Role.String(),
		IP:                  s.IP,
		ExpiresAt:           s.ExpiresAt,
		MultiSessionEnabled: multi,
	}})
}

// --- 刷新当前会话（续期），更新过期时间，token 不变
func (h *sessionHandler) renewCurrentSession(c *echo.Context) error {
	token := httputil.CurrentToken(c)
	refreshed, err := h.session.Renew(c.Request().Context(), token)
	if err != nil {
		switch {
		case errors.Is(err, sessionsvc.ErrInvalidSessionToken),
			errors.Is(err, sessionsvc.ErrExpiredSession):
			return c.JSON(http.StatusUnauthorized, httputil.Response{Message: err.Error()})
		case errors.Is(err, sessionsvc.ErrPasswordChangeRequired):
			return c.JSON(http.StatusPreconditionRequired, httputil.Response{Message: err.Error()})
		default:
			return httputil.RespondError(c, err)
		}
	}
	return c.JSON(http.StatusOK, httputil.Response{Data: struct {
		ExpiresAt time.Time `json:"expiresAt"`
	}{ExpiresAt: refreshed.ExpiresAt}})
}

func (h *sessionHandler) listSessions(c *echo.Context) error {
	current, err := httputil.RequireSession(c)
	if err != nil {
		return err
	}

	p := httputil.ParsePage(c)
	sessions, total, err := h.session.ListByUserIDPage(c.Request().Context(), current.UserID, p.Limit, p.Offset)
	if err != nil {
		return httputil.RespondError(c, err)
	}

	return httputil.RespondPage(c, markCurrentSession(sessions, current.ID), total)
}

// ownSession 是用户会话列表项：在会话字段之外标出发起本次请求的会话。
type ownSession struct {
	model.Session
	Current bool `json:"current,omitzero"`
}

// markCurrentSession 按鉴权中间件已解析出的当前会话 ID 标记列表项。
func markCurrentSession(sessions []model.Session, currentID uint) []ownSession {
	out := make([]ownSession, len(sessions))
	for i, s := range sessions {
		out[i] = ownSession{Session: s, Current: s.ID == currentID}
	}
	return out
}

// --- 登出（删除用户当前session）
func (h *sessionHandler) logout(c *echo.Context) error {
	current, err := httputil.RequireSession(c)
	if err != nil {
		return err
	}
	if err := h.session.DeleteByAccessToken(c.Request().Context(), current.Token); err != nil {
		return httputil.RespondError(c, err)
	}
	return c.NoContent(http.StatusNoContent)
}

// --- 删除当前用户指定设备的 session
func (h *sessionHandler) deleteSessionByID(c *echo.Context) error {
	current, err := httputil.RequireSession(c)
	if err != nil {
		return err
	}

	sessionID, err := httputil.ParseUintParam(c, "id")
	if err != nil {
		return err
	}

	err = h.session.DeleteOwnedByID(c.Request().Context(), sessionID, current.UserID)
	switch {
	case errors.Is(err, sessionsvc.ErrSessionNotFound):
		return c.JSON(http.StatusNotFound, httputil.Response{Message: "session not found"})
	case errors.Is(err, sessionsvc.ErrSessionAccessDenied):
		return c.JSON(http.StatusForbidden, httputil.Response{Message: "permission denied"})
	case err != nil:
		return httputil.RespondError(c, err)
	default:
		return c.NoContent(http.StatusNoContent)
	}
}

// --- 登出所有设备（删除当前用户的全部 session）
func (h *sessionHandler) logoutAll(c *echo.Context) error {
	username := httputil.CurrentUsername(c)
	if err := h.session.Delete(c.Request().Context(), username); err != nil {
		return httputil.RespondError(c, err)
	}
	return c.NoContent(http.StatusNoContent)
}

func (h *sessionHandler) bulkDeleteSessions(c *echo.Context) error {
	current, err := httputil.RequireSession(c)
	if err != nil {
		return err
	}

	var req struct {
		IDs httputil.IDList `json:"ids"`
	}
	if err := c.Bind(&req); err != nil {
		return c.JSON(http.StatusBadRequest, httputil.Response{Message: err.Error()})
	}
	if len(req.IDs) == 0 {
		return c.JSON(http.StatusBadRequest, httputil.Response{Message: "ids is required"})
	}

	err = h.session.DeleteOwnedByIDs(c.Request().Context(), current.UserID, req.IDs...)
	switch {
	case errors.Is(err, sessionsvc.ErrSessionNotFound):
		return c.JSON(http.StatusNotFound, httputil.Response{Message: "session not found"})
	case errors.Is(err, sessionsvc.ErrSessionAccessDenied):
		return c.JSON(http.StatusForbidden, httputil.Response{Message: "permission denied"})
	case err != nil:
		return httputil.RespondError(c, err)
	default:
		return c.NoContent(http.StatusNoContent)
	}
}
