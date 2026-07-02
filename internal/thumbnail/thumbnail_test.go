package thumbnail

import (
	"bytes"
	"image"
	"os/exec"
	"strings"
	"testing"

	"streamuploader/internal/config"
)

func TestResizeContainPreservesAspectRatio(t *testing.T) {
	src := image.NewRGBA(image.Rect(0, 0, 800, 400))
	thumb := resize(src, 400, 400, "contain", false)
	if got := thumb.Bounds().Size(); got.X != 400 || got.Y != 200 {
		t.Fatalf("contain size = %dx%d, want 400x200", got.X, got.Y)
	}
}

func TestResizeCoverPreservesAspectRatioAndCrops(t *testing.T) {
	src := image.NewRGBA(image.Rect(0, 0, 800, 400))
	thumb := resize(src, 400, 400, "cover", false)
	if got := thumb.Bounds().Size(); got.X != 400 || got.Y != 400 {
		t.Fatalf("cover size = %dx%d, want 400x400", got.X, got.Y)
	}
}

func TestResizeDoesNotUpscaleByDefault(t *testing.T) {
	src := image.NewRGBA(image.Rect(0, 0, 200, 100))
	thumb := resize(src, 400, 400, "contain", false)
	if got := thumb.Bounds().Size(); got.X != 200 || got.Y != 100 {
		t.Fatalf("no upscale size = %dx%d, want 200x100", got.X, got.Y)
	}
}

func TestFFmpegOutputCandidatesPreferModernFormats(t *testing.T) {
	policy := config.DefaultSecurityPolicy().Thumbnails
	policy.PreferredFormat = "avif"
	policy.LosslessPolicy = "force_avif_reduction"
	candidates := ffmpegOutputCandidates(policy)
	if len(candidates) < 4 {
		t.Fatalf("candidates = %+v", candidates)
	}
	if candidates[0].contentType != "image/avif" || candidates[2].contentType != "image/webp" || candidates[len(candidates)-1].contentType != "image/jpeg" {
		t.Fatalf("candidate order = %+v", candidates)
	}
}

func TestFFmpegOutputCandidatesUseWebPLossless(t *testing.T) {
	policy := config.DefaultSecurityPolicy().Thumbnails
	policy.PreferredFormat = "avif"
	policy.LosslessPolicy = "webp_lossless"
	candidates := ffmpegOutputCandidates(policy)
	if len(candidates) != 2 {
		t.Fatalf("candidates = %+v", candidates)
	}
	if candidates[0].contentType != "image/webp" || candidates[0].backend != "ffmpeg:libwebp" {
		t.Fatalf("first candidate = %+v", candidates[0])
	}
	if candidates[0].extra[0] != "-lossless" || candidates[1].contentType != "image/jpeg" {
		t.Fatalf("candidate order = %+v", candidates)
	}
}

func TestProbePlanFiltersUnavailableFFmpegEncoders(t *testing.T) {
	policy := config.DefaultSecurityPolicy().Thumbnails
	policy.Enabled = true
	policy.PreferredFormat = "avif"
	policy.LosslessPolicy = "force_avif_reduction"
	plan := ProbePlan(policy)
	if _, err := exec.LookPath("ffmpeg"); err != nil {
		if len(plan.ToolCandidates) != 0 {
			t.Fatalf("tool candidates without ffmpeg = %+v", plan.ToolCandidates)
		}
		return
	}
	for _, candidate := range plan.ToolCandidates {
		if candidate.kind == "ffmpeg" && !plan.FFmpegEncoders[candidate.ffmpeg.codec] {
			t.Fatalf("candidate uses unprobed encoder: %+v encoders=%+v", candidate, plan.FFmpegEncoders)
		}
	}
}

func TestConvertFallsBackToFFmpegForNonGoImage(t *testing.T) {
	if _, err := exec.LookPath("ffmpeg"); err != nil {
		t.Skip("ffmpeg is not available")
	}
	ppm := []byte("P6\n2 2\n255\n\xff\x00\x00\x00\xff\x00\x00\x00\xff\xff\xff\xff")
	policy := config.DefaultSecurityPolicy().Thumbnails
	policy.Enabled = true
	policy.Width = 2
	policy.Height = 2
	policy.Fit = "contain"
	policy.PreferredFormat = "avif"
	policy.LosslessPolicy = "force_avif_reduction"
	plan := ProbePlan(policy)

	body, contentType, backend, width, height, err := ConvertWithPlan(bytes.NewReader(ppm), policy, plan)
	if err != nil {
		t.Fatal(err)
	}
	if contentType != "image/avif" && contentType != "image/webp" && contentType != "image/jpeg" {
		t.Fatalf("fallback = %s %s", contentType, backend)
	}
	if !strings.HasPrefix(backend, "ffmpeg:") {
		t.Fatalf("backend = %s", backend)
	}
	if len(body) == 0 {
		t.Fatalf("thumbnail size=%dx%d bytes=%d", width, height, len(body))
	}
	if contentType == "image/jpeg" && (width != 2 || height != 2) {
		t.Fatalf("jpeg thumbnail size=%dx%d bytes=%d", width, height, len(body))
	}
}
