package config

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/santhosh-tekuri/jsonschema/v6"
	"gopkg.in/yaml.v3"
)

func TestLoadSecurityPolicyFromYAML(t *testing.T) {
	path := filepath.Join(t.TempDir(), "security.yaml")
	body := []byte(`
mime_magic:
  enabled: false
  prefix_bytes: 128
  reject_script_uploads: false
  allowed_script_types:
    shell: true
  allowed_script_extensions:
    py: true
  allow_file_types:
    images: true
    pdf: true
  deny_file_types:
    exe: true
  allow_mime_types:
    image/jpeg: true
    text/plain: true
  deny_mime_types:
    application/x-executable: true
  equivalent_mime_types:
    - [image/jpeg, image/pjpeg]
archive_guard:
  enabled: true
  strict: true
  allow_encrypted: false
  max_total_uncompressed_bytes: 2048
  max_single_entry_bytes: 1024
  max_compression_ratio: 10
  max_entries: 12
  max_depth: 2
  max_filename_bytes: 128
  max_inspection_time_ms: 1000
  max_probe_bytes: 4096
  worker_memory_bytes: 1048576
  decompress_buffer_bytes: 4096
`)
	if err := os.WriteFile(path, body, 0o644); err != nil {
		t.Fatal(err)
	}

	_, err := LoadSecurityPolicy(path)
	if err == nil {
		t.Fatal("expected prefix_bytes validation error")
	}

	body = []byte(`
mime_magic:
  enabled: false
  prefix_bytes: 512
  reject_script_uploads: false
  allowed_script_types:
    shell: true
  allowed_script_extensions:
    py: true
  allow_file_types:
    images: true
    pdf: true
  deny_file_types:
    exe: true
  allow_mime_types:
    image/jpeg: true
    text/plain: true
  deny_mime_types:
    application/x-executable: true
  equivalent_mime_types:
    - [image/jpeg, image/pjpeg]
archive_guard:
  enabled: true
  strict: true
  allow_encrypted: false
  max_total_uncompressed_bytes: 2048
  max_single_entry_bytes: 1024
  max_compression_ratio: 10
  max_entries: 12
  max_depth: 2
  max_filename_bytes: 128
  max_inspection_time_ms: 1000
  max_probe_bytes: 4096
  worker_memory_bytes: 1048576
  decompress_buffer_bytes: 4096
`)
	if err := os.WriteFile(path, body, 0o644); err != nil {
		t.Fatal(err)
	}

	policy, err := LoadSecurityPolicy(path)
	if err != nil {
		t.Fatal(err)
	}
	if policy.MimeMagic.Enabled {
		t.Fatal("enabled should be false from YAML")
	}
	if policy.MimeMagic.PrefixBytes != 512 {
		t.Fatalf("prefix bytes = %d", policy.MimeMagic.PrefixBytes)
	}
	if policy.MimeMagic.RejectScriptUploads {
		t.Fatal("reject_script_uploads should be false from YAML")
	}
	if !policy.MimeMagic.AllowedScriptTypes["shell"] {
		t.Fatalf("allowed script types = %+v", policy.MimeMagic.AllowedScriptTypes)
	}
	if !policy.MimeMagic.AllowedScriptExtensions["py"] {
		t.Fatalf("allowed script extensions = %+v", policy.MimeMagic.AllowedScriptExtensions)
	}
	if len(policy.MimeMagic.AllowFileTypes) != 2 || !policy.MimeMagic.AllowFileTypes["images"] {
		t.Fatalf("allow file types = %+v", policy.MimeMagic.AllowFileTypes)
	}
	if len(policy.MimeMagic.ExpandedAllowMIMETypes) == 0 {
		t.Fatal("expanded allow MIME types are empty")
	}
	if !containsString(policy.MimeMagic.ExpandedAllowMIMETypes, "image/png") || !containsString(policy.MimeMagic.ExpandedAllowMIMETypes, "application/pdf") {
		t.Fatalf("expanded allow MIME types = %+v", policy.MimeMagic.ExpandedAllowMIMETypes)
	}
	if len(policy.MimeMagic.DenyFileTypes) != 1 || !policy.MimeMagic.DenyFileTypes["exe"] {
		t.Fatalf("deny file types = %+v", policy.MimeMagic.DenyFileTypes)
	}
	if len(policy.MimeMagic.AllowMIMETypes) != 2 || !policy.MimeMagic.AllowMIMETypes["image/jpeg"] {
		t.Fatalf("allow list = %+v", policy.MimeMagic.AllowMIMETypes)
	}
	if !policy.MimeMagic.DenyMIMETypes["application/x-executable"] {
		t.Fatalf("deny list = %+v", policy.MimeMagic.DenyMIMETypes)
	}
	if len(policy.MimeMagic.EquivalentMIMETypes) != 1 || policy.MimeMagic.EquivalentMIMETypes[0][1] != "image/pjpeg" {
		t.Fatalf("equivalent groups = %+v", policy.MimeMagic.EquivalentMIMETypes)
	}
	if !policy.ArchiveGuard.Enabled || !policy.ArchiveGuard.Strict {
		t.Fatalf("archive guard flags = %+v", policy.ArchiveGuard)
	}
	if policy.ArchiveGuard.MaxTotalUncompressedBytes != 2048 || policy.ArchiveGuard.MaxSingleEntryBytes != 1024 {
		t.Fatalf("archive size limits = %+v", policy.ArchiveGuard)
	}
	if policy.ArchiveGuard.DecompressBufferBytes != 4096 {
		t.Fatalf("archive decompress buffer = %d", policy.ArchiveGuard.DecompressBufferBytes)
	}
}

func TestLoadSecurityPolicyRejectsUnknownSwitches(t *testing.T) {
	path := filepath.Join(t.TempDir(), "security.yaml")
	body := []byte(`
mime_magic:
  allow_file_types:
    pdff: true
`)
	if err := os.WriteFile(path, body, 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := LoadSecurityPolicy(path); err == nil {
		t.Fatal("expected unknown file type to fail schema validation")
	}
}

func TestLoadSecurityPolicyRejectsListSyntax(t *testing.T) {
	path := filepath.Join(t.TempDir(), "security.yaml")
	body := []byte(`
mime_magic:
  allow_file_types:
    - pdf
`)
	if err := os.WriteFile(path, body, 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := LoadSecurityPolicy(path); err == nil {
		t.Fatal("expected list syntax to fail schema validation")
	}
}

func TestLoadSecurityPolicyRejectsUnknownScriptType(t *testing.T) {
	path := filepath.Join(t.TempDir(), "security.yaml")
	body := []byte(`
mime_magic:
  allowed_script_types:
    pythno: true
`)
	if err := os.WriteFile(path, body, 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := LoadSecurityPolicy(path); err == nil {
		t.Fatal("expected unknown script type to fail schema validation")
	}
}

func TestSecuritySchemaFileValidatesSampleConfig(t *testing.T) {
	schema := loadSecuritySchemaFile(t)
	body, err := os.ReadFile("../../config/security.yaml")
	if err != nil {
		t.Fatal(err)
	}
	if err := validateYAMLWithSchema(body, schema); err != nil {
		t.Fatal(err)
	}
}

func TestSecuritySchemaFileRejectsTypo(t *testing.T) {
	schema := loadSecuritySchemaFile(t)
	body := []byte(`
mime_magic:
  allow_file_types:
    pdff: true
`)
	if err := validateYAMLWithSchema(body, schema); err == nil {
		t.Fatal("expected schema file to reject typo")
	}
}

func TestEnvPrefersSUPrefix(t *testing.T) {
	t.Setenv("UPLOAD_BASE_PATH", "/legacy")
	t.Setenv("SU_UPLOAD_BASE_PATH", "/prefixed")

	if got := env("UPLOAD_BASE_PATH", ""); got != "/prefixed" {
		t.Fatalf("env = %q", got)
	}
}

func TestThumbnailVideoCandidateKeyframesConfig(t *testing.T) {
	path := filepath.Join(t.TempDir(), "security.yaml")
	body := []byte(`
thumbnails:
  enabled: true
  video_candidate_keyframes: 24
`)
	if err := os.WriteFile(path, body, 0o644); err != nil {
		t.Fatal(err)
	}
	policy, err := LoadSecurityPolicy(path)
	if err != nil {
		t.Fatal(err)
	}
	if policy.Thumbnails.VideoCandidateKeyframes != 24 {
		t.Fatalf("video candidate keyframes = %d", policy.Thumbnails.VideoCandidateKeyframes)
	}

	body = []byte(`
thumbnails:
  enabled: true
  video_candidate_keyframes: 60
`)
	if err := os.WriteFile(path, body, 0o644); err != nil {
		t.Fatal(err)
	}
	policy, err = LoadSecurityPolicy(path)
	if err != nil {
		t.Fatal(err)
	}
	if policy.Thumbnails.VideoCandidateKeyframes != 60 {
		t.Fatalf("max video candidate keyframes = %d", policy.Thumbnails.VideoCandidateKeyframes)
	}
}

func TestLoadEnablesClamAVFromEnvironment(t *testing.T) {
	t.Setenv("SU_CLAMAV_HOST", "clamd.local:3310")
	t.Setenv("SU_CLAMAV_SCAN_TIMEOUT_MS", "12000")
	t.Setenv("SU_CLAMAV_STREAM_CHUNK_BYTES", "65536")

	cfg := Load()
	if !cfg.Security.ClamAV.Enabled {
		t.Fatal("clamav should be enabled when SU_CLAMAV_HOST is set")
	}
	if cfg.Security.ClamAV.Address != "clamd.local:3310" {
		t.Fatalf("clamav address = %q", cfg.Security.ClamAV.Address)
	}
	if cfg.Security.ClamAV.ScanTimeoutMS != 12000 || cfg.Security.ClamAV.StreamChunkBytes != 65536 {
		t.Fatalf("clamav limits = %+v", cfg.Security.ClamAV)
	}
}

func loadSecuritySchemaFile(t *testing.T) *jsonschema.Schema {
	t.Helper()
	body, err := os.ReadFile("../../config/security.schema.json")
	if err != nil {
		t.Fatal(err)
	}
	var schemaDoc any
	if err := json.Unmarshal(body, &schemaDoc); err != nil {
		t.Fatal(err)
	}
	compiler := jsonschema.NewCompiler()
	if err := compiler.AddResource("security.schema.json", schemaDoc); err != nil {
		t.Fatal(err)
	}
	schema, err := compiler.Compile("security.schema.json")
	if err != nil {
		t.Fatal(err)
	}
	return schema
}

func validateYAMLWithSchema(body []byte, schema *jsonschema.Schema) error {
	var raw any
	if err := yaml.Unmarshal(body, &raw); err != nil {
		return err
	}
	jsonBody, err := json.Marshal(yamlToJSONValue(raw))
	if err != nil {
		return err
	}
	var value any
	if err := json.Unmarshal(jsonBody, &value); err != nil {
		return err
	}
	return schema.Validate(value)
}

func TestMIMEFileTypeExpandsCategoriesAndShortNames(t *testing.T) {
	tests := map[string]string{
		"images": "image/png",
		"jpeg":   "image/jpeg",
		".png":   "image/png",
		"pdf":    "application/pdf",
		"heic":   "image/heic",
		"jxl":    "image/jxl",
		"jp2":    "image/jp2",
		"psd":    "image/vnd.adobe.photoshop",
		"tga":    "image/x-tga",
	}
	for input, want := range tests {
		if got := MIMEFileType(input); !containsString(got, want) {
			t.Fatalf("MIMEFileType(%q) = %+v, want %q", input, got, want)
		}
	}
}

func containsString(values []string, want string) bool {
	for _, value := range values {
		if value == want {
			return true
		}
	}
	return false
}
