package main

import (
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/ttopias/glci/internal/gitctx"
	"github.com/ttopias/glci/internal/gitlabci"
	"github.com/ttopias/glci/internal/runner"
	"github.com/ttopias/glci/internal/upgrade"
	"github.com/ttopias/glci/internal/version"
	"gopkg.in/yaml.v3"
)

func main() {
	if err := run(os.Args[1:]); err != nil {
		fmt.Fprintln(os.Stderr, "glci:", err)
		os.Exit(1)
	}
}

func run(args []string) error {
	if len(args) == 0 {
		return runCmd("run", nil)
	}
	cmd := args[0]
	switch cmd {
	case "run", "list", "compile", "upgrade", "version", "help", "-h", "--help", "-v", "--version":
	default:
		if strings.HasPrefix(cmd, "-") {
			cmd = "run"
			args = append([]string{"run"}, args...)
		}
	}
	switch cmd {
	case "help", "-h", "--help":
		printHelp()
		return nil
	case "version", "-v", "--version":
		fmt.Printf("glci version %s\n", version.String())
		return nil
	case "upgrade":
		return upgradeCmd(args[1:])
	case "run", "list", "compile":
		return runCmd(cmd, args[1:])
	default:
		return fmt.Errorf("unknown command %q (try glci help)", cmd)
	}
}

func printHelp() {
	fmt.Print(`glci — run GitLab CI pipelines fully locally in Docker

Usage:
  glci                      Run created jobs (skip when:manual)
  glci run [flags]          Run created jobs
  glci list [flags]         List jobs that would run
  glci compile [flags]      Print the compiled pipeline
  glci upgrade [tag]        Replace this binary with the latest (or pinned) release
  glci version              Print the version

Created jobs run by default. when:manual jobs need --manual or --job NAME.
Logs land in .glci/logs/, artifacts in .glci/artifacts/, summary in .glci/report.json.
Docker-in-Docker (docker:*-dind services) is privileged automatically.

Flags:
  -C, --dir DIR             Project directory (default .)
  -f, --file FILE           CI config (default .gitlab-ci.yml)
  --source SOURCE           push|merge_request_event|schedule|web|api|pipeline|trigger
  --mr                      Shortcut for --source merge_request_event
  --var KEY=VAL             Extra variable (repeatable)
  --job NAME                Run this job and its needs (repeatable)
  --stage NAME              Run jobs in this stage
  --manual                  Also run when:manual jobs
  --shell                   Run on the host instead of Docker
  --docker                  Force docker executor (default)
  --privileged              Privileged containers (also auto for dind)
  --allow-remote            Allow include:remote HTTP fetches
  --dry-run                 Compile and print the plan only
  --concurrency N           Parallel jobs (default 4)
  --input KEY=VAL           spec:inputs value (repeatable)

Upgrade flags:
  --check                   Only report whether a newer release exists
  --force                   Reinstall even if the version already matches
`)
}

func upgradeCmd(args []string) error {
	fs := flag.NewFlagSet("upgrade", flag.ContinueOnError)
	check := fs.Bool("check", false, "print whether an upgrade is available")
	force := fs.Bool("force", false, "reinstall even if versions match")
	if err := fs.Parse(args); err != nil {
		return err
	}
	target := fs.Arg(0)
	return upgrade.Run(upgrade.Options{Target: target, Check: *check, Force: *force})
}

type flags struct {
	dir, file, source string
	mr, manual        bool
	shell, docker     bool
	privileged        bool
	allowRemote       bool
	dryRun            bool
	concurrency       int
	vars              []string
	jobs              []string
	stage             string
	inputs            []string
}

func parseFlags(args []string) (*flags, error) {
	f := &flags{dir: ".", file: ".gitlab-ci.yml", source: "push", concurrency: 4}
	fs := flag.NewFlagSet("glci", flag.ContinueOnError)
	fs.StringVar(&f.dir, "C", ".", "project directory")
	fs.StringVar(&f.dir, "dir", ".", "project directory")
	fs.StringVar(&f.file, "f", ".gitlab-ci.yml", "CI yaml")
	fs.StringVar(&f.file, "file", ".gitlab-ci.yml", "CI yaml")
	fs.StringVar(&f.source, "source", "push", "pipeline source")
	fs.BoolVar(&f.mr, "mr", false, "merge request pipeline")
	fs.BoolVar(&f.manual, "manual", false, "run when:manual jobs")
	fs.BoolVar(&f.shell, "shell", false, "shell executor")
	fs.BoolVar(&f.docker, "docker", false, "docker executor")
	fs.BoolVar(&f.privileged, "privileged", false, "privileged docker")
	fs.BoolVar(&f.allowRemote, "allow-remote", false, "allow remote includes")
	fs.BoolVar(&f.dryRun, "dry-run", false, "dry run")
	fs.IntVar(&f.concurrency, "concurrency", 4, "parallel jobs")
	fs.StringVar(&f.stage, "stage", "", "stage filter")
	fs.Func("var", "KEY=VAL", func(s string) error { f.vars = append(f.vars, s); return nil })
	fs.Func("job", "job name", func(s string) error { f.jobs = append(f.jobs, s); return nil })
	fs.Func("input", "KEY=VAL", func(s string) error { f.inputs = append(f.inputs, s); return nil })
	if err := fs.Parse(args); err != nil {
		return nil, err
	}
	return f, nil
}

func runCmd(cmd string, args []string) error {
	f, err := parseFlags(args)
	if err != nil {
		return err
	}
	root, err := filepath.Abs(f.dir)
	if err != nil {
		return err
	}
	git, err := gitctx.Detect(root)
	if err != nil {
		return err
	}
	cfg := gitlabci.LoadLocalConfig(root)
	extra := map[string]string{}
	for k, v := range cfg.Variables {
		extra[k] = v
	}
	for _, kv := range f.vars {
		k, v, ok := strings.Cut(kv, "=")
		if !ok {
			return fmt.Errorf("invalid --var %q (want KEY=VAL)", kv)
		}
		extra[k] = v
	}
	inputs := map[string]string{}
	for k, v := range cfg.Inputs {
		inputs[k] = v
	}
	for _, kv := range f.inputs {
		k, v, ok := strings.Cut(kv, "=")
		if !ok {
			return fmt.Errorf("invalid --input %q", kv)
		}
		inputs[k] = v
	}
	source := f.source
	if f.mr {
		source = "merge_request_event"
	}
	execKind := cfg.Executor
	if execKind == "" || execKind == "auto" {
		execKind = "docker"
	}
	if f.shell {
		execKind = "shell"
	}
	if f.docker {
		execKind = "docker"
	}
	defaultImage := os.Getenv("GLCI_DEFAULT_IMAGE")
	if defaultImage == "" {
		defaultImage = "alpine:3.24"
	}
	copt := gitlabci.CompileOptions{
		Root: root, File: f.file, Git: git, Source: source,
		ExtraVars: extra, Projects: cfg.Projects, Components: cfg.Components,
		TemplatesDir: cfg.TemplatesDir, AllowRemote: f.allowRemote || cfg.AllowRemote,
		Inputs: inputs, Protected: gitlabci.IsProtected(cfg, git.Ref),
		MRSource: git.Branch, MRTarget: git.DefaultBranch,
		DefaultImage: defaultImage,
	}
	p, err := gitlabci.Compile(copt)
	if err != nil {
		return err
	}

	jobs := p.Jobs
	if f.stage != "" {
		var names []string
		for _, j := range jobs {
			if j.Stage == f.stage {
				names = append(names, j.Name)
			}
		}
		jobs = gitlabci.FilterJobs(p, names)
	}
	if len(f.jobs) > 0 {
		jobs = gitlabci.FilterJobs(&gitlabci.Pipeline{Jobs: p.Jobs, Stages: p.Stages}, f.jobs)
	}

	switch cmd {
	case "compile":
		enc := yaml.NewEncoder(os.Stdout)
		enc.SetIndent(2)
		return enc.Encode(p)
	case "list":
		fmt.Printf("pipeline source=%s jobs=%d\n", source, len(jobs))
		for _, j := range jobs {
			img := "(shell)"
			if j.Image != nil {
				img = j.Image.Name
			}
			fmt.Printf("  %-32s stage=%-12s when=%-12s image=%s\n", j.Name, j.Stage, j.When, img)
		}
		return nil
	default:
		if f.dryRun {
			fmt.Printf("dry-run: %d jobs\n", len(jobs))
			for _, j := range jobs {
				fmt.Printf("  %s\n", j.Name)
			}
			return nil
		}
		includeManual := f.manual || cfg.Manual == "run"
		_, err := runner.Run(runner.Options{
			Root: root, Pipeline: p, Jobs: jobs, Executor: execKind,
			IncludeManual: includeManual, SelectedJobs: f.jobs,
			DryRun: f.dryRun, Privileged: f.privileged || cfg.Privileged,
			Concurrency: f.concurrency, DefaultImage: defaultImage, Compile: copt,
		})
		return err
	}
}
