package config

import (
	"os"
	"strconv"
	"strings"
	"time"
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
	PresignTTL              time.Duration
	AllowFrontendFileAccess bool
	EnableSharedKey         bool
	SharedKeyBits           int
	SharedKeyPrefix         string
	MaxArchiveFiles         int
	MaxArchiveBytes         int64
}

func Load() Config {
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
		PresignTTL:              envDuration("PRESIGN_TTL", 15*time.Minute),
		AllowFrontendFileAccess: envBool("ALLOW_FRONTEND_FILE_ACCESS", false),
		EnableSharedKey:         envBool("ENABLE_SHARED_KEY", false),
		SharedKeyBits:           envInt("SHARED_KEY_BITS", 128),
		SharedKeyPrefix:         cleanPrefix(env("SHARED_KEY_PREFIX", ".streamuploader/shared/")),
		MaxArchiveFiles:         envInt("MAX_ARCHIVE_FILES", 100),
		MaxArchiveBytes:         envInt64("MAX_ARCHIVE_BYTES", 1<<30),
	}
}

func env(key, fallback string) string {
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
	value := os.Getenv(key)
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
	value := os.Getenv(key)
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
	value := os.Getenv(key)
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
	value := os.Getenv(key)
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
