package api

import (
	"errors"
	"net"
	"net/http"
	"strconv"
	"time"

	"design-cloud-engine/internal/model"
	"design-cloud-engine/internal/server/portal/httputil"
	nodesvc "design-cloud-engine/internal/server/service/node"

	"github.com/labstack/echo/v5"
)

// route: /api/admin/system/nodes
func (h *nodeHandler) adminNodesAPI(g *echo.Group) {
	g.GET("", h.listNodes)
	g.GET("/:id", h.getNode)
	g.GET("/:id/status", h.getNodeStatus)
	g.GET("/:id/config", h.getAgentConfig)
	g.GET("/:id/settings", h.getNodeSettings)
	g.GET("/:id/settings/schema", h.getNodeSettingsSchema)
	g.POST("", h.createNode)
	g.POST("/refresh", h.refreshNodes)
	g.POST("/settings/batch", h.batchUpdateNodeSettings)
	g.PUT("/:id", h.updateNode)
	g.PUT("/:id/settings", h.updateNodeSettings)
	g.PUT("/:id/certificate", h.updateNodeCert)
	g.POST("/:id/tokens", h.issueNodeToken)
	g.DELETE("/:id/certificate", h.revokeNodeCert)
	g.POST("/certificates", h.createClusterCert)
}

func (h *nodeHandler) listNodes(c *echo.Context) error {
	p := httputil.ParsePagination(c, 50, 200)

	nodes, err := h.node.ListViews(c.Request().Context())
	if err != nil {
		return httputil.RespondError(c, err)
	}

	total := len(nodes)
	if p.Offset >= total {
		return httputil.RespondPage(c, []struct{}{}, total)
	}
	end := min(p.Offset+p.Limit, total)
	return httputil.RespondPage(c, nodes[p.Offset:end], total)
}

func (h *nodeHandler) getNode(c *echo.Context) error {
	view, err := h.node.GetView(c.Request().Context(), c.Param("id"))
	if err != nil {
		return handleNodeError(c, err)
	}
	return httputil.RespondData(c, view)
}

func (h *nodeHandler) getNodeStatus(c *echo.Context) error {
	nodeID := c.Param("id")
	report, err := h.node.GetResourceReport(c.Request().Context(), nodeID)
	if err != nil {
		return c.JSON(http.StatusNotFound, httputil.Response{Message: "no resource report for node"})
	}
	return c.JSON(http.StatusOK, httputil.Response{Data: nodesvc.BuildNodeStatusView(report)})
}

func (h *nodeHandler) updateNode(c *echo.Context) error {
	nodeID := c.Param("id")
	var body struct {
		IsServer         *bool             `json:"isServer"`
		Status           string            `json:"status"`
		ClientHost       *string           `json:"clientHost"`
		Labels           map[string]string `json:"labels"`
		AllowConnections *bool             `json:"allowConnections"`
		MaxConnections   *uint             `json:"maxConnections"`
	}
	if err := c.Bind(&body); err != nil {
		return c.JSON(http.StatusBadRequest, httputil.Response{Message: err.Error()})
	}

	var status model.NodeStatus
	if body.Status != "" {
		var parseErr error
		status, parseErr = model.ParseNodeStatus(body.Status)
		if parseErr != nil {
			return c.JSON(http.StatusBadRequest, httputil.Response{Message: parseErr.Error()})
		}
	}
	req := nodesvc.UpdateNodeRequest{
		IsServer:         body.IsServer,
		Status:           status,
		ClientHost:       body.ClientHost,
		Labels:           body.Labels,
		AllowConnections: body.AllowConnections,
		MaxConnections:   body.MaxConnections,
	}

	node, err := h.node.Update(c.Request().Context(), nodeID, req)
	if err != nil {
		return handleNodeError(c, err)
	}
	return c.JSON(http.StatusOK, httputil.Response{Data: node})
}

func (h *nodeHandler) createNode(c *echo.Context) error {
	var req nodesvc.ReqCreateNodeForm
	if err := c.Bind(&req); err != nil {
		return c.JSON(http.StatusBadRequest, httputil.Response{Message: err.Error()})
	}
	node, err := h.node.RegisterNodeFromInput(c.Request().Context(), req)
	if err != nil {
		return handleNodeError(c, err)
	}
	return c.JSON(http.StatusCreated, httputil.Response{Data: node})
}

func (h *nodeHandler) updateNodeCert(c *echo.Context) error {
	nodeID := c.Param("id")
	var body struct {
		CertFingerprint string `json:"certFingerprint"`
		CertSubject     string `json:"certSubject"`
		CertExpiresAt   string `json:"certExpiresAt"`
	}
	if err := c.Bind(&body); err != nil {
		return c.JSON(http.StatusBadRequest, httputil.Response{Message: err.Error()})
	}

	var expiresAt time.Time
	if body.CertExpiresAt != "" {
		parsed, err := time.Parse(time.RFC3339, body.CertExpiresAt)
		if err != nil {
			return c.JSON(http.StatusBadRequest, httputil.Response{Message: err.Error()})
		}
		expiresAt = parsed
	}

	node, err := h.node.UpdateCert(c.Request().Context(), nodeID, body.CertFingerprint, body.CertSubject, expiresAt)
	if err != nil {
		return handleNodeError(c, err)
	}
	node.Token = ""
	return c.JSON(http.StatusOK, httputil.Response{Data: node})
}

func (h *nodeHandler) revokeNodeCert(c *echo.Context) error {
	_, err := h.node.UpdateCert(c.Request().Context(), c.Param("id"), "", "", time.Time{})
	if err != nil {
		return handleNodeError(c, err)
	}
	return c.NoContent(http.StatusNoContent)
}

func (h *nodeHandler) issueNodeToken(c *echo.Context) error {
	node, err := h.node.GenerateToken(c.Request().Context(), c.Param("id"))
	if err != nil {
		return handleNodeError(c, err)
	}
	return c.JSON(http.StatusCreated, httputil.Response{Data: buildNodeTokenPayload(node)})
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
	return c.JSON(http.StatusOK, httputil.Response{Data: agentCfg})
}

// createClusterCert is the recommended bootstrap path for internal clusters:
// sign a node certificate on the server side and optionally sync its metadata back into the node record.
func (h *nodeHandler) createClusterCert(c *echo.Context) error {
	var body struct {
		NodeID    string   `json:"nodeId"`
		Hosts     []string `json:"hosts"`
		ValidDays int      `json:"validDays"`
		SyncNode  *bool    `json:"syncNode"`
	}
	if err := c.Bind(&body); err != nil {
		return c.JSON(http.StatusBadRequest, httputil.Response{Message: err.Error()})
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

	return httputil.RespondData(c, result)
}

func (h *nodeHandler) refreshNodes(c *echo.Context) error {
	if err := h.node.TriggerCollect(c.Request().Context()); err != nil {
		return httputil.RespondError(c, err)
	}
	return c.NoContent(http.StatusAccepted)
}

func handleNodeError(c *echo.Context, err error) error {
	switch {
	case errors.Is(err, nodesvc.ErrNodeNotFound):
		return c.JSON(http.StatusNotFound, httputil.Response{Message: "node not found"})
	case errors.Is(err, nodesvc.ErrInvalidNodeID),
		errors.Is(err, nodesvc.ErrInvalidNodeStatus),
		errors.Is(err, nodesvc.ErrInvalidClientHost),
		errors.Is(err, nodesvc.ErrInvalidNodeCert),
		errors.Is(err, nodesvc.ErrInvalidNodeCertOptions):
		return c.JSON(http.StatusBadRequest, httputil.Response{Message: err.Error()})
	case errors.Is(err, nodesvc.ErrInvalidNodeToken):
		return c.JSON(http.StatusUnauthorized, httputil.Response{Message: err.Error()})
	case errors.Is(err, nodesvc.ErrExpiredNodeToken),
		errors.Is(err, nodesvc.ErrMissingNodeToken),
		errors.Is(err, nodesvc.ErrNodeAlreadyRegistered):
		return c.JSON(http.StatusConflict, httputil.Response{Message: err.Error()})
	default:
		return httputil.RespondError(c, err)
	}
}

func buildNodeTokenPayload(node *model.Node) map[string]any {
	return map[string]any{
		"nodeID":         node.NodeID,
		"token":          node.Token,
		"tokenCreatedAt": node.TokenCreatedAt,
	}
}
