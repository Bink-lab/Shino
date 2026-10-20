package dump

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"
)

const (
	maxUploadPartSize = 64 << 20
	maxUploadParts    = 10_000
	maxUploadResponse = 1 << 20
)

var uploadClient = &http.Client{
	Transport: &http.Transport{
		DialContext: (&net.Dialer{
			Timeout: 30 * time.Second,
		}).DialContext,
		TLSHandshakeTimeout:   10 * time.Second,
		ResponseHeaderTimeout: 60 * time.Second,
		IdleConnTimeout:       90 * time.Second,
	},
}

type UploadConfig struct {
	BaseURL  string
	APIKey   string
	FilePath string
	OnStart  func(total int)
}

type uploadStart struct {
	ID       string `json:"id"`
	UploadID string `json:"uploadId"`
	PartSize int64  `json:"partSize"`
}

type uploadPart struct {
	PartNumber int    `json:"partNumber"`
	ETag       string `json:"etag"`
}

func UploadDonation(ctx context.Context, cfg UploadConfig, onPart func(done, total int)) (string, error) {
	base := strings.TrimRight(cfg.BaseURL, "/") + "/api/donations/uploads"
	do := func(req *http.Request) (*http.Response, error) {
		req.Header.Set("x-api-key", cfg.APIKey)
		return uploadClient.Do(req)
	}
	info, err := os.Stat(cfg.FilePath)
	if err != nil {
		return "", err
	}
	startBody, _ := json.Marshal(map[string]any{
		"fileName": filepath.Base(cfg.FilePath),
		"size":     info.Size(),
	})
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, base, bytes.NewReader(startBody))
	if err != nil {
		return "", err
	}
	req.Header.Set("content-type", "application/json")
	resp, err := do(req)
	if err != nil {
		return "", err
	}
	if resp.StatusCode == http.StatusUnauthorized {
		resp.Body.Close()
		return "", fmt.Errorf("upload rejected: unauthorized, check the ShinoWeb api key")
	}
	raw, err := readUploadResponse(resp)
	if err != nil {
		return "", err
	}
	if resp.StatusCode != http.StatusCreated {
		return "", fmt.Errorf("upload start failed: %s", string(raw))
	}
	var start uploadStart
	if err := json.Unmarshal(raw, &start); err != nil {
		return "", err
	}
	if start.PartSize <= 0 {
		return "", fmt.Errorf("server returned invalid part size %d", start.PartSize)
	}
	if start.PartSize > maxUploadPartSize {
		return "", fmt.Errorf("server returned part size %d, limit %d", start.PartSize, maxUploadPartSize)
	}
	file, err := os.Open(cfg.FilePath)
	if err != nil {
		return "", err
	}
	defer file.Close()
	totalParts := int((info.Size() + start.PartSize - 1) / start.PartSize)
	if totalParts > maxUploadParts {
		return "", fmt.Errorf("upload needs %d parts, limit %d", totalParts, maxUploadParts)
	}
	if cfg.OnStart != nil {
		cfg.OnStart(totalParts)
	}
	parts := make([]uploadPart, 0, totalParts)
	buffer := make([]byte, int(start.PartSize))
	for partNumber := 1; ; partNumber++ {
		n, readErr := io.ReadFull(file, buffer)
		if readErr == io.EOF && n == 0 {
			break
		}
		if readErr != nil && readErr != io.ErrUnexpectedEOF {
			return "", readErr
		}
		partURL := fmt.Sprintf("%s/%s/part?uploadId=%s&partNumber=%d", base, start.ID, start.UploadID, partNumber)
		part, err := uploadPartWithRetry(ctx, do, partURL, partNumber, buffer[:n])
		if err != nil {
			return "", err
		}
		parts = append(parts, part)
		if onPart != nil {
			onPart(partNumber, totalParts)
		}
		if partNumber >= totalParts {
			break
		}
	}
	completeBody, _ := json.Marshal(map[string]any{
		"uploadId": start.UploadID,
		"parts":    parts,
	})
	completeURL := fmt.Sprintf("%s/%s/complete", base, start.ID)
	req, err = http.NewRequestWithContext(ctx, http.MethodPost, completeURL, bytes.NewReader(completeBody))
	if err != nil {
		return "", err
	}
	req.Header.Set("content-type", "application/json")
	completeResp, err := do(req)
	if err != nil {
		return "", err
	}
	completeRaw, err := readUploadResponse(completeResp)
	if err != nil {
		return "", err
	}
	if completeResp.StatusCode != http.StatusCreated {
		return "", fmt.Errorf("upload complete failed: %s", string(completeRaw))
	}
	return start.ID, nil
}

func readUploadResponse(resp *http.Response) ([]byte, error) {
	defer resp.Body.Close()
	raw, err := io.ReadAll(io.LimitReader(resp.Body, maxUploadResponse+1))
	if err != nil {
		return nil, err
	}
	if len(raw) > maxUploadResponse {
		return nil, fmt.Errorf("upload response exceeds %d bytes", maxUploadResponse)
	}
	return raw, nil
}

func retryablePartStatus(code int) bool {
	return code == http.StatusTooManyRequests || code >= 500
}

func sleepContext(ctx context.Context, d time.Duration) error {
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-time.After(d):
		return nil
	}
}

func uploadPartWithRetry(ctx context.Context, do func(*http.Request) (*http.Response, error), partURL string, partNumber int, body []byte) (uploadPart, error) {
	backoff := 500 * time.Millisecond
	var lastErr error
	for attempt := 1; attempt <= 3; attempt++ {
		if attempt > 1 {
			if err := sleepContext(ctx, backoff); err != nil {
				return uploadPart{}, err
			}
			backoff *= 2
		}
		req, err := http.NewRequestWithContext(ctx, http.MethodPost, partURL, bytes.NewReader(body))
		if err != nil {
			return uploadPart{}, err
		}
		resp, err := do(req)
		if err != nil {
			lastErr = err
			continue
		}
		raw, err := readUploadResponse(resp)
		if err != nil {
			lastErr = err
			continue
		}
		if resp.StatusCode == http.StatusOK {
			part := uploadPart{PartNumber: partNumber}
			if err := json.Unmarshal(raw, &part); err != nil {
				return uploadPart{}, err
			}
			return part, nil
		}
		if !retryablePartStatus(resp.StatusCode) {
			return uploadPart{}, fmt.Errorf("part %d failed: %s", partNumber, string(raw))
		}
		lastErr = fmt.Errorf("%s", string(raw))
	}
	return uploadPart{}, fmt.Errorf("part %d failed: %w", partNumber, lastErr)
}
