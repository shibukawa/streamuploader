//go:build !cgo

package thumbnail

import (
	"bytes"
	"fmt"
	"image"

	"github.com/eringen/gowebper"

	"streamuploader/internal/config"
)

func encodeAVIF(image.Image) ([]byte, string, string, error) {
	return nil, "", "", fmt.Errorf("avif requires cgo thumbnail build")
}

func encodeWebP(img image.Image, _ bool) ([]byte, string, string, error) {
	var buf bytes.Buffer
	err := gowebper.Encode(&buf, img, &gowebper.Options{Level: gowebper.LevelDefault})
	return buf.Bytes(), "image/webp", "eringen/gowebper", err
}

func cgoEnabled() bool {
	return false
}

func goEncoderCandidates(policy config.ThumbnailPolicy) []encoderCandidate {
	webpCandidate := encoderCandidate{contentType: "image/webp", backend: "eringen/gowebper", lossless: policy.LosslessPolicy == "webp_lossless", encode: encodeWebP}
	jpegCandidate := encoderCandidate{contentType: "image/jpeg", backend: "image/jpeg", encode: func(img image.Image, _ bool) ([]byte, string, string, error) {
		return encodeJPEG(img)
	}}
	if policy.LosslessPolicy == "webp_lossless" || policy.PreferredFormat == "webp" {
		return []encoderCandidate{webpCandidate, jpegCandidate}
	}
	if policy.PreferredFormat == "jpg" || policy.PreferredFormat == "jpeg" {
		return []encoderCandidate{jpegCandidate}
	}
	return []encoderCandidate{webpCandidate, jpegCandidate}
}

func encoderBackendSummary(policy config.ThumbnailPolicy) string {
	if policy.LosslessPolicy == "webp_lossless" || policy.PreferredFormat == "webp" {
		return "go-pure:webp:eringen/gowebper fallback jpeg"
	}
	return "go-pure:avif-unavailable fallback jpeg"
}
