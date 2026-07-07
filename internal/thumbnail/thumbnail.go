package thumbnail

import (
	"archive/zip"
	"bytes"
	"context"
	"fmt"
	"image"
	"image/color"
	stddraw "image/draw"
	_ "image/gif"
	"image/jpeg"
	_ "image/jpeg"
	_ "image/png"
	"io"
	"math"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"sync"
	"time"

	xdraw "golang.org/x/image/draw"

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
	MutoolPath       string
	FFmpegPath       string
	FFmpegEncoders   map[string]bool
	SipsPath         string
	SipsFormats      map[string]bool
	Summary          string
	UnavailableNotes []string
}

type CandidateSummary struct {
	Kind        string
	Backend     string
	ContentType string
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
	plan.MutoolPath = probeMutool()
	if plan.MutoolPath != "" {
		plan.ToolCandidates = append(plan.ToolCandidates, toolCandidate{kind: "mutool", contentType: "image/png", backend: "mutool:draw"})
	} else {
		plan.UnavailableNotes = append(plan.UnavailableNotes, "mutool unavailable")
	}
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

func ToolCandidateSummaries(plan Plan) []CandidateSummary {
	out := make([]CandidateSummary, 0, len(plan.ToolCandidates))
	for _, candidate := range plan.ToolCandidates {
		out = append(out, CandidateSummary{
			Kind:        candidate.kind,
			Backend:     candidate.backend,
			ContentType: candidate.contentType,
		})
	}
	return out
}

func Generate(ctx context.Context, store storage.Store, bucket, sourceKey string, policy config.ThumbnailPolicy) (Result, error) {
	return GenerateWithContentType(ctx, store, bucket, sourceKey, "", policy)
}

func GenerateWithContentType(ctx context.Context, store storage.Store, bucket, sourceKey, sourceContentType string, policy config.ThumbnailPolicy) (Result, error) {
	obj, err := store.GetObject(ctx, storage.GetInput{Bucket: bucket, Key: sourceKey})
	if err != nil {
		return Result{ObjectKey: sourceKey + policy.ObjectKeySuffix}, err
	}
	defer obj.Body.Close()
	converted, err := ConvertFromReaderWithContentType(ctx, obj.Body, sourceKey, sourceContentType, policy)
	if err != nil {
		return converted.Result, err
	}
	if err := StoreConversion(ctx, store, bucket, converted); err != nil {
		return converted.Result, err
	}
	return converted.Result, nil
}

func ConvertFromReader(ctx context.Context, r io.Reader, sourceKey string, policy config.ThumbnailPolicy) (Conversion, error) {
	return ConvertFromReaderWithContentType(ctx, r, sourceKey, "", policy)
}

func ConvertFromReaderWithContentType(ctx context.Context, r io.Reader, sourceKey, sourceContentType string, policy config.ThumbnailPolicy) (Conversion, error) {
	out := Conversion{Result: Result{ObjectKey: sourceKey + policy.ObjectKeySuffix}}
	if !policy.Enabled {
		return out, fmt.Errorf("thumbnail generation is disabled")
	}
	if policy.ExternalWebhookURL != "" {
		return ConvertWithWebhook(ctx, r, sourceKey, sourceContentType, policy)
	}
	body, contentType, backend, width, height, err := ConvertWithPlanForContentType(r, sourceContentType, policy, currentPlan(policy))
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

func ConvertWithWebhook(ctx context.Context, r io.Reader, sourceKey, sourceContentType string, policy config.ThumbnailPolicy) (Conversion, error) {
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
	req.Header.Set("X-Source-Content-Type", normalizeContentType(sourceContentType))
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
	return ConvertWithPlanForContentType(r, "", policy, plan)
}

func ConvertWithPlanForContentType(r io.Reader, sourceContentType string, policy config.ThumbnailPolicy, plan Plan) ([]byte, string, string, int, int, error) {
	input, err := io.ReadAll(io.LimitReader(r, 256<<20))
	if err != nil {
		return nil, "", "", 0, 0, err
	}
	sourceContentType = normalizeContentType(sourceContentType)
	if isOOXMLContentType(sourceContentType) {
		if thumb, thumbType, err := extractOOXMLThumbnail(input); err == nil {
			return convertImageBytes(thumb, thumbType, policy, plan, "embedded")
		}
		if pdf, err := convertOfficeToPDF(input, sourceContentType, policy); err == nil {
			if body, contentType, backend, width, height, err := convertWithTools(pdf, "application/pdf", policy, plan); err == nil {
				return body, contentType, "libreoffice:" + backend, width, height, nil
			}
		}
	}
	if strings.HasPrefix(sourceContentType, "video/") {
		if body, contentType, backend, width, height, err := convertVideoStill(input, policy, plan); err == nil {
			return body, contentType, backend, width, height, nil
		}
	}
	img, _, err := image.Decode(bytes.NewReader(input))
	if err != nil {
		if body, contentType, backend, width, height, toolErr := convertWithTools(input, sourceContentType, policy, plan); toolErr == nil {
			return body, contentType, backend, width, height, nil
		}
		return nil, "", "", 0, 0, fmt.Errorf("decode image: %w", err)
	}
	return convertDecodedImage(img, policy, plan, "")
}

func convertImageBytes(input []byte, sourceContentType string, policy config.ThumbnailPolicy, plan Plan, backendPrefix string) ([]byte, string, string, int, int, error) {
	img, _, err := image.Decode(bytes.NewReader(input))
	if err != nil {
		return convertWithTools(input, normalizeContentType(sourceContentType), policy, plan)
	}
	return convertDecodedImage(img, policy, plan, backendPrefix)
}

func convertDecodedImage(img image.Image, policy config.ThumbnailPolicy, plan Plan, backendPrefix string) ([]byte, string, string, int, int, error) {
	thumb := resize(img, policy.Width, policy.Height, policy.Fit, policy.Upscale)
	var lastErr error
	for _, candidate := range plan.GoCandidates {
		body, contentType, backend, err := candidate.encode(thumb, candidate.lossless)
		if err == nil {
			if backendPrefix != "" {
				backend = backendPrefix + ":" + backend
			}
			return body, contentType, backend, thumb.Bounds().Dx(), thumb.Bounds().Dy(), nil
		}
		lastErr = err
	}
	body, contentType, backend, err := encodeJPEG(thumb)
	if err == nil {
		if backendPrefix != "" {
			backend = backendPrefix + ":" + backend
		}
		return body, contentType, backend, thumb.Bounds().Dx(), thumb.Bounds().Dy(), nil
	}
	lastErr = err
	if lastErr != nil {
		return nil, "", "", 0, 0, lastErr
	}
	return nil, "", "", 0, 0, fmt.Errorf("thumbnail encoder is unavailable")
}

func convertWithTools(input []byte, sourceContentType string, policy config.ThumbnailPolicy, plan Plan) ([]byte, string, string, int, int, error) {
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
		case "mutool":
			body, err := runMutoolThumbnail(ctx, plan.MutoolPath, input, policy, sourceContentType)
			if err != nil {
				lastErr = err
				continue
			}
			return convertImageBytes(body, "image/png", policy, plan, "mutool")
		case "ffmpeg":
			body, err := runFFmpegThumbnail(ctx, plan.FFmpegPath, input, filter, candidate.ffmpeg)
			if err != nil {
				lastErr = err
				continue
			}
			width, height := decodedSize(body)
			return body, candidate.contentType, candidate.backend, width, height, nil
		case "sips":
			body, err := runSipsThumbnail(ctx, plan.SipsPath, input, policy, candidate.sipsFormat, sourceContentType)
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

func probeMutool() string {
	path, err := exec.LookPath("mutool")
	if err != nil {
		return ""
	}
	return path
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

func convertVideoStill(input []byte, policy config.ThumbnailPolicy, plan Plan) ([]byte, string, string, int, int, error) {
	if plan.FFmpegPath == "" {
		return nil, "", "", 0, 0, fmt.Errorf("ffmpeg unavailable")
	}
	timeout := policy.ExternalTimeout
	if timeout <= 0 {
		timeout = 30 * time.Second
	}
	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()
	if attached, err := extractVideoAttachedPicture(ctx, plan.FFmpegPath, input); err == nil && len(attached) > 0 {
		if body, contentType, backend, width, height, err := convertVideoStillImage(attached, policy, plan, "video-attached-picture"); err == nil {
			return body, contentType, backend, width, height, nil
		}
	}
	frames, err := extractVideoKeyframes(ctx, plan.FFmpegPath, input, policy)
	if err != nil {
		return nil, "", "", 0, 0, err
	}
	best := frames[0]
	for _, frame := range frames[1:] {
		if len(frame) > len(best) {
			best = frame
		}
	}
	return convertVideoStillImage(best, policy, plan, "video-keyframe")
}

func convertVideoStillImage(input []byte, policy config.ThumbnailPolicy, plan Plan, backendPrefix string) ([]byte, string, string, int, int, error) {
	img, _, err := image.Decode(bytes.NewReader(input))
	if err != nil {
		return nil, "", "", 0, 0, err
	}
	thumb := addPlayOverlay(img)
	body, contentType, backend, width, height, err := convertDecodedImage(thumb, policy, plan, backendPrefix)
	if err != nil {
		return nil, "", "", 0, 0, err
	}
	return body, contentType, backend, width, height, nil
}

func extractVideoAttachedPicture(ctx context.Context, ffmpegPath string, input []byte) ([]byte, error) {
	args := []string{
		"-v", "error",
		"-i", "pipe:0",
		"-map", "0:v",
		"-map", "-0:V",
		"-frames:v", "1",
		"-f", "image2pipe",
		"-vcodec", "mjpeg",
		"pipe:1",
	}
	cmd := exec.CommandContext(ctx, ffmpegPath, args...)
	cmd.Stdin = bytes.NewReader(input)
	var stdout bytes.Buffer
	var stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		if stderr.Len() > 0 {
			return nil, fmt.Errorf("ffmpeg:attached-picture: %w: %s", err, strings.TrimSpace(stderr.String()))
		}
		return nil, err
	}
	if stdout.Len() == 0 {
		return nil, fmt.Errorf("ffmpeg:attached-picture: empty output")
	}
	return stdout.Bytes(), nil
}

func extractVideoKeyframes(ctx context.Context, ffmpegPath string, input []byte, policy config.ThumbnailPolicy) ([][]byte, error) {
	limit := policy.VideoCandidateKeyframes
	if limit <= 0 {
		limit = 10
	}
	if limit > 60 {
		limit = 60
	}
	frames, err := runFFmpegFrameExtract(ctx, ffmpegPath, input, policy, true, limit)
	if err == nil && len(frames) > 0 {
		return frames, nil
	}
	return runFFmpegFrameExtract(ctx, ffmpegPath, input, policy, false, limit)
}

func runFFmpegFrameExtract(ctx context.Context, ffmpegPath string, input []byte, policy config.ThumbnailPolicy, keyframesOnly bool, limit int) ([][]byte, error) {
	dir, err := os.MkdirTemp("", "streamuploader-video-thumb-*")
	if err != nil {
		return nil, err
	}
	defer os.RemoveAll(dir)
	pattern := filepath.Join(dir, "frame-%03d.jpg")
	scale := fmt.Sprintf("scale=w='min(%d\\,iw)':h='min(%d\\,ih)':force_original_aspect_ratio=decrease", max(1, policy.Width), max(1, policy.Height))
	args := []string{"-v", "error"}
	if keyframesOnly {
		args = append(args, "-skip_frame", "nokey")
	}
	args = append(args,
		"-i", "pipe:0",
		"-vf", scale,
		"-frames:v", strconv.Itoa(max(1, limit)),
		"-vsync", "0",
		"-q:v", "3",
		pattern,
	)
	cmd := exec.CommandContext(ctx, ffmpegPath, args...)
	cmd.Stdin = bytes.NewReader(input)
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		if stderr.Len() > 0 {
			return nil, fmt.Errorf("ffmpeg:keyframes: %w: %s", err, strings.TrimSpace(stderr.String()))
		}
		return nil, err
	}
	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil, err
	}
	var frames [][]byte
	for _, entry := range entries {
		if entry.IsDir() || !strings.HasSuffix(entry.Name(), ".jpg") {
			continue
		}
		body, err := os.ReadFile(filepath.Join(dir, entry.Name()))
		if err == nil && len(body) > 0 {
			frames = append(frames, body)
		}
	}
	if len(frames) == 0 {
		return nil, fmt.Errorf("ffmpeg:keyframes: empty output")
	}
	return frames, nil
}

func addPlayOverlay(src image.Image) image.Image {
	b := src.Bounds()
	dst := image.NewRGBA(image.Rect(0, 0, b.Dx(), b.Dy()))
	stddraw.Draw(dst, dst.Bounds(), src, b.Min, stddraw.Src)
	w, h := dst.Bounds().Dx(), dst.Bounds().Dy()
	if w <= 0 || h <= 0 {
		return dst
	}
	size := int(math.Round(float64(min(w, h)) * 0.36))
	if size < 12 {
		size = min(w, h)
	}
	cx, cy := w/2, h/2
	left := cx - size/3
	top := cy - size/2
	bottom := cy + size/2
	right := cx + size/2
	overlay := color.RGBA{255, 255, 255, 210}
	for y := top; y <= bottom; y++ {
		if y < 0 || y >= h {
			continue
		}
		t := float64(y-top) / float64(max(1, bottom-top))
		edge := left + int(math.Abs(t-0.5)*2*float64(size/3))
		for x := edge; x <= right; x++ {
			if x >= 0 && x < w {
				dst.Set(x, y, overlay)
			}
		}
	}
	return dst
}

func runSipsThumbnail(ctx context.Context, sipsPath string, input []byte, policy config.ThumbnailPolicy, format, sourceContentType string) ([]byte, error) {
	dir, err := os.MkdirTemp("", "streamuploader-thumbnail-*")
	if err != nil {
		return nil, err
	}
	defer os.RemoveAll(dir)
	src := filepath.Join(dir, "source"+extensionForContentType(sourceContentType))
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

func runMutoolThumbnail(ctx context.Context, mutoolPath string, input []byte, policy config.ThumbnailPolicy, sourceContentType string) ([]byte, error) {
	if mutoolPath == "" {
		return nil, fmt.Errorf("mutool unavailable")
	}
	dir, err := os.MkdirTemp("", "streamuploader-mutool-thumb-*")
	if err != nil {
		return nil, err
	}
	defer os.RemoveAll(dir)
	src := filepath.Join(dir, "source"+extensionForContentType(sourceContentType))
	dst := filepath.Join(dir, "page.png")
	if err := os.WriteFile(src, input, 0600); err != nil {
		return nil, err
	}
	args := []string{
		"draw",
		"-q",
		"-F", "png",
		"-o", dst,
		"-w", strconv.Itoa(max(1, policy.Width)),
		"-h", strconv.Itoa(max(1, policy.Height)),
		src,
		"1",
	}
	var stderr bytes.Buffer
	cmd := exec.CommandContext(ctx, mutoolPath, args...)
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		if stderr.Len() > 0 {
			return nil, fmt.Errorf("mutool draw: %w: %s", err, strings.TrimSpace(stderr.String()))
		}
		return nil, err
	}
	return os.ReadFile(dst)
}

func extractOOXMLThumbnail(input []byte) ([]byte, string, error) {
	reader, err := zip.NewReader(bytes.NewReader(input), int64(len(input)))
	if err != nil {
		return nil, "", err
	}
	for _, file := range reader.File {
		name := strings.ToLower(file.Name)
		var contentType string
		switch name {
		case "docprops/thumbnail.jpeg", "docprops/thumbnail.jpg":
			contentType = "image/jpeg"
		case "docprops/thumbnail.png":
			contentType = "image/png"
		case "docprops/thumbnail.emf", "docprops/thumbnail.wmf":
			continue
		default:
			continue
		}
		if file.UncompressedSize64 > 32<<20 {
			continue
		}
		rc, err := file.Open()
		if err != nil {
			continue
		}
		body, readErr := io.ReadAll(io.LimitReader(rc, 32<<20))
		_ = rc.Close()
		if readErr == nil && len(body) > 0 {
			return body, contentType, nil
		}
	}
	return nil, "", fmt.Errorf("ooxml thumbnail not found")
}

func convertOfficeToPDF(input []byte, sourceContentType string, policy config.ThumbnailPolicy) ([]byte, error) {
	tool, err := officeConverterPath()
	if err != nil {
		return nil, err
	}
	timeout := policy.ExternalTimeout
	if timeout <= 0 {
		timeout = 30 * time.Second
	}
	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()
	dir, err := os.MkdirTemp("", "streamuploader-office-thumb-*")
	if err != nil {
		return nil, err
	}
	defer os.RemoveAll(dir)
	src := filepath.Join(dir, "source"+officeExtensionForContentType(sourceContentType))
	if err := os.WriteFile(src, input, 0600); err != nil {
		return nil, err
	}
	userInstall := "file://" + filepath.Join(dir, "libreoffice-profile")
	cmd := exec.CommandContext(ctx, tool, "-env:UserInstallation="+userInstall, "--headless", "--convert-to", "pdf", "--outdir", dir, src)
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		if stderr.Len() > 0 {
			return nil, fmt.Errorf("libreoffice: %w: %s", err, strings.TrimSpace(stderr.String()))
		}
		return nil, err
	}
	pdfPath := filepath.Join(dir, "source.pdf")
	body, err := os.ReadFile(pdfPath)
	if err != nil {
		return nil, err
	}
	if len(body) == 0 {
		return nil, fmt.Errorf("libreoffice: empty PDF output")
	}
	return body, nil
}

func officeConverterPath() (string, error) {
	for _, name := range []string{"soffice", "libreoffice"} {
		path, err := exec.LookPath(name)
		if err == nil {
			return path, nil
		}
	}
	return "", fmt.Errorf("libreoffice unavailable")
}

func normalizeContentType(contentType string) string {
	return strings.ToLower(strings.TrimSpace(strings.Split(contentType, ";")[0]))
}

func isOOXMLContentType(contentType string) bool {
	switch normalizeContentType(contentType) {
	case "application/vnd.openxmlformats-officedocument.wordprocessingml.document",
		"application/vnd.openxmlformats-officedocument.spreadsheetml.sheet",
		"application/vnd.openxmlformats-officedocument.presentationml.presentation":
		return true
	default:
		return false
	}
}

func officeExtensionForContentType(contentType string) string {
	switch normalizeContentType(contentType) {
	case "application/vnd.openxmlformats-officedocument.wordprocessingml.document":
		return ".docx"
	case "application/vnd.openxmlformats-officedocument.spreadsheetml.sheet":
		return ".xlsx"
	case "application/vnd.openxmlformats-officedocument.presentationml.presentation":
		return ".pptx"
	default:
		return ".docx"
	}
}

func extensionForContentType(contentType string) string {
	switch normalizeContentType(contentType) {
	case "application/pdf":
		return ".pdf"
	case "image/svg+xml":
		return ".svg"
	case "image/tiff", "image/x-tiff":
		return ".tiff"
	case "image/heif", "image/heif-sequence":
		return ".heif"
	case "image/heic", "image/heic-sequence":
		return ".heic"
	case "image/jxl":
		return ".jxl"
	case "image/jp2":
		return ".jp2"
	case "image/jpx":
		return ".jpx"
	case "image/vnd.adobe.photoshop", "image/x-photoshop":
		return ".psd"
	case "image/x-tga", "image/tga":
		return ".tga"
	case "image/bmp":
		return ".bmp"
	default:
		return ""
	}
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
	xdraw.CatmullRom.Scale(dst, dst.Bounds(), src, b, xdraw.Over, nil)
	if cover && dstW >= maxW && dstH >= maxH {
		cropped := image.NewRGBA(image.Rect(0, 0, maxW, maxH))
		srcRect := image.Rect((dstW-maxW)/2, (dstH-maxH)/2, (dstW-maxW)/2+maxW, (dstH-maxH)/2+maxH)
		stddraw.Draw(cropped, cropped.Bounds(), dst, srcRect.Min, stddraw.Src)
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
