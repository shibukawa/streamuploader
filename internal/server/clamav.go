package server

import (
	"bufio"
	"context"
	"encoding/binary"
	"fmt"
	"io"
	"net"
	"net/http"
	"strings"
	"sync"
	"time"

	"streamuploader/internal/config"
	"streamuploader/internal/storage"
)

func (s *Server) putObjectWithSecurityScan(ctx context.Context, input storage.PutInput, measured *progressReader, sideWriters ...*io.PipeWriter) (storage.PutResult, error) {
	if !s.cfg.Security.ClamAV.Enabled && len(sideWriters) == 0 {
		input.Body = measured
		return s.store.PutObject(ctx, input)
	}

	s3Reader, s3Writer := io.Pipe()
	writers := make([]io.Writer, 0, 2+len(sideWriters))
	writers = append(writers, s3Writer)
	var avReader *io.PipeReader
	var avWriter *io.PipeWriter
	if s.cfg.Security.ClamAV.Enabled {
		avReader, avWriter = io.Pipe()
		writers = append(writers, avWriter)
	}
	for _, writer := range sideWriters {
		writers = append(writers, writer)
	}

	var wg sync.WaitGroup
	var putResult storage.PutResult
	var putErr error
	var scanErr error

	wg.Add(1)
	go func() {
		defer wg.Done()
		putInput := input
		putInput.Body = s3Reader
		putResult, putErr = s.store.PutObject(ctx, putInput)
		_ = s3Reader.Close()
	}()
	if s.cfg.Security.ClamAV.Enabled {
		wg.Add(1)
		go func() {
			defer wg.Done()
			scanErr = scanReaderWithClamAV(ctx, avReader, s.cfg.Security.ClamAV)
			_ = avReader.Close()
		}()
	}

	_, copyErr := io.CopyBuffer(io.MultiWriter(writers...), measured, make([]byte, 32<<10))
	if copyErr != nil {
		_ = s3Writer.CloseWithError(copyErr)
		if avWriter != nil {
			_ = avWriter.CloseWithError(copyErr)
		}
		for _, writer := range sideWriters {
			_ = writer.CloseWithError(copyErr)
		}
	} else {
		_ = s3Writer.Close()
		if avWriter != nil {
			_ = avWriter.Close()
		}
		for _, writer := range sideWriters {
			_ = writer.Close()
		}
	}
	wg.Wait()

	if copyErr != nil {
		return storage.PutResult{}, copyErr
	}
	if scanErr != nil {
		return storage.PutResult{}, scanErr
	}
	if putErr != nil {
		return storage.PutResult{}, putErr
	}
	return putResult, nil
}

func scanReaderWithClamAV(ctx context.Context, reader io.Reader, policy config.ClamAVPolicy) error {
	timeout := time.Duration(policy.ScanTimeoutMS) * time.Millisecond
	if timeout <= 0 {
		timeout = 30 * time.Second
	}
	if policy.StreamChunkBytes <= 0 {
		policy.StreamChunkBytes = 128 << 10
	}
	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	dialer := net.Dialer{}
	conn, err := dialer.DialContext(ctx, "tcp", policy.Address)
	if err != nil {
		return virusScanError(http.StatusBadGateway, "virus_scan_unavailable", fmt.Sprintf("clamav connection failed: %v", err))
	}
	defer conn.Close()
	_ = conn.SetDeadline(time.Now().Add(timeout))

	if _, err := conn.Write([]byte("zINSTREAM\x00")); err != nil {
		return virusScanError(http.StatusBadGateway, "virus_scan_failed", fmt.Sprintf("clamav command failed: %v", err))
	}

	buf := make([]byte, policy.StreamChunkBytes)
	var sizePrefix [4]byte
	for {
		n, readErr := reader.Read(buf)
		if n > 0 {
			binary.BigEndian.PutUint32(sizePrefix[:], uint32(n))
			if _, err := conn.Write(sizePrefix[:]); err != nil {
				return virusScanError(http.StatusBadGateway, "virus_scan_failed", fmt.Sprintf("clamav stream failed: %v", err))
			}
			if _, err := conn.Write(buf[:n]); err != nil {
				return virusScanError(http.StatusBadGateway, "virus_scan_failed", fmt.Sprintf("clamav stream failed: %v", err))
			}
		}
		if readErr != nil {
			if readErr == io.EOF {
				break
			}
			return readErr
		}
	}
	binary.BigEndian.PutUint32(sizePrefix[:], 0)
	if _, err := conn.Write(sizePrefix[:]); err != nil {
		return virusScanError(http.StatusBadGateway, "virus_scan_failed", fmt.Sprintf("clamav stream finalization failed: %v", err))
	}

	response, err := bufio.NewReader(conn).ReadString(0)
	if err != nil && err != io.EOF {
		return virusScanError(http.StatusBadGateway, "virus_scan_failed", fmt.Sprintf("clamav response failed: %v", err))
	}
	response = strings.TrimSpace(strings.TrimRight(response, "\x00"))
	switch {
	case strings.HasSuffix(response, " OK"):
		return nil
	case strings.Contains(response, " FOUND"):
		return virusScanError(http.StatusUnsupportedMediaType, "malware_detected", fmt.Sprintf("clamav detected malware: %s", response))
	case strings.Contains(response, " ERROR"):
		return virusScanError(http.StatusBadGateway, "virus_scan_failed", fmt.Sprintf("clamav scan error: %s", response))
	default:
		return virusScanError(http.StatusBadGateway, "virus_scan_failed", fmt.Sprintf("clamav returned unexpected response: %s", response))
	}
}

func virusScanError(status int, code, message string) error {
	return securityUploadError{
		status:  status,
		code:    code,
		message: message,
	}
}
