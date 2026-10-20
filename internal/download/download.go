package download

import (
	"context"
	"encoding/hex"
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"sync/atomic"

	"gitee.com/zhReimu/steam-go/application/content/cdn"
	"gitee.com/zhReimu/steam-go/application/content/depot"
	"gitee.com/zhReimu/steam-go/infrastructure/protocol/protobuf"

	"github.com/Bink-lab/Shino/internal/lua"
	"github.com/Bink-lab/Shino/internal/steam"
)

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

func DownloadDepot(ctx context.Context, sess *steam.Session, hosts []string, opt Options) (warnings []string, retErr error) {
	if len(hosts) == 0 {
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
	var tokMu sync.Mutex
	tokenFor := func(host string) string {
		tokMu.Lock()
		defer tokMu.Unlock()
		if t, ok := tokens[host]; ok {
			return t
		}
		t, err := sess.CDNToken(ctx, opt.AppID, opt.Depot.ID, host)
		if err != nil {
			tokenFailed = append(tokenFailed, host)
			t = ""
		}
		tokens[host] = t
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
		path := filepath.Join(opt.OutputDir, name)
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
		fh, err := os.OpenFile(j.path, os.O_CREATE|os.O_RDWR, 0o644)
		if err != nil {
			return warnings, err
		}
		if err := fh.Truncate(int64(j.mapping.GetSize())); err != nil {
			fh.Close()
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
				data, err := downloadChunk(ctx, key, hosts, tokenFor, opt.Depot.ID, ch)
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
		fh.Close()
		if firstErr != nil {
			return warnings, firstErr
		}
	}
	opt.logf("depot %d: complete", opt.Depot.ID)
	return warnings, nil
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

func firstHost[T any](ctx context.Context, hosts []string, fn func(string) (T, error)) (T, error) {
	var zero T
	var err error
	for _, host := range hosts {
		if ctx.Err() != nil {
			return zero, ctx.Err()
		}
		var v T
		if v, err = fn(host); err == nil {
			return v, nil
		}
	}
	return zero, err
}

func downloadChunk(ctx context.Context, key []byte, hosts []string, tokenFor func(string) string, depotID uint32, ch *protobuf.ContentManifestPayload_FileMapping_ChunkData) ([]byte, error) {
	client := cdn.NewCDNClient(key)
	client.ChunkRetry = 2
	return firstHost(ctx, hosts, func(host string) ([]byte, error) {
		return client.DownloadChunkContext(ctx, host, depotID, tokenFor(host), ch)
	})
}

func decryptIfNeeded(m *depot.DepotManifest, key []byte) error {
	if m.FilenamesEncrypted() {
		return m.DecryptFilenames(key)
	}
	return nil
}
