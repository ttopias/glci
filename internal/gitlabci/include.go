package gitlabci

import (
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"
)

type LoadOptions struct {
	Root         string
	File         string
	Projects     map[string]string
	Components   map[string]string
	TemplatesDir string
	AllowRemote  bool
	Inputs       map[string]string
	Vars         map[string]string
	EvalInclude  func(rules []any) bool
}

func Load(opts LoadOptions) (map[string]any, error) {
	if opts.File == "" {
		opts.File = ".gitlab-ci.yml"
	}
	if opts.Root == "" {
		opts.Root = "."
	}
	ctx := &loadCtx{
		opts:   opts,
		seen:   map[string]int{},
		maxInc: 150,
		http:   &http.Client{Timeout: 15 * time.Second},
	}
	abs := opts.File
	if !filepath.IsAbs(abs) {
		abs = filepath.Join(opts.Root, opts.File)
	}
	return ctx.loadPath(abs, opts.Inputs)
}

type loadCtx struct {
	opts   LoadOptions
	seen   map[string]int
	count  int
	maxInc int
	http   *http.Client
}

func (c *loadCtx) loadPath(path string, inputs map[string]string) (map[string]any, error) {
	header, doc, err := loadYAMLFile(path)
	if err != nil {
		return nil, fmt.Errorf("load %s: %w", path, err)
	}
	resolved, err := applyInputs(header, inputs)
	if err != nil {
		return nil, fmt.Errorf("%s: %w", path, err)
	}
	doc, _ = interpolateInputs(doc, resolved).(map[string]any)
	return c.resolveIncludes(filepath.Dir(path), doc)
}

func (c *loadCtx) resolveIncludes(base string, doc map[string]any) (map[string]any, error) {
	incs, err := normalizeIncludes(doc["include"])
	if err != nil {
		return nil, err
	}
	delete(doc, "include")
	merged := map[string]any{}
	for _, inc := range incs {
		if len(inc.Rules) > 0 && c.opts.EvalInclude != nil && !c.opts.EvalInclude(inc.Rules) {
			continue
		}
		children, err := c.loadInclude(base, inc)
		if err != nil {
			return nil, err
		}
		for _, child := range children {
			resolved, err := c.resolveIncludes(child.dir, child.doc)
			if err != nil {
				return nil, err
			}
			merged = mergeConfig(merged, resolved)
		}
	}
	return mergeConfig(merged, doc), nil
}

type loadedDoc struct {
	dir string
	doc map[string]any
}

func (c *loadCtx) loadInclude(base string, inc includeSpec) ([]loadedDoc, error) {
	c.count++
	if c.count > c.maxInc {
		return nil, fmt.Errorf("too many includes (max %d)", c.maxInc)
	}
	inc.Local = c.expand(inc.Local)
	inc.Project = c.expand(inc.Project)
	inc.Remote = c.expand(inc.Remote)
	inc.Template = c.expand(inc.Template)
	inc.Component = c.expand(inc.Component)
	inc.Ref = c.expand(inc.Ref)
	for i, f := range inc.File {
		inc.File[i] = c.expand(f)
	}
	switch {
	case inc.Local != "":
		if filepath.IsAbs(inc.Local) {
			return nil, fmt.Errorf("include:local %q must be a relative path", inc.Local)
		}
		p := joinLocal(c.opts.Root, inc.Local)
		if _, err := os.Stat(p); err != nil {
			p = joinLocal(base, inc.Local)
		}
		if err := pathUnder(c.opts.Root, p); err != nil {
			return nil, fmt.Errorf("include:local %q: %w", inc.Local, err)
		}
		doc, err := c.loadPath(p, inc.Inputs)
		if err != nil {
			return nil, err
		}
		return []loadedDoc{{dir: filepath.Dir(p), doc: doc}}, nil
	case inc.Project != "":
		root, ok := c.opts.Projects[inc.Project]
		if !ok {
			return nil, fmt.Errorf("include:project %q is not mapped; add it to .glci.yml under projects", inc.Project)
		}
		if !filepath.IsAbs(root) {
			root = filepath.Join(c.opts.Root, root)
		}
		files := inc.File
		if len(files) == 0 {
			files = []string{"/.gitlab-ci.yml"}
		}
		var out []loadedDoc
		for _, f := range files {
			p := joinLocal(root, f)
			doc, err := c.loadPath(p, inc.Inputs)
			if err != nil {
				return nil, err
			}
			out = append(out, loadedDoc{dir: filepath.Dir(p), doc: doc})
		}
		return out, nil
	case inc.Template != "":
		if c.opts.TemplatesDir == "" {
			return nil, fmt.Errorf("include:template %q needs templates_dir in .glci.yml (vendor GitLab templates locally)", inc.Template)
		}
		p := filepath.Join(c.opts.TemplatesDir, filepath.FromSlash(inc.Template))
		doc, err := c.loadPath(p, inc.Inputs)
		if err != nil {
			return nil, err
		}
		return []loadedDoc{{dir: filepath.Dir(p), doc: doc}}, nil
	case inc.Component != "":
		loc := mapComponent(c.opts.Components, inc.Component)
		if loc == "" {
			return nil, fmt.Errorf("include:component %q is not mapped; add it to .glci.yml under components", inc.Component)
		}
		if !filepath.IsAbs(loc) {
			loc = filepath.Join(c.opts.Root, loc)
		}
		p, err := findComponentFile(loc, inc.Component)
		if err != nil {
			return nil, err
		}
		doc, err := c.loadPath(p, inc.Inputs)
		if err != nil {
			return nil, err
		}
		return []loadedDoc{{dir: filepath.Dir(p), doc: doc}}, nil
	case inc.Remote != "":
		p, ok := c.opts.Projects[inc.Remote]
		if ok {
			if !filepath.IsAbs(p) {
				p = filepath.Join(c.opts.Root, p)
			}
			doc, err := c.loadPath(p, inc.Inputs)
			if err != nil {
				return nil, err
			}
			return []loadedDoc{{dir: filepath.Dir(p), doc: doc}}, nil
		}
		if !c.opts.AllowRemote {
			return nil, fmt.Errorf("include:remote %q blocked (100%% local). Vendor the file and map it in .glci.yml, or pass --allow-remote", inc.Remote)
		}
		data, err := c.fetch(inc.Remote)
		if err != nil {
			return nil, err
		}
		tmp, err := os.CreateTemp("", "glci-remote-*.yml")
		if err != nil {
			return nil, err
		}
		defer os.Remove(tmp.Name())
		if _, err := tmp.Write(data); err != nil {
			tmp.Close()
			return nil, err
		}
		tmp.Close()
		doc, err := c.loadPath(tmp.Name(), inc.Inputs)
		if err != nil {
			return nil, err
		}
		return []loadedDoc{{dir: base, doc: doc}}, nil
	default:
		return nil, fmt.Errorf("unsupported include: %+v", inc)
	}
}

func (c *loadCtx) expand(s string) string {
	if s == "" || c.opts.Vars == nil {
		return s
	}
	return Expand(s, c.opts.Vars)
}

func (c *loadCtx) fetch(url string) ([]byte, error) {
	resp, err := c.http.Get(url)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != 200 {
		return nil, fmt.Errorf("GET %s: %s", url, resp.Status)
	}
	return io.ReadAll(resp.Body)
}

func mapComponent(m map[string]string, addr string) string {
	if m == nil {
		return ""
	}
	if v, ok := m[addr]; ok {
		return v
	}
	// component addresses look like host/org/proj/name@version — try without version
	if i := strings.LastIndex(addr, "@"); i > 0 {
		if v, ok := m[addr[:i]]; ok {
			return v
		}
		addr = addr[:i]
	}
	if i := strings.LastIndex(addr, "/"); i > 0 {
		if v, ok := m[addr[i+1:]]; ok {
			return v
		}
	}
	return ""
}

func findComponentFile(loc, addr string) (string, error) {
	name := addr
	if i := strings.LastIndex(name, "@"); i > 0 {
		name = name[:i]
	}
	if i := strings.LastIndex(name, "/"); i >= 0 {
		name = name[i+1:]
	}
	cands := []string{
		loc,
		filepath.Join(loc, "template.yml"),
		filepath.Join(loc, "template.yaml"),
		filepath.Join(loc, name+".yml"),
		filepath.Join(loc, name+".yaml"),
		filepath.Join(loc, "templates", name+".yml"),
		filepath.Join(loc, "templates", name+".yaml"),
		filepath.Join(loc, "templates", name, "template.yml"),
		filepath.Join(loc, "templates", name, "template.yaml"),
	}
	for _, p := range cands {
		if st, err := os.Stat(p); err == nil && !st.IsDir() {
			return p, nil
		}
	}
	return "", fmt.Errorf("component %q: no template.yml under %s", addr, loc)
}
