// Package auth defines the authentication middleware extension points for
// streamuploader deployments.
//
// This package is covered by the auth middleware license exception described in
// LICENSE-AUTH-EXCEPTION.
package auth

import (
	"net/http"
	"sync"

	"streamuploader/internal/config"
)

// Config is the streamuploader runtime configuration passed to auth middleware.
type Config = config.Config

// Middleware wraps an HTTP handler with deployment-specific authentication.
type Middleware func(next http.Handler, config *Config) http.Handler

var (
	mu                 sync.RWMutex
	frontendMiddleware Middleware = passThrough
	backendMiddleware  Middleware = passThrough
)

// SetFrontendAuthMiddleware replaces the browser-facing API authentication
// middleware. Passing nil resets it to the default pass-through behavior.
func SetFrontendAuthMiddleware(middleware Middleware) {
	mu.Lock()
	defer mu.Unlock()
	if middleware == nil {
		frontendMiddleware = passThrough
		return
	}
	frontendMiddleware = middleware
}

// SetBackendAuthMiddleware replaces the backend control API authentication
// middleware. Passing nil resets it to the default pass-through behavior.
func SetBackendAuthMiddleware(middleware Middleware) {
	mu.Lock()
	defer mu.Unlock()
	if middleware == nil {
		backendMiddleware = passThrough
		return
	}
	backendMiddleware = middleware
}

// NewFrontendAuthMiddleware applies the configured frontend auth middleware.
func NewFrontendAuthMiddleware(next http.Handler, config *Config) http.Handler {
	mu.RLock()
	middleware := frontendMiddleware
	mu.RUnlock()
	return middleware(next, config)
}

// NewBackendAuthMiddleware applies the configured backend auth middleware.
func NewBackendAuthMiddleware(next http.Handler, config *Config) http.Handler {
	mu.RLock()
	middleware := backendMiddleware
	mu.RUnlock()
	return middleware(next, config)
}

func passThrough(next http.Handler, _ *Config) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		next.ServeHTTP(w, r)
	})
}
