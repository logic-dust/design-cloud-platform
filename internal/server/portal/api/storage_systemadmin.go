package api

import (
	"context"
	"net/http"
	"time"

	"design-cloud-engine/internal/contract"
	"design-cloud-engine/internal/model"
	"design-cloud-engine/internal/platform/format"
	"design-cloud-engine/internal/server/portal/httputil"
	storagesvc "design-cloud-engine/internal/server/service/storage"

	"github.com/labstack/echo/v5"
)

type storageService interface {
	GetLatestReports(ctx context.Context, windowSeconds int) (map[string]model.StorageReport, error)
	GetHistory(ctx context.Context, storageID string, from, to time.Time) ([]model.StorageReport, error)
	TriggerCollect(ctx context.Context, target contract.CollectTarget) error
}

type storageHandler struct {
	storage  storageService
	settings settingsService
}

func newStorageHandler(storageSvc storageService, settingsSvc settingsService) *storageHandler {
	return &storageHandler{storage: storageSvc, settings: settingsSvc}
}

// route: /api/admin/system/storage
func (h *storageHandler) storageAPI(g *echo.Group) {
	g.GET("", h.getSharedStorageSnapshots)
	g.GET("/:id/history", h.getSharedStorageHistory)
	g.POST("/refresh", h.refreshStorage)
}

type sharedStorageSnapshot struct {
	StorageID string  `json:"storageId"`
	Name      string  `json:"name"`
	Type      string  `json:"type"`
	Total     float64 `json:"total"`
	Used      float64 `json:"used"`
	Unit      string  `json:"unit"`
	SampledBy string  `json:"sampledBy"`
	SampledAt string  `json:"sampledAt"`
}

func (h *storageHandler) getSharedStorageSnapshots(c *echo.Context) error {
	settings, err := h.settings.GetStorageSettings(c.Request().Context())
	if err != nil {
		return httputil.RespondError(c, err)
	}

	windowSeconds := 7 * 24 * 60 * 60
	if dashSettings, err := h.settings.GetOverviewSettings(c.Request().Context()); err == nil && dashSettings.StorageQueryWindow > 0 {
		windowSeconds = dashSettings.StorageQueryWindow
	}

	reports, err := h.storage.GetLatestReports(c.Request().Context(), windowSeconds)
	if err != nil {
		return httputil.RespondError(c, err)
	}

	result := make([]sharedStorageSnapshot, 0, len(settings.Storages))
	for _, storage := range settings.Storages {
		snap := sharedStorageSnapshot{
			StorageID: storage.ID,
			Name:      storage.Name,
			Type:      storage.Type,
		}
		if report, ok := reports[storage.ID]; ok {
			totalVal, unit := format.AutoScaleKB(report.Total)
			snap.Total = totalVal
			snap.Used = format.ScaleKBTo(report.Used, unit)
			snap.Unit = unit
			snap.SampledBy = report.SampledBy
			snap.SampledAt = report.ReportedAt.Format(time.RFC3339)
		} else {
			snap.Total = -1
			snap.Used = -1
			snap.Unit = "TB"
		}
		result = append(result, snap)
	}

	return c.JSON(http.StatusOK, httputil.Response{Data: result})
}

func (h *storageHandler) getSharedStorageHistory(c *echo.Context) error {
	storageID := c.Param("id")
	if storageID == "" {
		return c.JSON(http.StatusBadRequest, httputil.Response{Message: "id is required"})
	}

	days, err := queryInt(c, "days", storagesvc.DefaultStorageHistoryDays, 1, storagesvc.MaxStorageHistoryDays)
	if err != nil {
		return httputil.RespondError(c, err)
	}
	to := time.Now()
	from := to.AddDate(0, 0, -days)

	reports, err := h.storage.GetHistory(c.Request().Context(), storageID, from, to)
	if err != nil {
		return httputil.RespondError(c, err)
	}

	var maxTotal int64
	for _, r := range reports {
		if r.Total > maxTotal {
			maxTotal = r.Total
		}
	}
	_, unit := format.AutoScaleKB(maxTotal)

	result := make([]sharedStorageSnapshot, 0, len(reports))
	for _, r := range reports {
		result = append(result, sharedStorageSnapshot{
			StorageID: r.StorageID,
			Total:     format.ScaleKBTo(r.Total, unit),
			Used:      format.ScaleKBTo(r.Used, unit),
			Unit:      unit,
			SampledBy: r.SampledBy,
			SampledAt: r.ReportedAt.Format(time.RFC3339),
		})
	}

	return c.JSON(http.StatusOK, httputil.Response{Data: result})
}

func (h *storageHandler) refreshStorage(c *echo.Context) error {
	if err := h.storage.TriggerCollect(c.Request().Context(), contract.CollectTargetStorage); err != nil {
		return httputil.RespondError(c, err)
	}
	return c.NoContent(http.StatusAccepted)
}
