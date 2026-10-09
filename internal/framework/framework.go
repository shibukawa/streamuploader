// Package framework wires streamuploader's servers into Popcorn Web.
//
// Every binary in this repository (streamuploader, drive, the demo app) runs
// its routes under the framework's request chain: request IDs, the access log,
// panic recovery, the body cap, security headers and the operational
// endpoints all come from popcornweb and are configured through its
// config.{APP_ENV}.toml file, environment variables and command-line options.
// What this package adds is the part an upload service cannot leave to the
// framework defaults, applied once after the configuration is parsed.
package framework

import (
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"strings"

	"github.com/shibukawa/popcornweb/middlewares"
	"github.com/shibukawa/popcornweb/pw"
	"github.com/shibukawa/popcornweb/pwconfig"
	"github.com/shibukawa/tinybind-go/configbind"
)

// Options are the requirements an application states for the listener.
type Options struct {
	// MaxRequestBody is the largest body the application itself admits on
	// any route. The framework caps request bodies at server.max_request_body
	// (10 MiB by default), which would refuse an upload the upload policy
	// accepts; when the configured cap is below this value it is lifted
	// entirely, because the application bounds its own bodies. A cap
	// configured above it is kept.
	MaxRequestBody int64
	// StreamingUploads turns server.read_timeout off. The framework default
	// of 30s is a bound on reading one request, and an upload that streams for
	// minutes is the ordinary case here.
	StreamingUploads bool
	// LegacyLogFormat and LegacyLogLevel are the SU_LOG_FORMAT / SU_LOG_LEVEL
	// values of the deployment, used only when the framework's own
	// observability settings were not given: the framework writes the access
	// log, so both should agree on one encoding.
	LegacyLogFormat string
	LegacyLogLevel  string
}

// Prepare parses the framework configuration once and applies opts on top.
// It must run before pw.Run or pw.Middlewares builds the request chain.
func Prepare(opts Options) error {
	applyLegacyLogSettings(opts)
	if err := pw.ParseConfig(); err != nil {
		return err
	}
	server := pw.ConfigContext[pw.ServerConfig](nil)
	changed := false
	if opts.MaxRequestBody > 0 && server.MaxRequestBody > 0 && server.MaxRequestBody < opts.MaxRequestBody {
		slog.Info("framework_body_cap_lifted",
			"configured", server.MaxRequestBody,
			"application_limit", opts.MaxRequestBody,
			"effect", "server.max_request_body is below the upload limit, so the framework cap is disabled and the upload policy bounds request bodies")
		server.MaxRequestBody = 0
		changed = true
	}
	if opts.StreamingUploads && server.ReadTimeout > 0 {
		server.ReadTimeout = 0
		changed = true
	}
	if server.Public.Enabled && middlewares.RegisteredPublicFS() == nil {
		// No embedded public tree is linked into this binary (a test binary,
		// typically); the framework refuses to start with the mount enabled
		// and nothing behind it.
		server.Public.Enabled = false
		changed = true
	}
	if changed {
		pwconfig.Seed(server)
	}
	configureDefaultLogger(pw.ConfigContext[pw.ObservabilityConfig](nil))
	return nil
}

// Server returns the parsed listener settings, with the adjustments Prepare
// made. The timeouts are applied to a listener the application owns, so an
// http.Server built here behaves like the one pw.Run would start.
func Server() pw.ServerConfig {
	return pw.ConfigContext[pw.ServerConfig](nil)
}

// NewHTTPServer builds a listener on addr carrying the framework's timeouts.
func NewHTTPServer(addr string, handler http.Handler) *http.Server {
	config := Server()
	return &http.Server{
		Addr:              addr,
		Handler:           handler,
		ReadHeaderTimeout: config.ReadHeaderTimeout,
		ReadTimeout:       config.ReadTimeout,
		WriteTimeout:      config.WriteTimeout,
		IdleTimeout:       config.IdleTimeout,
	}
}

// applyLegacyLogSettings maps the pre-framework SU_LOG_FORMAT / SU_LOG_LEVEL
// variables onto the framework's observability keys when those were not set,
// so a deployment that only knows the old names keeps its log encoding.
func applyLegacyLogSettings(opts Options) {
	if _, set := os.LookupEnv("OBSERVABILITY_STDOUT_FORMAT"); !set {
		switch strings.ToLower(strings.TrimSpace(opts.LegacyLogFormat)) {
		case "json":
			_ = os.Setenv("OBSERVABILITY_STDOUT_FORMAT", pw.StdoutFormatJSON)
		case "text", "plaintext":
			_ = os.Setenv("OBSERVABILITY_STDOUT_FORMAT", pw.StdoutFormatPlaintext)
		}
	}
	if _, set := os.LookupEnv("OBSERVABILITY_MINIMUM_LEVEL"); !set {
		switch level := strings.ToLower(strings.TrimSpace(opts.LegacyLogLevel)); level {
		case "debug", "info", "warn", "error":
			_ = os.Setenv("OBSERVABILITY_MINIMUM_LEVEL", level)
		}
	}
}

// configureDefaultLogger points log/slog at the same encoding and severity
// floor the framework resolved, so the application's own records and the
// framework's access log read as one stream.
func configureDefaultLogger(config pw.ObservabilityConfig) {
	level := slog.LevelInfo
	switch strings.ToLower(config.MinimumLevel) {
	case "trace", "debug":
		level = slog.LevelDebug
	case "warn":
		level = slog.LevelWarn
	case "error", "off":
		level = slog.LevelError
	}
	options := &slog.HandlerOptions{Level: level}
	if strings.EqualFold(config.StdoutFormat, pw.StdoutFormatPlaintext) {
		slog.SetDefault(slog.New(slog.NewTextHandler(os.Stdout, options)))
		return
	}
	slog.SetDefault(slog.New(slog.NewJSONHandler(os.Stdout, options)))
}

// RunAction answers a framework action named on the command line
// (--generate-config, healthcheck) and reports whether it took the
// invocation. A binary that serves with pw.Run calls it right after Prepare,
// before it opens stores or starts workers, so the action runs without them.
func RunAction() (bool, error) {
	return pwconfig.RunFrameworkAction()
}

// Exit ends the process for a startup error. A usage request (--help, or a
// command line the framework could not parse) prints the usage text rather
// than a log record: help exits 0, a parse failure exits 2, and everything
// else is logged and exits 1.
func Exit(err error) {
	var usage *configbind.UsageError
	if errors.As(err, &usage) {
		if usage.Message == "" {
			fmt.Fprintln(os.Stdout, usage.Usage)
			os.Exit(0)
		}
		fmt.Fprintln(os.Stderr, usage.Error())
		os.Exit(2)
	}
	slog.Error("startup_failed", "error", err)
	os.Exit(1)
}
