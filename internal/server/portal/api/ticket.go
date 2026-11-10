package api

import (
	json "design-cloud-engine/internal/platform/jsoncodec"
	"errors"
	"net/http"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"

	"design-cloud-engine/internal/model"
	"design-cloud-engine/internal/server/portal/httputil"
	ticketsvc "design-cloud-engine/internal/server/service/ticket"
	"design-cloud-engine/internal/server/service/user"

	"github.com/labstack/echo/v5"
)

type ticketHandler struct {
	ticket            *ticketsvc.Service
	user              *user.Service
	userSearchLimiter *perUserLimiter
}

func newTicketHandler(ticket *ticketsvc.Service, user *user.Service) *ticketHandler {
	return &ticketHandler{ticket: ticket, user: user, userSearchLimiter: newPerUserLimiter(5, 5)}
}

// route: /api/tickets
func (h *ticketHandler) ticketsAPI(g *echo.Group) {
	g.GET("", h.listTickets)
	g.POST("", h.createTicket)
	g.POST("/batch-delete", h.bulkDeleteTickets)
	g.GET("/assignees", h.listTicketAssignees)
	g.GET("/projects", h.listTicketProjects)
	g.GET("/summary", h.getTicketSummary)
	g.GET("/:id", h.getTicket)
	g.PATCH("/:id", h.updateTicket)
	g.DELETE("/:id", h.deleteTicket)
	g.POST("/:id/move", h.moveTicket)
	g.GET("/:id/comments", h.listTicketComments)
	g.POST("/:id/comments", h.createTicketComment)
	g.PATCH("/:id/comments/:commentId", h.updateTicketComment)
	g.DELETE("/:id/comments/:commentId", h.deleteTicketComment)
	g.GET("/:id/events", h.listTicketEvents)
}

// --- 公共解析 ---

func ticketActor(c *echo.Context) (ticketsvc.Actor, error) {
	s, err := httputil.RequireSession(c)
	if err != nil {
		return ticketsvc.Actor{}, err
	}
	return ticketsvc.Actor{ID: s.UserID, Username: s.Username, Role: s.Role, Scopes: s.AdminScopes}, nil
}

func unprocessable(msg string) error { return echo.NewHTTPError(http.StatusUnprocessableEntity, msg) }

// bindStrict 解码 JSON 请求体，拒绝未知字段与尾随内容。
func bindStrict(c *echo.Context, dst any) error {
	if err := json.UnmarshalStrictRead(c.Request().Body, dst); err != nil {
		return unprocessable("invalid request body")
	}
	return nil
}

// queryInt 解析可选整数查询参数并校验范围；缺省返回 def。
func queryInt(c *echo.Context, name string, def, lo, hi int) (int, error) {
	return httputil.ParseQueryIntInRange(c, name, def, lo, hi)
}

func queryPage(c *echo.Context) (limit, offset int, err error) {
	p, err := httputil.ParseTicketPage(c)
	if err != nil {
		return 0, 0, err
	}
	return p.Limit, p.Offset, nil
}

func queryBool(c *echo.Context, name string) (bool, error) {
	raw := c.QueryParam(name)
	if raw == "" {
		return false, nil
	}
	v, err := strconv.ParseBool(raw)
	if err != nil {
		return false, unprocessable("invalid " + name)
	}
	return v, nil
}

func queryID(c *echo.Context, name string) (*uint, error) {
	raw := c.QueryParam(name)
	if raw == "" {
		return nil, nil
	}
	v, err := strconv.ParseUint(raw, 10, 0)
	if err != nil || v == 0 {
		return nil, unprocessable("invalid " + name)
	}
	id := uint(v)
	return &id, nil
}

// queryFloat 解析可选浮点查询参数；缺省返回 nil。
func queryFloat(c *echo.Context, name string) (*float64, error) {
	raw := c.QueryParam(name)
	if raw == "" {
		return nil, nil
	}
	v, err := strconv.ParseFloat(raw, 64)
	if err != nil || strings.ContainsAny(raw, " \t\r\n") {
		return nil, unprocessable("invalid " + name)
	}
	return &v, nil
}

// queryValues 收集多值参数：支持重复参数与逗号分隔。
func queryValues(c *echo.Context, name string) []string {
	var out []string
	for _, raw := range c.QueryParams()[name] {
		for part := range strings.SplitSeq(raw, ",") {
			if part = strings.TrimSpace(part); part != "" {
				out = append(out, part)
			}
		}
	}
	return out
}

func queryEnums[T any](c *echo.Context, name string, parse func(string) (T, error)) ([]T, error) {
	var out []T
	for _, v := range queryValues(c, name) {
		parsed, err := parse(v)
		if err != nil {
			return nil, unprocessable("invalid " + name)
		}
		out = append(out, parsed)
	}
	return out, nil
}

// parseTicketListQuery 解析普通与管理员列表共用的查询参数。
func parseTicketListQuery(c *echo.Context) (ticketsvc.ListQuery, error) {
	var q ticketsvc.ListQuery
	var err error
	if q.ProjectID, err = queryID(c, "projectId"); err != nil {
		return q, err
	}
	if q.ReporterID, err = queryID(c, "reporterId"); err != nil {
		return q, err
	}
	if c.QueryParam("assigneeId") == "none" {
		q.Unassigned = true
	} else if q.AssigneeID, err = queryID(c, "assigneeId"); err != nil {
		return q, err
	}
	if q.Types, err = queryEnums(c, "type", model.ParseTicketType); err != nil {
		return q, err
	}
	if q.Statuses, err = queryEnums(c, "status", model.ParseTicketStatus); err != nil {
		return q, err
	}
	if q.Priorities, err = queryEnums(c, "priority", model.ParseTicketPriority); err != nil {
		return q, err
	}
	q.Q = strings.TrimSpace(c.QueryParam("q"))
	if utf8.RuneCountInString(q.Q) > 256 {
		return q, unprocessable("invalid q")
	}
	switch q.Sort = c.QueryParam("sort"); q.Sort {
	case "", "sortOrder", "createdAt", "updatedAt", "dueDate", "priority":
	default:
		return q, unprocessable("invalid sort")
	}
	switch q.Order = c.QueryParam("order"); q.Order {
	case "", "asc", "desc":
	default:
		return q, unprocessable("invalid order")
	}
	q.Limit, q.Offset, err = queryPage(c)
	if err != nil {
		return q, err
	}
	afterSortSet := c.QueryParam("afterSort") != ""
	afterIDSet := c.QueryParam("afterId") != ""
	if afterSortSet != afterIDSet {
		if afterSortSet {
			return q, unprocessable("invalid afterId")
		}
		return q, unprocessable("invalid afterSort")
	}
	if afterSortSet {
		if q.AfterSort, err = queryFloat(c, "afterSort"); err != nil {
			return q, err
		}
		if q.AfterID, err = queryID(c, "afterId"); err != nil {
			return q, err
		}
		// 第一期仅 sortOrder 支持 keyset；与 offset 同时出现时游标优先（忽略 offset）。
		switch q.Sort {
		case "", "sortOrder":
		default:
			return q, unprocessable("invalid afterSort")
		}
	} else if q.Offset >= httputil.MaxPageLimit {
		// sortOrder 深分页统一走 keyset，避免大 OFFSET。
		switch q.Sort {
		case "", "sortOrder":
			return q, unprocessable("invalid offset")
		}
	}
	return q, nil
}

func respondTicketError(c *echo.Context, err error) error {
	if be, ok := errors.AsType[*ticketsvc.BatchError](err); ok {
		return c.JSON(be.Status, httputil.Response{Message: be.Msg, Details: map[string]any{be.Key: be.IDs}})
	}
	return httputil.RespondError(c, err)
}

// --- 工单 ---

// GET /api/tickets
func (h *ticketHandler) listTickets(c *echo.Context) error {
	q, err := parseTicketListQuery(c)
	if err != nil {
		return httputil.RespondError(c, err)
	}
	page, err := h.ticket.List(c.Request().Context(), q)
	if err != nil {
		return httputil.RespondError(c, err)
	}
	return c.JSON(http.StatusOK, httputil.Response{Data: page})
}

// POST /api/tickets
func (h *ticketHandler) createTicket(c *echo.Context) error {
	actor, err := ticketActor(c)
	if err != nil {
		return httputil.RespondError(c, err)
	}
	var req ticketsvc.CreateTicketRequest
	if err := bindStrict(c, &req); err != nil {
		return httputil.RespondError(c, err)
	}
	t, err := h.ticket.Create(c.Request().Context(), actor, req)
	if err != nil {
		return httputil.RespondError(c, err)
	}
	return c.JSON(http.StatusCreated, httputil.Response{Data: t})
}

// GET /api/tickets/:id
func (h *ticketHandler) getTicket(c *echo.Context) error {
	id, err := httputil.ParseUintParam(c, "id")
	if err != nil {
		return httputil.RespondError(c, err)
	}
	t, err := h.ticket.Get(c.Request().Context(), id)
	if err != nil {
		return httputil.RespondError(c, err)
	}
	return c.JSON(http.StatusOK, httputil.Response{Data: t})
}

// PATCH /api/tickets/:id
func (h *ticketHandler) updateTicket(c *echo.Context) error {
	actor, err := ticketActor(c)
	if err != nil {
		return httputil.RespondError(c, err)
	}
	id, err := httputil.ParseUintParam(c, "id")
	if err != nil {
		return httputil.RespondError(c, err)
	}
	var req ticketsvc.UpdateTicketRequest
	if err := bindStrict(c, &req); err != nil {
		return httputil.RespondError(c, err)
	}
	t, err := h.ticket.Update(c.Request().Context(), actor, id, req)
	if err != nil {
		return httputil.RespondError(c, err)
	}
	return c.JSON(http.StatusOK, httputil.Response{Data: t})
}

// POST /api/tickets/:id/move
func (h *ticketHandler) moveTicket(c *echo.Context) error {
	actor, err := ticketActor(c)
	if err != nil {
		return httputil.RespondError(c, err)
	}
	id, err := httputil.ParseUintParam(c, "id")
	if err != nil {
		return httputil.RespondError(c, err)
	}
	var req ticketsvc.MoveTicketRequest
	if err := bindStrict(c, &req); err != nil {
		return httputil.RespondError(c, err)
	}
	t, err := h.ticket.Move(c.Request().Context(), actor, id, req)
	if err != nil {
		return httputil.RespondError(c, err)
	}
	return c.JSON(http.StatusOK, httputil.Response{Data: t})
}

// DELETE /api/tickets/:id
func (h *ticketHandler) deleteTicket(c *echo.Context) error {
	actor, err := ticketActor(c)
	if err != nil {
		return httputil.RespondError(c, err)
	}
	id, err := httputil.ParseUintParam(c, "id")
	if err != nil {
		return httputil.RespondError(c, err)
	}
	if err := h.ticket.Delete(c.Request().Context(), actor, id); err != nil {
		return respondTicketError(c, err)
	}
	return c.NoContent(http.StatusNoContent)
}

// batchIDsRequest 的 ids 接受数字或数字字符串，去重后交给 service 校验数量。
type batchIDsRequest struct {
	IDs httputil.IDList `json:"ids"`
}

// POST /api/tickets/batch-delete
func (h *ticketHandler) bulkDeleteTickets(c *echo.Context) error {
	actor, err := ticketActor(c)
	if err != nil {
		return httputil.RespondError(c, err)
	}
	var req batchIDsRequest
	if err := bindStrict(c, &req); err != nil {
		return httputil.RespondError(c, err)
	}
	if err := h.ticket.Delete(c.Request().Context(), actor, req.IDs...); err != nil {
		return respondTicketError(c, err)
	}
	return c.NoContent(http.StatusNoContent)
}

// --- 评论与变更记录 ---

func ticketAndCommentIDs(c *echo.Context) (uint, uint, error) {
	id, err := httputil.ParseUintParam(c, "id")
	if err != nil {
		return 0, 0, err
	}
	cid, err := httputil.ParseUintParam(c, "commentId")
	return id, cid, err
}

// GET /api/tickets/:id/comments
func (h *ticketHandler) listTicketComments(c *echo.Context) error {
	id, err := httputil.ParseUintParam(c, "id")
	if err != nil {
		return httputil.RespondError(c, err)
	}
	limit, offset, err := queryPage(c)
	if err != nil {
		return httputil.RespondError(c, err)
	}
	page, err := h.ticket.ListComments(c.Request().Context(), id, limit, offset)
	if err != nil {
		return httputil.RespondError(c, err)
	}
	return c.JSON(http.StatusOK, httputil.Response{Data: page})
}

// POST /api/tickets/:id/comments
func (h *ticketHandler) createTicketComment(c *echo.Context) error {
	actor, err := ticketActor(c)
	if err != nil {
		return httputil.RespondError(c, err)
	}
	id, err := httputil.ParseUintParam(c, "id")
	if err != nil {
		return httputil.RespondError(c, err)
	}
	var req ticketsvc.CommentRequest
	if err := bindStrict(c, &req); err != nil {
		return httputil.RespondError(c, err)
	}
	cm, err := h.ticket.AddComment(c.Request().Context(), actor, id, req)
	if err != nil {
		return httputil.RespondError(c, err)
	}
	return c.JSON(http.StatusCreated, httputil.Response{Data: cm})
}

// PATCH /api/tickets/:id/comments/:commentId
func (h *ticketHandler) updateTicketComment(c *echo.Context) error {
	actor, err := ticketActor(c)
	if err != nil {
		return httputil.RespondError(c, err)
	}
	id, cid, err := ticketAndCommentIDs(c)
	if err != nil {
		return httputil.RespondError(c, err)
	}
	var req ticketsvc.CommentRequest
	if err := bindStrict(c, &req); err != nil {
		return httputil.RespondError(c, err)
	}
	cm, err := h.ticket.EditComment(c.Request().Context(), actor, id, cid, req)
	if err != nil {
		return httputil.RespondError(c, err)
	}
	return c.JSON(http.StatusOK, httputil.Response{Data: cm})
}

// DELETE /api/tickets/:id/comments/:commentId
func (h *ticketHandler) deleteTicketComment(c *echo.Context) error {
	actor, err := ticketActor(c)
	if err != nil {
		return httputil.RespondError(c, err)
	}
	id, cid, err := ticketAndCommentIDs(c)
	if err != nil {
		return httputil.RespondError(c, err)
	}
	if err := h.ticket.DeleteComment(c.Request().Context(), actor, id, cid); err != nil {
		return httputil.RespondError(c, err)
	}
	return c.NoContent(http.StatusNoContent)
}

// GET /api/tickets/:id/events
func (h *ticketHandler) listTicketEvents(c *echo.Context) error {
	id, err := httputil.ParseUintParam(c, "id")
	if err != nil {
		return httputil.RespondError(c, err)
	}
	limit, offset, err := queryPage(c)
	if err != nil {
		return httputil.RespondError(c, err)
	}
	page, err := h.ticket.ListEvents(c.Request().Context(), id, limit, offset)
	if err != nil {
		return httputil.RespondError(c, err)
	}
	return c.JSON(http.StatusOK, httputil.Response{Data: page})
}

// --- 指派人、项目、统计 ---

const (
	ticketAssigneeQueryMinRunes = 2
	ticketAssigneeResultLimit   = 20
	ticketAssigneeHotLimit      = 10
)

// GET /api/tickets/assignees?q= — 工单指派人搜索（任意已登录用户可用）。
// q 去首尾空白后不足 2 个字符时返回近期热点指派人（最多 10 条，cache）；否则按用户名
// 不区分大小写包含匹配，仅返回状态正常的普通用户的 id 与 username，以 q 开头者排前，
// 最多 20 条，不返回总数。除全局限流外另有按用户的独立限流。
func (h *ticketHandler) listTicketAssignees(c *echo.Context) error {
	current, err := httputil.RequireSession(c)
	if err != nil {
		return err
	}
	if !h.userSearchLimiter.allow(current.UserID) {
		return httputil.RespondError(c, echo.NewHTTPError(http.StatusTooManyRequests, "too many requests"))
	}

	type userItem struct {
		ID       uint   `json:"id"`
		Username string `json:"username"`
	}
	toItems := func(users []model.User) []userItem {
		items := make([]userItem, 0, len(users))
		for _, u := range users {
			items = append(items, userItem{ID: u.ID, Username: u.Username})
		}
		return items
	}

	if h.user == nil {
		return c.JSON(http.StatusOK, map[string]any{"data": []userItem{}})
	}

	q := strings.TrimSpace(c.QueryParam("q"))
	if utf8.RuneCountInString(q) < ticketAssigneeQueryMinRunes {
		users, err := h.user.HotAssignees(c.Request().Context(), ticketAssigneeHotLimit)
		if err != nil {
			return httputil.RespondError(c, err)
		}
		return c.JSON(http.StatusOK, map[string]any{"data": toItems(users)})
	}

	users, err := h.user.SearchAssignees(c.Request().Context(), q, ticketAssigneeResultLimit)
	if err != nil {
		return httputil.RespondError(c, err)
	}

	return c.JSON(http.StatusOK, map[string]any{"data": toItems(users)}) // 无匹配时也返回 "data": []
}

// GET /api/tickets/projects — 未删除、未归档的项目
func (h *ticketHandler) listTicketProjects(c *echo.Context) error {
	limit, offset, err := queryPage(c)
	if err != nil {
		return httputil.RespondError(c, err)
	}
	page, err := h.ticket.ListProjects(c.Request().Context(), limit, offset, false, false)
	if err != nil {
		return httputil.RespondError(c, err)
	}
	return c.JSON(http.StatusOK, httputil.Response{Data: page})
}

// GET /api/tickets/summary
func (h *ticketHandler) getTicketSummary(c *echo.Context) error {
	projectID, err := queryID(c, "projectId")
	if err != nil {
		return httputil.RespondError(c, err)
	}
	days, err := queryInt(c, "days", ticketsvc.DefaultSummaryDays, 1, ticketsvc.MaxSummaryDays)
	if err != nil {
		return httputil.RespondError(c, err)
	}
	loc := time.Local
	if tz := c.QueryParam("tz"); tz != "" {
		if loc, err = time.LoadLocation(tz); err != nil || tz == "Local" {
			return httputil.RespondError(c, unprocessable("invalid tz"))
		}
	}
	var pid uint
	if projectID != nil {
		pid = *projectID
	}
	s, err := h.ticket.GetSummary(c.Request().Context(), pid, days, loc)
	if err != nil {
		return httputil.RespondError(c, err)
	}
	return c.JSON(http.StatusOK, httputil.Response{Data: s})
}
