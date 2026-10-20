package appinfo

import (
	"bytes"
	"compress/gzip"
	"context"
	"fmt"
	"io"
	"net/http"

	"gitee.com/zhReimu/steam-go/domain/keyvalues"
)

const (
	maxCompressedBytes   = 32 << 20
	maxDecompressedBytes = 64 << 20
)

func FetchHTTP(ctx context.Context, client *http.Client, url string) (*keyvalues.KeyValues, error) {
	if client == nil {
		client = http.DefaultClient
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return nil, err
	}
	resp, err := client.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return nil, fmt.Errorf("appinfo request failed with status %s", resp.Status)
	}
	raw, err := io.ReadAll(io.LimitReader(resp.Body, maxCompressedBytes+1))
	if err != nil {
		return nil, err
	}
	if len(raw) > maxCompressedBytes {
		return nil, fmt.Errorf("appinfo exceeds size limit")
	}
	gz, err := gzip.NewReader(bytes.NewReader(raw))
	if err != nil {
		return nil, err
	}
	defer gz.Close()
	text, err := io.ReadAll(io.LimitReader(gz, maxDecompressedBytes+1))
	if err != nil {
		return nil, err
	}
	if len(text) > maxDecompressedBytes {
		return nil, fmt.Errorf("appinfo exceeds size limit")
	}
	return keyvalues.ParseText(text)
}
