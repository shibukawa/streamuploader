package server

import (
	"net/http"
	"net/http/httptest"
	"os"
	"sync"
	"testing"

	"github.com/shibukawa/popcornweb/pw"

	"streamuploader/internal/framework"
)

func init() {
	// The framework reads its configuration once per process. A test process
	// is not a development run, and the startup summary would be printed by
	// every server a test starts.
	_ = os.Setenv("APP_ENV", "test")
	_ = os.Setenv("OBSERVABILITY_BOOT_LOG", "off")
	_ = os.Setenv("OBSERVABILITY_MINIMUM_LEVEL", "error")
}

var prepareFramework sync.Once

// newTestServer serves handler under the same framework request chain the
// binaries use.
func newTestServer(t *testing.T, handler http.Handler) *httptest.Server {
	t.Helper()
	prepareFramework.Do(func() {
		if err := framework.Prepare(framework.Options{MaxRequestBody: 1 << 30, StreamingUploads: true}); err != nil {
			t.Fatalf("prepare framework: %v", err)
		}
	})
	wrapped, err := pw.Middlewares(handler)
	if err != nil {
		t.Fatalf("framework middlewares: %v", err)
	}
	return httptest.NewServer(wrapped)
}
