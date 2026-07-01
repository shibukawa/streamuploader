package config

import (
	"encoding/json"
	"fmt"
	"log"
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/santhosh-tekuri/jsonschema/v6"
	"gopkg.in/yaml.v3"
)

type Config struct {
	Addr                    string
	BackendAddr             string
	BackendBasePath         string
	BackendAuthToken        string
	Mode                    string
	PublicBaseURL           string
	UploadBasePath          string
	ApplicationServerURL    string
	AllowedOrigins          []string
	Bucket                  string
	S3Endpoint              string
	S3PublicEndpoint        string
	S3Region                string
	S3AccessKey             string
	S3SecretKey             string
	S3ForcePathStyle        bool
	S3PublicRead            bool
	SessionTTL              time.Duration
	MaxUploadBytes          int64
	SecurityConfigPath      string
	Security                SecurityPolicy
	PresignTTL              time.Duration
	AllowFrontendFileAccess bool
	EnableSharedKey         bool
	SharedKeyBits           int
	SharedKeyPrefix         string
	MaxArchiveFiles         int
	MaxArchiveBytes         int64
}

type SecurityPolicy struct {
	MimeMagic    MimeMagicPolicy    `yaml:"mime_magic"`
	ArchiveGuard ArchiveGuardPolicy `yaml:"archive_guard"`
}

type MimeMagicPolicy struct {
	Enabled                 bool            `yaml:"enabled"`
	PrefixBytes             int64           `yaml:"prefix_bytes"`
	RejectScriptUploads     bool            `yaml:"reject_script_uploads"`
	AllowFileTypes          map[string]bool `yaml:"allow_file_types"`
	DenyFileTypes           map[string]bool `yaml:"deny_file_types"`
	AllowMIMETypes          map[string]bool `yaml:"allow_mime_types"`
	DenyMIMETypes           map[string]bool `yaml:"deny_mime_types"`
	AllowedScriptTypes      map[string]bool `yaml:"allowed_script_types"`
	AllowedScriptExtensions map[string]bool `yaml:"allowed_script_extensions"`
	EquivalentMIMETypes     [][]string      `yaml:"equivalent_mime_types"`
	ExpandedAllowMIMETypes  []string        `yaml:"-"`
	ExpandedDenyMIMETypes   []string        `yaml:"-"`
}

type ArchiveGuardPolicy struct {
	Enabled                   bool    `yaml:"enabled"`
	Strict                    bool    `yaml:"strict"`
	AllowEncrypted            bool    `yaml:"allow_encrypted"`
	MaxTotalUncompressedBytes int64   `yaml:"max_total_uncompressed_bytes"`
	MaxSingleEntryBytes       int64   `yaml:"max_single_entry_bytes"`
	MaxCompressionRatio       float64 `yaml:"max_compression_ratio"`
	MaxEntries                int64   `yaml:"max_entries"`
	MaxDepth                  int64   `yaml:"max_depth"`
	MaxFilenameBytes          int64   `yaml:"max_filename_bytes"`
	MaxInspectionTimeMS       int64   `yaml:"max_inspection_time_ms"`
	MaxProbeBytes             int64   `yaml:"max_probe_bytes"`
	WorkerMemoryBytes         int64   `yaml:"worker_memory_bytes"`
	DecompressBufferBytes     int64   `yaml:"decompress_buffer_bytes"`
}

func Load() Config {
	securityConfigPath := env("SECURITY_CONFIG", "")
	security, err := LoadSecurityPolicy(securityConfigPath)
	if err != nil {
		log.Fatalf("load security config: %v", err)
	}
	security.MimeMagic.Enabled = envBool("MIME_MIGAIC_CHECK", security.MimeMagic.Enabled)
	security.MimeMagic.Enabled = envBool("MIME_MAGIC_CHECK", security.MimeMagic.Enabled)
	return Config{
		Addr:             env("ADDR", ":8080"),
		BackendAddr:      env("BACKEND_ADDR", ""),
		BackendBasePath:  cleanBasePathDefault(env("BACKEND_BASE_PATH", "/internal"), "/internal"),
		BackendAuthToken: env("BACKEND_AUTH_TOKEN", ""),
		Mode:             env("MODE", "simple_fronting_reverse_proxy"),
		PublicBaseURL:    env("PUBLIC_BASE_URL", "http://localhost:8080"),
		UploadBasePath:   cleanBasePath(env("UPLOAD_BASE_PATH", "/api/upload")),
		ApplicationServerURL: env("APPLICATION_SERVER_URL",
			env("APP_SERVER_URL", "http://demo-app:8081")),
		AllowedOrigins:          split(env("ALLOWED_ORIGINS", "*")),
		Bucket:                  env("S3_BUCKET", "stream-upload"),
		S3Endpoint:              env("S3_ENDPOINT", "http://rustfs:9000"),
		S3PublicEndpoint:        env("S3_PUBLIC_ENDPOINT", "http://localhost:9000"),
		S3Region:                env("S3_REGION", "us-east-1"),
		S3AccessKey:             env("S3_ACCESS_KEY_ID", "rustfsadmin"),
		S3SecretKey:             env("S3_SECRET_ACCESS_KEY", "rustfsadmin"),
		S3ForcePathStyle:        envBool("S3_FORCE_PATH_STYLE", true),
		S3PublicRead:            envBool("S3_PUBLIC_READ", false),
		SessionTTL:              envDuration("SESSION_TTL", 24*time.Hour),
		MaxUploadBytes:          envInt64("MAX_UPLOAD_BYTES", 1<<30),
		SecurityConfigPath:      securityConfigPath,
		Security:                security,
		PresignTTL:              envDuration("PRESIGN_TTL", 15*time.Minute),
		AllowFrontendFileAccess: envBool("ALLOW_FRONTEND_FILE_ACCESS", false),
		EnableSharedKey:         envBool("ENABLE_SHARED_KEY", false),
		SharedKeyBits:           envInt("SHARED_KEY_BITS", 128),
		SharedKeyPrefix:         cleanPrefix(env("SHARED_KEY_PREFIX", ".streamuploader/shared/")),
		MaxArchiveFiles:         envInt("MAX_ARCHIVE_FILES", 100),
		MaxArchiveBytes:         envInt64("MAX_ARCHIVE_BYTES", 1<<30),
	}
}

func DefaultSecurityPolicy() SecurityPolicy {
	return SecurityPolicy{
		MimeMagic: MimeMagicPolicy{
			Enabled:             true,
			PrefixBytes:         3072,
			RejectScriptUploads: true,
			EquivalentMIMETypes: [][]string{
				{"application/xml", "text/xml"},
				{"image/jpeg", "image/pjpeg"},
				{"application/gzip", "application/x-gzip"},
			},
			AllowFileTypes:          map[string]bool{},
			DenyFileTypes:           map[string]bool{},
			AllowMIMETypes:          map[string]bool{},
			AllowedScriptTypes:      map[string]bool{},
			AllowedScriptExtensions: map[string]bool{},
			DenyMIMETypes: map[string]bool{
				"application/x-dosexec":    true,
				"application/x-executable": true,
				"application/x-sharedlib":  true,
				"application/x-msdownload": true,
			},
		},
		ArchiveGuard: ArchiveGuardPolicy{
			Enabled:                   true,
			Strict:                    true,
			AllowEncrypted:            false,
			MaxTotalUncompressedBytes: 512 << 20,
			MaxSingleEntryBytes:       256 << 20,
			MaxCompressionRatio:       100,
			MaxEntries:                10000,
			MaxDepth:                  3,
			MaxFilenameBytes:          512,
			MaxInspectionTimeMS:       5000,
			MaxProbeBytes:             64 << 20,
			WorkerMemoryBytes:         64 << 20,
			DecompressBufferBytes:     32 << 10,
		},
	}
}

func LoadSecurityPolicy(path string) (SecurityPolicy, error) {
	policy := DefaultSecurityPolicy()
	path = strings.TrimSpace(path)
	if path == "" {
		return policy, nil
	}
	body, err := os.ReadFile(path)
	if err != nil {
		return SecurityPolicy{}, fmt.Errorf("%s: %w", path, err)
	}
	if err := ValidateSecurityPolicyYAML(body); err != nil {
		return SecurityPolicy{}, fmt.Errorf("%s: %w", path, err)
	}
	if err := yaml.Unmarshal(body, &policy); err != nil {
		return SecurityPolicy{}, fmt.Errorf("%s: %w", path, err)
	}
	normalizeSecurityPolicy(&policy)
	return policy, nil
}

func normalizeSecurityPolicy(policy *SecurityPolicy) {
	if policy.MimeMagic.PrefixBytes <= 0 {
		policy.MimeMagic.PrefixBytes = 3072
	}
	if policy.MimeMagic.PrefixBytes < 512 {
		policy.MimeMagic.PrefixBytes = 512
	}
	if policy.MimeMagic.PrefixBytes > 1<<20 {
		policy.MimeMagic.PrefixBytes = 1 << 20
	}
	policy.MimeMagic.AllowFileTypes = normalizeFileTypeSwitches(policy.MimeMagic.AllowFileTypes)
	policy.MimeMagic.DenyFileTypes = normalizeFileTypeSwitches(policy.MimeMagic.DenyFileTypes)
	policy.MimeMagic.AllowMIMETypes = normalizeMIMESwitches(policy.MimeMagic.AllowMIMETypes)
	policy.MimeMagic.DenyMIMETypes = normalizeMIMESwitches(policy.MimeMagic.DenyMIMETypes)
	policy.MimeMagic.AllowedScriptTypes = normalizeScriptSwitches(policy.MimeMagic.AllowedScriptTypes)
	policy.MimeMagic.AllowedScriptExtensions = normalizeExtensionSwitches(policy.MimeMagic.AllowedScriptExtensions)
	for i, group := range policy.MimeMagic.EquivalentMIMETypes {
		policy.MimeMagic.EquivalentMIMETypes[i] = normalizeMIMETypes(group)
	}
	policy.MimeMagic.ExpandedAllowMIMETypes = expandMIMETypes(policy.MimeMagic.AllowMIMETypes, policy.MimeMagic.AllowFileTypes)
	policy.MimeMagic.ExpandedDenyMIMETypes = expandMIMETypes(policy.MimeMagic.DenyMIMETypes, policy.MimeMagic.DenyFileTypes)
	normalizeArchiveGuardPolicy(&policy.ArchiveGuard)
}

func normalizeArchiveGuardPolicy(policy *ArchiveGuardPolicy) {
	defaults := DefaultSecurityPolicy().ArchiveGuard
	if policy.MaxTotalUncompressedBytes <= 0 {
		policy.MaxTotalUncompressedBytes = defaults.MaxTotalUncompressedBytes
	}
	if policy.MaxSingleEntryBytes <= 0 {
		policy.MaxSingleEntryBytes = defaults.MaxSingleEntryBytes
	}
	if policy.MaxCompressionRatio <= 0 {
		policy.MaxCompressionRatio = defaults.MaxCompressionRatio
	}
	if policy.MaxEntries <= 0 {
		policy.MaxEntries = defaults.MaxEntries
	}
	if policy.MaxDepth <= 0 {
		policy.MaxDepth = defaults.MaxDepth
	}
	if policy.MaxFilenameBytes <= 0 {
		policy.MaxFilenameBytes = defaults.MaxFilenameBytes
	}
	if policy.MaxInspectionTimeMS <= 0 {
		policy.MaxInspectionTimeMS = defaults.MaxInspectionTimeMS
	}
	if policy.MaxProbeBytes <= 0 {
		policy.MaxProbeBytes = defaults.MaxProbeBytes
	}
	if policy.WorkerMemoryBytes <= 0 {
		policy.WorkerMemoryBytes = defaults.WorkerMemoryBytes
	}
	if policy.DecompressBufferBytes <= 0 {
		policy.DecompressBufferBytes = defaults.DecompressBufferBytes
	}
	if policy.DecompressBufferBytes < 4096 {
		policy.DecompressBufferBytes = 4096
	}
	if policy.DecompressBufferBytes > 1<<20 {
		policy.DecompressBufferBytes = 1 << 20
	}
}

func normalizeScriptSwitches(values map[string]bool) map[string]bool {
	if values == nil {
		return map[string]bool{}
	}
	out := map[string]bool{}
	for key, enabled := range values {
		normalized := strings.ToLower(strings.TrimSpace(key))
		if normalized != "" {
			out[normalized] = enabled
		}
	}
	return out
}

func normalizeExtensionSwitches(values map[string]bool) map[string]bool {
	if values == nil {
		return map[string]bool{}
	}
	out := map[string]bool{}
	for key, enabled := range values {
		normalized := strings.ToLower(strings.TrimPrefix(strings.TrimSpace(key), "."))
		if normalized != "" {
			out[normalized] = enabled
		}
	}
	return out
}

func normalizeFileTypeSwitches(values map[string]bool) map[string]bool {
	if values == nil {
		return map[string]bool{}
	}
	out := map[string]bool{}
	for key, enabled := range values {
		normalized := normalizeFileType(key)
		if normalized != "" {
			out[normalized] = enabled
		}
	}
	return out
}

func normalizeMIMESwitches(values map[string]bool) map[string]bool {
	if values == nil {
		return map[string]bool{}
	}
	out := map[string]bool{}
	for key, enabled := range values {
		normalized := strings.ToLower(strings.TrimSpace(key))
		if normalized != "" {
			out[normalized] = enabled
		}
	}
	return out
}

func normalizeMIMETypes(values []string) []string {
	out := make([]string, 0, len(values))
	seen := map[string]struct{}{}
	for _, value := range values {
		normalized := strings.ToLower(strings.TrimSpace(value))
		if normalized == "" {
			continue
		}
		if _, ok := seen[normalized]; ok {
			continue
		}
		seen[normalized] = struct{}{}
		out = append(out, normalized)
	}
	return out
}

func expandMIMETypes(mimeTypes, fileTypes map[string]bool) []string {
	out := make([]string, 0, len(mimeTypes))
	seen := map[string]struct{}{}
	add := func(value string) {
		normalized := strings.ToLower(strings.TrimSpace(value))
		if normalized == "" {
			return
		}
		if _, ok := seen[normalized]; ok {
			return
		}
		seen[normalized] = struct{}{}
		out = append(out, normalized)
	}
	for value, enabled := range mimeTypes {
		if enabled {
			add(value)
		}
	}
	for value, enabled := range fileTypes {
		if !enabled {
			continue
		}
		for _, mimeType := range MIMEFileType(value) {
			add(mimeType)
		}
	}
	return out
}

func MIMEFileType(value string) []string {
	mimeTypes := mimeFileTypes[normalizeFileType(value)]
	return append([]string(nil), mimeTypes...)
}

func normalizeFileType(value string) string {
	return strings.ToLower(strings.TrimPrefix(strings.TrimSpace(value), "."))
}

var mimeFileTypes = map[string][]string{
	"images":     {"image/png", "image/jpeg", "image/gif", "image/webp", "image/avif", "image/tiff", "image/bmp", "image/svg+xml"},
	"image":      {"image/png", "image/jpeg", "image/gif", "image/webp", "image/avif", "image/tiff", "image/bmp", "image/svg+xml"},
	"documents":  {"application/pdf", "text/plain", "text/csv", "application/rtf", "application/msword", "application/vnd.openxmlformats-officedocument.wordprocessingml.document", "application/vnd.ms-excel", "application/vnd.openxmlformats-officedocument.spreadsheetml.sheet", "application/vnd.ms-powerpoint", "application/vnd.openxmlformats-officedocument.presentationml.presentation"},
	"document":   {"application/pdf", "text/plain", "text/csv", "application/rtf", "application/msword", "application/vnd.openxmlformats-officedocument.wordprocessingml.document", "application/vnd.ms-excel", "application/vnd.openxmlformats-officedocument.spreadsheetml.sheet", "application/vnd.ms-powerpoint", "application/vnd.openxmlformats-officedocument.presentationml.presentation"},
	"archives":   {"application/zip", "application/gzip", "application/x-gzip", "application/zstd", "application/x-zstd", "application/x-brotli", "application/br", "application/x-tar", "application/x-bzip2", "application/x-xz", "application/x-7z-compressed"},
	"archive":    {"application/zip", "application/gzip", "application/x-gzip", "application/zstd", "application/x-zstd", "application/x-brotli", "application/br", "application/x-tar", "application/x-bzip2", "application/x-xz", "application/x-7z-compressed"},
	"audio":      {"audio/mpeg", "audio/mp4", "audio/ogg", "audio/wav", "audio/webm", "audio/flac", "audio/aac"},
	"videos":     {"video/mp4", "video/mpeg", "video/quicktime", "video/webm", "video/x-msvideo", "video/x-matroska"},
	"video":      {"video/mp4", "video/mpeg", "video/quicktime", "video/webm", "video/x-msvideo", "video/x-matroska"},
	"text":       {"text/plain", "text/csv", "text/markdown", "application/json", "application/xml", "text/xml"},
	"png":        {"image/png"},
	"jpeg":       {"image/jpeg", "image/pjpeg"},
	"jpg":        {"image/jpeg", "image/pjpeg"},
	"gif":        {"image/gif"},
	"webp":       {"image/webp"},
	"avif":       {"image/avif"},
	"tiff":       {"image/tiff"},
	"tif":        {"image/tiff"},
	"bmp":        {"image/bmp"},
	"svg":        {"image/svg+xml"},
	"pdf":        {"application/pdf"},
	"txt":        {"text/plain"},
	"plain":      {"text/plain"},
	"csv":        {"text/csv"},
	"json":       {"application/json"},
	"xml":        {"application/xml", "text/xml"},
	"zip":        {"application/zip"},
	"gzip":       {"application/gzip", "application/x-gzip"},
	"gz":         {"application/gzip", "application/x-gzip"},
	"zstd":       {"application/zstd", "application/x-zstd"},
	"zst":        {"application/zstd", "application/x-zstd"},
	"brotli":     {"application/x-brotli", "application/br"},
	"br":         {"application/x-brotli", "application/br"},
	"bzip2":      {"application/x-bzip2"},
	"bz2":        {"application/x-bzip2"},
	"xz":         {"application/x-xz"},
	"7z":         {"application/x-7z-compressed"},
	"exe":        {"application/x-dosexec", "application/x-executable", "application/x-sharedlib", "application/x-msdownload"},
	"dll":        {"application/x-dosexec", "application/x-executable", "application/x-sharedlib", "application/x-msdownload"},
	"elf":        {"application/x-dosexec", "application/x-executable", "application/x-sharedlib", "application/x-msdownload"},
	"executable": {"application/x-dosexec", "application/x-executable", "application/x-sharedlib", "application/x-msdownload"},
}

func ValidateSecurityPolicyYAML(body []byte) error {
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
	var schemaDoc any
	if err := json.Unmarshal([]byte(SecurityPolicyJSONSchema()), &schemaDoc); err != nil {
		return err
	}
	compiler := jsonschema.NewCompiler()
	if err := compiler.AddResource("streamuploader-security.schema.json", schemaDoc); err != nil {
		return err
	}
	schema, err := compiler.Compile("streamuploader-security.schema.json")
	if err != nil {
		return err
	}
	return schema.Validate(value)
}

func yamlToJSONValue(value any) any {
	switch typed := value.(type) {
	case map[string]any:
		out := map[string]any{}
		for key, value := range typed {
			out[key] = yamlToJSONValue(value)
		}
		return out
	case map[any]any:
		out := map[string]any{}
		for key, value := range typed {
			out[fmt.Sprint(key)] = yamlToJSONValue(value)
		}
		return out
	case []any:
		out := make([]any, len(typed))
		for i, value := range typed {
			out[i] = yamlToJSONValue(value)
		}
		return out
	default:
		return value
	}
}

func SecurityPolicyJSONSchema() string {
	schema := map[string]any{
		"$schema":              "https://json-schema.org/draft/2020-12/schema",
		"title":                "StreamUploader security policy",
		"type":                 "object",
		"additionalProperties": false,
		"properties": map[string]any{
			"mime_magic": map[string]any{
				"type":                 "object",
				"additionalProperties": false,
				"properties": map[string]any{
					"enabled":                   map[string]any{"type": "boolean"},
					"prefix_bytes":              map[string]any{"type": "integer", "minimum": 512, "maximum": 1048576},
					"reject_script_uploads":     map[string]any{"type": "boolean"},
					"allow_file_types":          boolSwitchSchema(knownFileTypeNames()),
					"deny_file_types":           boolSwitchSchema(knownFileTypeNames()),
					"allow_mime_types":          boolSwitchSchema(knownMIMETypes()),
					"deny_mime_types":           boolSwitchSchema(knownMIMETypes()),
					"allowed_script_types":      boolSwitchSchema(knownScriptTypes()),
					"allowed_script_extensions": boolSwitchSchema(knownScriptExtensions()),
					"equivalent_mime_types": map[string]any{
						"type": "array",
						"items": map[string]any{
							"type":     "array",
							"minItems": 2,
							"items":    map[string]any{"type": "string", "enum": knownMIMETypes()},
						},
					},
				},
			},
			"archive_guard": map[string]any{
				"type":                 "object",
				"additionalProperties": false,
				"properties": map[string]any{
					"enabled":                      map[string]any{"type": "boolean"},
					"strict":                       map[string]any{"type": "boolean"},
					"allow_encrypted":              map[string]any{"type": "boolean"},
					"max_total_uncompressed_bytes": map[string]any{"type": "integer", "minimum": 1},
					"max_single_entry_bytes":       map[string]any{"type": "integer", "minimum": 1},
					"max_compression_ratio":        map[string]any{"type": "number", "exclusiveMinimum": 0},
					"max_entries":                  map[string]any{"type": "integer", "minimum": 1},
					"max_depth":                    map[string]any{"type": "integer", "minimum": 1},
					"max_filename_bytes":           map[string]any{"type": "integer", "minimum": 1},
					"max_inspection_time_ms":       map[string]any{"type": "integer", "minimum": 1},
					"max_probe_bytes":              map[string]any{"type": "integer", "minimum": 1},
					"worker_memory_bytes":          map[string]any{"type": "integer", "minimum": 1},
					"decompress_buffer_bytes":      map[string]any{"type": "integer", "minimum": 4096, "maximum": 1048576},
				},
			},
		},
	}
	body, err := json.Marshal(schema)
	if err != nil {
		panic(err)
	}
	return string(body)
}

func knownScriptTypes() []string {
	return []string{"shell", "python", "node", "ruby", "perl", "php"}
}

func knownScriptExtensions() []string {
	return []string{"sh", "bash", "zsh", "ksh", "py", "js", "mjs", "cjs", "rb", "pl", "php"}
}

func boolSwitchSchema(keys []string) map[string]any {
	properties := map[string]any{}
	for _, key := range keys {
		properties[key] = map[string]any{"type": "boolean"}
	}
	return map[string]any{
		"type":                 "object",
		"additionalProperties": false,
		"properties":           properties,
	}
}

func knownFileTypeNames() []string {
	out := make([]string, 0, len(mimeFileTypes))
	for key := range mimeFileTypes {
		out = append(out, key)
	}
	return out
}

func knownMIMETypes() []string {
	seen := map[string]struct{}{}
	for _, values := range mimeFileTypes {
		for _, value := range values {
			seen[value] = struct{}{}
		}
	}
	for _, group := range DefaultSecurityPolicy().MimeMagic.EquivalentMIMETypes {
		for _, value := range group {
			seen[value] = struct{}{}
		}
	}
	for _, value := range []string{"text/x-shellscript", "text/x-python", "text/javascript", "text/x-ruby", "text/x-perl", "application/x-httpd-php"} {
		seen[value] = struct{}{}
	}
	out := make([]string, 0, len(seen))
	for value := range seen {
		out = append(out, value)
	}
	return out
}

func env(key, fallback string) string {
	if value := os.Getenv("SU_" + key); value != "" {
		return value
	}
	if value := os.Getenv(key); value != "" {
		return value
	}
	return fallback
}

func split(value string) []string {
	parts := strings.Split(value, ",")
	out := make([]string, 0, len(parts))
	for _, part := range parts {
		if trimmed := strings.TrimSpace(part); trimmed != "" {
			out = append(out, trimmed)
		}
	}
	return out
}

func envBool(key string, fallback bool) bool {
	value := env(key, "")
	if value == "" {
		return fallback
	}
	parsed, err := strconv.ParseBool(value)
	if err != nil {
		return fallback
	}
	return parsed
}

func envInt64(key string, fallback int64) int64 {
	value := env(key, "")
	if value == "" {
		return fallback
	}
	parsed, err := strconv.ParseInt(value, 10, 64)
	if err != nil {
		return fallback
	}
	return parsed
}

func envInt(key string, fallback int) int {
	value := env(key, "")
	if value == "" {
		return fallback
	}
	parsed, err := strconv.Atoi(value)
	if err != nil {
		return fallback
	}
	return parsed
}

func envDuration(key string, fallback time.Duration) time.Duration {
	value := env(key, "")
	if value == "" {
		return fallback
	}
	parsed, err := time.ParseDuration(value)
	if err != nil {
		return fallback
	}
	return parsed
}

func cleanPrefix(value string) string {
	value = strings.TrimSpace(value)
	value = strings.TrimLeft(value, "/")
	if value == "" {
		value = ".streamuploader/shared/"
	}
	if !strings.HasSuffix(value, "/") {
		value += "/"
	}
	return value
}

func cleanBasePath(value string) string {
	return cleanBasePathDefault(value, "/api/upload")
}

func cleanBasePathDefault(value, fallback string) string {
	value = strings.TrimSpace(value)
	if value == "" || value == "/" {
		return fallback
	}
	if !strings.HasPrefix(value, "/") {
		value = "/" + value
	}
	return strings.TrimRight(value, "/")
}
