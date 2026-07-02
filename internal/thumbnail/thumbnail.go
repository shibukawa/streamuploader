package thumbnail

import (
	"bytes"
	"context"
	"fmt"
	"image"
	_ "image/gif"
	"image/jpeg"
	_ "image/jpeg"
	_ "image/png"
	"io"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"sync"
	"time"

	"golang.org/x/image/draw"

	"streamuploader/internal/config"
	"streamuploader/internal/storage"
)

type Result struct {
	ObjectKey   string
	ContentType string
	Width       int
	Height      int
	SizeBytes   int64
	Backend     string
}

type Conversion struct {
	Result
	SourceObjectKey string
	Body            []byte
}

type Plan struct {
	Enabled          bool
	ExternalWebhook  bool
	ExternalURL      string
	CGOEnabled       bool
	GoCandidates     []encoderCandidate
	ToolCandidates   []toolCandidate
	FFmpegPath       string
	FFmpegEncoders   map[string]bool
	SipsPath         string
	SipsFormats      map[string]bool
	Summary          string
	UnavailableNotes []string
}

type encoderCandidate struct {
	contentType string
	backend     string
	lossless    bool
	encode      func(image.Image, bool) ([]byte, string, string, error)
}

type toolCandidate struct {
	kind        string
	contentType string
	backend     string
	ffmpeg      ffmpegOutput
	sipsFormat  string
}

var (
	activePlanMu sync.RWMutex
	activePlan   Plan
)

func Configure(policy config.ThumbnailPolicy) Plan {
	plan := ProbePlan(policy)
	activePlanMu.Lock()
	activePlan = plan
	activePlanMu.Unlock()
	return plan
}

func ProbePlan(policy config.ThumbnailPolicy) Plan {
	plan := Plan{
		Enabled:         policy.Enabled,
		ExternalWebhook: policy.ExternalWebhookURL != "",
		ExternalURL:     policy.ExternalWebhookURL,
		CGOEnabled:      cgoEnabled(),
		FFmpegEncoders:  map[string]bool{},
		SipsFormats:     map[string]bool{},
	}
	if !policy.Enabled {
		plan.Summary = "disabled"
		return plan
	}
	if plan.ExternalWebhook {
		plan.ToolCandidates = append(plan.ToolCandidates, toolCandidate{kind: "external-webhook", contentType: "application/octet-stream", backend: "external-webhook"})
		plan.Summary = "external-webhook:" + policy.ExternalWebhookURL
		return plan
	}
	plan.GoCandidates = goEncoderCandidates(policy)
	plan.FFmpegPath, plan.FFmpegEncoders = probeFFmpegEncoders()
	if plan.FFmpegPath == "" {
		plan.UnavailableNotes = append(plan.UnavailableNotes, "ffmpeg unavailable")
	}
	plan.SipsPath, plan.SipsFormats = probeSipsFormats()
	if runtime.GOOS == "darwin" && plan.SipsPath == "" {
		plan.UnavailableNotes = append(plan.UnavailableNotes, "sips unavailable")
	}
	for _, candidate := range sipsOutputCandidates(policy, plan.SipsFormats) {
		plan.ToolCandidates = append(plan.ToolCandidates, candidate)
	}
	for _, candidate := range ffmpegOutputCandidates(policy) {
		if plan.FFmpegPath != "" && plan.FFmpegEncoders[candidate.codec] {
			plan.ToolCandidates = append(plan.ToolCandidates, toolCandidate{
				kind:        "ffmpeg",
				contentType: candidate.contentType,
				backend:     candidate.backend,
				ffmpeg:      candidate,
			})
		}
	}
	plan.Summary = summarizePlan(plan)
	return plan
}

func currentPlan(policy config.ThumbnailPolicy) Plan {
	activePlanMu.RLock()
	plan := activePlan
	activePlanMu.RUnlock()
	if plan.Enabled || plan.Summary != "" {
		return plan
	}
	return Configure(policy)
}

func Generate(ctx context.Context, store storage.Store, bucket, sourceKey string, policy config.ThumbnailPolicy) (Result, error) {
	obj, err := store.GetObject(ctx, storage.GetInput{Bucket: bucket, Key: sourceKey})
	if err != nil {
		return Result{ObjectKey: sourceKey + policy.ObjectKeySuffix}, err
	}
	defer obj.Body.Close()
	converted, err := ConvertFromReader(ctx, obj.Body, sourceKey, policy)
	if err != nil {
		return converted.Result, err
	}
	if err := StoreConversion(ctx, store, bucket, converted); err != nil {
		return converted.Result, err
	}
	return converted.Result, nil
}

func ConvertFromReader(ctx context.Context, r io.Reader, sourceKey string, policy config.ThumbnailPolicy) (Conversion, error) {
	out := Conversion{Result: Result{ObjectKey: sourceKey + policy.ObjectKeySuffix}}
	if !policy.Enabled {
		return out, fmt.Errorf("thumbnail generation is disabled")
	}
	if policy.ExternalWebhookURL != "" {
		return ConvertWithWebhook(ctx, r, sourceKey, policy)
	}
	body, contentType, backend, width, height, err := ConvertWithPlan(r, policy, currentPlan(policy))
	if err != nil {
		return out, err
	}
	out.SourceObjectKey = sourceKey
	out.Body = body
	out.ContentType = contentType
	out.Backend = backend
	out.Width = width
	out.Height = height
	out.SizeBytes = int64(len(body))
	return out, nil
}

func StoreConversion(ctx context.Context, store storage.Store, bucket string, converted Conversion) error {
	if _, err := store.PutObject(ctx, storage.PutInput{
		Bucket:      bucket,
		Key:         converted.ObjectKey,
		Body:        bytes.NewReader(converted.Body),
		ContentType: converted.ContentType,
		Metadata: map[string]string{
			"source-object-key": converted.SourceObjectKey,
			"thumbnail-backend": converted.Backend,
		},
	}); err != nil {
		return err
	}
	return nil
}

func ConvertWithWebhook(ctx context.Context, r io.Reader, sourceKey string, policy config.ThumbnailPolicy) (Conversion, error) {
	timeout := policy.ExternalTimeout
	if timeout <= 0 {
		timeout = 30 * time.Second
	}
	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, policy.ExternalWebhookURL, r)
	if err != nil {
		return Conversion{Result: Result{ObjectKey: sourceKey + policy.ObjectKeySuffix}}, err
	}
	req.Header.Set("Content-Type", "application/octet-stream")
	req.Header.Set("Accept", "image/avif, image/webp, image/jpeg")
	req.Header.Set("X-Source-Object-Key", sourceKey)
	req.Header.Set("X-Thumbnail-Width", strconv.Itoa(policy.Width))
	req.Header.Set("X-Thumbnail-Height", strconv.Itoa(policy.Height))
	req.Header.Set("X-Thumbnail-Fit", policy.Fit)
	req.Header.Set("X-Thumbnail-Upscale", strconv.FormatBool(policy.Upscale))
	req.Header.Set("X-Thumbnail-Preferred-Format", policy.PreferredFormat)
	req.Header.Set("X-Thumbnail-Lossless-Policy", policy.LosslessPolicy)

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return Conversion{Result: Result{ObjectKey: sourceKey + policy.ObjectKeySuffix}}, err
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		body, _ := io.ReadAll(io.LimitReader(resp.Body, 8<<10))
		return Conversion{Result: Result{ObjectKey: sourceKey + policy.ObjectKeySuffix}}, fmt.Errorf("thumbnail webhook status %d: %s", resp.StatusCode, strings.TrimSpace(string(body)))
	}
	body, err := io.ReadAll(io.LimitReader(resp.Body, 64<<20))
	if err != nil {
		return Conversion{Result: Result{ObjectKey: sourceKey + policy.ObjectKeySuffix}}, err
	}
	contentType := strings.TrimSpace(strings.Split(resp.Header.Get("Content-Type"), ";")[0])
	if contentType == "" {
		contentType = "application/octet-stream"
	}
	width, _ := strconv.Atoi(resp.Header.Get("X-Thumbnail-Width"))
	height, _ := strconv.Atoi(resp.Header.Get("X-Thumbnail-Height"))
	backend := resp.Header.Get("X-Thumbnail-Backend")
	if backend == "" {
		backend = "external-webhook"
	}
	return Conversion{
		Result: Result{
			ObjectKey:   sourceKey + policy.ObjectKeySuffix,
			ContentType: contentType,
			Width:       width,
			Height:      height,
			SizeBytes:   int64(len(body)),
			Backend:     backend,
		},
		SourceObjectKey: sourceKey,
		Body:            body,
	}, nil
}

func Convert(r io.Reader, policy config.ThumbnailPolicy) ([]byte, string, string, int, int, error) {
	return ConvertWithPlan(r, policy, currentPlan(policy))
}

func ConvertWithPlan(r io.Reader, policy config.ThumbnailPolicy, plan Plan) ([]byte, string, string, int, int, error) {
	input, err := io.ReadAll(io.LimitReader(r, 256<<20))
	if err != nil {
		return nil, "", "", 0, 0, err
	}
	img, _, err := image.Decode(bytes.NewReader(input))
	if err != nil {
		if body, contentType, backend, width, height, toolErr := convertWithTools(input, policy, plan); toolErr == nil {
			return body, contentType, backend, width, height, nil
		}
		return nil, "", "", 0, 0, fmt.Errorf("decode image: %w", err)
	}
	thumb := resize(img, policy.Width, policy.Height, policy.Fit, policy.Upscale)
	var lastErr error
	for _, candidate := range plan.GoCandidates {
		body, contentType, backend, err := candidate.encode(thumb, candidate.lossless)
		if err == nil {
			return body, contentType, backend, thumb.Bounds().Dx(), thumb.Bounds().Dy(), nil
		}
		lastErr = err
	}
	if lastErr != nil {
		return nil, "", "", 0, 0, lastErr
	}
	return nil, "", "", 0, 0, fmt.Errorf("thumbnail encoder is unavailable")
}

func convertWithTools(input []byte, policy config.ThumbnailPolicy, plan Plan) ([]byte, string, string, int, int, error) {
	timeout := policy.ExternalTimeout
	if timeout <= 0 {
		timeout = 30 * time.Second
	}
	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()
	widthExpr := fmt.Sprintf("min(%d\\,iw)", max(1, policy.Width))
	heightExpr := fmt.Sprintf("min(%d\\,ih)", max(1, policy.Height))
	if policy.Upscale {
		widthExpr = strconv.Itoa(max(1, policy.Width))
		heightExpr = strconv.Itoa(max(1, policy.Height))
	}
	filter := fmt.Sprintf("scale=w='%s':h='%s':force_original_aspect_ratio=decrease", widthExpr, heightExpr)
	var lastErr error
	for _, candidate := range plan.ToolCandidates {
		switch candidate.kind {
		case "ffmpeg":
			body, err := runFFmpegThumbnail(ctx, plan.FFmpegPath, input, filter, candidate.ffmpeg)
			if err != nil {
				lastErr = err
				continue
			}
			width, height := decodedSize(body)
			return body, candidate.contentType, candidate.backend, width, height, nil
		case "sips":
			body, err := runSipsThumbnail(ctx, plan.SipsPath, input, policy, candidate.sipsFormat)
			if err != nil {
				lastErr = err
				continue
			}
			width, height := decodedSize(body)
			return body, candidate.contentType, candidate.backend, width, height, nil
		}
	}
	if lastErr != nil {
		return nil, "", "", 0, 0, lastErr
	}
	return nil, "", "", 0, 0, fmt.Errorf("ffmpeg thumbnail output is unavailable")
}

type ffmpegOutput struct {
	format      string
	codec       string
	extra       []string
	contentType string
	backend     string
}

func ffmpegOutputCandidates(policy config.ThumbnailPolicy) []ffmpegOutput {
	avif := []ffmpegOutput{
		{format: "avif", codec: "libaom-av1", extra: []string{"-still-picture", "1", "-cpu-used", "8", "-crf", "35"}, contentType: "image/avif", backend: "ffmpeg:libaom-av1"},
		{format: "avif", codec: "libsvtav1", extra: []string{"-preset", "12", "-crf", "35"}, contentType: "image/avif", backend: "ffmpeg:libsvtav1"},
	}
	webp := []ffmpegOutput{{format: "webp", codec: "libwebp", extra: []string{"-quality", "78"}, contentType: "image/webp", backend: "ffmpeg:libwebp"}}
	if policy.LosslessPolicy == "webp_lossless" {
		webp[0].extra = []string{"-lossless", "1"}
		return append(webp, ffmpegOutput{format: "image2pipe", codec: "mjpeg", contentType: "image/jpeg", backend: "ffmpeg:mjpeg"})
	}
	jpeg := ffmpegOutput{format: "image2pipe", codec: "mjpeg", contentType: "image/jpeg", backend: "ffmpeg:mjpeg"}
	switch strings.ToLower(policy.PreferredFormat) {
	case "webp":
		return append(append(webp, avif...), jpeg)
	case "jpg", "jpeg":
		return []ffmpegOutput{jpeg}
	default:
		return append(append(avif, webp...), jpeg)
	}
}

func sipsOutputCandidates(policy config.ThumbnailPolicy, formats map[string]bool) []toolCandidate {
	if runtime.GOOS != "darwin" || len(formats) == 0 {
		return nil
	}
	webp := toolCandidate{kind: "sips", contentType: "image/webp", backend: "sips:webp", sipsFormat: "webp"}
	avif := toolCandidate{kind: "sips", contentType: "image/avif", backend: "sips:avif", sipsFormat: "avif"}
	jpeg := toolCandidate{kind: "sips", contentType: "image/jpeg", backend: "sips:jpeg", sipsFormat: "jpeg"}
	var candidates []toolCandidate
	add := func(candidate toolCandidate) {
		if formats[candidate.sipsFormat] {
			candidates = append(candidates, candidate)
		}
	}
	if policy.LosslessPolicy == "webp_lossless" {
		add(webp)
		add(jpeg)
		return candidates
	}
	switch strings.ToLower(policy.PreferredFormat) {
	case "webp":
		add(webp)
		add(avif)
		add(jpeg)
	case "jpg", "jpeg":
		add(jpeg)
	default:
		add(avif)
		add(webp)
		add(jpeg)
	}
	return candidates
}

func probeFFmpegEncoders() (string, map[string]bool) {
	path, err := exec.LookPath("ffmpeg")
	if err != nil {
		return "", map[string]bool{}
	}
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	out, err := exec.CommandContext(ctx, path, "-hide_banner", "-encoders").CombinedOutput()
	if err != nil {
		return path, map[string]bool{}
	}
	encoders := map[string]bool{}
	for _, line := range strings.Split(string(out), "\n") {
		fields := strings.Fields(line)
		if len(fields) >= 2 {
			encoders[fields[1]] = true
		}
	}
	return path, encoders
}

func probeSipsFormats() (string, map[string]bool) {
	if runtime.GOOS != "darwin" {
		return "", map[string]bool{}
	}
	path, err := exec.LookPath("sips")
	if err != nil {
		return "", map[string]bool{}
	}
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	out, err := exec.CommandContext(ctx, path, "-h").CombinedOutput()
	if err != nil {
		return path, map[string]bool{}
	}
	text := strings.ToLower(string(out))
	formats := map[string]bool{}
	for _, format := range []string{"jpeg", "webp", "avif"} {
		if strings.Contains(text, format) {
			formats[format] = true
		}
	}
	return path, formats
}

func summarizePlan(plan Plan) string {
	if !plan.Enabled {
		return "disabled"
	}
	var parts []string
	if plan.CGOEnabled {
		parts = append(parts, "cgo=enabled")
	} else {
		parts = append(parts, "cgo=disabled")
	}
	if len(plan.GoCandidates) > 0 {
		var backends []string
		for _, candidate := range plan.GoCandidates {
			backends = append(backends, candidate.backend)
		}
		parts = append(parts, "go="+strings.Join(backends, ">"))
	}
	if len(plan.ToolCandidates) > 0 {
		var backends []string
		for _, candidate := range plan.ToolCandidates {
			backends = append(backends, candidate.backend)
		}
		parts = append(parts, "tools="+strings.Join(backends, ">"))
	}
	if len(plan.UnavailableNotes) > 0 {
		parts = append(parts, "unavailable="+strings.Join(plan.UnavailableNotes, ","))
	}
	return strings.Join(parts, " ")
}

func runFFmpegThumbnail(ctx context.Context, ffmpegPath string, input []byte, filter string, out ffmpegOutput) ([]byte, error) {
	args := []string{
		"-v", "error",
		"-i", "pipe:0",
		"-vf", filter,
		"-frames:v", "1",
		"-f", out.format,
		"-vcodec", out.codec,
	}
	args = append(args, out.extra...)
	args = append(args, "pipe:1")
	cmd := exec.CommandContext(ctx, ffmpegPath, args...)
	cmd.Stdin = bytes.NewReader(input)
	var stdout bytes.Buffer
	var stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		if stderr.Len() > 0 {
			return nil, fmt.Errorf("%s: %w: %s", out.backend, err, strings.TrimSpace(stderr.String()))
		}
		return nil, fmt.Errorf("%s: %w", out.backend, err)
	}
	if stdout.Len() == 0 {
		return nil, fmt.Errorf("%s: empty output", out.backend)
	}
	return stdout.Bytes(), nil
}

func runSipsThumbnail(ctx context.Context, sipsPath string, input []byte, policy config.ThumbnailPolicy, format string) ([]byte, error) {
	dir, err := os.MkdirTemp("", "streamuploader-thumbnail-*")
	if err != nil {
		return nil, err
	}
	defer os.RemoveAll(dir)
	src := filepath.Join(dir, "source")
	dst := filepath.Join(dir, "thumb")
	if err := os.WriteFile(src, input, 0600); err != nil {
		return nil, err
	}
	args := []string{
		"-Z", strconv.Itoa(max(1, max(policy.Width, policy.Height))),
		"-s", "format", format,
		src,
		"--out", dst,
	}
	var stderr bytes.Buffer
	cmd := exec.CommandContext(ctx, sipsPath, args...)
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		if stderr.Len() > 0 {
			return nil, fmt.Errorf("sips:%s: %w: %s", format, err, strings.TrimSpace(stderr.String()))
		}
		return nil, err
	}
	return os.ReadFile(dst)
}

func decodedSize(body []byte) (int, int) {
	img, _, err := image.Decode(bytes.NewReader(body))
	if err != nil {
		return 0, 0
	}
	return img.Bounds().Dx(), img.Bounds().Dy()
}

func resize(src image.Image, maxW, maxH int, fit string, upscale bool) image.Image {
	b := src.Bounds()
	srcW := b.Dx()
	srcH := b.Dy()
	if srcW <= 0 || srcH <= 0 {
		return src
	}
	if maxW <= 0 {
		maxW = srcW
	}
	if maxH <= 0 {
		maxH = srcH
	}
	scaleW := float64(maxW) / float64(srcW)
	scaleH := float64(maxH) / float64(srcH)
	scale := scaleW
	cover := fit == "cover"
	if !cover {
		if scaleH < scale {
			scale = scaleH
		}
	} else if scaleH > scale {
		scale = scaleH
	}
	if !upscale && scale > 1 {
		scale = 1
	}
	dstW := max(1, int(float64(srcW)*scale+0.5))
	dstH := max(1, int(float64(srcH)*scale+0.5))
	dst := image.NewRGBA(image.Rect(0, 0, dstW, dstH))
	draw.CatmullRom.Scale(dst, dst.Bounds(), src, b, draw.Over, nil)
	if cover && dstW >= maxW && dstH >= maxH {
		cropped := image.NewRGBA(image.Rect(0, 0, maxW, maxH))
		srcRect := image.Rect((dstW-maxW)/2, (dstH-maxH)/2, (dstW-maxW)/2+maxW, (dstH-maxH)/2+maxH)
		draw.Draw(cropped, cropped.Bounds(), dst, srcRect.Min, draw.Src)
		return cropped
	}
	return dst
}

func encodePreferred(img image.Image, policy config.ThumbnailPolicy) ([]byte, string, string, error) {
	format := strings.ToLower(policy.PreferredFormat)
	if policy.LosslessPolicy == "webp_lossless" {
		format = "webp"
	}
	switch format {
	case "avif":
		return encodeAVIF(img)
	case "webp":
		return encodeWebP(img, policy.LosslessPolicy == "webp_lossless")
	case "jpg", "jpeg":
		return encodeJPEG(img)
	default:
		return nil, "", "", fmt.Errorf("unsupported thumbnail format %q", format)
	}
}

func encodeJPEG(img image.Image) ([]byte, string, string, error) {
	var buf bytes.Buffer
	if err := jpeg.Encode(&buf, img, &jpeg.Options{Quality: 85}); err != nil {
		return nil, "", "", err
	}
	return buf.Bytes(), "image/jpeg", "image/jpeg", nil
}

func BackendSummary(policy config.ThumbnailPolicy) string {
	if !policy.Enabled {
		return "disabled"
	}
	if policy.ExternalWebhookURL != "" {
		return "external-webhook:" + policy.ExternalWebhookURL
	}
	return encoderBackendSummary(policy)
}
