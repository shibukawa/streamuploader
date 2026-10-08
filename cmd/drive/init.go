package main

import (
	"errors"
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"streamuploader/drive/deploy"
)

// exitError carries the process exit code for an init failure: 2 for an
// unsupported combination or usage error, 3 for a refused overwrite.
type exitError struct {
	code int
	err  error
}

func (e exitError) Error() string { return e.err.Error() }
func (e exitError) Unwrap() error { return e.err }

// runInit implements `drive init`. It needs no environment and no object
// store: it renders a deployment kit from flags and writes it to --out.
func runInit(args []string, stdout, stderr *os.File) error {
	fs := flag.NewFlagSet("drive init", flag.ContinueOnError)
	fs.SetOutput(stderr)
	var p deploy.Profile
	var out string
	var force, dryRun bool
	fs.StringVar(&p.Target, "target", "compose", "deployment target: "+strings.Join(deploy.Targets, ", "))
	fs.StringVar(&p.Storage, "storage", "", "object store (default per target; rustfs for compose)")
	fs.StringVar(&p.Delivery, "delivery", "", "proxy (default) or presigned")
	fs.BoolVar(&p.ClamAV, "clamav", false, "add a ClamAV service and scan every upload")
	fs.StringVar(&p.Image, "image", "", "prebuilt image reference instead of building from --context")
	fs.StringVar(&p.Context, "context", "", "build context (default: the streamuploader checkout that contains the working directory)")
	fs.StringVar(&p.Name, "name", "drive", "compose project name and local image tag")
	fs.IntVar(&p.Port, "port", 8080, "host port of the Drive")
	fs.StringVar(&out, "out", "", "output directory (default deploy/<target>)")
	fs.BoolVar(&force, "force", false, "overwrite files that already exist")
	fs.BoolVar(&dryRun, "dry-run", false, "print the files that would be written and exit")
	fs.Usage = func() {
		fmt.Fprintln(stderr, "usage: drive init [flags]")
		fmt.Fprintln(stderr, "Writes the deployment files for one target. See .knowledge/concepts/requirement/drive-init-subcommand.yaml.")
		fs.PrintDefaults()
	}
	if err := fs.Parse(args); err != nil {
		if errors.Is(err, flag.ErrHelp) {
			return nil
		}
		return exitError{2, err}
	}
	if fs.NArg() > 0 {
		return exitError{2, fmt.Errorf("unexpected argument %q", fs.Arg(0))}
	}
	p.Defaults()
	if out == "" {
		out = filepath.Join("deploy", p.Target)
	}
	if p.Image == "" && p.Context == "" {
		cwd, err := os.Getwd()
		if err != nil {
			return exitError{2, err}
		}
		root, err := deploy.FindRepoRoot(cwd)
		if err != nil {
			return exitError{2, fmt.Errorf("%w; pass --context DIR or --image REF", err)}
		}
		p.Context = deploy.RelativeContext(out, root)
	}
	kit, err := deploy.Render(p)
	if err != nil {
		var unsupported *deploy.UnsupportedError
		if errors.As(err, &unsupported) {
			return exitError{2, fmt.Errorf("unsupported: %w", err)}
		}
		return exitError{2, err}
	}
	if dryRun {
		fmt.Fprintf(stdout, "would write to %s:\n", out)
		for _, path := range kit.Paths() {
			fmt.Fprintf(stdout, "  %s\n", path)
		}
		return nil
	}
	if err := deploy.Write(kit, out, force); err != nil {
		var exists *deploy.ExistsError
		if errors.As(err, &exists) {
			return exitError{3, err}
		}
		return exitError{1, err}
	}
	fmt.Fprintf(stdout, "wrote %s:\n", out)
	for _, path := range kit.Paths() {
		fmt.Fprintf(stdout, "  %s\n", path)
	}
	if holes := deploy.Placeholders(kit); len(holes) > 0 {
		fmt.Fprintln(stdout, "fill in before deploying:")
		for _, h := range holes {
			fmt.Fprintf(stdout, "  %s\n", h)
		}
	}
	fmt.Fprintf(stdout, "next: cd %s && docker compose up", out)
	if p.Image == "" {
		fmt.Fprint(stdout, " --build")
	}
	fmt.Fprintln(stdout, " -d")
	return nil
}
