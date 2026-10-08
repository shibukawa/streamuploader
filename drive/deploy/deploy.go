// Package deploy renders the deployment kit that `drive init` writes: the
// .env file, compose.yaml or a provider's service spec, the streamuploader
// security policy and a README, all from one small Profile. The design is
// recorded in .knowledge/concepts/requirement/drive-init-subcommand.yaml and
// the flow in .knowledge/concepts/flow/drive-init-generation.yaml.
//
// Rendering is deterministic: no timestamps, hostnames or random values enter
// a kit, so re-running init reproduces it byte for byte and the golden kits
// under testdata/ stay stable.
package deploy

import (
	"bytes"
	"embed"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"text/template"
)

//go:embed templates
var templates embed.FS

// Targets lists every target init knows, in the order of the knowledge file.
// Only compose is generated in the m3-init-core slice; the others are refused
// with an UnsupportedError that names the knowledge id.
var Targets = []string{"compose", "aws", "google", "azure", "cloudflare"}

// Storage providers a kit can be asked for. azure-blob is listed so that the
// refusal can explain why it is not supported.
var Storages = []string{"rustfs", "s3", "gcs", "r2", "b2", "azure-blob"}

const (
	DeliveryProxy     = "proxy"
	DeliveryPresigned = "presigned"

	// KnowledgeInit is the requirement that scopes what init generates.
	KnowledgeInit = "requirement:drive-init-subcommand"
	// KnowledgeTargets records per-target limits and refused combinations.
	KnowledgeTargets = "system:drive-deployment-targets"
)

// Profile is the in-memory result of the init flags. It is not persisted;
// the generated .env file is its durable form.
type Profile struct {
	Target   string
	Storage  string
	Delivery string
	ClamAV   bool
	// Image is a prebuilt image reference. Empty means the kit builds the
	// image from Context with the repository's Dockerfile.drive.
	Image string
	// Context is the build context as written into the kit: a path relative
	// to the kit directory, or an absolute path.
	Context string
	// Name is the compose project name and the local image tag prefix.
	Name string
	// Port is the host port the Drive listens on.
	Port int
}

// Defaults fills the fields the user left empty.
func (p *Profile) Defaults() {
	if p.Target == "" {
		p.Target = "compose"
	}
	if p.Storage == "" {
		p.Storage = defaultStorage[p.Target]
	}
	if p.Delivery == "" {
		p.Delivery = DeliveryProxy
		if p.Target == "cloudflare" {
			p.Delivery = DeliveryPresigned
		}
	}
	if p.Name == "" {
		p.Name = "drive"
	}
	if p.Port == 0 {
		p.Port = 8080
	}
}

var defaultStorage = map[string]string{
	"compose":    "rustfs",
	"aws":        "s3",
	"google":     "gcs",
	"azure":      "r2",
	"cloudflare": "r2",
}

// UnsupportedError reports a combination the binary cannot run yet or that
// init does not generate yet. KnowledgeID names the concept that records why.
type UnsupportedError struct {
	Reason      string
	KnowledgeID string
}

func (e *UnsupportedError) Error() string {
	return e.Reason + " (see " + e.KnowledgeID + ")"
}

// ExistsError lists the kit paths that already exist in the output directory.
type ExistsError struct {
	Paths []string
}

func (e *ExistsError) Error() string {
	return "refusing to overwrite " + strings.Join(e.Paths, ", ") + " (use --force)"
}

var nameRe = regexp.MustCompile(`^[a-z0-9][a-z0-9_-]*$`)

// Validate checks the profile against the combinations init supports. The
// error is an *UnsupportedError when the combination is known but refused.
func Validate(p Profile) error {
	if !contains(Targets, p.Target) {
		return fmt.Errorf("unknown target %q (one of %s)", p.Target, strings.Join(Targets, ", "))
	}
	if !contains(Storages, p.Storage) {
		return fmt.Errorf("unknown storage %q (one of %s)", p.Storage, strings.Join(Storages, ", "))
	}
	if p.Delivery != DeliveryProxy && p.Delivery != DeliveryPresigned {
		return fmt.Errorf("unknown delivery %q (proxy or presigned)", p.Delivery)
	}
	if !nameRe.MatchString(p.Name) {
		return fmt.Errorf("name %q must be lowercase letters, digits, - or _ and start with a letter or digit", p.Name)
	}
	if p.Port < 1 || p.Port > 65535 {
		return fmt.Errorf("port %d is out of range", p.Port)
	}
	if p.Image != "" && p.Context != "" {
		return errors.New("use either --image or --context, not both")
	}
	if p.Image == "" && p.Context == "" {
		return errors.New("no build context: pass --context DIR (the streamuploader checkout) or --image REF")
	}
	if p.Storage == "azure-blob" {
		return &UnsupportedError{
			Reason:      "Azure Blob Storage is not S3-compatible and the Drive has no adapter; point the kit at an S3-compatible bucket instead",
			KnowledgeID: KnowledgeTargets,
		}
	}
	if p.Target != "compose" {
		return &UnsupportedError{
			Reason:      fmt.Sprintf("target %s is not generated yet; the compose kit is the first slice (m3-init-core)", p.Target),
			KnowledgeID: KnowledgeInit,
		}
	}
	if p.Storage != "rustfs" {
		return &UnsupportedError{
			Reason:      fmt.Sprintf("storage %s with the compose kit comes with the cloud kits; the first slice uses the local RustFS container", p.Storage),
			KnowledgeID: KnowledgeInit,
		}
	}
	return nil
}

// File is one rendered kit entry. Path is relative to the kit directory.
type File struct {
	Path string
	Data []byte
	Mode fs.FileMode
}

// Kit is the rendered file set, sorted by path.
type Kit []File

// Paths returns the kit paths in order.
func (k Kit) Paths() []string {
	out := make([]string, 0, len(k))
	for _, f := range k {
		out = append(out, f.Path)
	}
	return out
}

// Get returns the file at path, or nil.
func (k Kit) Get(path string) *File {
	for i := range k {
		if k[i].Path == path {
			return &k[i]
		}
	}
	return nil
}

// view is what the templates see: the profile plus derived values.
type view struct {
	Profile
	// Build is true when compose builds the image from Context.
	Build bool
	// ImageRef is the image the services run.
	ImageRef string
	// PublicBaseURL is what browsers use to reach the Drive.
	PublicBaseURL string
	// Flags reproduces the init invocation for the README.
	Flags string
}

func newView(p Profile) view {
	v := view{Profile: p, ImageRef: p.Image, Build: p.Image == ""}
	if v.Build {
		v.ImageRef = p.Name + ":local"
	}
	v.PublicBaseURL = fmt.Sprintf("http://localhost:%d", p.Port)
	var flags []string
	flags = append(flags, "--target "+p.Target)
	if p.Storage != defaultStorage[p.Target] {
		flags = append(flags, "--storage "+p.Storage)
	}
	if p.Delivery != DeliveryProxy {
		flags = append(flags, "--delivery "+p.Delivery)
	}
	if p.ClamAV {
		flags = append(flags, "--clamav")
	}
	if p.Image != "" {
		flags = append(flags, "--image "+p.Image)
	} else {
		flags = append(flags, "--context "+p.Context)
	}
	if p.Name != "drive" {
		flags = append(flags, "--name "+p.Name)
	}
	if p.Port != 8080 {
		flags = append(flags, fmt.Sprintf("--port %d", p.Port))
	}
	v.Flags = strings.Join(flags, " ")
	return v
}

// Render validates the profile and renders the kit for its target.
func Render(p Profile) (Kit, error) {
	p.Defaults()
	if err := Validate(p); err != nil {
		return nil, err
	}
	v := newView(p)
	var kit Kit
	add := func(path string, data []byte, mode fs.FileMode) {
		kit = append(kit, File{Path: path, Data: data, Mode: mode})
	}
	render := func(name string) ([]byte, error) {
		src, err := templates.ReadFile("templates/" + name)
		if err != nil {
			return nil, err
		}
		t, err := template.New(name).Option("missingkey=error").Parse(string(src))
		if err != nil {
			return nil, fmt.Errorf("parse %s: %w", name, err)
		}
		var buf bytes.Buffer
		if err := t.Execute(&buf, v); err != nil {
			return nil, fmt.Errorf("render %s: %w", name, err)
		}
		return buf.Bytes(), nil
	}
	security, err := templates.ReadFile("templates/common/security.yaml")
	if err != nil {
		return nil, err
	}
	add("security.yaml", security, 0o644)
	switch p.Target {
	case "compose":
		for _, f := range []struct{ tmpl, out string }{
			{"compose/env.tmpl", ".env"},
			{"compose/compose.yaml.tmpl", "compose.yaml"},
			{"compose/README.md.tmpl", "README.md"},
		} {
			data, err := render(f.tmpl)
			if err != nil {
				return nil, err
			}
			add(f.out, data, 0o644)
		}
	default:
		return nil, &UnsupportedError{Reason: "target " + p.Target + " has no templates", KnowledgeID: KnowledgeInit}
	}
	sort.Slice(kit, func(i, j int) bool { return kit[i].Path < kit[j].Path })
	if missing := missingEnv(kit); len(missing) > 0 {
		return nil, fmt.Errorf("internal: compose.yaml references variables missing from .env: %s", strings.Join(missing, ", "))
	}
	return kit, nil
}

var envRefRe = regexp.MustCompile(`\$\{([A-Z][A-Z0-9_]*)(?::?[-?+][^}]*)?\}`)

// EnvReferences lists the ${VAR} names a compose file interpolates.
func EnvReferences(compose []byte) []string {
	seen := map[string]bool{}
	var out []string
	for _, m := range envRefRe.FindAllSubmatch(compose, -1) {
		name := string(m[1])
		if !seen[name] {
			seen[name] = true
			out = append(out, name)
		}
	}
	sort.Strings(out)
	return out
}

// EnvKeys lists the variable names a .env file defines.
func EnvKeys(env []byte) map[string]bool {
	keys := map[string]bool{}
	for _, line := range strings.Split(string(env), "\n") {
		line = strings.TrimSpace(line)
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		if i := strings.IndexByte(line, '='); i > 0 {
			keys[line[:i]] = true
		}
	}
	return keys
}

func missingEnv(kit Kit) []string {
	compose, env := kit.Get("compose.yaml"), kit.Get(".env")
	if compose == nil || env == nil {
		return nil
	}
	keys := EnvKeys(env.Data)
	var missing []string
	for _, name := range EnvReferences(compose.Data) {
		if !keys[name] {
			missing = append(missing, name)
		}
	}
	return missing
}

var placeholderRe = regexp.MustCompile(`<<FILL:[A-Z0-9_]+>>`)

// Placeholders lists the <<FILL:NAME>> markers left in a kit, with the file
// that holds each one, so init can tell the operator what to fill in.
func Placeholders(kit Kit) []string {
	var out []string
	for _, f := range kit {
		seen := map[string]bool{}
		for _, m := range placeholderRe.FindAll(f.Data, -1) {
			if !seen[string(m)] {
				seen[string(m)] = true
				out = append(out, f.Path+": "+string(m))
			}
		}
	}
	return out
}

// Collisions returns the kit paths that already exist under dir.
func Collisions(kit Kit, dir string) []string {
	var out []string
	for _, f := range kit {
		if _, err := os.Lstat(filepath.Join(dir, filepath.FromSlash(f.Path))); err == nil {
			out = append(out, f.Path)
		}
	}
	return out
}

// Write creates dir and writes the kit into it. Without force, any existing
// kit path makes it return an *ExistsError before anything is written. Each
// file is written to a temporary name and renamed into place.
func Write(kit Kit, dir string, force bool) error {
	if !force {
		if c := Collisions(kit, dir); len(c) > 0 {
			return &ExistsError{Paths: c}
		}
	}
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return err
	}
	for _, f := range kit {
		target := filepath.Join(dir, filepath.FromSlash(f.Path))
		if err := os.MkdirAll(filepath.Dir(target), 0o755); err != nil {
			return err
		}
		tmp, err := os.CreateTemp(filepath.Dir(target), "."+filepath.Base(target)+".*")
		if err != nil {
			return err
		}
		tmpName := tmp.Name()
		if _, err := tmp.Write(f.Data); err != nil {
			tmp.Close()
			os.Remove(tmpName)
			return fmt.Errorf("write %s: %w", f.Path, err)
		}
		if err := tmp.Chmod(f.Mode); err != nil {
			tmp.Close()
			os.Remove(tmpName)
			return fmt.Errorf("chmod %s: %w", f.Path, err)
		}
		if err := tmp.Close(); err != nil {
			os.Remove(tmpName)
			return fmt.Errorf("close %s: %w", f.Path, err)
		}
		if err := os.Rename(tmpName, target); err != nil {
			os.Remove(tmpName)
			return fmt.Errorf("rename %s: %w", f.Path, err)
		}
	}
	return nil
}

// ErrNoRepo is returned when no streamuploader checkout contains the start
// directory.
var ErrNoRepo = errors.New("no streamuploader checkout (go.mod with module streamuploader) above the working directory")

// FindRepoRoot walks up from start to the directory whose go.mod declares
// module streamuploader, which is the build context Dockerfile.drive needs.
func FindRepoRoot(start string) (string, error) {
	dir, err := filepath.Abs(start)
	if err != nil {
		return "", err
	}
	for {
		body, err := os.ReadFile(filepath.Join(dir, "go.mod"))
		if err == nil && isStreamuploaderModule(body) {
			return dir, nil
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			return "", ErrNoRepo
		}
		dir = parent
	}
}

func isStreamuploaderModule(gomod []byte) bool {
	for _, line := range strings.Split(string(gomod), "\n") {
		if strings.TrimSpace(line) == "module streamuploader" {
			return true
		}
	}
	return false
}

// RelativeContext returns root as a path relative to kitDir, with forward
// slashes, when the kit lives inside the checkout; otherwise the absolute
// root, which reads better than a long chain of "..".
func RelativeContext(kitDir, root string) string {
	absKit, err := filepath.Abs(kitDir)
	if err != nil {
		return root
	}
	rel, err := filepath.Rel(absKit, root)
	if err != nil {
		return root
	}
	if rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
		// The kit is at or above the root (rel is "." or a subdirectory),
		// which only happens with an explicit --out; keep it relative.
		return filepath.ToSlash(rel)
	}
	if inside, err := filepath.Rel(root, absKit); err == nil && !strings.HasPrefix(inside, "..") {
		return filepath.ToSlash(rel)
	}
	return root
}

func contains(list []string, s string) bool {
	for _, v := range list {
		if v == s {
			return true
		}
	}
	return false
}
