package download

import (
	"context"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"gitee.com/zhReimu/steam-go/application/content/cdn"
	"gitee.com/zhReimu/steam-go/application/content/depot"
	cryptoutil "gitee.com/zhReimu/steam-go/infrastructure/crypto"
	"gitee.com/zhReimu/steam-go/infrastructure/protocol/protobuf"

	"github.com/Bink-lab/Shino/internal/lua"
	"github.com/Bink-lab/Shino/internal/steam"
)

const tokenRetryInterval = 30 * time.Second

var cdnHTTPClient = &http.Client{
	Transport: &http.Transport{
		DialContext: (&net.Dialer{
			Timeout: 5 * time.Second,
		}).DialContext,
		TLSHandshakeTimeout:   5 * time.Second,
		ResponseHeaderTimeout: 10 * time.Second,
		IdleConnTimeout:       90 * time.Second,
		MaxIdleConnsPerHost:   16,
	},
	Timeout: 30 * time.Second,
}

type Options struct {
	AppID        uint32
	Depot        lua.Depot
	ManifestsDir string
	OutputDir    string
	MaxParallel  int
	OnProgress   func(done, total uint64)
	OnLog        func(line string)
}

func (o Options) logf(format string, args ...any) {
	if o.OnLog != nil {
		o.OnLog(fmt.Sprintf(format, args...))
	}
}

func DownloadDepot(ctx context.Context, sess *steam.Session, endpoints []Endpoint, proxy *Endpoint, opt Options) (warnings []string, retErr error) {
	if len(endpoints) == 0 {
		return warnings, fmt.Errorf("no cdn hosts")
	}
	if opt.Depot.Key == "" {
		return warnings, fmt.Errorf("depot %d has no key", opt.Depot.ID)
	}
	if !opt.Depot.HasManifest {
		return warnings, fmt.Errorf("depot %d has no manifest", opt.Depot.ID)
	}
	key, err := hex.DecodeString(opt.Depot.Key)
	if err != nil {
		return warnings, err
	}
	opt.logf("depot %d: resolving manifest %d", opt.Depot.ID, opt.Depot.ManifestID)
	m, err := resolveManifest(opt.ManifestsDir, opt.Depot.ID, opt.Depot.ManifestID)
	if err != nil {
		return warnings, err
	}
	if err := decryptIfNeeded(m, key); err != nil {
		return warnings, err
	}
	opt.logf("depot %d: manifest ready", opt.Depot.ID)
	tokens := map[string]string{}
	var tokenFailed []string
	failedAt := map[string]time.Time{}
	var tokMu sync.Mutex
	tokenFor := func(ep Endpoint) string {
		key := ep.TokenHost
		tokMu.Lock()
		defer tokMu.Unlock()
		if t, ok := tokens[key]; ok {
			return t
		}
		if since, ok := failedAt[key]; ok && time.Since(since) < tokenRetryInterval {
			return ""
		}
		tctx, cancel := context.WithTimeout(ctx, 10*time.Second)
		t, err := sess.CDNToken(tctx, opt.AppID, opt.Depot.ID, key)
		cancel()
		if err != nil {
			failedAt[key] = time.Now()
			if !slices.Contains(tokenFailed, key) {
				tokenFailed = append(tokenFailed, key)
			}
			return ""
		}
		tokens[key] = t
		return t
	}
	defer func() {
		if retErr == nil {
			return
		}
		for _, host := range tokenFailed {
			warnings = append(warnings, fmt.Sprintf("cdn token failed for %s, trying without", host))
		}
	}()
	type job struct {
		mapping *protobuf.ContentManifestPayload_FileMapping
		path    string
	}
	var jobs []job
	var total, skipped uint64
	for _, f := range m.Payload.GetMappings() {
		name := filepath.FromSlash(f.GetFilename())
		if name == "" {
			continue
		}
		if f.GetLinktarget() != "" {
			warnings = append(warnings, fmt.Sprintf("skipped symlink %s", name))
			continue
		}
		path, ok := outputPath(opt.OutputDir, name)
		if !ok {
			warnings = append(warnings, fmt.Sprintf("skipped unsafe path %s", name))
			continue
		}
		if len(f.GetChunks()) == 0 {
			if f.GetSize() == 0 {
				os.MkdirAll(path, 0o755)
			} else {
				warnings = append(warnings, fmt.Sprintf("skipped chunkless file %s", name))
			}
			continue
		}
		total += f.GetSize()
		if st, err := os.Stat(path); err == nil && uint64(st.Size()) == f.GetSize() {
			skipped += f.GetSize()
			continue
		}
		jobs = append(jobs, job{f, path})
	}
	opt.logf("depot %d: %d files · %s to fetch · %s cached", opt.Depot.ID, len(jobs), FormatBytes(total-skipped), FormatBytes(skipped))
	var done uint64
	par := opt.MaxParallel
	if par < 1 {
		par = 16
	}
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()
	sem := make(chan struct{}, par)
	var mu sync.Mutex
	report := func(n uint64) {
		d := atomic.AddUint64(&done, n)
		mu.Lock()
		if opt.OnProgress != nil {
			opt.OnProgress(d, total)
		}
		mu.Unlock()
	}
	if skipped > 0 {
		report(skipped)
	}
	var wg sync.WaitGroup
	var once sync.Once
	var firstErr error
	fail := func(err error) {
		once.Do(func() {
			firstErr = err
			cancel()
		})
	}
	for _, j := range jobs {
		if err := os.MkdirAll(filepath.Dir(j.path), 0o755); err != nil {
			return warnings, err
		}
		fh, err := os.CreateTemp(filepath.Dir(j.path), ".shino-part-*")
		if err != nil {
			return warnings, err
		}
		partPath := fh.Name()
		if err := fh.Chmod(0o644); err != nil {
			fh.Close()
			os.Remove(partPath)
			return warnings, err
		}
		if err := fh.Truncate(int64(j.mapping.GetSize())); err != nil {
			fh.Close()
			os.Remove(partPath)
			return warnings, err
		}
		for _, ch := range j.mapping.GetChunks() {
			wg.Add(1)
			go func(fh *os.File, ch *protobuf.ContentManifestPayload_FileMapping_ChunkData) {
				defer wg.Done()
				select {
				case sem <- struct{}{}:
					defer func() { <-sem }()
				case <-ctx.Done():
					return
				}
				data, err := downloadChunk(ctx, key, endpoints, proxy, tokenFor, opt.Depot.ID, ch)
				if err != nil {
					fail(err)
					return
				}
				if _, err := fh.WriteAt(data, int64(ch.GetOffset())); err != nil {
					fail(err)
					return
				}
				report(uint64(len(data)))
			}(fh, ch)
		}
		wg.Wait()
		closeErr := fh.Close()
		if firstErr != nil {
			os.Remove(partPath)
			return warnings, firstErr
		}
		if err := ctx.Err(); err != nil {
			os.Remove(partPath)
			return warnings, err
		}
		if closeErr != nil {
			os.Remove(partPath)
			return warnings, closeErr
		}
		if err := replaceFile(partPath, j.path); err != nil {
			os.Remove(partPath)
			return warnings, fmt.Errorf("commit %s: %w", filepath.Base(j.path), err)
		}
	}
	opt.logf("depot %d: complete", opt.Depot.ID)
	return warnings, nil
}

func outputPath(root, name string) (string, bool) {
	if name == "" || !filepath.IsLocal(name) {
		return "", false
	}
	return filepath.Join(root, name), true
}

func replaceFile(src, dst string) error {
	if err := os.Rename(src, dst); err == nil {
		return nil
	}
	if err := os.Remove(dst); err != nil && !os.IsNotExist(err) {
		return err
	}
	return os.Rename(src, dst)
}

func FormatBytes(n uint64) string {
	const unit = 1024
	if n < unit {
		return fmt.Sprintf("%d B", n)
	}
	div, exp := uint64(unit), 0
	for m := n / unit; m >= unit; m /= unit {
		div *= unit
		exp++
	}
	return fmt.Sprintf("%.1f %ciB", float64(n)/float64(div), "KMGT"[exp])
}

type httpStatusError struct {
	code int
	msg string
}

func (e *httpStatusError) Error() string { return e.msg }

var chunkRR uint64

func downloadChunk(ctx context.Context, key []byte, endpoints []Endpoint, proxy *Endpoint, tokenFor func(Endpoint) string, depotID uint32, ch *protobuf.ContentManifestPayload_FileMapping_ChunkData) ([]byte, error) {
	if ch == nil {
		return nil, fmt.Errorf("depot %d: nil chunk", depotID)
	}
	n := len(endpoints)
	if n == 0 {
		return nil, fmt.Errorf("depot %d: no cdn hosts", depotID)
	}
	shaHex := fmt.Sprintf("%x", ch.GetSha())
	start := int(atomic.AddUint64(&chunkRR, 1) % uint64(n))
	const maxAttempts = 8
	var lastErr error
	fatalHosts := 0
	for attempt := 0; attempt < maxAttempts; attempt++ {
		if ctx.Err() != nil {
			return nil, ctx.Err()
		}
		ep := endpoints[(start+attempt)%n]
		token := tokenFor(ep)
		target := buildChunkURL(ep, proxy, depotID, shaHex, token)
		attemptCtx, cancel := context.WithTimeout(ctx, 20*time.Second)
		data, err := fetchChunk(attemptCtx, target, key, ch)
		cancel()
		if err == nil {
			return data, nil
		}
		if ctx.Err() != nil {
			return nil, ctx.Err()
		}
		lastErr = fmt.Errorf("%s: %w", ep.Display(), err)
		if isFatalStatus(err) {
			fatalHosts++
			if fatalHosts >= n {
				break
			}
			continue
		}
		if attempt < maxAttempts-1 {
			backoff := 250 * time.Millisecond << uint(attempt)
			if backoff > 4*time.Second {
				backoff = 4 * time.Second
			}
			select {
			case <-ctx.Done():
				return nil, ctx.Err()
			case <-time.After(backoff):
			}
		}
	}
	return nil, fmt.Errorf("depot %d chunk %s: %w", depotID, shaHex[:min(12, len(shaHex))], lastErr)
}

func isFatalStatus(err error) bool {
	var statusErr *httpStatusError
	if !errors.As(err, &statusErr) {
		return false
	}
	if statusErr.code == 408 || statusErr.code == 429 {
		return false
	}
	return statusErr.code >= 400 && statusErr.code < 500
}

func fetchChunk(ctx context.Context, target string, key []byte, ch *protobuf.ContentManifestPayload_FileMapping_ChunkData) ([]byte, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, target, nil)
	if err != nil {
		return nil, err
	}
	resp, err := cdnHTTPClient.Do(req)
	if err != nil {
		var urlErr *url.Error
		if errors.As(err, &urlErr) {
			return nil, &url.Error{Op: urlErr.Op, URL: redactURL(urlErr.URL), Err: urlErr.Err}
		}
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return nil, &httpStatusError{code: resp.StatusCode, msg: fmt.Sprintf("cdn %s", resp.Status)}
	}
	body, err := io.ReadAll(io.LimitReader(resp.Body, cdn.MaxChunkSize+1))
	if err != nil {
		return nil, err
	}
	if int64(len(body)) > cdn.MaxChunkSize {
		return nil, fmt.Errorf("chunk exceeds %d bytes", cdn.MaxChunkSize)
	}
	decrypted, err := cryptoutil.SymmetricDecryptCBCWithKey(key, body)
	if err != nil {
		return nil, fmt.Errorf("decrypt: %w", err)
	}
	decompressed, err := cdn.DecompressChunk(decrypted, int(ch.GetCbOriginal()))
	if err != nil {
		return nil, fmt.Errorf("decompress: %w", err)
	}
	if err := cdn.VerifyChecksum(decompressed, ch.GetCrc()); err != nil {
		return nil, err
	}
	return decompressed, nil
}

func redactURL(target string) string {
	if i := strings.IndexByte(target, '?'); i >= 0 {
		return target[:i+1] + "redacted"
	}
	return target
}

func decryptIfNeeded(m *depot.DepotManifest, key []byte) error {
	if m.FilenamesEncrypted() {
		return m.DecryptFilenames(key)
	}
	return nil
}
