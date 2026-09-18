package mcpadapter

import (
	"fmt"
	"strings"

	"github.com/hollis-labs/torque/internal/service"
)

func reqQueryString(req map[string]any, key string) (string, error) {
	v, ok := req[key]
	if !ok || v == nil {
		return "", nil
	}
	s, ok := v.(string)
	if !ok {
		return "", argError(ErrCodeArgInvalid, fmt.Sprintf("%s must be a string", key), key)
	}
	return s, nil
}

func reqQueryInt(req map[string]any, key string) (int, error) {
	v, ok := req[key]
	if !ok {
		return 0, nil
	}
	if v == nil {
		return 0, argError(ErrCodeArgInvalid, key+" must be an integer", key)
	}
	n, _, err := reqTaskListInt(req, key)
	if err != nil {
		return 0, argError(ErrCodeArgInvalid, err.Error(), key)
	}
	return n, nil
}

func reqQueryFloat(req map[string]any, key string) (*float64, error) {
	v, ok := req[key]
	if !ok {
		return nil, nil
	}
	if v == nil {
		return nil, argError(ErrCodeArgInvalid, key+" must be a finite number", key)
	}
	f, err := reqTaskListFloat(req, key)
	if err != nil {
		return nil, argError(ErrCodeArgInvalid, err.Error(), key)
	}
	return &f, nil
}

func reqQueryBool(req map[string]any, key string) (bool, error) {
	v, ok := req[key]
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
			return false, argError(ErrCodeArgInvalid, key+" must be true/false, 1/0, or yes/no", key)
		}
	default:
		return false, argError(ErrCodeArgInvalid, key+" must be true/false, 1/0, or yes/no", key)
	}
}

func reqExactBool(req map[string]any, key string) (bool, error) {
	v, ok := req[key]
	if !ok {
		return false, nil
	}
	b, ok := v.(bool)
	if !ok {
		return false, argError(ErrCodeArgInvalid, key+" must be a boolean", key)
	}
	return b, nil
}

func reqQueryCursor(req map[string]any) (service.CursorQuery, error) {
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
