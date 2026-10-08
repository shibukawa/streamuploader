package deploy

import (
	"bytes"
	"errors"
	"flag"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

// assignmentRe matches KEY=value and KEY: value lines.
var assignmentRe = regexp.MustCompile(`^\s*([A-Za-z0-9_]+)\s*[:=]\s*(.*)$`)

// looksLikeCredential reports whether a key names a secret, password, token
// or access key as a whole underscore-separated word (TOKENIZER is not one).
func looksLikeCredential(key string) bool {
	upper := strings.ToUpper(key)
	if strings.Contains(upper, "ACCESS_KEY") {
		return true
	}
	for _, part := range strings.Split(upper, "_") {
		switch part {
		case "SECRET", "PASSWORD", "TOKEN":
			return true
		}
	}
	return false
}

var update = flag.Bool("update", false, "rewrite the golden kits under testdata")

// goldenCases are the supported combinations of the m3-init-core slice. Each
// one has a golden kit under testdata/<name> that Render must reproduce byte
// for byte (acceptance golden-kits in requirement:drive-init-subcommand).
var goldenCases = []struct {
	name string
	p    Profile
}{
	{"compose-default", Profile{Target: "compose", Context: "../.."}},
	{"compose-clamav", Profile{Target: "compose", Context: "../..", ClamAV: true}},
	{"compose-image-presigned", Profile{Target: "compose", Image: "ghcr.io/shibukawa/streamuploader-drive:v0.1.0", Delivery: DeliveryPresigned, Name: "mydrive", Port: 18080}},
}

func TestGoldenKits(t *testing.T) {
	for _, tc := range goldenCases {
		t.Run(tc.name, func(t *testing.T) {
			kit, err := Render(tc.p)
			if err != nil {
				t.Fatalf("render: %v", err)
			}
			dir := filepath.Join("testdata", tc.name)
			if *update {
				if err := os.RemoveAll(dir); err != nil {
					t.Fatal(err)
				}
				if err := Write(kit, dir, true); err != nil {
					t.Fatal(err)
				}
				return
			}
			entries, err := os.ReadDir(dir)
			if err != nil {
				t.Fatalf("golden kit missing, run go test ./drive/deploy -update: %v", err)
			}
			if len(entries) != len(kit) {
				t.Fatalf("golden kit has %d files, render produced %d: %v", len(entries), len(kit), kit.Paths())
			}
			for _, f := range kit {
				want, err := os.ReadFile(filepath.Join(dir, f.Path))
				if err != nil {
					t.Fatalf("golden %s: %v", f.Path, err)
				}
				if !bytes.Equal(want, f.Data) {
					t.Errorf("%s differs from golden; run go test ./drive/deploy -update and review the diff", f.Path)
				}
			}
		})
	}
}

func TestRenderIsDeterministic(t *testing.T) {
	a, err := Render(goldenCases[0].p)
	if err != nil {
		t.Fatal(err)
	}
	b, err := Render(goldenCases[0].p)
	if err != nil {
		t.Fatal(err)
	}
	for i := range a {
		if a[i].Path != b[i].Path || !bytes.Equal(a[i].Data, b[i].Data) {
			t.Fatalf("second render of %s differs", a[i].Path)
		}
	}
}

func TestWriteRefusesOverwrite(t *testing.T) {
	kit, err := Render(goldenCases[0].p)
	if err != nil {
		t.Fatal(err)
	}
	dir := filepath.Join(t.TempDir(), "kit")
	if err := Write(kit, dir, false); err != nil {
		t.Fatalf("first write: %v", err)
	}
	// Change one file so a refused second write is observable.
	envPath := filepath.Join(dir, ".env")
	if err := os.WriteFile(envPath, []byte("EDITED=1\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	err = Write(kit, dir, false)
	var exists *ExistsError
	if !errors.As(err, &exists) {
		t.Fatalf("second write: want ExistsError, got %v", err)
	}
	if len(exists.Paths) != len(kit) {
		t.Fatalf("ExistsError lists %v, want every kit path", exists.Paths)
	}
	if got, _ := os.ReadFile(envPath); string(got) != "EDITED=1\n" {
		t.Fatalf("refused write changed .env: %q", got)
	}
	for _, f := range kit {
		if f.Path == ".env" {
			continue
		}
		got, _ := os.ReadFile(filepath.Join(dir, f.Path))
		if !bytes.Equal(got, f.Data) {
			t.Fatalf("refused write changed %s", f.Path)
		}
	}
	if err := Write(kit, dir, true); err != nil {
		t.Fatalf("forced write: %v", err)
	}
	if got, _ := os.ReadFile(envPath); string(got) == "EDITED=1\n" {
		t.Fatal("forced write left the edited .env")
	}
	if c := Collisions(kit, dir); len(c) != len(kit) {
		t.Fatalf("collisions after write: %v", c)
	}
	if c := Collisions(kit, filepath.Join(dir, "empty")); len(c) != 0 {
		t.Fatalf("collisions in an empty dir: %v", c)
	}
}

func TestUnsupportedCombinations(t *testing.T) {
	cases := []struct {
		name string
		p    Profile
		id   string
	}{
		{"cloud target", Profile{Target: "google", Image: "x"}, KnowledgeInit},
		{"aws", Profile{Target: "aws", Image: "x"}, KnowledgeInit},
		{"azure", Profile{Target: "azure", Image: "x"}, KnowledgeInit},
		{"cloudflare", Profile{Target: "cloudflare", Image: "x"}, KnowledgeInit},
		{"azure-blob", Profile{Target: "azure", Storage: "azure-blob", Image: "x"}, KnowledgeTargets},
		{"external storage with compose", Profile{Target: "compose", Storage: "r2", Image: "x"}, KnowledgeInit},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			_, err := Render(tc.p)
			var unsupported *UnsupportedError
			if !errors.As(err, &unsupported) {
				t.Fatalf("want UnsupportedError, got %v", err)
			}
			if unsupported.KnowledgeID != tc.id {
				t.Fatalf("knowledge id %q, want %q", unsupported.KnowledgeID, tc.id)
			}
			if !strings.Contains(err.Error(), tc.id) {
				t.Fatalf("error %q does not name the knowledge id", err)
			}
		})
	}
	usage := []Profile{
		{Target: "nope", Image: "x"},
		{Target: "compose", Storage: "floppy", Image: "x"},
		{Target: "compose", Delivery: "carrier-pigeon", Image: "x"},
		{Target: "compose", Image: "x", Name: "Bad Name"},
		{Target: "compose", Image: "x", Port: 70000},
		{Target: "compose", Image: "x", Context: "."},
		{Target: "compose"},
	}
	for _, p := range usage {
		_, err := Render(p)
		var unsupported *UnsupportedError
		if err == nil || errors.As(err, &unsupported) {
			t.Fatalf("profile %+v: want a usage error, got %v", p, err)
		}
	}
}

func TestEnvRoundtrip(t *testing.T) {
	for _, tc := range goldenCases {
		kit, err := Render(tc.p)
		if err != nil {
			t.Fatal(err)
		}
		compose, env := kit.Get("compose.yaml"), kit.Get(".env")
		if compose == nil || env == nil {
			t.Fatalf("%s: kit lacks compose.yaml or .env: %v", tc.name, kit.Paths())
		}
		keys := EnvKeys(env.Data)
		refs := EnvReferences(compose.Data)
		if len(refs) == 0 {
			t.Fatalf("%s: compose.yaml references no variables", tc.name)
		}
		for _, name := range refs {
			if !keys[name] {
				t.Errorf("%s: compose.yaml references %s, which .env does not define", tc.name, name)
			}
		}
		for _, must := range []string{"SU_S3_ENDPOINT", "SU_S3_BUCKET", "DRIVE_INDEX_DIR", "DRIVE_DELIVERY", "SU_SECURITY_CONFIG"} {
			if !keys[must] {
				t.Errorf("%s: .env lacks %s", tc.name, must)
			}
		}
	}
}

func TestEnvReferences(t *testing.T) {
	got := EnvReferences([]byte("a: ${FOO}\nb: ${BAR:-x}\nc: ${BAZ?err}\nd: ${FOO}\ne: $NOT\n"))
	want := []string{"BAR", "BAZ", "FOO"}
	if strings.Join(got, ",") != strings.Join(want, ",") {
		t.Fatalf("got %v want %v", got, want)
	}
}

func TestSecurityPolicyMatchesRepoConfig(t *testing.T) {
	embedded, err := templates.ReadFile("templates/common/security.yaml")
	if err != nil {
		t.Fatal(err)
	}
	repo, err := os.ReadFile(filepath.Join("..", "..", "config", "security.yaml"))
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(embedded, repo) {
		t.Fatal("drive/deploy/templates/common/security.yaml differs from config/security.yaml; copy it over")
	}
}

func TestComposeKitNeedsNoEdits(t *testing.T) {
	for _, tc := range goldenCases {
		kit, err := Render(tc.p)
		if err != nil {
			t.Fatal(err)
		}
		if holes := Placeholders(kit); len(holes) != 0 {
			t.Fatalf("%s: compose kit has placeholders: %v", tc.name, holes)
		}
		// The only credential in a compose kit is the RustFS development pair
		// (acceptance no-secrets-in-kit).
		for _, f := range kit {
			for _, line := range strings.Split(string(f.Data), "\n") {
				m := assignmentRe.FindStringSubmatch(line)
				if m == nil || !looksLikeCredential(m[1]) {
					continue
				}
				if value := strings.TrimSpace(m[2]); value == "rustfsadmin" || value == "${SU_S3_ACCESS_KEY_ID}" || value == "${SU_S3_SECRET_ACCESS_KEY}" {
					continue
				}
				t.Errorf("%s/%s: line looks like a credential: %q", tc.name, f.Path, line)
			}
		}
	}
}

func TestPlaceholders(t *testing.T) {
	kit := Kit{{Path: "a", Data: []byte("x=<<FILL:KEY>> y=<<FILL:KEY>> z=<<FILL:OTHER>>")}, {Path: "b", Data: []byte("none")}}
	got := Placeholders(kit)
	if strings.Join(got, ";") != "a: <<FILL:KEY>>;a: <<FILL:OTHER>>" {
		t.Fatalf("got %v", got)
	}
}

func TestFindRepoRoot(t *testing.T) {
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "go.mod"), []byte("module streamuploader\n\ngo 1.27\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	nested := filepath.Join(root, "deploy", "compose")
	if err := os.MkdirAll(nested, 0o755); err != nil {
		t.Fatal(err)
	}
	got, err := FindRepoRoot(nested)
	if err != nil {
		t.Fatal(err)
	}
	if real, _ := filepath.EvalSymlinks(root); got != root && got != real {
		t.Fatalf("got %q want %q", got, root)
	}
	if rel := RelativeContext(nested, got); rel != "../.." {
		t.Fatalf("relative context %q, want ../..", rel)
	}
	outside := filepath.Join(t.TempDir(), "elsewhere", "kit")
	if rel := RelativeContext(outside, got); rel != got {
		t.Fatalf("context for a kit outside the checkout %q, want the absolute root %q", rel, got)
	}
	other := t.TempDir()
	if err := os.WriteFile(filepath.Join(other, "go.mod"), []byte("module something/else\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := FindRepoRoot(other); !errors.Is(err, ErrNoRepo) {
		t.Fatalf("want ErrNoRepo, got %v", err)
	}
	// This test file lives inside the real checkout.
	if _, err := FindRepoRoot("."); err != nil {
		t.Fatalf("real checkout: %v", err)
	}
}

// TestDockerComposeConfig resolves every generated kit with the real docker
// compose when it is installed (acceptance env-roundtrip).
func TestDockerComposeConfig(t *testing.T) {
	if _, err := exec.LookPath("docker"); err != nil {
		t.Skip("docker not installed")
	}
	if err := exec.Command("docker", "compose", "version").Run(); err != nil {
		t.Skip("docker compose not available")
	}
	root, err := FindRepoRoot(".")
	if err != nil {
		t.Fatal(err)
	}
	for _, tc := range goldenCases {
		p := tc.p
		if p.Context != "" {
			p.Context = root
		}
		kit, err := Render(p)
		if err != nil {
			t.Fatal(err)
		}
		dir := filepath.Join(t.TempDir(), tc.name)
		if err := Write(kit, dir, false); err != nil {
			t.Fatal(err)
		}
		cmd := exec.Command("docker", "compose", "config", "--quiet")
		cmd.Dir = dir
		var stderr bytes.Buffer
		cmd.Stderr = &stderr
		if err := cmd.Run(); err != nil {
			t.Fatalf("%s: docker compose config: %v\n%s", tc.name, err, stderr.String())
		}
		if strings.Contains(strings.ToLower(stderr.String()), "warn") {
			t.Fatalf("%s: docker compose config warned:\n%s", tc.name, stderr.String())
		}
	}
}
