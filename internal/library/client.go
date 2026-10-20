package library

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math/rand/v2"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"sync"
	"time"
)

const (
	DefaultAppListURL    = "https://api.bonker.dev/api/applist?type=game&format=names"
	DefaultAppDetailsURL = "https://api.bonker.dev/api/appdetails"
	DefaultRyuuURL       = "https://generator.ryuu.lol/secure_download"
	DefaultHubcapURL     = "https://hubcapmanifest.com/api/v1/manifest"
	DefaultShinoWebURL   = "http://localhost:3000"
	DefaultStoreURL      = "https://store.steampowered.com/api/appdetails"

	storeUserAgent = "Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/126.0 Safari/537.36"

	maxAppListBytes    = 64 << 20
	maxAppDetailsBytes = 1 << 20
	maxImageBytes      = 16 << 20
	maxBundleBytes     = 1 << 30
)

type App struct {
	AppID uint32
	Name  string
}

type ClientConfig struct {
	HTTPClient    *http.Client
	AppListURL    string
	AppDetailsURL string
	RyuuURL       string
	HubcapURL     string
	ShinoWebURL   string
	StoreURL      string
}

type Client struct {
	httpClient    *http.Client
	appListURL    string
	appDetailsURL string
	ryuuURL       string
	hubcapURL     string
	shinoWebURL   string
	storeURL      string
}

func NewClient(config ClientConfig) *Client {
	httpClient := config.HTTPClient
	if httpClient == nil {
		httpClient = &http.Client{Timeout: 30 * time.Second}
	}
	appListURL := config.AppListURL
	if appListURL == "" {
		appListURL = DefaultAppListURL
	}
	appDetailsURL := config.AppDetailsURL
	if appDetailsURL == "" {
		appDetailsURL = DefaultAppDetailsURL
	}
	ryuuURL := config.RyuuURL
	if ryuuURL == "" {
		ryuuURL = DefaultRyuuURL
	}
	hubcapURL := config.HubcapURL
	if hubcapURL == "" {
		hubcapURL = DefaultHubcapURL
	}
	shinoWebURL := config.ShinoWebURL
	if shinoWebURL == "" {
		shinoWebURL = DefaultShinoWebURL
	}
	storeURL := config.StoreURL
	if storeURL == "" {
		storeURL = DefaultStoreURL
	}
	return &Client{
		httpClient:    httpClient,
		appListURL:    appListURL,
		appDetailsURL: appDetailsURL,
		ryuuURL:       ryuuURL,
		hubcapURL:     hubcapURL,
		shinoWebURL:   shinoWebURL,
		storeURL:      storeURL,
	}
}

func (c *Client) FetchApps(ctx context.Context) ([]App, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, c.appListURL, nil)
	if err != nil {
		return nil, errors.New("invalid app list url")
	}
	resp, err := c.httpClient.Do(req)
	if err != nil {
		return nil, errors.New("app list request failed")
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return nil, fmt.Errorf("app list request failed with status %s", resp.Status)
	}
	var payload struct {
		Apps []struct {
			AppID uint32 `json:"appid"`
			Name  string `json:"name"`
		} `json:"apps"`
	}
	if err := decodeJSONLimited(resp.Body, maxAppListBytes, &payload); err != nil {
		return nil, fmt.Errorf("app list decode failed: %w", err)
	}
	apps := make([]App, 0, len(payload.Apps))
	for _, a := range payload.Apps {
		apps = append(apps, App{AppID: a.AppID, Name: a.Name})
	}
	return apps, nil
}

func (c *Client) FetchImage(ctx context.Context, appID uint32) ([]byte, error) {
	detailsURL, err := url.Parse(c.appDetailsURL)
	if err != nil {
		return nil, errors.New("invalid app details url")
	}
	query := detailsURL.Query()
	query.Set("appids", strconv.FormatUint(uint64(appID), 10))
	query.Set("detail", "basic")
	detailsURL.RawQuery = query.Encode()

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, detailsURL.String(), nil)
	if err != nil {
		return nil, errors.New("invalid app details url")
	}
	resp, err := c.httpClient.Do(req)
	if err != nil {
		return nil, errors.New("app details request failed")
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		resp.Body.Close()
		return nil, fmt.Errorf("app details request failed with status %s", resp.Status)
	}
	var payload struct {
		Success bool `json:"success"`
		Data    struct {
			HeaderImage string `json:"header_image"`
		} `json:"data"`
	}
	err = decodeJSONLimited(resp.Body, maxAppDetailsBytes, &payload)
	resp.Body.Close()
	if err != nil {
		return nil, fmt.Errorf("app details decode failed: %w", err)
	}
	if payload.Data.HeaderImage == "" {
		return nil, fmt.Errorf("app %d has no header image", appID)
	}
	return fetchBytes(ctx, c.httpClient, payload.Data.HeaderImage, maxImageBytes)
}

func (c *Client) DownloadRyuu(ctx context.Context, appID uint32, authCode string, dst io.Writer) error {
	ryuuURL, err := url.Parse(c.ryuuURL)
	if err != nil {
		return errors.New("invalid ryuu url")
	}
	query := ryuuURL.Query()
	query.Set("appid", strconv.FormatUint(uint64(appID), 10))
	query.Set("auth_code", authCode)
	ryuuURL.RawQuery = query.Encode()

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, ryuuURL.String(), nil)
	if err != nil {
		return errors.New("invalid ryuu request")
	}
	return c.streamArchive(req, "ryuu", dst)
}

func (c *Client) DownloadHubcap(ctx context.Context, appID uint32, apiKey string, dst io.Writer) error {
	hubcapURL, err := url.Parse(c.hubcapURL)
	if err != nil {
		return errors.New("invalid hubcap url")
	}
	hubcapURL.Path = strings.TrimRight(hubcapURL.Path, "/") + "/" + strconv.FormatUint(uint64(appID), 10)

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, hubcapURL.String(), nil)
	if err != nil {
		return errors.New("invalid hubcap request")
	}
	req.Header.Set("Authorization", "Bearer "+apiKey)
	return c.streamArchive(req, "hubcap", dst)
}

func (c *Client) DownloadShinoWeb(ctx context.Context, appID uint32, apiKey string, dst io.Writer) error {
	endpoint := strings.TrimRight(c.shinoWebURL, "/") + "/api/v1/games/" + strconv.FormatUint(uint64(appID), 10) + "/download"
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint, nil)
	if err != nil {
		return errors.New("invalid shino url")
	}
	req.Header.Set("Authorization", "Bearer "+apiKey)
	resp, err := c.httpClient.Do(req)
	if err != nil {
		return errors.New("shino request failed")
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return fmt.Errorf("shino request failed with status %s", resp.Status)
	}
	var payload struct {
		DownloadURL string `json:"download_url"`
	}
	if err := decodeJSONLimited(resp.Body, maxAppDetailsBytes, &payload); err != nil {
		return fmt.Errorf("shino decode failed: %w", err)
	}
	if payload.DownloadURL == "" {
		return errors.New("shino response missing download url")
	}
	dlReq, err := http.NewRequestWithContext(ctx, http.MethodGet, payload.DownloadURL, nil)
	if err != nil {
		return errors.New("invalid shino download url")
	}
	return c.streamArchive(dlReq, "shino", dst)
}

func (c *Client) streamArchive(req *http.Request, label string, dst io.Writer) error {
	resp, err := c.httpClient.Do(req)
	if err != nil {
		return fmt.Errorf("%s request failed", label)
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return fmt.Errorf("%s request failed with status %s", label, resp.Status)
	}
	written, err := io.Copy(dst, io.LimitReader(resp.Body, maxBundleBytes+1))
	if err != nil {
		return fmt.Errorf("%s download interrupted", label)
	}
	if written > maxBundleBytes {
		return fmt.Errorf("%s archive exceeds %d bytes", label, int64(maxBundleBytes))
	}
	return nil
}

func decodeJSONLimited(r io.Reader, limit int64, v any) error {
	data, err := readLimited(r, limit)
	if err != nil {
		return err
	}
	return json.Unmarshal(data, v)
}

func readLimited(r io.Reader, limit int64) ([]byte, error) {
	data, err := io.ReadAll(io.LimitReader(r, limit+1))
	if err != nil {
		return nil, err
	}
	if int64(len(data)) > limit {
		return nil, errors.New("response exceeds size limit")
	}
	return data, nil
}

func fetchBytes(ctx context.Context, httpClient *http.Client, rawURL string, limit int64) ([]byte, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, rawURL, nil)
	if err != nil {
		return nil, errors.New("invalid image url")
	}
	resp, err := httpClient.Do(req)
	if err != nil {
		return nil, errors.New("image request failed")
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return nil, fmt.Errorf("image request failed with status %s", resp.Status)
	}
	return readLimited(resp.Body, limit)
}

type storeAppDetails struct {
	Success bool `json:"success"`
	Data    struct {
		IsFree *bool    `json:"is_free"`
		DLC    []uint32 `json:"dlc"`
	} `json:"data"`
}

type StoreApp struct {
	Free  bool
	Known bool
	DLCs  []uint32
}

func parseStoreApp(appID uint32, data []byte) StoreApp {
	var payload map[string]storeAppDetails
	if err := json.Unmarshal(data, &payload); err != nil {
		return StoreApp{}
	}
	entry, ok := payload[strconv.FormatUint(uint64(appID), 10)]
	if !ok || !entry.Success || entry.Data.IsFree == nil {
		return StoreApp{}
	}
	seen := map[uint32]bool{}
	var dlcs []uint32
	for _, id := range entry.Data.DLC {
		if id == 0 || id == appID || seen[id] {
			continue
		}
		seen[id] = true
		dlcs = append(dlcs, id)
	}
	return StoreApp{Free: *entry.Data.IsFree, Known: true, DLCs: dlcs}
}

func parseFreeStatus(appID uint32, data []byte) (free bool, known bool) {
	info := parseStoreApp(appID, data)
	return info.Free, info.Known
}

func (c *Client) FetchFreeStatus(ctx context.Context, appID uint32) (bool, bool) {
	info := c.fetchStoreApp(ctx, appID)
	return info.Free, info.Known
}

func (c *Client) fetchFreeBody(ctx context.Context, target string) ([]byte, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, target, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("User-Agent", storeUserAgent)
	resp, err := c.httpClient.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return nil, fmt.Errorf("store request failed with status %s", resp.Status)
	}
	return readLimited(resp.Body, maxAppDetailsBytes)
}

func (c *Client) FetchFreeMap(ctx context.Context, appIDs []uint32) map[uint32]bool {
	free := map[uint32]bool{}
	for id, info := range c.FetchStoreMap(ctx, appIDs) {
		if info.Known && info.Free {
			free[id] = true
		}
	}
	return free
}

func (c *Client) FetchStoreMap(ctx context.Context, appIDs []uint32) map[uint32]StoreApp {
	found := map[uint32]StoreApp{}
	if len(appIDs) == 0 {
		return found
	}
	var mu sync.Mutex
	var wg sync.WaitGroup
	sem := make(chan struct{}, 4)
	for _, appID := range appIDs {
		wg.Add(1)
		go func(appID uint32) {
			defer wg.Done()
			select {
			case <-ctx.Done():
				return
			case sem <- struct{}{}:
			}
			defer func() { <-sem }()
			if info := c.fetchStoreApp(ctx, appID); info.Known {
				mu.Lock()
				found[appID] = info
				mu.Unlock()
			}
		}(appID)
	}
	wg.Wait()
	return found
}

func (c *Client) fetchStoreApp(ctx context.Context, appID uint32) StoreApp {
	storeURL, err := url.Parse(c.storeURL)
	if err != nil {
		return StoreApp{}
	}
	query := storeURL.Query()
	query.Set("appids", strconv.FormatUint(uint64(appID), 10))
	storeURL.RawQuery = query.Encode()

	var data []byte
	for attempt := 0; attempt < 2; attempt++ {
		if attempt > 0 {
			delay := 1500*time.Millisecond + time.Duration(rand.N(500))*time.Millisecond
			select {
			case <-ctx.Done():
				return StoreApp{}
			case <-time.After(delay):
			}
		}
		data, err = c.fetchFreeBody(ctx, storeURL.String())
		if err == nil {
			break
		}
	}
	if err != nil {
		return StoreApp{}
	}
	return parseStoreApp(appID, data)
}
