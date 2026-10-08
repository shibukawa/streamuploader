// Package objerr classifies object store errors across implementations.
package objerr

import (
	"errors"
	"net/http"

	"github.com/aws/smithy-go"
	smithyhttp "github.com/aws/smithy-go/transport/http"

	"streamuploader/drive/memstore"
)

// IsNotFound reports whether err means the object does not exist.
func IsNotFound(err error) bool {
	if err == nil {
		return false
	}
	if errors.Is(err, memstore.ErrNotFound) {
		return true
	}
	var apiErr smithy.APIError
	if errors.As(err, &apiErr) {
		switch apiErr.ErrorCode() {
		case "NoSuchKey", "NotFound", "404":
			return true
		}
	}
	var respErr *smithyhttp.ResponseError
	if errors.As(err, &respErr) && respErr.HTTPStatusCode() == http.StatusNotFound {
		return true
	}
	return false
}
