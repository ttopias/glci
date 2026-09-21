package gitlabci

import (
	"bytes"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"strings"

	"gopkg.in/yaml.v3"
)

type specHeader struct {
	Inputs map[string]inputSpec
}

type inputSpec struct {
	Default     *string
	Description string
	Options     []string
	Regex       string
	Type        string
}

type includeSpec struct {
	Local     string
	Project   string
	File      []string
	Ref       string
	Remote    string
	Template  string
	Component string
	Inputs    map[string]string
	Rules     []any
}

func decodeYAML(data []byte) (header specHeader, doc map[string]any, err error) {
	dec := yaml.NewDecoder(bytes.NewReader(data))
	var nodes []*yaml.Node
	for {
		var n yaml.Node
		if e := dec.Decode(&n); e != nil {
			if e == io.EOF {
				break
			}
			return specHeader{}, nil, e
		}
		nodes = append(nodes, &n)
	}
	if len(nodes) == 0 {
		return specHeader{}, map[string]any{}, nil
	}
	if len(nodes) == 1 {
		v, err := nodeToAny(nodes[0])
		if err != nil {
			return specHeader{}, nil, err
		}
		m, _ := asMap(v)
		if m == nil {
			m = map[string]any{}
		}
		if spec, ok := asMap(m["spec"]); ok {
			header = parseSpec(spec)
			delete(m, "spec")
		}
		return header, m, nil
	}
	hv, err := nodeToAny(nodes[0])
	if err != nil {
		return specHeader{}, nil, err
	}
	hm, _ := asMap(hv)
	if spec, ok := asMap(hm["spec"]); ok {
		header = parseSpec(spec)
	}
	dv, err := nodeToAny(nodes[1])
	if err != nil {
		return specHeader{}, nil, err
	}
	m, _ := asMap(dv)
	if m == nil {
		m = map[string]any{}
	}
	return header, m, nil
}

func parseSpec(spec map[string]any) specHeader {
	h := specHeader{Inputs: map[string]inputSpec{}}
	inputs, _ := asMap(spec["inputs"])
	for name, raw := range inputs {
		var in inputSpec
		if m, ok := asMap(raw); ok {
			if v, exists := m["default"]; exists {
				s := asString(v)
				in.Default = &s
			}
			in.Description = asString(m["description"])
			in.Options = asStringSlice(m["options"])
			in.Regex = asString(m["regex"])
			in.Type = asString(m["type"])
		} else if raw == nil {
			// mandatory input with no metadata
		} else {
			s := asString(raw)
			in.Default = &s
		}
		h.Inputs[name] = in
	}
	return h
}

func nodeToAny(n *yaml.Node) (any, error) {
	if n == nil {
		return nil, nil
	}
	if n.Kind == yaml.DocumentNode && len(n.Content) > 0 {
		return nodeToAny(n.Content[0])
	}
	if n.Tag == "!reference" || strings.HasSuffix(n.Tag, ":reference") {
		var path []string
		for _, c := range n.Content {
			path = append(path, c.Value)
		}
		if len(path) == 0 {
			var seq []any
			if err := n.Decode(&seq); err == nil {
				for _, v := range seq {
					path = append(path, asString(v))
				}
			}
		}
		return reference{Path: path}, nil
	}
	switch n.Kind {
	case yaml.MappingNode:
		out := map[string]any{}
		for i := 0; i+1 < len(n.Content); i += 2 {
			k := n.Content[i].Value
			v, err := nodeToAny(n.Content[i+1])
			if err != nil {
				return nil, err
			}
			if k == "<<" {
				if m, ok := asMap(v); ok {
					out = mergeConfig(out, m)
					continue
				}
			}
			out[k] = v
		}
		return out, nil
	case yaml.SequenceNode:
		out := make([]any, 0, len(n.Content))
		for _, c := range n.Content {
			v, err := nodeToAny(c)
			if err != nil {
				return nil, err
			}
			out = append(out, v)
		}
		return out, nil
	case yaml.ScalarNode:
		var v any
		if err := n.Decode(&v); err != nil {
			return n.Value, nil
		}
		return v, nil
	case yaml.AliasNode:
		return nodeToAny(n.Alias)
	default:
		return nil, nil
	}
}

var inputPat = regexp.MustCompile(`\$\[\[\s*inputs\.([A-Za-z0-9_-]+)\s*\]\]`)

func interpolateInputs(v any, inputs map[string]string) any {
	switch n := v.(type) {
	case string:
		return inputPat.ReplaceAllStringFunc(n, func(m string) string {
			sub := inputPat.FindStringSubmatch(m)
			if len(sub) < 2 {
				return m
			}
			return inputs[sub[1]]
		})
	case map[string]any:
		out := make(map[string]any, len(n))
		for k, val := range n {
			out[interpolateInputs(k, inputs).(string)] = interpolateInputs(val, inputs)
		}
		return out
	case []any:
		out := make([]any, len(n))
		for i, val := range n {
			out[i] = interpolateInputs(val, inputs)
		}
		return out
	default:
		return n
	}
}

func applyInputs(header specHeader, provided map[string]string) (map[string]string, error) {
	out := map[string]string{}
	for name, spec := range header.Inputs {
		val, ok := provided[name]
		if !ok {
			if spec.Default != nil {
				val = *spec.Default
			} else {
				return nil, fmt.Errorf("missing required input %q", name)
			}
		}
		if spec.Regex != "" {
			re, err := regexp.Compile(spec.Regex)
			if err != nil {
				return nil, fmt.Errorf("input %s regex: %w", name, err)
			}
			if !re.MatchString(val) {
				return nil, fmt.Errorf("input %s value %q does not match %s", name, val, spec.Regex)
			}
		}
		if len(spec.Options) > 0 {
			found := false
			for _, o := range spec.Options {
				if o == val {
					found = true
					break
				}
			}
			if !found {
				return nil, fmt.Errorf("input %s value %q is not one of %v", name, val, spec.Options)
			}
		}
		out[name] = val
	}
	for k, v := range provided {
		if _, ok := out[k]; !ok {
			out[k] = v
		}
	}
	return out, nil
}

func normalizeIncludes(v any) ([]includeSpec, error) {
	if v == nil {
		return nil, nil
	}
	var out []includeSpec
	for _, item := range asList(v) {
		if s, ok := item.(string); ok {
			out = append(out, includeSpec{Local: s})
			continue
		}
		m, ok := asMap(item)
		if !ok {
			return nil, fmt.Errorf("invalid include entry: %T", item)
		}
		spec := includeSpec{
			Local:     asString(m["local"]),
			Project:   asString(m["project"]),
			Ref:       asString(m["ref"]),
			Remote:    asString(m["remote"]),
			Template:  asString(m["template"]),
			Component: asString(m["component"]),
			Inputs:    stringMap(m["inputs"]),
			Rules:     asList(m["rules"]),
		}
		if spec.Local == "" && spec.Project == "" && spec.Remote == "" && spec.Template == "" && spec.Component == "" {
			if f := asString(m["file"]); f != "" && spec.Project == "" {
				spec.Local = f
			}
		}
		if files, ok := m["file"]; ok {
			spec.File = asStringSlice(files)
		}
		out = append(out, spec)
	}
	return out, nil
}

func stringMap(v any) map[string]string {
	m, ok := asMap(v)
	if !ok {
		return nil
	}
	out := make(map[string]string, len(m))
	for k, val := range m {
		out[k] = asString(val)
	}
	return out
}

func loadYAMLFile(path string) (specHeader, map[string]any, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return specHeader{}, nil, err
	}
	return decodeYAML(data)
}

func joinLocal(root, rel string) string {
	rel = strings.TrimPrefix(rel, "/")
	return filepath.Join(root, filepath.FromSlash(rel))
}

func pathUnder(root, p string) error {
	absRoot, err := filepath.Abs(root)
	if err != nil {
		return err
	}
	absP, err := filepath.Abs(p)
	if err != nil {
		return err
	}
	rel, err := filepath.Rel(absRoot, absP)
	if err != nil || rel == ".." || strings.HasPrefix(rel, ".."+string(os.PathSeparator)) {
		return fmt.Errorf("%s is outside %s", p, root)
	}
	return nil
}
