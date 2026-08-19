package httputil

import (
	"cmp"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strconv"
	"strings"

	"github.com/labstack/echo/v5"
)

// Pagination holds limit and offset parameters for paginated requests.
type Pagination struct {
	Limit  int
	Offset int
}

// ParsePagination parses "limit" and "offset" query parameters with safe defaults and clamps.
// defaultLimit is used when limit is missing/0. maxLimit is the upper bound.
func ParsePagination(c *echo.Context, defaultLimit, maxLimit int) Pagination {
	limit, _ := strconv.Atoi(c.QueryParam("limit"))
	limit = cmp.Or(limit, defaultLimit)
	limit = min(maxLimit, max(1, limit))

	offset, _ := strconv.Atoi(c.QueryParam("offset"))
	offset = max(0, offset)

	return Pagination{
		Limit:  limit,
		Offset: offset,
	}
}

// ParseUintParam parses a positive integer parameter from the request path and validates it.
func ParseUintParam(c *echo.Context, paramName string) (uint, error) {
	valStr := c.Param(paramName)
	if valStr == "" {
		return 0, echo.NewHTTPError(http.StatusBadRequest, "missing ["+paramName+"] parameter")
	}

	val, err := strconv.ParseUint(valStr, 10, 0)
	if err != nil || val == 0 {
		return 0, echo.NewHTTPError(http.StatusBadRequest, "param ["+paramName+"] must be a positive integer")
	}

	return uint(val), nil
}

// ParseOptionalIntQuery parses an optional positive integer parameter from the query string.
func ParseOptionalIntQuery(c *echo.Context, paramName string) (int, error) {
	valStr := c.QueryParam(paramName)
	if valStr == "" {
		return 0, nil
	}
	val, err := strconv.Atoi(valStr)
	if err != nil || val <= 0 {
		return 0, echo.NewHTTPError(http.StatusBadRequest, "query ["+paramName+"] must be a positive integer")
	}
	return val, nil
}

// ParseOptionalUintQuery parses an optional positive integer parameter from the query string.
func ParseOptionalUintQuery(c *echo.Context, paramName string) (uint, error) {
	valStr := c.QueryParam(paramName)
	if valStr == "" {
		return 0, nil
	}
	val, err := strconv.ParseUint(valStr, 10, 64)
	if err != nil || val == 0 {
		return 0, echo.NewHTTPError(http.StatusBadRequest, "query ["+paramName+"] must be a positive integer")
	}
	return uint(val), nil
}

// IDList represents a slice of positive integer IDs that can be unmarshaled
// from either a JSON array of numbers ([1, 2]) or a JSON array of strings (["1", "2"]).
// Whitespace is trimmed, invalid values return an error, and duplicates are removed.
type IDList []uint

func (l *IDList) UnmarshalJSON(data []byte) error {
	// 1. Try unmarshaling as []uint directly
	var uints []uint
	if err := json.Unmarshal(data, &uints); err == nil {
		seen := make(map[uint]struct{}, len(uints))
		res := make([]uint, 0, len(uints))
		for _, u := range uints {
			if u == 0 {
				continue
			}
			if _, ok := seen[u]; !ok {
				seen[u] = struct{}{}
				res = append(res, u)
			}
		}
		*l = res
		return nil
	}

	// 2. Try unmarshaling as []string
	var stringsList []string
	if err := json.Unmarshal(data, &stringsList); err == nil {
		seen := make(map[uint]struct{}, len(stringsList))
		res := make([]uint, 0, len(stringsList))
		for _, s := range stringsList {
			s = strings.TrimSpace(s)
			if s == "" {
				continue
			}
			parsed, err := strconv.ParseUint(s, 10, 64)
			if err != nil || parsed == 0 {
				return fmt.Errorf("invalid id %q: must be a positive integer", s)
			}
			uid := uint(parsed)
			if _, ok := seen[uid]; !ok {
				seen[uid] = struct{}{}
				res = append(res, uid)
			}
		}
		*l = res
		return nil
	}

	return errors.New("ids must be an array of positive integers or strings")
}
