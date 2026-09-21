package gitlabci

import (
	"fmt"
	"strings"
)

type reference struct {
	Path []string
}

func resolveReferences(root map[string]any) (map[string]any, error) {
	v, err := walkRefs(root, root, nil)
	if err != nil {
		return nil, err
	}
	m, _ := asMap(v)
	return m, nil
}

func walkRefs(node any, root map[string]any, stack []string) (any, error) {
	switch n := node.(type) {
	case reference:
		key := strings.Join(n.Path, ".")
		for _, s := range stack {
			if s == key {
				return nil, fmt.Errorf("cyclic !reference involving %s", key)
			}
		}
		got, err := lookupPath(root, n.Path)
		if err != nil {
			return nil, err
		}
		return walkRefs(got, root, append(stack, key))
	case map[string]any:
		out := make(map[string]any, len(n))
		for k, v := range n {
			rv, err := walkRefs(v, root, stack)
			if err != nil {
				return nil, err
			}
			out[k] = rv
		}
		return out, nil
	case []any:
		var out []any
		for _, item := range n {
			rv, err := walkRefs(item, root, stack)
			if err != nil {
				return nil, err
			}
			if list, ok := rv.([]any); ok {
				out = append(out, list...)
				continue
			}
			out = append(out, rv)
		}
		return out, nil
	default:
		return n, nil
	}
}

func lookupPath(root map[string]any, path []string) (any, error) {
	var cur any = root
	for i, p := range path {
		m, ok := asMap(cur)
		if !ok {
			return nil, fmt.Errorf("!reference %v: %s is not a mapping", path, strings.Join(path[:i], "."))
		}
		next, ok := m[p]
		if !ok {
			return nil, fmt.Errorf("!reference %v: %q not found", path, p)
		}
		cur = next
	}
	return clone(cur), nil
}
