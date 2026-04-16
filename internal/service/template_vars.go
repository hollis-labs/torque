package service

import (
	"fmt"
	"regexp"
	"strings"
)

// varPattern matches {{identifier}} where identifier follows Go naming
// rules (letter or underscore followed by letters/digits/underscores).
// Whitespace, digits-first, and punctuation inside braces are left as
// literal text — useful when a templated value legitimately contains
// double-brace text that isn't a variable reference.
var varPattern = regexp.MustCompile(`\{\{([a-zA-Z_][a-zA-Z0-9_]*)\}\}`)

// ResolveVars performs flat {{var}} substitution in s. Unresolved variables
// (referenced but not present in vars) cause an error listing the missing
// names — callers map this to a 422 response. No expressions, no defaults,
// no escapes; richer templating is deferred to BLG-032.
func ResolveVars(s string, vars map[string]string) (string, error) {
	var missing []string
	out := varPattern.ReplaceAllStringFunc(s, func(match string) string {
		name := strings.TrimSuffix(strings.TrimPrefix(match, "{{"), "}}")
		v, ok := vars[name]
		if !ok {
			missing = append(missing, name)
			return match
		}
		return v
	})
	if len(missing) > 0 {
		return "", fmt.Errorf("unresolved variables: %v", missing)
	}
	return out, nil
}

// ResolveVarsInMap recursively walks m and applies ResolveVars to every
// string leaf. Map and slice containers are traversed; non-string scalars
// (numbers, bools, nil) are returned unchanged. Used to resolve
// metadata_template values when instantiating a template.
func ResolveVarsInMap(m map[string]any, vars map[string]string) (map[string]any, error) {
	out := make(map[string]any, len(m))
	for k, v := range m {
		resolved, err := resolveAny(v, vars)
		if err != nil {
			return nil, err
		}
		out[k] = resolved
	}
	return out, nil
}

func resolveAny(v any, vars map[string]string) (any, error) {
	switch x := v.(type) {
	case string:
		return ResolveVars(x, vars)
	case map[string]any:
		return ResolveVarsInMap(x, vars)
	case []any:
		out := make([]any, len(x))
		for i, item := range x {
			r, err := resolveAny(item, vars)
			if err != nil {
				return nil, err
			}
			out[i] = r
		}
		return out, nil
	default:
		return v, nil
	}
}
