package gitlabci

import (
	"fmt"
	"sort"
	"strconv"
	"strings"
)

func asMap(v any) (map[string]any, bool) {
	switch m := v.(type) {
	case map[string]any:
		return m, true
	case map[any]any:
		out := make(map[string]any, len(m))
		for k, val := range m {
			out[fmt.Sprint(k)] = val
		}
		return out, true
	default:
		return nil, false
	}
}

func asList(v any) []any {
	switch x := v.(type) {
	case []any:
		return x
	case nil:
		return nil
	default:
		return []any{v}
	}
}

func asString(v any) string {
	switch x := v.(type) {
	case nil:
		return ""
	case string:
		return x
	case bool:
		if x {
			return "true"
		}
		return "false"
	case int:
		return strconv.Itoa(x)
	case int64:
		return strconv.FormatInt(x, 10)
	case float64:
		if x == float64(int64(x)) {
			return strconv.FormatInt(int64(x), 10)
		}
		return strconv.FormatFloat(x, 'f', -1, 64)
	default:
		return fmt.Sprint(x)
	}
}

func asStringSlice(v any) []string {
	if v == nil {
		return nil
	}
	if s, ok := v.(string); ok {
		return []string{s}
	}
	var out []string
	for _, item := range asList(v) {
		out = append(out, scriptLine(item))
	}
	return out
}

// scriptLine stringifies a sequence item the way GitLab CI does.
// Unquoted commands that contain ": " are parsed as one-key YAML maps.
func scriptLine(v any) string {
	if s, ok := v.(string); ok {
		return s
	}
	if m, ok := asMap(v); ok && len(m) == 1 {
		for k, val := range m {
			if val == nil || asString(val) == "" {
				return k
			}
			return k + ": " + asString(val)
		}
	}
	if list := asList(v); len(list) > 0 && fmt.Sprintf("%T", v) != "string" {
		if _, isList := v.([]any); isList {
			var parts []string
			for _, item := range list {
				parts = append(parts, scriptLine(item))
			}
			return strings.Join(parts, "\n")
		}
	}
	return asString(v)
}

func asBool(v any, def bool) bool {
	if v == nil {
		return def
	}
	switch x := v.(type) {
	case bool:
		return x
	case string:
		switch strings.ToLower(x) {
		case "true", "yes", "on", "1":
			return true
		case "false", "no", "off", "0":
			return false
		}
	}
	return def
}

func asInt(v any, def int) int {
	switch x := v.(type) {
	case int:
		return x
	case int64:
		return int(x)
	case float64:
		return int(x)
	case string:
		n, err := strconv.Atoi(x)
		if err == nil {
			return n
		}
	}
	return def
}

func clone(v any) any {
	switch x := v.(type) {
	case map[string]any:
		out := make(map[string]any, len(x))
		for k, val := range x {
			out[k] = clone(val)
		}
		return out
	case []any:
		out := make([]any, len(x))
		for i, val := range x {
			out[i] = clone(val)
		}
		return out
	default:
		return v
	}
}

func sortedKeys(m map[string]any) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return keys
}

var reserved = map[string]bool{
	"default": true, "include": true, "stages": true, "variables": true,
	"workflow": true, "spec": true, "image": true, "services": true,
	"cache": true, "before_script": true, "after_script": true,
}

func isJobName(name string) bool {
	if name == "" || strings.HasPrefix(name, ".") || reserved[name] {
		return false
	}
	return true
}

func isHidden(name string) bool {
	return strings.HasPrefix(name, ".")
}

// MatchJobName reports whether a compiled job name is spec or a parallel/matrix child of spec.
func MatchJobName(name, spec string) bool {
	if name == spec {
		return true
	}
	return strings.HasPrefix(name, spec+":") || strings.HasPrefix(name, spec+" ")
}
