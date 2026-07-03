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
	SharedKeyTTL            time.Duration
	SharedKeyMaxTTL         time.Duration
	MaxArchiveFiles         int
	MaxArchiveBytes         int64
	UploadDeadlines         UploadDeadlinePolicy
	HTTPCache               HTTPCachePolicy
	Logging                 LoggingPolicy
	Thumbnails              ThumbnailPolicy
}

type SecurityPolicy struct {
	MimeMagic            MimeMagicPolicy            `yaml:"mime_magic"`
	ArchiveGuard         ArchiveGuardPolicy         `yaml:"archive_guard"`
	ClamAV               ClamAVPolicy               `yaml:"clamav"`
	ResourceLimits       ResourceLimitPolicy        `yaml:"resource_limits"`
	StructuralValidation StructuralValidationPolicy `yaml:"structural_validation"`
	FileSanitization     FileSanitizationPolicy     `yaml:"file_sanitization"`
	UploadDeadlines      UploadDeadlinePolicy       `yaml:"upload_deadlines"`
	SharedKey            SharedKeyPolicy            `yaml:"shared_key"`
	HTTPCache            HTTPCachePolicy            `yaml:"http_cache"`
	Logging              LoggingPolicy              `yaml:"logging"`
	Thumbnails           ThumbnailPolicy            `yaml:"thumbnails"`
}

type MimeMagicPolicy struct {
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

type ClamAVPolicy struct {
	Enabled          bool   `yaml:"enabled"`
	Address          string `yaml:"address"`
	ScanTimeoutMS    int64  `yaml:"scan_timeout_ms"`
	StreamChunkBytes int64  `yaml:"stream_chunk_bytes"`
}

type ResourceLimitPolicy struct {
	Enabled                  bool  `yaml:"enabled"`
	MaxFileSizeBytes         int64 `yaml:"max_file_size_bytes"`
	MaxDecompressedSizeBytes int64 `yaml:"max_decompressed_size_bytes"`
	MaxPDFPageCount          int64 `yaml:"max_pdf_page_count"`
	MaxImageWidth            int   `yaml:"max_image_width"`
	MaxImageHeight           int   `yaml:"max_image_height"`
	MaxImagePixelCount       int64 `yaml:"max_image_pixel_count"`
	MaxObjectCount           int64 `yaml:"max_object_count"`
	MaxXMLDepth              int   `yaml:"max_xml_depth"`
	MaxZIPEntries            int64 `yaml:"max_zip_entries"`
	MaxEmbeddedObjectCount   int64 `yaml:"max_embedded_object_count"`
	MaxParserTimeMS          int64 `yaml:"max_parser_time_ms"`
	MaxSanitizedMemoryBytes  int64 `yaml:"max_sanitized_memory_bytes"`
}

type StructuralValidationPolicy struct {
	Enabled bool `yaml:"enabled"`
	Strict  bool `yaml:"strict"`
}

type FileSanitizationPolicy struct {
	Enabled                  bool                           `yaml:"enabled"`
	DefaultMode              string                         `yaml:"default_mode"`
	PerFileType              map[string]FileTypePolicy      `yaml:"per_file_type"`
	ImageVideoMetadata       ImageVideoMetadataPolicy       `yaml:"image_video_metadata"`
	OfficePDF                OfficePDFSanitizationPolicy    `yaml:"office_pdf"`
	LegacyOffice             LegacyOfficeSanitizationPolicy `yaml:"legacy_office"`
	LegacyOrComplexDocuments LegacyOfficeSanitizationPolicy `yaml:"legacy_or_complex_documents"`
	SVG                      SVGSanitizationPolicy          `yaml:"svg"`
	Markup                   MarkupSanitizationPolicy       `yaml:"markup"`
}

type FileTypePolicy struct {
	Mode string `yaml:"mode"`
}

type ImageVideoMetadataPolicy struct {
	DefaultMode string   `yaml:"default_mode"`
	Preserve    []string `yaml:"preserve"`
	NoReencode  bool     `yaml:"no_reencode"`
}

type OfficePDFSanitizationPolicy struct {
	DefaultMode      string `yaml:"default_mode"`
	FullScanRequired bool   `yaml:"full_scan_required"`
}

type LegacyOfficeSanitizationPolicy struct {
	DefaultMode string `yaml:"default_mode"`
}

type SVGSanitizationPolicy struct {
	DefaultMode              string `yaml:"default_mode"`
	PreferStreamingXMLParser bool   `yaml:"prefer_streaming_xml_parser"`
	AllowDataURLs            bool   `yaml:"allow_data_urls"`
}

type MarkupSanitizationPolicy struct {
	DefaultMode                 string `yaml:"default_mode"`
	MarkdownRawHTMLInspection   bool   `yaml:"markdown_raw_html_inspection"`
	HTMLActiveContentInspection bool   `yaml:"html_active_content_inspection"`
	XMLExternalEntityResolution string `yaml:"xml_external_entity_resolution"`
}

type UploadDeadlinePolicy struct {
	Enabled         bool          `yaml:"enabled"`
	MarkerPrefix    string        `yaml:"marker_prefix"`
	StartTimeout    time.Duration `yaml:"start_timeout"`
	FinishTimeout   time.Duration `yaml:"finish_timeout"`
	CleanupEnabled  bool          `yaml:"cleanup_enabled"`
	CleanupInterval time.Duration `yaml:"cleanup_interval"`
	CleanupMode     string        `yaml:"cleanup_mode"`
}

func (p *UploadDeadlinePolicy) UnmarshalYAML(value *yaml.Node) error {
	type rawPolicy struct {
		Enabled         bool   `yaml:"enabled"`
		MarkerPrefix    string `yaml:"marker_prefix"`
		StartTimeout    string `yaml:"start_timeout"`
		FinishTimeout   string `yaml:"finish_timeout"`
		CleanupEnabled  bool   `yaml:"cleanup_enabled"`
		CleanupInterval string `yaml:"cleanup_interval"`
		CleanupMode     string `yaml:"cleanup_mode"`
	}
	var raw rawPolicy
	if err := value.Decode(&raw); err != nil {
		return err
	}
	start, err := parseOptionalDuration(raw.StartTimeout)
	if err != nil {
		return err
	}
	finish, err := parseOptionalDuration(raw.FinishTimeout)
	if err != nil {
		return err
	}
	cleanup, err := parseOptionalDuration(raw.CleanupInterval)
	if err != nil {
		return err
	}
	*p = UploadDeadlinePolicy{
		Enabled:         raw.Enabled,
		MarkerPrefix:    raw.MarkerPrefix,
		StartTimeout:    start,
		FinishTimeout:   finish,
		CleanupEnabled:  raw.CleanupEnabled,
		CleanupInterval: cleanup,
		CleanupMode:     raw.CleanupMode,
	}
	return nil
}

type SharedKeyPolicy struct {
	DefaultTTL time.Duration `yaml:"default_ttl"`
	MaxTTL     time.Duration `yaml:"max_ttl"`
}

func (p *SharedKeyPolicy) UnmarshalYAML(value *yaml.Node) error {
	var raw struct {
		DefaultTTL string `yaml:"default_ttl"`
		MaxTTL     string `yaml:"max_ttl"`
	}
	if err := value.Decode(&raw); err != nil {
		return err
	}
	defaultTTL, err := parseOptionalDuration(raw.DefaultTTL)
	if err != nil {
		return err
	}
	maxTTL, err := parseOptionalDuration(raw.MaxTTL)
	if err != nil {
		return err
	}
	*p = SharedKeyPolicy{DefaultTTL: defaultTTL, MaxTTL: maxTTL}
	return nil
}

type HTTPCachePolicy struct {
	Mode           string        `yaml:"mode"`
	MaxAge         time.Duration `yaml:"max_age"`
	SMaxAge        time.Duration `yaml:"s_max_age"`
	ForwardETag    bool          `yaml:"forward_etag"`
	ForwardLastMod bool          `yaml:"forward_last_modified"`
}

func (p *HTTPCachePolicy) UnmarshalYAML(value *yaml.Node) error {
	var raw struct {
		Mode           string `yaml:"mode"`
		MaxAge         string `yaml:"max_age"`
		SMaxAge        string `yaml:"s_max_age"`
		ForwardETag    bool   `yaml:"forward_etag"`
		ForwardLastMod bool   `yaml:"forward_last_modified"`
	}
	if err := value.Decode(&raw); err != nil {
		return err
	}
	maxAge, err := parseOptionalDuration(raw.MaxAge)
	if err != nil {
		return err
	}
	sMaxAge, err := parseOptionalDuration(raw.SMaxAge)
	if err != nil {
		return err
	}
	*p = HTTPCachePolicy{
		Mode:           raw.Mode,
		MaxAge:         maxAge,
		SMaxAge:        sMaxAge,
		ForwardETag:    raw.ForwardETag,
		ForwardLastMod: raw.ForwardLastMod,
	}
	return nil
}

type LoggingPolicy struct {
	Format string `yaml:"format"`
	Level  string `yaml:"level"`
}

type ThumbnailPolicy struct {
	Enabled                 bool          `yaml:"enabled"`
	ExecutionMode           string        `yaml:"execution_mode"`
	Width                   int           `yaml:"width"`
	Height                  int           `yaml:"height"`
	Fit                     string        `yaml:"fit"`
	Upscale                 bool          `yaml:"upscale"`
	LosslessPolicy          string        `yaml:"lossless_policy"`
	PreferredFormat         string        `yaml:"preferred_format"`
	ObjectKeySuffix         string        `yaml:"object_key_suffix"`
	ExternalWebhookURL      string        `yaml:"external_webhook_url"`
	ExternalTimeout         time.Duration `yaml:"external_timeout"`
	VideoCandidateKeyframes int           `yaml:"video_candidate_keyframes"`
}

func (p *ThumbnailPolicy) UnmarshalYAML(value *yaml.Node) error {
	type rawPolicy struct {
		Enabled                 bool   `yaml:"enabled"`
		ExecutionMode           string `yaml:"execution_mode"`
		Width                   int    `yaml:"width"`
		Height                  int    `yaml:"height"`
		Fit                     string `yaml:"fit"`
		Upscale                 bool   `yaml:"upscale"`
		LosslessPolicy          string `yaml:"lossless_policy"`
		PreferredFormat         string `yaml:"preferred_format"`
		ObjectKeySuffix         string `yaml:"object_key_suffix"`
		ExternalWebhookURL      string `yaml:"external_webhook_url"`
		ExternalTimeout         string `yaml:"external_timeout"`
		VideoCandidateKeyframes int    `yaml:"video_candidate_keyframes"`
	}
	var raw rawPolicy
	if err := value.Decode(&raw); err != nil {
		return err
	}
	timeout, err := parseOptionalDuration(raw.ExternalTimeout)
	if err != nil {
		return err
	}
	*p = ThumbnailPolicy{
		Enabled:                 raw.Enabled,
		ExecutionMode:           raw.ExecutionMode,
		Width:                   raw.Width,
		Height:                  raw.Height,
		Fit:                     raw.Fit,
		Upscale:                 raw.Upscale,
		LosslessPolicy:          raw.LosslessPolicy,
		PreferredFormat:         raw.PreferredFormat,
		ObjectKeySuffix:         raw.ObjectKeySuffix,
		ExternalWebhookURL:      raw.ExternalWebhookURL,
		ExternalTimeout:         timeout,
		VideoCandidateKeyframes: raw.VideoCandidateKeyframes,
	}
	return nil
}

func parseOptionalDuration(value string) (time.Duration, error) {
	value = strings.TrimSpace(value)
	if value == "" {
		return 0, nil
	}
	return time.ParseDuration(value)
}

func Load() Config {
	securityConfigPath := env("SECURITY_CONFIG", "")
	security, err := LoadSecurityPolicy(securityConfigPath)
	if err != nil {
		log.Fatalf("load security config: %v", err)
	}
	clamAVHost := env("CLAMAV_HOST", env("CLAMAV_ADDR", ""))
	if clamAVHost != "" {
		security.ClamAV.Enabled = true
		security.ClamAV.Address = clamAVHost
	}
	security.ClamAV.Enabled = envBool("CLAMAV_ENABLED", security.ClamAV.Enabled)
	security.ClamAV.ScanTimeoutMS = envInt64("CLAMAV_SCAN_TIMEOUT_MS", security.ClamAV.ScanTimeoutMS)
	security.ClamAV.StreamChunkBytes = envInt64("CLAMAV_STREAM_CHUNK_BYTES", security.ClamAV.StreamChunkBytes)
	security.UploadDeadlines.FinishTimeout = envDuration("UPLOAD_FINISH_TIMEOUT", security.UploadDeadlines.FinishTimeout)
	security.UploadDeadlines.FinishTimeout = envDurationSeconds("UPLOAD_FINISH_TIMEOUT_SECONDS", security.UploadDeadlines.FinishTimeout)
	security.UploadDeadlines.CleanupInterval = envDuration("UPLOAD_CLEANUP_INTERVAL", security.UploadDeadlines.CleanupInterval)
	security.UploadDeadlines.CleanupInterval = envDurationSeconds("UPLOAD_CLEANUP_INTERVAL_SECONDS", security.UploadDeadlines.CleanupInterval)
	security.SharedKey.DefaultTTL = envDuration("SHARED_KEY_TTL", security.SharedKey.DefaultTTL)
	security.SharedKey.DefaultTTL = envDurationSeconds("SHARED_KEY_TTL_SECONDS", security.SharedKey.DefaultTTL)
	security.SharedKey.MaxTTL = envDuration("SHARED_KEY_MAX_TTL", security.SharedKey.MaxTTL)
	security.SharedKey.MaxTTL = envDurationSeconds("SHARED_KEY_MAX_TTL_SECONDS", security.SharedKey.MaxTTL)
	security.HTTPCache.Mode = env("HTTP_CACHE_MODE", security.HTTPCache.Mode)
	security.HTTPCache.MaxAge = envDuration("HTTP_CACHE_MAX_AGE", security.HTTPCache.MaxAge)
	security.HTTPCache.MaxAge = envDurationSeconds("HTTP_CACHE_MAX_AGE_SECONDS", security.HTTPCache.MaxAge)
	security.Logging.Format = env("LOG_FORMAT", env("SLOG_FORMAT", security.Logging.Format))
	security.Logging.Level = env("LOG_LEVEL", security.Logging.Level)
	security.Thumbnails.Enabled = envBool("THUMBNAILS_ENABLED", security.Thumbnails.Enabled)
	security.Thumbnails.ExecutionMode = env("THUMBNAILS_EXECUTION_MODE", security.Thumbnails.ExecutionMode)
	security.Thumbnails.Width = envInt("THUMBNAILS_WIDTH", security.Thumbnails.Width)
	security.Thumbnails.Height = envInt("THUMBNAILS_HEIGHT", security.Thumbnails.Height)
	security.Thumbnails.Fit = env("THUMBNAILS_FIT", security.Thumbnails.Fit)
	security.Thumbnails.Upscale = envBool("THUMBNAILS_UPSCALE", security.Thumbnails.Upscale)
	security.Thumbnails.LosslessPolicy = env("THUMBNAILS_LOSSLESS_POLICY", security.Thumbnails.LosslessPolicy)
	security.Thumbnails.PreferredFormat = env("THUMBNAILS_PREFERRED_FORMAT", security.Thumbnails.PreferredFormat)
	security.Thumbnails.ObjectKeySuffix = env("THUMBNAILS_OBJECT_KEY_SUFFIX", security.Thumbnails.ObjectKeySuffix)
	security.Thumbnails.ExternalWebhookURL = env("THUMBNAIL_WEBHOOK_URL", env("THUMBNAILS_EXTERNAL_WEBHOOK_URL", security.Thumbnails.ExternalWebhookURL))
	security.Thumbnails.ExternalTimeout = envDuration("THUMBNAILS_EXTERNAL_TIMEOUT", security.Thumbnails.ExternalTimeout)
	security.Thumbnails.VideoCandidateKeyframes = envInt("THUMBNAILS_VIDEO_CANDIDATE_KEYFRAMES", security.Thumbnails.VideoCandidateKeyframes)
	normalizeExtendedPolicies(&security)
	normalizeClamAVPolicy(&security.ClamAV)
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
		SharedKeyTTL:            security.SharedKey.DefaultTTL,
		SharedKeyMaxTTL:         security.SharedKey.MaxTTL,
		MaxArchiveFiles:         envInt("MAX_ARCHIVE_FILES", 100),
		MaxArchiveBytes:         envInt64("MAX_ARCHIVE_BYTES", 1<<30),
		UploadDeadlines:         security.UploadDeadlines,
		HTTPCache:               security.HTTPCache,
		Logging:                 security.Logging,
		Thumbnails:              security.Thumbnails,
	}
}

func DefaultSecurityPolicy() SecurityPolicy {
	return SecurityPolicy{
		MimeMagic: MimeMagicPolicy{
			PrefixBytes:         3072,
			RejectScriptUploads: true,
			EquivalentMIMETypes: [][]string{
				{"application/xml", "text/xml"},
				{"image/jpeg", "image/pjpeg"},
				{"application/gzip", "application/x-gzip"},
				{"application/rtf", "text/rtf"},
				{"application/msword", "application/vnd.ms-excel", "application/vnd.ms-powerpoint", "application/x-ole-storage"},
			},
			AllowFileTypes:          map[string]bool{},
			DenyFileTypes:           map[string]bool{},
			AllowMIMETypes:          map[string]bool{},
			AllowedScriptTypes:      map[string]bool{},
			AllowedScriptExtensions: map[string]bool{},
			DenyMIMETypes: map[string]bool{
				"application/x-dosexec":     true,
				"application/x-executable":  true,
				"application/x-mach-binary": true,
				"application/x-sharedlib":   true,
				"application/x-msdownload":  true,
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
		ClamAV: ClamAVPolicy{
			Enabled:          false,
			Address:          "clamav:3310",
			ScanTimeoutMS:    30000,
			StreamChunkBytes: 128 << 10,
		},
		ResourceLimits: ResourceLimitPolicy{
			Enabled:                  true,
			MaxFileSizeBytes:         1 << 30,
			MaxDecompressedSizeBytes: 512 << 20,
			MaxPDFPageCount:          500,
			MaxImageWidth:            32768,
			MaxImageHeight:           32768,
			MaxImagePixelCount:       268435456,
			MaxObjectCount:           100000,
			MaxXMLDepth:              64,
			MaxZIPEntries:            10000,
			MaxEmbeddedObjectCount:   0,
			MaxParserTimeMS:          5000,
			MaxSanitizedMemoryBytes:  64 << 20,
		},
		StructuralValidation: StructuralValidationPolicy{
			Enabled: true,
			Strict:  true,
		},
		FileSanitization: FileSanitizationPolicy{
			Enabled:     true,
			DefaultMode: "secure_default",
			PerFileType: map[string]FileTypePolicy{},
			ImageVideoMetadata: ImageVideoMetadataPolicy{
				DefaultMode: "sanitize_metadata",
				Preserve:    []string{"Orientation", "ICC Profile"},
				NoReencode:  true,
			},
			OfficePDF: OfficePDFSanitizationPolicy{
				DefaultMode:      "reject_active_content",
				FullScanRequired: true,
			},
			LegacyOffice: LegacyOfficeSanitizationPolicy{
				DefaultMode: "reject",
			},
			LegacyOrComplexDocuments: LegacyOfficeSanitizationPolicy{
				DefaultMode: "reject",
			},
			SVG: SVGSanitizationPolicy{
				DefaultMode:              "reject_active_or_external_content",
				PreferStreamingXMLParser: true,
				AllowDataURLs:            false,
			},
			Markup: MarkupSanitizationPolicy{
				DefaultMode:                 "reject_active_or_external_content",
				MarkdownRawHTMLInspection:   true,
				HTMLActiveContentInspection: true,
				XMLExternalEntityResolution: "disabled",
			},
		},
		UploadDeadlines: UploadDeadlinePolicy{
			Enabled:         true,
			MarkerPrefix:    ".uploading/",
			StartTimeout:    10 * time.Second,
			FinishTimeout:   time.Minute,
			CleanupEnabled:  true,
			CleanupInterval: time.Minute,
			CleanupMode:     "server_loop",
		},
		SharedKey: SharedKeyPolicy{},
		HTTPCache: HTTPCachePolicy{
			Mode:           "private",
			MaxAge:         24 * time.Hour,
			ForwardETag:    true,
			ForwardLastMod: true,
		},
		Logging: LoggingPolicy{
			Format: "text",
			Level:  "info",
		},
		Thumbnails: ThumbnailPolicy{
			Enabled:                 false,
			ExecutionMode:           "async",
			Width:                   400,
			Height:                  400,
			Fit:                     "contain",
			Upscale:                 false,
			LosslessPolicy:          "force_avif_reduction",
			PreferredFormat:         "avif",
			ObjectKeySuffix:         "/thumbnail",
			ExternalTimeout:         30 * time.Second,
			VideoCandidateKeyframes: 10,
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
	normalizeClamAVPolicy(&policy.ClamAV)
	normalizeResourceLimitPolicy(&policy.ResourceLimits)
	normalizeStructuralValidationPolicy(&policy.StructuralValidation)
	normalizeFileSanitizationPolicy(&policy.FileSanitization)
	normalizeExtendedPolicies(policy)
}

func normalizeExtendedPolicies(policy *SecurityPolicy) {
	defaults := DefaultSecurityPolicy()
	if strings.TrimSpace(policy.UploadDeadlines.MarkerPrefix) == "" {
		policy.UploadDeadlines.MarkerPrefix = defaults.UploadDeadlines.MarkerPrefix
	}
	policy.UploadDeadlines.MarkerPrefix = cleanPrefix(policy.UploadDeadlines.MarkerPrefix)
	if policy.UploadDeadlines.StartTimeout <= 0 {
		policy.UploadDeadlines.StartTimeout = defaults.UploadDeadlines.StartTimeout
	}
	if policy.UploadDeadlines.FinishTimeout <= 0 {
		policy.UploadDeadlines.FinishTimeout = defaults.UploadDeadlines.FinishTimeout
	}
	if policy.UploadDeadlines.CleanupInterval <= 0 {
		policy.UploadDeadlines.CleanupInterval = defaults.UploadDeadlines.CleanupInterval
	}
	if policy.UploadDeadlines.CleanupMode == "" {
		policy.UploadDeadlines.CleanupMode = defaults.UploadDeadlines.CleanupMode
	}
	switch policy.UploadDeadlines.CleanupMode {
	case "server_loop", "cleanup_once", "disabled":
	default:
		policy.UploadDeadlines.CleanupMode = defaults.UploadDeadlines.CleanupMode
	}
	if policy.UploadDeadlines.CleanupMode == "disabled" {
		policy.UploadDeadlines.CleanupEnabled = false
	}
	policy.HTTPCache.Mode = strings.ToLower(strings.TrimSpace(policy.HTTPCache.Mode))
	if policy.HTTPCache.Mode == "" {
		policy.HTTPCache.Mode = defaults.HTTPCache.Mode
	}
	switch policy.HTTPCache.Mode {
	case "private", "public", "no-store":
	default:
		policy.HTTPCache.Mode = defaults.HTTPCache.Mode
	}
	if policy.HTTPCache.MaxAge <= 0 {
		policy.HTTPCache.MaxAge = defaults.HTTPCache.MaxAge
	}
	if !policy.HTTPCache.ForwardETag && !policy.HTTPCache.ForwardLastMod {
		policy.HTTPCache.ForwardETag = defaults.HTTPCache.ForwardETag
		policy.HTTPCache.ForwardLastMod = defaults.HTTPCache.ForwardLastMod
	}
	policy.Logging.Format = strings.ToLower(strings.TrimSpace(policy.Logging.Format))
	if policy.Logging.Format == "" {
		policy.Logging.Format = defaults.Logging.Format
	}
	if policy.Logging.Format != "text" && policy.Logging.Format != "json" {
		policy.Logging.Format = defaults.Logging.Format
	}
	policy.Logging.Level = strings.ToLower(strings.TrimSpace(policy.Logging.Level))
	if policy.Logging.Level == "" {
		policy.Logging.Level = defaults.Logging.Level
	}
	normalizeThumbnailPolicy(&policy.Thumbnails)
}

func normalizeThumbnailPolicy(policy *ThumbnailPolicy) {
	defaults := DefaultSecurityPolicy().Thumbnails
	policy.ExecutionMode = strings.ToLower(strings.TrimSpace(policy.ExecutionMode))
	if policy.ExecutionMode == "" {
		policy.ExecutionMode = defaults.ExecutionMode
	}
	switch policy.ExecutionMode {
	case "async", "sequential":
	default:
		policy.ExecutionMode = defaults.ExecutionMode
	}
	if policy.Width <= 0 {
		policy.Width = defaults.Width
	}
	if policy.Height <= 0 {
		policy.Height = defaults.Height
	}
	if policy.Width > 4096 {
		policy.Width = 4096
	}
	if policy.Height > 4096 {
		policy.Height = 4096
	}
	policy.Fit = strings.ToLower(strings.TrimSpace(policy.Fit))
	if policy.Fit == "" {
		policy.Fit = defaults.Fit
	}
	switch policy.Fit {
	case "contain", "cover":
	default:
		policy.Fit = defaults.Fit
	}
	policy.LosslessPolicy = strings.ToLower(strings.TrimSpace(policy.LosslessPolicy))
	if policy.LosslessPolicy == "" {
		policy.LosslessPolicy = defaults.LosslessPolicy
	}
	switch policy.LosslessPolicy {
	case "force_avif_reduction", "webp_lossless":
	default:
		policy.LosslessPolicy = defaults.LosslessPolicy
	}
	policy.PreferredFormat = strings.ToLower(strings.TrimSpace(policy.PreferredFormat))
	if policy.PreferredFormat == "" {
		policy.PreferredFormat = defaults.PreferredFormat
	}
	switch policy.PreferredFormat {
	case "avif", "webp", "jpeg", "jpg":
	default:
		policy.PreferredFormat = defaults.PreferredFormat
	}
	policy.ObjectKeySuffix = strings.TrimSpace(policy.ObjectKeySuffix)
	if policy.ObjectKeySuffix == "" {
		policy.ObjectKeySuffix = defaults.ObjectKeySuffix
	}
	if !strings.HasPrefix(policy.ObjectKeySuffix, "/") {
		policy.ObjectKeySuffix = "/" + policy.ObjectKeySuffix
	}
	if policy.ExternalTimeout <= 0 {
		policy.ExternalTimeout = defaults.ExternalTimeout
	}
	if policy.VideoCandidateKeyframes <= 0 {
		policy.VideoCandidateKeyframes = defaults.VideoCandidateKeyframes
	}
	if policy.VideoCandidateKeyframes < 1 {
		policy.VideoCandidateKeyframes = 1
	}
	if policy.VideoCandidateKeyframes > 60 {
		policy.VideoCandidateKeyframes = 60
	}
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

func normalizeClamAVPolicy(policy *ClamAVPolicy) {
	defaults := DefaultSecurityPolicy().ClamAV
	if strings.TrimSpace(policy.Address) == "" {
		policy.Address = defaults.Address
	}
	if policy.ScanTimeoutMS <= 0 {
		policy.ScanTimeoutMS = defaults.ScanTimeoutMS
	}
	if policy.StreamChunkBytes <= 0 {
		policy.StreamChunkBytes = defaults.StreamChunkBytes
	}
	if policy.StreamChunkBytes < 1024 {
		policy.StreamChunkBytes = 1024
	}
	if policy.StreamChunkBytes > 1<<20 {
		policy.StreamChunkBytes = 1 << 20
	}
}

func normalizeResourceLimitPolicy(policy *ResourceLimitPolicy) {
	defaults := DefaultSecurityPolicy().ResourceLimits
	if policy.MaxFileSizeBytes <= 0 {
		policy.MaxFileSizeBytes = defaults.MaxFileSizeBytes
	}
	if policy.MaxDecompressedSizeBytes <= 0 {
		policy.MaxDecompressedSizeBytes = defaults.MaxDecompressedSizeBytes
	}
	if policy.MaxPDFPageCount <= 0 {
		policy.MaxPDFPageCount = defaults.MaxPDFPageCount
	}
	if policy.MaxImageWidth <= 0 {
		policy.MaxImageWidth = defaults.MaxImageWidth
	}
	if policy.MaxImageHeight <= 0 {
		policy.MaxImageHeight = defaults.MaxImageHeight
	}
	if policy.MaxImagePixelCount <= 0 {
		policy.MaxImagePixelCount = defaults.MaxImagePixelCount
	}
	if policy.MaxObjectCount <= 0 {
		policy.MaxObjectCount = defaults.MaxObjectCount
	}
	if policy.MaxXMLDepth <= 0 {
		policy.MaxXMLDepth = defaults.MaxXMLDepth
	}
	if policy.MaxZIPEntries <= 0 {
		policy.MaxZIPEntries = defaults.MaxZIPEntries
	}
	if policy.MaxParserTimeMS <= 0 {
		policy.MaxParserTimeMS = defaults.MaxParserTimeMS
	}
	if policy.MaxSanitizedMemoryBytes <= 0 {
		policy.MaxSanitizedMemoryBytes = defaults.MaxSanitizedMemoryBytes
	}
}

func normalizeStructuralValidationPolicy(policy *StructuralValidationPolicy) {
	defaults := DefaultSecurityPolicy().StructuralValidation
	if !policy.Enabled && !policy.Strict {
		*policy = defaults
	}
}

func normalizeFileSanitizationPolicy(policy *FileSanitizationPolicy) {
	defaults := DefaultSecurityPolicy().FileSanitization
	if policy.DefaultMode == "" && policy.ImageVideoMetadata.DefaultMode == "" && policy.OfficePDF.DefaultMode == "" && policy.LegacyOffice.DefaultMode == "" && policy.LegacyOrComplexDocuments.DefaultMode == "" && policy.SVG.DefaultMode == "" && policy.Markup.DefaultMode == "" && policy.PerFileType == nil {
		*policy = defaults
		return
	}
	policy.DefaultMode = normalizeSanitizationMode(policy.DefaultMode)
	if policy.DefaultMode == "" {
		policy.DefaultMode = defaults.DefaultMode
	}
	if policy.PerFileType == nil {
		policy.PerFileType = map[string]FileTypePolicy{}
	}
	normalizedPerType := map[string]FileTypePolicy{}
	for key, value := range policy.PerFileType {
		normalizedKey := strings.ToLower(strings.TrimSpace(key))
		mode := normalizeSanitizationMode(value.Mode)
		if normalizedKey != "" && mode != "" {
			normalizedPerType[normalizedKey] = FileTypePolicy{Mode: mode}
		}
	}
	policy.PerFileType = normalizedPerType
	policy.ImageVideoMetadata.DefaultMode = normalizeSanitizationMode(policy.ImageVideoMetadata.DefaultMode)
	if policy.ImageVideoMetadata.DefaultMode == "" {
		policy.ImageVideoMetadata.DefaultMode = defaults.ImageVideoMetadata.DefaultMode
	}
	if len(policy.ImageVideoMetadata.Preserve) == 0 {
		policy.ImageVideoMetadata.Preserve = append([]string(nil), defaults.ImageVideoMetadata.Preserve...)
	}
	if !policy.ImageVideoMetadata.NoReencode {
		policy.ImageVideoMetadata.NoReencode = defaults.ImageVideoMetadata.NoReencode
	}
	policy.OfficePDF.DefaultMode = normalizeSanitizationMode(policy.OfficePDF.DefaultMode)
	if policy.OfficePDF.DefaultMode == "" {
		policy.OfficePDF.DefaultMode = defaults.OfficePDF.DefaultMode
	}
	if !policy.OfficePDF.FullScanRequired {
		policy.OfficePDF.FullScanRequired = defaults.OfficePDF.FullScanRequired
	}
	policy.LegacyOffice.DefaultMode = normalizeSanitizationMode(policy.LegacyOffice.DefaultMode)
	if policy.LegacyOffice.DefaultMode == "" {
		policy.LegacyOffice.DefaultMode = defaults.LegacyOffice.DefaultMode
	}
	policy.LegacyOrComplexDocuments.DefaultMode = normalizeSanitizationMode(policy.LegacyOrComplexDocuments.DefaultMode)
	if policy.LegacyOrComplexDocuments.DefaultMode == "" {
		policy.LegacyOrComplexDocuments.DefaultMode = policy.LegacyOffice.DefaultMode
	}
	policy.SVG.DefaultMode = normalizeSanitizationMode(policy.SVG.DefaultMode)
	if policy.SVG.DefaultMode == "" {
		policy.SVG.DefaultMode = defaults.SVG.DefaultMode
	}
	if !policy.SVG.PreferStreamingXMLParser {
		policy.SVG.PreferStreamingXMLParser = defaults.SVG.PreferStreamingXMLParser
	}
	policy.Markup.DefaultMode = normalizeSanitizationMode(policy.Markup.DefaultMode)
	if policy.Markup.DefaultMode == "" {
		policy.Markup.DefaultMode = defaults.Markup.DefaultMode
	}
	if !policy.Markup.MarkdownRawHTMLInspection {
		policy.Markup.MarkdownRawHTMLInspection = defaults.Markup.MarkdownRawHTMLInspection
	}
	if !policy.Markup.HTMLActiveContentInspection {
		policy.Markup.HTMLActiveContentInspection = defaults.Markup.HTMLActiveContentInspection
	}
	policy.Markup.XMLExternalEntityResolution = strings.ToLower(strings.TrimSpace(policy.Markup.XMLExternalEntityResolution))
	if policy.Markup.XMLExternalEntityResolution == "" {
		policy.Markup.XMLExternalEntityResolution = defaults.Markup.XMLExternalEntityResolution
	}
}

func normalizeSanitizationMode(value string) string {
	value = strings.ToLower(strings.TrimSpace(value))
	switch value {
	case "secure_default", "accept_as_is", "sanitize_metadata", "reject_on_sensitive_metadata", "reject_active_content", "reject_active_or_external_content", "reject", "sanitize_when_supported":
		return value
	default:
		return ""
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
	"images":       {"image/png", "image/jpeg", "image/gif", "image/webp", "image/avif", "image/tiff", "image/x-tiff", "image/bmp", "image/svg+xml", "image/heif", "image/heic", "image/heif-sequence", "image/heic-sequence", "image/jxl", "image/jp2", "image/jpx", "image/jpm", "image/jpf", "image/vnd.adobe.photoshop", "image/x-photoshop", "image/x-tga", "image/tga"},
	"image":        {"image/png", "image/jpeg", "image/gif", "image/webp", "image/avif", "image/tiff", "image/x-tiff", "image/bmp", "image/svg+xml", "image/heif", "image/heic", "image/heif-sequence", "image/heic-sequence", "image/jxl", "image/jp2", "image/jpx", "image/jpm", "image/jpf", "image/vnd.adobe.photoshop", "image/x-photoshop", "image/x-tga", "image/tga"},
	"documents":    {"application/pdf", "text/plain", "text/csv", "application/rtf", "application/msword", "application/vnd.openxmlformats-officedocument.wordprocessingml.document", "application/vnd.ms-excel", "application/vnd.openxmlformats-officedocument.spreadsheetml.sheet", "application/vnd.ms-powerpoint", "application/vnd.openxmlformats-officedocument.presentationml.presentation"},
	"document":     {"application/pdf", "text/plain", "text/csv", "application/rtf", "application/msword", "application/vnd.openxmlformats-officedocument.wordprocessingml.document", "application/vnd.ms-excel", "application/vnd.openxmlformats-officedocument.spreadsheetml.sheet", "application/vnd.ms-powerpoint", "application/vnd.openxmlformats-officedocument.presentationml.presentation"},
	"archives":     {"application/zip", "application/gzip", "application/x-gzip", "application/zstd", "application/x-zstd", "application/x-brotli", "application/br", "application/x-tar", "application/x-bzip2", "application/x-xz", "application/x-7z-compressed"},
	"archive":      {"application/zip", "application/gzip", "application/x-gzip", "application/zstd", "application/x-zstd", "application/x-brotli", "application/br", "application/x-tar", "application/x-bzip2", "application/x-xz", "application/x-7z-compressed"},
	"audio":        {"audio/mpeg", "audio/mp4", "audio/ogg", "audio/wav", "audio/webm", "audio/flac", "audio/aac"},
	"videos":       {"video/mp4", "video/mpeg", "video/quicktime", "video/webm", "video/x-msvideo", "video/x-matroska"},
	"video":        {"video/mp4", "video/mpeg", "video/quicktime", "video/webm", "video/x-msvideo", "video/x-matroska"},
	"text":         {"text/plain", "text/csv", "text/markdown", "text/html", "application/json", "application/xml", "text/xml"},
	"png":          {"image/png"},
	"jpeg":         {"image/jpeg", "image/pjpeg"},
	"jpg":          {"image/jpeg", "image/pjpeg"},
	"gif":          {"image/gif"},
	"webp":         {"image/webp"},
	"avif":         {"image/avif"},
	"tiff":         {"image/tiff", "image/x-tiff"},
	"tif":          {"image/tiff", "image/x-tiff"},
	"bmp":          {"image/bmp"},
	"svg":          {"image/svg+xml"},
	"heif":         {"image/heif", "image/heif-sequence"},
	"heic":         {"image/heic", "image/heic-sequence"},
	"jxl":          {"image/jxl"},
	"jpegxl":       {"image/jxl"},
	"jp2":          {"image/jp2"},
	"jpx":          {"image/jpx"},
	"jpm":          {"image/jpm"},
	"jpf":          {"image/jpf"},
	"jpeg2000":     {"image/jp2", "image/jpx"},
	"psd":          {"image/vnd.adobe.photoshop", "image/x-photoshop"},
	"photoshop":    {"image/vnd.adobe.photoshop", "image/x-photoshop"},
	"tga":          {"image/x-tga", "image/tga"},
	"pdf":          {"application/pdf"},
	"txt":          {"text/plain"},
	"plain":        {"text/plain"},
	"csv":          {"text/csv"},
	"md":           {"text/markdown"},
	"markdown":     {"text/markdown"},
	"html":         {"text/html", "application/xhtml+xml"},
	"htm":          {"text/html", "application/xhtml+xml"},
	"xhtml":        {"application/xhtml+xml"},
	"rtf":          {"application/rtf", "text/rtf"},
	"json":         {"application/json"},
	"xml":          {"application/xml", "text/xml"},
	"bin":          {"application/octet-stream"},
	"octet":        {"application/octet-stream"},
	"octet-stream": {"application/octet-stream"},
	"zip":          {"application/zip"},
	"gzip":         {"application/gzip", "application/x-gzip"},
	"gz":           {"application/gzip", "application/x-gzip"},
	"zstd":         {"application/zstd", "application/x-zstd"},
	"zst":          {"application/zstd", "application/x-zstd"},
	"brotli":       {"application/x-brotli", "application/br"},
	"br":           {"application/x-brotli", "application/br"},
	"bzip2":        {"application/x-bzip2"},
	"bz2":          {"application/x-bzip2"},
	"xz":           {"application/x-xz"},
	"7z":           {"application/x-7z-compressed"},
	"exe":          {"application/x-dosexec", "application/x-executable", "application/x-mach-binary", "application/x-sharedlib", "application/x-msdownload"},
	"dll":          {"application/x-dosexec", "application/x-executable", "application/x-mach-binary", "application/x-sharedlib", "application/x-msdownload"},
	"elf":          {"application/x-dosexec", "application/x-executable", "application/x-mach-binary", "application/x-sharedlib", "application/x-msdownload"},
	"mach-o":       {"application/x-mach-binary"},
	"macho":        {"application/x-mach-binary"},
	"executable":   {"application/x-dosexec", "application/x-executable", "application/x-mach-binary", "application/x-sharedlib", "application/x-msdownload"},
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
			"clamav": map[string]any{
				"type":                 "object",
				"additionalProperties": false,
				"properties": map[string]any{
					"enabled":            map[string]any{"type": "boolean"},
					"address":            map[string]any{"type": "string"},
					"scan_timeout_ms":    map[string]any{"type": "integer", "minimum": 1},
					"stream_chunk_bytes": map[string]any{"type": "integer", "minimum": 1024, "maximum": 1048576},
				},
			},
			"resource_limits": policyObjectSchema(map[string]any{
				"enabled":                     map[string]any{"type": "boolean"},
				"max_file_size_bytes":         map[string]any{"type": "integer", "minimum": 1},
				"max_decompressed_size_bytes": map[string]any{"type": "integer", "minimum": 1},
				"max_pdf_page_count":          map[string]any{"type": "integer", "minimum": 1},
				"max_image_width":             map[string]any{"type": "integer", "minimum": 1},
				"max_image_height":            map[string]any{"type": "integer", "minimum": 1},
				"max_image_pixel_count":       map[string]any{"type": "integer", "minimum": 1},
				"max_object_count":            map[string]any{"type": "integer", "minimum": 1},
				"max_xml_depth":               map[string]any{"type": "integer", "minimum": 1},
				"max_zip_entries":             map[string]any{"type": "integer", "minimum": 1},
				"max_embedded_object_count":   map[string]any{"type": "integer", "minimum": 0},
				"max_parser_time_ms":          map[string]any{"type": "integer", "minimum": 1},
				"max_sanitized_memory_bytes":  map[string]any{"type": "integer", "minimum": 1},
			}),
			"structural_validation": policyObjectSchema(map[string]any{
				"enabled": map[string]any{"type": "boolean"},
				"strict":  map[string]any{"type": "boolean"},
			}),
			"file_sanitization": policyObjectSchema(map[string]any{
				"enabled":      map[string]any{"type": "boolean"},
				"default_mode": sanitizationModeSchema("secure_default", "accept_as_is"),
				"per_file_type": map[string]any{
					"type": "object",
					"additionalProperties": policyObjectSchema(map[string]any{
						"mode": sanitizationModeSchema("sanitize_metadata", "reject_on_sensitive_metadata", "reject_active_content", "reject_active_or_external_content", "reject", "sanitize_when_supported", "accept_as_is"),
					}),
				},
				"image_video_metadata": policyObjectSchema(map[string]any{
					"default_mode": sanitizationModeSchema("sanitize_metadata", "reject_on_sensitive_metadata", "accept_as_is"),
					"preserve": map[string]any{
						"type":  "array",
						"items": map[string]any{"type": "string", "enum": []string{"Orientation", "ICC Profile"}},
					},
					"no_reencode": map[string]any{"type": "boolean"},
				}),
				"office_pdf": policyObjectSchema(map[string]any{
					"default_mode":       sanitizationModeSchema("reject_active_content", "sanitize_when_supported", "accept_as_is"),
					"full_scan_required": map[string]any{"type": "boolean"},
				}),
				"legacy_office": policyObjectSchema(map[string]any{
					"default_mode": sanitizationModeSchema("reject", "accept_as_is"),
				}),
				"legacy_or_complex_documents": policyObjectSchema(map[string]any{
					"default_mode": sanitizationModeSchema("reject", "accept_as_is"),
				}),
				"svg": policyObjectSchema(map[string]any{
					"default_mode":                sanitizationModeSchema("reject_active_or_external_content", "sanitize_when_supported", "accept_as_is"),
					"prefer_streaming_xml_parser": map[string]any{"type": "boolean"},
					"allow_data_urls":             map[string]any{"type": "boolean"},
				}),
				"markup": policyObjectSchema(map[string]any{
					"default_mode":                   sanitizationModeSchema("reject_active_or_external_content", "sanitize_when_supported", "accept_as_is"),
					"markdown_raw_html_inspection":   map[string]any{"type": "boolean"},
					"html_active_content_inspection": map[string]any{"type": "boolean"},
					"xml_external_entity_resolution": map[string]any{"type": "string", "enum": []string{"disabled"}},
				}),
			}),
			"upload_deadlines": policyObjectSchema(map[string]any{
				"enabled":          map[string]any{"type": "boolean"},
				"marker_prefix":    map[string]any{"type": "string"},
				"start_timeout":    durationSchema(),
				"finish_timeout":   durationSchema(),
				"cleanup_enabled":  map[string]any{"type": "boolean"},
				"cleanup_interval": durationSchema(),
				"cleanup_mode":     map[string]any{"type": "string", "enum": []string{"server_loop", "cleanup_once", "disabled"}},
			}),
			"shared_key": policyObjectSchema(map[string]any{
				"default_ttl": durationSchema(),
				"max_ttl":     durationSchema(),
			}),
			"http_cache": policyObjectSchema(map[string]any{
				"mode":                  map[string]any{"type": "string", "enum": []string{"private", "public", "no-store"}},
				"max_age":               durationSchema(),
				"s_max_age":             durationSchema(),
				"forward_etag":          map[string]any{"type": "boolean"},
				"forward_last_modified": map[string]any{"type": "boolean"},
			}),
			"logging": policyObjectSchema(map[string]any{
				"format": map[string]any{"type": "string", "enum": []string{"text", "json"}},
				"level":  map[string]any{"type": "string", "enum": []string{"debug", "info", "warn", "error"}},
			}),
			"thumbnails": policyObjectSchema(map[string]any{
				"enabled":              map[string]any{"type": "boolean"},
				"execution_mode":       map[string]any{"type": "string", "enum": []string{"async", "sequential"}},
				"width":                map[string]any{"type": "integer", "minimum": 1, "maximum": 4096},
				"height":               map[string]any{"type": "integer", "minimum": 1, "maximum": 4096},
				"fit":                  map[string]any{"type": "string", "enum": []string{"contain", "cover"}},
				"upscale":              map[string]any{"type": "boolean"},
				"lossless_policy":      map[string]any{"type": "string", "enum": []string{"force_avif_reduction", "webp_lossless"}},
				"preferred_format":     map[string]any{"type": "string", "enum": []string{"avif", "webp", "jpeg", "jpg"}},
				"object_key_suffix":    map[string]any{"type": "string"},
				"external_webhook_url": map[string]any{"type": "string"},
				"external_timeout":     durationSchema(),
				"video_candidate_keyframes": map[string]any{
					"type":    "integer",
					"minimum": 1,
					"maximum": 60,
				},
			}),
		},
	}
	body, err := json.Marshal(schema)
	if err != nil {
		panic(err)
	}
	return string(body)
}

func durationSchema() map[string]any {
	return map[string]any{"type": "string", "pattern": `^$|^[0-9]+(ns|us|µs|ms|s|m|h)$`}
}

func sanitizationModeSchema(values ...string) map[string]any {
	return map[string]any{"type": "string", "enum": values}
}

func policyObjectSchema(properties map[string]any) map[string]any {
	return map[string]any{
		"type":                 "object",
		"additionalProperties": false,
		"properties":           properties,
	}
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

func envDurationSeconds(key string, fallback time.Duration) time.Duration {
	value := env(key, "")
	if value == "" {
		return fallback
	}
	parsed, err := strconv.ParseInt(value, 10, 64)
	if err != nil {
		return fallback
	}
	if parsed <= 0 {
		return 0
	}
	return time.Duration(parsed) * time.Second
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
