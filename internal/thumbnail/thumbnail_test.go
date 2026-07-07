package thumbnail

import (
	"archive/zip"
	"bytes"
	"context"
	"image"
	"image/color"
	"image/png"
	"os/exec"
	"runtime"
	"strings"
	"testing"
	"time"

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
	if _, ok := findTool("ffmpeg"); !ok {
		for _, candidate := range plan.ToolCandidates {
			if candidate.kind == "ffmpeg" {
				t.Fatalf("ffmpeg candidate without ffmpeg = %+v", candidate)
			}
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
	requireTool(t, "ffmpeg")
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

func TestConvertExtractsOOXMLEmbeddedThumbnail(t *testing.T) {
	var imageBuf bytes.Buffer
	img := image.NewRGBA(image.Rect(0, 0, 8, 4))
	for y := 0; y < 4; y++ {
		for x := 0; x < 8; x++ {
			img.Set(x, y, color.RGBA{R: 200, G: 40, B: 20, A: 255})
		}
	}
	if err := png.Encode(&imageBuf, img); err != nil {
		t.Fatal(err)
	}
	var doc bytes.Buffer
	zw := zip.NewWriter(&doc)
	w, err := zw.Create("docProps/thumbnail.png")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := w.Write(imageBuf.Bytes()); err != nil {
		t.Fatal(err)
	}
	if err := zw.Close(); err != nil {
		t.Fatal(err)
	}

	policy := config.DefaultSecurityPolicy().Thumbnails
	policy.Enabled = true
	policy.Width = 4
	policy.Height = 4
	policy.Fit = "contain"
	policy.PreferredFormat = "jpeg"
	plan := Plan{GoCandidates: goEncoderCandidates(policy)}
	body, contentType, backend, width, height, err := ConvertWithPlanForContentType(
		bytes.NewReader(doc.Bytes()),
		"application/vnd.openxmlformats-officedocument.wordprocessingml.document",
		policy,
		plan,
	)
	if err != nil {
		t.Fatal(err)
	}
	if contentType != "image/jpeg" || !strings.HasPrefix(backend, "embedded:") {
		t.Fatalf("embedded thumbnail output = %s %s", contentType, backend)
	}
	if width != 4 || height != 2 || len(body) == 0 {
		t.Fatalf("embedded thumbnail size=%dx%d bytes=%d", width, height, len(body))
	}
}

func TestRunSipsThumbnailIfAvailable(t *testing.T) {
	if runtime.GOOS != "darwin" {
		t.Skip("sips is only available on macOS")
	}
	sipsPath := requireTool(t, "sips")
	input := makePNG(t, 12, 6)
	policy := config.DefaultSecurityPolicy().Thumbnails
	policy.Enabled = true
	policy.Width = 6
	policy.Height = 6
	policy.Fit = "contain"
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	body, err := runSipsThumbnail(ctx, sipsPath, input, policy, "jpeg", "image/png")
	if err != nil {
		t.Fatal(err)
	}
	width, height := decodedSize(body)
	if width == 0 || height == 0 || len(body) == 0 {
		t.Fatalf("sips thumbnail size=%dx%d bytes=%d", width, height, len(body))
	}
}

func TestConvertOfficeToPDFWithLibreOfficeIfAvailable(t *testing.T) {
	requireAnyTool(t, "soffice", "libreoffice")
	docx := makeMinimalDOCX(t)
	policy := config.DefaultSecurityPolicy().Thumbnails
	policy.ExternalTimeout = 20 * time.Second

	body, err := convertOfficeToPDF(docx, "application/vnd.openxmlformats-officedocument.wordprocessingml.document", policy)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.HasPrefix(body, []byte("%PDF-")) {
		t.Fatalf("libreoffice output does not look like PDF: %.16q", body)
	}
}

func requireTool(t *testing.T, name string) string {
	t.Helper()
	path, ok := findTool(name)
	if !ok {
		t.Skipf("%s is not available", name)
	}
	return path
}

func requireAnyTool(t *testing.T, names ...string) string {
	t.Helper()
	for _, name := range names {
		if path, ok := findTool(name); ok {
			return path
		}
	}
	t.Skipf("none of these tools are available: %s", strings.Join(names, ", "))
	return ""
}

func findTool(name string) (string, bool) {
	path, err := exec.LookPath(name)
	return path, err == nil
}

func makePNG(t *testing.T, width, height int) []byte {
	t.Helper()
	var buf bytes.Buffer
	img := image.NewRGBA(image.Rect(0, 0, width, height))
	for y := 0; y < height; y++ {
		for x := 0; x < width; x++ {
			img.Set(x, y, color.RGBA{R: uint8(20 * x), G: uint8(30 * y), B: 120, A: 255})
		}
	}
	if err := png.Encode(&buf, img); err != nil {
		t.Fatal(err)
	}
	return buf.Bytes()
}

func makeMinimalDOCX(t *testing.T) []byte {
	t.Helper()
	var buf bytes.Buffer
	zw := zip.NewWriter(&buf)
	addZipFile(t, zw, "[Content_Types].xml", `<?xml version="1.0" encoding="UTF-8"?>
<Types xmlns="http://schemas.openxmlformats.org/package/2006/content-types">
  <Default Extension="rels" ContentType="application/vnd.openxmlformats-package.relationships+xml"/>
  <Default Extension="xml" ContentType="application/xml"/>
  <Override PartName="/word/document.xml" ContentType="application/vnd.openxmlformats-officedocument.wordprocessingml.document.main+xml"/>
</Types>`)
	addZipFile(t, zw, "_rels/.rels", `<?xml version="1.0" encoding="UTF-8"?>
<Relationships xmlns="http://schemas.openxmlformats.org/package/2006/relationships">
  <Relationship Id="rId1" Type="http://schemas.openxmlformats.org/officeDocument/2006/relationships/officeDocument" Target="word/document.xml"/>
</Relationships>`)
	addZipFile(t, zw, "word/document.xml", `<?xml version="1.0" encoding="UTF-8"?>
<w:document xmlns:w="http://schemas.openxmlformats.org/wordprocessingml/2006/main">
  <w:body><w:p><w:r><w:t>thumbnail test</w:t></w:r></w:p></w:body>
</w:document>`)
	if err := zw.Close(); err != nil {
		t.Fatal(err)
	}
	return buf.Bytes()
}

func addZipFile(t *testing.T, zw *zip.Writer, name, body string) {
	t.Helper()
	w, err := zw.Create(name)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := w.Write([]byte(body)); err != nil {
		t.Fatal(err)
	}
}
