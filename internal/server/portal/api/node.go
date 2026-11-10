package api

import (
	"net"
	"net/http"
	"strconv"

	"design-cloud-engine/internal/platform/config"
	"design-cloud-engine/internal/server/portal/httputil"
	nodesvc "design-cloud-engine/internal/server/service/node"

	"github.com/labstack/echo/v5"
)

type nodeHandler struct {
	node        *nodesvc.Service
	clusterPort int
}

func newNodeHandler(nodeSvc *nodesvc.Service, cfg *config.Config) *nodeHandler {
	return &nodeHandler{
		node:        nodeSvc,
		clusterPort: cfg.Cluster.Port,
	}
}

// route: /api/nodes
func (h *nodeHandler) nodeRegistrationAPI(g *echo.Group) {
	g.POST("/:id/registration", h.registerNode)
}

// registerNode is the OTP registration path: the node presents a one-time token and receives cert/key/ca materials.
func (h *nodeHandler) registerNode(c *echo.Context) error {
	nodeID := c.Param("id")
	var body struct {
		Token string `json:"token"`
	}
	if err := c.Bind(&body); err != nil {
		return c.JSON(http.StatusBadRequest, httputil.Response{Message: err.Error()})
	}

	serverHost, _, err := net.SplitHostPort(c.Request().Host)
	if err != nil {
		serverHost = c.Request().Host
	}
	serverAddr := serverHost + ":" + strconv.Itoa(h.clusterPort)

	enrollment, err := h.node.EnrollWithToken(c.Request().Context(), nodeID, body.Token, serverAddr)
	if err != nil {
		return handleNodeError(c, err)
	}
	return c.JSON(http.StatusOK, httputil.Response{Data: enrollment})
}
