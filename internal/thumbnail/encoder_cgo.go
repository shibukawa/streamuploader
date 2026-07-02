//go:build cgo

package thumbnail

import (
	"bytes"
	"image"
	"strings"

	"github.com/chai2010/webp"
	avif "github.com/vegidio/avif-go"

	"streamuploader/internal/config"
)

func encodeAVIF(img image.Image) ([]byte, string, string, error) {
	var buf bytes.Buffer
	err := avif.Encode(&buf, img, &avif.Options{Speed: 8, AlphaQuality: 70, ColorQuality: 72})
	return buf.Bytes(), "image/avif", "vegidio/avif-go", err
}

func encodeWebP(img image.Image, lossless bool) ([]byte, string, string, error) {
	var buf bytes.Buffer
	err := webp.Encode(&buf, img, &webp.Options{Lossless: lossless, Quality: 78})
	return buf.Bytes(), "image/webp", "chai2010/webp", err
}

func cgoEnabled() bool {
	return true
}

func goEncoderCandidates(policy config.ThumbnailPolicy) []encoderCandidate {
	avifCandidate := encoderCandidate{contentType: "image/avif", backend: "vegidio/avif-go", encode: func(img image.Image, _ bool) ([]byte, string, string, error) {
		return encodeAVIF(img)
	}}
	webpCandidate := encoderCandidate{contentType: "image/webp", backend: "chai2010/webp", lossless: policy.LosslessPolicy == "webp_lossless", encode: encodeWebP}
	jpegCandidate := encoderCandidate{contentType: "image/jpeg", backend: "image/jpeg", encode: func(img image.Image, _ bool) ([]byte, string, string, error) {
		return encodeJPEG(img)
	}}
	if policy.LosslessPolicy == "webp_lossless" {
		return []encoderCandidate{webpCandidate, jpegCandidate}
	}
	switch strings.ToLower(policy.PreferredFormat) {
	case "webp":
		return []encoderCandidate{webpCandidate, avifCandidate, jpegCandidate}
	case "jpg", "jpeg":
		return []encoderCandidate{jpegCandidate}
	default:
		return []encoderCandidate{avifCandidate, webpCandidate, jpegCandidate}
	}
}

func encoderBackendSummary(policy config.ThumbnailPolicy) string {
	if policy.LosslessPolicy == "webp_lossless" || policy.PreferredFormat == "webp" {
		return "go-cgo:webp:chai2010/webp"
	}
	return "go-cgo:avif:vegidio/avif-go fallback webp:chai2010/webp fallback jpeg"
}
