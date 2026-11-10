package api

import (
	json "design-cloud-engine/internal/infra/jsoncodec"
	"errors"
	"io"
	"net"
	"net/http"
	"strconv"
	"strings"
	"time"

	"design-cloud-engine/internal/contract/schema"
	"design-cloud-engine/internal/server/portal/httputil"
	nodesvc "design-cloud-engine/internal/server/service/node"

	"github.com/labstack/echo/v5"
)

// route: /api/admin/system/nodes
func (h *nodeHandler) adminNodesAPI(g *echo.Group) {
	g.GET("", h.listNodes)
	g.GET("/metrics", h.listNodeMetrics)
	g.POST("/metrics/refresh", h.refreshNodeMetrics)
	g.GET("/:id", h.getNode)
	g.GET("/:id/metrics", h.getNodeMetrics)
	g.GET("/:id/config", h.getAgentConfig)
	g.GET("/:id/settings", h.getNodeSettings)
	g.GET("/:id/settings/schema", h.getNodeSettingsSchema)
	g.POST("", h.createNode)
	g.POST("/batch-update", h.batchUpdateNodes)
	g.POST("/settings/batch-update", h.batchUpdateNodeSettings)
	g.PUT("/:id", h.updateNode)
	g.PUT("/:id/settings", h.updateNodeSettings)
	g.PUT("/:id/certificate", h.updateNodeCert)
	g.POST("/:id/tokens", h.issueNodeToken)
	g.DELETE("/:id/certificate", h.revokeNodeCert)
	g.POST("/certificates", h.createClusterCert)
}

func parseGroupsQuery(c *echo.Context) []string {
	raw := strings.TrimSpace(c.QueryParam("groups"))
	if raw == "" {
		return nil
	}
	var groups []string
	for g := range strings.SplitSeq(raw, ",") {
		g = strings.TrimSpace(g)
		if g != "" {
			groups = append(groups, g)
		}
	}
	return groups
}

func (h *nodeHandler) listNodes(c *echo.Context) error {
	p := httputil.ParsePage(c)
	nodes, total, err := h.node.ListViewsPage(c.Request().Context(), parseGroupsQuery(c), p.Limit, p.Offset)
	if err != nil {
		return httputil.RespondError(c, err)
	}
	return httputil.RespondPage(c, nodes, total)
}

func (h *nodeHandler) listNodeMetrics(c *echo.Context) error {
	p := httputil.ParsePage(c)
	items, total, err := h.node.ListMetricsPage(c.Request().Context(), parseGroupsQuery(c), p.Limit, p.Offset)
	if err != nil {
		return httputil.RespondError(c, err)
	}
	return httputil.RespondPage(c, items, total)
}

func (h *nodeHandler) getNode(c *echo.Context) error {
	view, err := h.node.GetView(c.Request().Context(), c.Param("id"))
	if err != nil {
		return handleNodeError(c, err)
	}
	return httputil.RespondOK(c, view)
}

func (h *nodeHandler) getNodeMetrics(c *echo.Context) error {
	nodeID := c.Param("id")
	metrics, err := h.node.GetMetrics(c.Request().Context(), nodeID)
	if err != nil {
		return httputil.RespondHTTPError(c, http.StatusNotFound, "no metrics for node")
	}
	return httputil.RespondOK(c, nodesvc.BuildNodeMetricsView(metrics))
}

func (h *nodeHandler) updateNode(c *echo.Context) error {
	nodeID := c.Param("id")
	var body struct {
		IsServer            *bool    `json:"isServer"`
		InMaintenance       *bool    `json:"inMaintenance"`
		DialHost            *string  `json:"dialHost"`
		Groups              []string `json:"groups"`
		Description         *string  `json:"description"`
		ConnectionsDisabled *bool    `json:"connectionsDisabled"`
		MaxConnections      *uint    `json:"maxConnections"`
	}
	if err := echo.BindBody(c, &body); err != nil {
		return httputil.RespondError(c, err)
	}

	req := nodesvc.UpdateNodeRequest{
		IsServer:            body.IsServer,
		InMaintenance:       body.InMaintenance,
		DialHost:            body.DialHost,
		ConnectionsDisabled: body.ConnectionsDisabled,
		MaxConnections:      body.MaxConnections,
	}
	if body.Groups != nil {
		req.Groups = body.Groups
	}
	if body.Description != nil {
		req.Description = body.Description
	}

	node, err := h.node.Update(c.Request().Context(), nodeID, req)
	if err != nil {
		return handleNodeError(c, err)
	}
	return httputil.RespondOK(c, node)
}

func (h *nodeHandler) batchUpdateNodes(c *echo.Context) error {
	var req struct {
		Items []nodesvc.BatchNodeUpdateItem `json:"items"`
	}
	if err := echo.BindBody(c, &req); err != nil {
		return httputil.RespondError(c, err)
	}
	out, err := h.node.AdminBatchUpdate(c.Request().Context(), req.Items)
	if err != nil {
		return handleNodeError(c, err)
	}
	return httputil.RespondOK(c, out)
}

func (h *nodeHandler) createNode(c *echo.Context) error {
	var req nodesvc.ReqCreateNodeForm
	if err := echo.BindBody(c, &req); err != nil {
		return httputil.RespondError(c, err)
	}
	view, _, err := h.node.RegisterNodeFromInput(c.Request().Context(), req)
	if err != nil {
		return handleNodeError(c, err)
	}
	return httputil.RespondCreated(c, view)
}

func (h *nodeHandler) updateNodeCert(c *echo.Context) error {
	nodeID := c.Param("id")
	var body struct {
		CertFingerprint string `json:"certFingerprint"`
		CertSubject     string `json:"certSubject"`
		CertExpiresAt   string `json:"certExpiresAt"`
	}
	if err := echo.BindBody(c, &body); err != nil {
		return httputil.RespondError(c, err)
	}

	var expiresAt time.Time
	if body.CertExpiresAt != "" {
		parsed, err := time.Parse(time.RFC3339, body.CertExpiresAt)
		if err != nil {
			return httputil.RespondHTTPError(c, http.StatusBadRequest, err.Error())
		}
		expiresAt = parsed
	}

	node, err := h.node.UpdateCert(c.Request().Context(), nodeID, body.CertFingerprint, body.CertSubject, expiresAt)
	if err != nil {
		return handleNodeError(c, err)
	}
	return httputil.RespondOK(c, node)
}

func (h *nodeHandler) revokeNodeCert(c *echo.Context) error {
	_, err := h.node.UpdateCert(c.Request().Context(), c.Param("id"), "", "", time.Time{})
	if err != nil {
		return handleNodeError(c, err)
	}
	return httputil.RespondNoContent(c)
}

func (h *nodeHandler) issueNodeToken(c *echo.Context) error {
	token, err := h.node.GenerateToken(c.Request().Context(), c.Param("id"))
	if err != nil {
		return handleNodeError(c, err)
	}
	return httputil.RespondCreated(c, token)
}

func (h *nodeHandler) getAgentConfig(c *echo.Context) error {
	nodeID := c.Param("id")

	host := c.Request().Host
	serverHost, _, err := net.SplitHostPort(host)
	if err != nil {
		serverHost = host
	}
	serverAddr := serverHost + ":" + strconv.Itoa(h.clusterPort)

	agentCfg, err := h.node.GetAgentConfig(c.Request().Context(), nodeID, serverAddr)
	if err != nil {
		return handleNodeError(c, err)
	}
	return httputil.RespondOK(c, agentCfg)
}

func (h *nodeHandler) createClusterCert(c *echo.Context) error {
	var body struct {
		NodeID    string   `json:"nodeId"`
		Hosts     []string `json:"hosts"`
		ValidDays int      `json:"validDays"`
		SyncNode  *bool    `json:"syncNode"`
	}
	if err := echo.BindBody(c, &body); err != nil {
		return httputil.RespondError(c, err)
	}

	syncNode := true
	if body.SyncNode != nil {
		syncNode = *body.SyncNode
	}
	result, err := h.node.CreateClusterCert(c.Request().Context(), nodesvc.CreateClusterCertRequest{
		NodeID:    body.NodeID,
		Hosts:     body.Hosts,
		ValidDays: body.ValidDays,
		SyncNode:  syncNode,
	})
	if err != nil {
		return handleNodeError(c, err)
	}

	return httputil.RespondOK(c, result)
}

func (h *nodeHandler) refreshNodeMetrics(c *echo.Context) error {
	if err := h.node.TriggerCollect(c.Request().Context()); err != nil {
		return httputil.RespondError(c, err)
	}
	return c.NoContent(http.StatusAccepted)
}

func (h *nodeHandler) getNodeSettings(c *echo.Context) error {
	nodeID := c.Param("id")
	view, err := h.node.GetNodeSettings(c.Request().Context(), nodeID)
	if err != nil {
		return handleNodeError(c, err)
	}
	return httputil.RespondOK(c, view)
}

func (h *nodeHandler) updateNodeSettings(c *echo.Context) error {
	nodeID := c.Param("id")

	body, err := io.ReadAll(c.Request().Body)
	if err != nil {
		return httputil.RespondHTTPError(c, http.StatusBadRequest, "failed to read request body")
	}

	view, err := h.node.UpdateNodeSettings(c.Request().Context(), nodeID, body)
	if err != nil {
		return handleNodeError(c, err)
	}
	return httputil.RespondOK(c, view)
}

func (h *nodeHandler) getNodeSettingsSchema(c *echo.Context) error {
	schemaObj := schema.GenerateJSONSchema(schema.NodeSettings{})
	return httputil.RespondOK(c, schemaObj)
}

func (h *nodeHandler) batchUpdateNodeSettings(c *echo.Context) error {
	body, err := io.ReadAll(c.Request().Body)
	if err != nil {
		return httputil.RespondHTTPError(c, http.StatusBadRequest, "failed to read request body")
	}
	var req nodesvc.BatchUpdateNodeSettingsRequest
	if err := json.Unmarshal(body, &req); err != nil {
		return httputil.RespondHTTPError(c, http.StatusBadRequest, "malformed request payload")
	}

	resp, err := h.node.BatchUpdateNodeSettings(c.Request().Context(), req)
	if err != nil {
		return handleNodeError(c, err)
	}
	return httputil.RespondOK(c, resp)
}

func handleNodeError(c *echo.Context, err error) error {
	if missing, ok := errors.AsType[nodesvc.MissingNodeIDsError](err); ok {
		return httputil.RespondErrorDetails(
			c,
			http.StatusUnprocessableEntity,
			err.Error(),
			map[string]any{"missingNodeIds": missing.MissingNodeIDs},
		)
	}
	switch {
	case errors.Is(err, nodesvc.ErrNodeNotFound):
		return httputil.RespondHTTPError(c, http.StatusNotFound, "node not found")
	case errors.Is(err, nodesvc.ErrInvalidNodeID),
		errors.Is(err, nodesvc.ErrInvalidDialHost),
		errors.Is(err, nodesvc.ErrInvalidNodeCert),
		errors.Is(err, nodesvc.ErrInvalidNodeCertOptions):
		return httputil.RespondHTTPError(c, http.StatusBadRequest, err.Error())
	case errors.Is(err, nodesvc.ErrInvalidNodeToken):
		return httputil.RespondHTTPError(c, http.StatusUnauthorized, err.Error())
	case errors.Is(err, nodesvc.ErrExpiredNodeToken),
		errors.Is(err, nodesvc.ErrMissingNodeToken),
		errors.Is(err, nodesvc.ErrNodeAlreadyRegistered),
		errors.Is(err, nodesvc.ErrEnrollmentIssueAfterConsume):
		return httputil.RespondHTTPError(c, http.StatusConflict, err.Error())
	default:
		return httputil.RespondError(c, err)
	}
}
