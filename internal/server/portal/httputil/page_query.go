package httputil

import (
	"fmt"
	"net/http"
	"strconv"

	"github.com/labstack/echo/v5"
)

// TicketMaxListOffset 工单列表 offset 上限（keyset 深翻另走 afterSort/afterId）。
const TicketMaxListOffset = 10000

// PageQueryMode 是 Portal 列表 limit/offset 的三种 HTTP 解析策略。
type PageQueryMode int

const (
	// PageQueryClamp：缺省/非法 limit 静默压到默认与上下界（多数目录列表 ParsePage）。
	PageQueryClamp PageQueryMode = iota
	// PageQueryStrict422：越界或非法数字返回 422 invalid limit/offset（工单域）。
	PageQueryStrict422
	// PageQueryOptional：参数缺省为 0（由 service/repo 再 ResolveListLimit）；出现但非法返回 400（审计 executions）。
	PageQueryOptional
)

// PageQueryOpts 配置 ParsePageQuery；零值 DefaultLimit/MaxLimit 时使用 Portal 默认 500/2000。
type PageQueryOpts struct {
	Mode         PageQueryMode
	DefaultLimit int
	MaxLimit     int
	MaxOffset    int // Strict422 时 offset 上限；0 表示仅保证 offset >= 0
}

// DefaultPageQueryOpts 返回带 Portal 默认 limit 上下界的 opts。
func DefaultPageQueryOpts(mode PageQueryMode) PageQueryOpts {
	o := PageQueryOpts{
		Mode:         mode,
		DefaultLimit: DefaultPageLimit,
		MaxLimit:     MaxPageLimit,
	}
	if mode == PageQueryStrict422 {
		o.MaxOffset = TicketMaxListOffset
	}
	return o
}

// ParsePageQuery 按模式解析 limit/offset。
func ParsePageQuery(c *echo.Context, opts PageQueryOpts) (Pagination, error) {
	if opts.DefaultLimit <= 0 {
		opts.DefaultLimit = DefaultPageLimit
	}
	if opts.MaxLimit <= 0 {
		opts.MaxLimit = MaxPageLimit
	}
	switch opts.Mode {
	case PageQueryClamp:
		return ParsePagination(c, opts.DefaultLimit, opts.MaxLimit), nil
	case PageQueryStrict422:
		return parsePageStrict422(c, opts)
	case PageQueryOptional:
		return parsePageOptional(c)
	default:
		return ParsePagination(c, opts.DefaultLimit, opts.MaxLimit), nil
	}
}

// ParsePage 使用 PageQueryClamp + Portal 默认上下界。
func ParsePage(c *echo.Context) Pagination {
	p, _ := ParsePageQuery(c, DefaultPageQueryOpts(PageQueryClamp))
	return p
}

// ParseTicketPage 工单列表 limit/offset（Strict422，offset 上限 TicketMaxListOffset）。
func ParseTicketPage(c *echo.Context) (Pagination, error) {
	return ParsePageQuery(c, DefaultPageQueryOpts(PageQueryStrict422))
}

// ParseOptionalListPage 审计 executions 等：缺省 limit/offset 为 0，由下游 ResolveListLimit。
func ParseOptionalListPage(c *echo.Context) (Pagination, error) {
	return ParsePageQuery(c, DefaultPageQueryOpts(PageQueryOptional))
}

// ParseQueryIntInRange 解析可选整数 Query；缺省 def；越界或非法返回 422 invalid <name>。
func ParseQueryIntInRange(c *echo.Context, name string, def, lo, hi int) (int, error) {
	raw := c.QueryParam(name)
	if raw == "" {
		return def, nil
	}
	v, err := strconv.Atoi(raw)
	if err != nil || v < lo || v > hi {
		return 0, echo.NewHTTPError(http.StatusUnprocessableEntity, "invalid "+name)
	}
	return v, nil
}

func parsePageStrict422(c *echo.Context, opts PageQueryOpts) (Pagination, error) {
	limit, err := ParseQueryIntInRange(c, "limit", opts.DefaultLimit, 1, opts.MaxLimit)
	if err != nil {
		return Pagination{}, err
	}
	maxOff := opts.MaxOffset
	if maxOff <= 0 {
		maxOff = TicketMaxListOffset
	}
	offset, err := ParseQueryIntInRange(c, "offset", 0, 0, maxOff)
	if err != nil {
		return Pagination{}, err
	}
	return Pagination{Limit: limit, Offset: offset}, nil
}

func parsePageOptional(c *echo.Context) (Pagination, error) {
	var p Pagination
	if raw := c.QueryParam("limit"); raw != "" {
		v, err := strconv.Atoi(raw)
		if err != nil {
			return Pagination{}, echo.NewHTTPError(http.StatusBadRequest, fmt.Sprintf("invalid limit: %s", raw))
		}
		p.Limit = v
	}
	if raw := c.QueryParam("offset"); raw != "" {
		v, err := strconv.Atoi(raw)
		if err != nil {
			return Pagination{}, echo.NewHTTPError(http.StatusBadRequest, fmt.Sprintf("invalid offset: %s", raw))
		}
		p.Offset = v
	}
	return p, nil
}
