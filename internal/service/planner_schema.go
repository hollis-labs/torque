package service

import (
	"embed"
	"encoding/json"
	"fmt"
	"math"
	"sort"
	"strings"
	"sync"
)

// The planner's item schemas are the portfolio-tracker schemas, embedded
// unchanged. Validation is the same small JSON Schema subset the tracker's
// service used: type, enum, const, required, properties, items,
// additionalProperties:false, minimum, maximum and "#/$defs/x" refs. Fields a
// schema does not name are allowed.

//go:embed plannerschema/*.schema.json
var plannerSchemaFS embed.FS

var (
	plannerSchemaOnce  sync.Once
	plannerSchemaCache map[string]map[string]any
	plannerSchemaErr   error
)

func plannerSchema(db string) (map[string]any, error) {
	plannerSchemaOnce.Do(func() {
		plannerSchemaCache = map[string]map[string]any{}
		entries, err := plannerSchemaFS.ReadDir("plannerschema")
		if err != nil {
			plannerSchemaErr = err
			return
		}
		for _, e := range entries {
			raw, err := plannerSchemaFS.ReadFile("plannerschema/" + e.Name())
			if err != nil {
				plannerSchemaErr = err
				return
			}
			var m map[string]any
			if err := json.Unmarshal(raw, &m); err != nil {
				plannerSchemaErr = fmt.Errorf("%s: %w", e.Name(), err)
				return
			}
			plannerSchemaCache[strings.TrimSuffix(e.Name(), ".schema.json")] = m
		}
	})
	if plannerSchemaErr != nil {
		return nil, plannerSchemaErr
	}
	s, ok := plannerSchemaCache[db]
	if !ok {
		return nil, &NotFoundError{Entity: "planner database", ID: db}
	}
	return s, nil
}

// itemSchema returns the schema of one item of db.
func plannerItemSchema(db string) (map[string]any, map[string]any, error) {
	root, err := plannerSchema(db)
	if err != nil {
		return nil, nil, err
	}
	props, _ := root["properties"].(map[string]any)
	items, _ := props["items"].(map[string]any)
	item, _ := items["items"].(map[string]any)
	if item == nil {
		return nil, nil, fmt.Errorf("planner schema for %s has no item schema", db)
	}
	return root, item, nil
}

// PlannerFieldError is one validation failure: a path and what is wrong.
type PlannerFieldError struct {
	Path    string `json:"path"`
	Message string `json:"message"`
}

func plannerJSONType(v any) string {
	switch v.(type) {
	case nil:
		return "null"
	case []any:
		return "array"
	case map[string]any:
		return "object"
	case string:
		return "string"
	case bool:
		return "boolean"
	case float64:
		return "number"
	}
	return fmt.Sprintf("%T", v)
}

func plannerIsType(v any, t string) bool {
	switch t {
	case "integer":
		f, ok := v.(float64)
		return ok && f == math.Trunc(f) && !math.IsInf(f, 0)
	case "number":
		f, ok := v.(float64)
		return ok && !math.IsInf(f, 0) && !math.IsNaN(f)
	}
	return plannerJSONType(v) == t
}

func plannerValidate(schema map[string]any, value any, root map[string]any, path string) []PlannerFieldError {
	var errs []PlannerFieldError
	add := func(p, m string) {
		if p == "" {
			p = "(root)"
		}
		errs = append(errs, PlannerFieldError{Path: p, Message: m})
	}
	if schema == nil {
		return nil
	}
	if ref, ok := schema["$ref"].(string); ok {
		var target any = root
		for _, k := range strings.Split(strings.TrimPrefix(ref, "#/"), "/") {
			m, _ := target.(map[string]any)
			target = m[k]
		}
		t, _ := target.(map[string]any)
		return plannerValidate(t, value, root, path)
	}
	if c, ok := schema["const"]; ok {
		if cb, _ := json.Marshal(c); true {
			if vb, _ := json.Marshal(value); string(cb) != string(vb) {
				add(path, "must be "+string(cb))
			}
		}
	}
	if enum, ok := schema["enum"].([]any); ok {
		found := false
		names := make([]string, 0, len(enum))
		for _, e := range enum {
			names = append(names, fmt.Sprint(e))
			if e == value {
				found = true
			}
		}
		if !found {
			add(path, "must be one of "+strings.Join(names, ", "))
		}
	}
	if t, ok := schema["type"].(string); ok && !plannerIsType(value, t) {
		add(path, "must be "+t)
		return errs
	}
	if f, ok := value.(float64); ok {
		if min, ok := schema["minimum"].(float64); ok && f < min {
			add(path, fmt.Sprintf("must be >= %v", min))
		}
		if max, ok := schema["maximum"].(float64); ok && f > max {
			add(path, fmt.Sprintf("must be <= %v", max))
		}
	}
	if arr, ok := value.([]any); ok {
		if items, ok := schema["items"].(map[string]any); ok {
			for i, v := range arr {
				errs = append(errs, plannerValidate(items, v, root, fmt.Sprintf("%s[%d]", path, i))...)
			}
		}
	}
	if obj, ok := value.(map[string]any); ok {
		join := func(k string) string {
			if path == "" {
				return k
			}
			return path + "." + k
		}
		if req, ok := schema["required"].([]any); ok {
			for _, r := range req {
				k, _ := r.(string)
				if v, present := obj[k]; !present || v == nil || v == "" {
					add(join(k), "is required")
				}
			}
		}
		props, _ := schema["properties"].(map[string]any)
		keys := make([]string, 0, len(obj))
		for k := range obj {
			keys = append(keys, k)
		}
		sort.Strings(keys)
		for _, k := range keys {
			if p, ok := props[k].(map[string]any); ok {
				errs = append(errs, plannerValidate(p, obj[k], root, join(k))...)
			} else if ap, ok := schema["additionalProperties"].(bool); ok && !ap {
				add(join(k), "unknown field")
			}
		}
	}
	return errs
}

// PlannerValidateItem validates one item against its database's schema.
func PlannerValidateItem(db string, item map[string]any) ([]PlannerFieldError, error) {
	root, itemSchema, err := plannerItemSchema(db)
	if err != nil {
		return nil, err
	}
	// Round-trip so numbers are float64 whatever the caller passed.
	b, err := json.Marshal(item)
	if err != nil {
		return nil, err
	}
	var norm any
	if err := json.Unmarshal(b, &norm); err != nil {
		return nil, err
	}
	return plannerValidate(itemSchema, norm, root, ""), nil
}

// PlannerLinkKinds are the external link kinds an item of db accepts.
func PlannerLinkKinds(db string) ([]string, error) {
	root, _, err := plannerItemSchema(db)
	if err != nil {
		return nil, err
	}
	defs, _ := root["$defs"].(map[string]any)
	link, _ := defs["link"].(map[string]any)
	props, _ := link["properties"].(map[string]any)
	kind, _ := props["kind"].(map[string]any)
	enum, _ := kind["enum"].([]any)
	out := make([]string, 0, len(enum))
	for _, e := range enum {
		out = append(out, fmt.Sprint(e))
	}
	return out, nil
}

// PlannerSchemaInfo is the compact description of one database's item
// schema, for forms and agents.
type PlannerSchemaInfo struct {
	DB          string                    `json:"db"`
	ID          string                    `json:"id"`
	Description string                    `json:"description"`
	Required    []string                  `json:"required"`
	Fields      map[string]map[string]any `json:"fields"`
	Enums       map[string][]string       `json:"enums"`
	LinkKinds   []string                  `json:"link_kinds"`
}

func strList(v any) []string {
	arr, _ := v.([]any)
	out := make([]string, 0, len(arr))
	for _, e := range arr {
		out = append(out, fmt.Sprint(e))
	}
	return out
}

func plannerDescribe(db string) (*PlannerSchemaInfo, error) {
	root, item, err := plannerItemSchema(db)
	if err != nil {
		return nil, err
	}
	info := &PlannerSchemaInfo{
		DB:       db,
		Required: strList(item["required"]),
		Fields:   map[string]map[string]any{},
		Enums:    map[string][]string{},
	}
	info.ID, _ = root["$id"].(string)
	info.Description, _ = root["description"].(string)
	props, _ := item["properties"].(map[string]any)
	for name, raw := range props {
		f, _ := raw.(map[string]any)
		out := map[string]any{}
		for _, k := range []string{"type", "const", "enum", "minimum", "maximum", "format", "description"} {
			if v, ok := f[k]; ok {
				out[k] = v
			}
		}
		ref := func(m map[string]any) string {
			r, _ := m["$ref"].(string)
			return strings.TrimPrefix(r, "#/$defs/")
		}
		if items, ok := f["items"].(map[string]any); ok {
			switch {
			case ref(items) != "":
				out["items"] = ref(items)
			case items["type"] != nil:
				out["items"] = items["type"]
			default:
				out["items"] = "any"
			}
			if e, ok := items["enum"]; ok {
				info.Enums[name] = strList(e)
			}
		}
		if r := ref(f); r != "" {
			out["ref"] = r
		}
		if e, ok := f["enum"]; ok {
			info.Enums[name] = strList(e)
		}
		info.Fields[name] = out
	}
	info.LinkKinds, _ = PlannerLinkKinds(db)
	return info, nil
}
