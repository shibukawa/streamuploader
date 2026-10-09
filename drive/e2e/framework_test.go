package e2e

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
	_ = os.Setenv("APP_ENV", "test")
	_ = os.Setenv("OBSERVABILITY_BOOT_LOG", "off")
	_ = os.Setenv("OBSERVABILITY_MINIMUM_LEVEL", "error")
}

var prepareFramework sync.Once

// newTestServer serves the Drive under the same framework request chain the
// drive binary uses.
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
