package api

import (
	"encoding/json"
	"io"
	"net/http"

	"design-cloud-engine/internal/server/portal/httputil"
	nodesvc "design-cloud-engine/internal/server/service/node"
	"design-cloud-engine/internal/server/service/settings/schema"

	"github.com/labstack/echo/v5"
)

func (h *nodeHandler) getNodeSettings(c *echo.Context) error {
	nodeID := c.Param("id")
	view, err := h.node.GetNodeSettings(c.Request().Context(), nodeID)
	if err != nil {
		return handleNodeError(c, err)
	}
	return httputil.RespondData(c, view)
}

func (h *nodeHandler) updateNodeSettings(c *echo.Context) error {
	nodeID := c.Param("id")

	body, err := io.ReadAll(c.Request().Body)
	if err != nil {
		return c.JSON(http.StatusBadRequest, httputil.Response{Message: "failed to read request body"})
	}

	view, err := h.node.UpdateNodeSettings(c.Request().Context(), nodeID, body)
	if err != nil {
		return handleNodeError(c, err)
	}
	return httputil.RespondData(c, view)
}

func (h *nodeHandler) getNodeSettingsSchema(c *echo.Context) error {
	schemaObj := schema.GenerateJSONSchema(schema.NodeSettings{})
	return httputil.RespondData(c, schemaObj)
}

func (h *nodeHandler) batchUpdateNodeSettings(c *echo.Context) error {
	body, err := io.ReadAll(c.Request().Body)
	if err != nil {
		return c.JSON(http.StatusBadRequest, httputil.Response{Message: "failed to read request body"})
	}
	var req nodesvc.BatchUpdateNodeSettingsRequest
	if err := json.Unmarshal(body, &req); err != nil {
		return c.JSON(http.StatusBadRequest, httputil.Response{Message: "malformed request payload"})
	}

	resp, err := h.node.BatchUpdateNodeSettings(c.Request().Context(), req)
	if err != nil {
		return handleNodeError(c, err)
	}
	return httputil.RespondData(c, resp)
}
