package mcpadapter

// go-mcp tool-schema DSL shim: go-mcp's server.Tool takes a raw JSON-schema
// InputSchema (any) rather than mark3labs/mcp-go's typed builder. This
// reproduces the small subset of that builder Torque used (~830 call sites
// across every *_tools.go file) so the CW-20260917-0014 migration is a
// token rename at each registration site rather than a hand rewrite of
// every schema. See tool_annotations.go for the per-tool hint table this
// wires into.

type propOpt func(schema map[string]any)

func desc(d string) propOpt {
	return func(s map[string]any) { s["description"] = d }
}

// requiredMarkerKey is a private sentinel a property builder (withString et
// al.) strips back out of the built property schema after promoting the
// property name to the tool's top-level `required` list. required() has no
// standalone meaning outside that promotion.
const requiredMarkerKey = "__torque_required__"

func required() propOpt {
	return func(s map[string]any) { s[requiredMarkerKey] = true }
}

func items(schema map[string]any) propOpt {
	return func(s map[string]any) { s["items"] = schema }
}

type toolSpec struct {
	Name        string
	Description string
	Properties  map[string]any
	Required    []string
}

type toolOpt func(*toolSpec)

func withDescription(d string) toolOpt {
	return func(t *toolSpec) { t.Description = d }
}

func property(name, kind string, opts ...propOpt) toolOpt {
	s := map[string]any{"type": kind}
	for _, opt := range opts {
		opt(s)
	}
	req := false
	if _, ok := s[requiredMarkerKey]; ok {
		req = true
		delete(s, requiredMarkerKey)
	}
	return func(t *toolSpec) {
		if t.Properties == nil {
			t.Properties = map[string]any{}
		}
		t.Properties[name] = s
		if req {
			t.Required = append(t.Required, name)
		}
	}
}

func withString(name string, opts ...propOpt) toolOpt  { return property(name, "string", opts...) }
func withBoolean(name string, opts ...propOpt) toolOpt { return property(name, "boolean", opts...) }
func withObject(name string, opts ...propOpt) toolOpt  { return property(name, "object", opts...) }
func withArray(name string, opts ...propOpt) toolOpt   { return property(name, "array", opts...) }

func newTool(name string, opts ...toolOpt) toolSpec {
	t := toolSpec{Name: name}
	for _, opt := range opts {
		opt(&t)
	}
	return t
}
