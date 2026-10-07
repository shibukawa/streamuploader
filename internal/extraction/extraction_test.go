package extraction

import (
	"bytes"
	"context"
	"testing"

	"streamuploader/internal/config"
)

func resetPlan(t *testing.T) {
	t.Helper()
	t.Cleanup(func() {
		Configure(config.DefaultSecurityPolicy().TextExtraction)
	})
}

func TestGeneratePlainTextUsesTextKey(t *testing.T) {
	resetPlan(t)
	Configure(config.TextExtractionPolicy{Enabled: true})
	result, err := Generate(context.Background(), "uploads/a.txt", "text/plain", bytes.NewBufferString("hello\n"), config.TextExtractionPolicy{
		MaxInputBytes:  1024,
		MaxOutputBytes: 1024,
	})
	if err != nil {
		t.Fatal(err)
	}
	if result.Status != "generated" || result.Content.Texts["text"] != "hello" {
		t.Fatalf("result = %+v", result)
	}
}
