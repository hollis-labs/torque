package mcpadapter

import (
	"fmt"
	"strings"

	"github.com/hollis-labs/torque/internal/service"
	"github.com/mark3labs/mcp-go/mcp"
)

func reqQueryString(req mcp.CallToolRequest, key string) (string, *mcp.CallToolResult) {
	v, ok := req.GetArguments()[key]
	if !ok || v == nil {
		return "", nil
	}
	s, ok := v.(string)
	if !ok {
		res, _ := errResult(ErrCodeArgInvalid, fmt.Sprintf("%s must be a string", key), key)
		return "", res
	}
	return s, nil
}

func reqQueryInt(req mcp.CallToolRequest, key string) (int, *mcp.CallToolResult) {
	v, ok := req.GetArguments()[key]
	if !ok {
		return 0, nil
	}
	if v == nil {
		res, _ := errResult(ErrCodeArgInvalid, key+" must be an integer", key)
		return 0, res
	}
	n, _, err := reqTaskListInt(req, key)
	if err != nil {
		res, _ := errResult(ErrCodeArgInvalid, err.Error(), key)
		return 0, res
	}
	return n, nil
}

func reqQueryFloat(req mcp.CallToolRequest, key string) (*float64, *mcp.CallToolResult) {
	v, ok := req.GetArguments()[key]
	if !ok {
		return nil, nil
	}
	if v == nil {
		res, _ := errResult(ErrCodeArgInvalid, key+" must be a finite number", key)
		return nil, res
	}
	f, err := reqTaskListFloat(req, key)
	if err != nil {
		res, _ := errResult(ErrCodeArgInvalid, err.Error(), key)
		return nil, res
	}
	return &f, nil
}

func reqQueryBool(req mcp.CallToolRequest, key string) (bool, *mcp.CallToolResult) {
	v, ok := req.GetArguments()[key]
	if !ok || v == nil {
		return false, nil
	}
	switch b := v.(type) {
	case bool:
		return b, nil
	case string:
		switch strings.ToLower(strings.TrimSpace(b)) {
		case "", "false", "0", "no":
			return false, nil
		case "true", "1", "yes":
			return true, nil
		default:
			res, _ := errResult(ErrCodeArgInvalid, key+" must be true/false, 1/0, or yes/no", key)
			return false, res
		}
	default:
		res, _ := errResult(ErrCodeArgInvalid, key+" must be true/false, 1/0, or yes/no", key)
		return false, res
	}
}

func reqQueryCursor(req mcp.CallToolRequest) (service.CursorQuery, *mcp.CallToolResult) {
	limit, errRes := reqQueryInt(req, "limit")
	if errRes != nil {
		return service.CursorQuery{}, errRes
	}
	sortBy, errRes := reqQueryString(req, "sort_by")
	if errRes != nil {
		return service.CursorQuery{}, errRes
	}
	sortDir, errRes := reqQueryString(req, "sort_dir")
	if errRes != nil {
		return service.CursorQuery{}, errRes
	}
	cursor, errRes := reqQueryString(req, "cursor")
	if errRes != nil {
		return service.CursorQuery{}, errRes
	}
	return service.CursorQuery{Limit: limit, SortBy: sortBy, SortDir: sortDir, Cursor: cursor}, nil
}
