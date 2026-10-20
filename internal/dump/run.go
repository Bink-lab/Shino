package dump

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"gitee.com/zhReimu/steam-go/application/apps"
	"gitee.com/zhReimu/steam-go/domain/keyvalues"
	"gitee.com/zhReimu/steam-go/infrastructure/protocol/steamlang"

	"github.com/Bink-lab/Shino/internal/library"
)

type Options struct {
	OutputDir string
	Filter    []uint32
	OnLog     func(string)
}

type Summary struct {
	Apps             int
	Depots           int
	Manifests        int
	Reused           int
	SkippedNoLicense int
	FailedTransient  int
	FreeSkipped      int
	OutputDir        string
	Archive          string
}

func (o Options) logf(format string, args ...any) {
	if o.OnLog != nil {
		o.OnLog(fmt.Sprintf(format, args...))
	}
}

type keptApp struct {
	id     uint32
	kv     *keyvalues.KeyValues
	extra  []ownedDepot
	dlcIDs []uint32
}

type ownedDepot struct {
	depot DepotEntry
	appID uint32
}

func attachDLCApps(
	kept []keptApp,
	infos map[uint32]*apps.PICSProductInfo,
	store map[uint32]library.StoreApp,
) (int, int) {
	depots, games := 0, 0
	for i := range kept {
		attached := false
		dlcs := store[kept[i].id].DLCs
		if info := infos[kept[i].id]; info != nil {
			dlcs = mergeIDs(dlcs, AppDLCs(info.KeyValues))
		}
		for _, dlcID := range dlcs {
			if dlcID == kept[i].id {
				continue
			}
			info, ok := infos[dlcID]
			if !ok || info == nil {
				continue
			}
			if appType := AppType(info.KeyValues); appType != "" && !strings.EqualFold(appType, "dlc") {
				continue
			}
			extra := AppDepots(info.KeyValues)
			if len(extra) == 0 {
				continue
			}
			for _, depot := range extra {
				kept[i].extra = append(kept[i].extra, ownedDepot{depot: depot, appID: dlcID})
			}
			kept[i].dlcIDs = append(kept[i].dlcIDs, dlcID)
			depots += len(extra)
			attached = true
		}
		if attached {
			games++
		}
	}
	return depots, games
}

func appCandidates(app keptApp, free bool) []ownedDepot {
	candidates := make([]ownedDepot, 0, len(app.extra)+1)
	if !free {
		for _, depot := range AppDepots(app.kv) {
			candidates = append(candidates, ownedDepot{depot: depot, appID: app.id})
		}
	}
	return append(candidates, app.extra...)
}

func RunWithClient(ctx context.Context, client *Client, opts Options) (Summary, error) {
	var summary Summary
	opts.logf("logged in as %s", client.SteamID())
	cache, cacheErr := newManifestCache(filepath.Join(cacheRoot(), "dump"), client.SteamID())
	if cacheErr != nil {
		opts.logf("manifest cache unavailable: %v", cacheErr)
		cache = nil
	}
	if opts.OutputDir == "" {
		opts.OutputDir = "shino-donation"
	}
	abs, err := filepath.Abs(opts.OutputDir)
	if err != nil {
		return summary, err
	}
	opts.OutputDir = abs
	if err := os.MkdirAll(opts.OutputDir, 0o755); err != nil {
		return summary, err
	}
	if logFile, err := os.OpenFile(filepath.Join(opts.OutputDir, "run.log"), os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0o644); err == nil {
		defer logFile.Close()
		base := opts.OnLog
		var fileMu sync.Mutex
		opts.OnLog = func(line string) {
			fileMu.Lock()
			fmt.Fprintln(logFile, time.Now().Format("15:04:05")+"  "+line)
			fileMu.Unlock()
			if base != nil {
				base(line)
			}
		}
	}
	opts.logf("output in %s", opts.OutputDir)
	client.WaitLicenses(ctx, 30*time.Second)
	opts.logf("%d licenses on account", len(client.Licenses()))
	ownedContent, err := client.OwnedContent(ctx, opts.logf)
	if err != nil {
		return summary, err
	}
	ownedAppIDs := make(map[uint32]bool, len(ownedContent.Apps))
	for _, id := range ownedContent.Apps {
		ownedAppIDs[id] = true
	}
	filterDepots := ownedContent.Complete && len(ownedContent.Depots) > 0
	owned := FilterAppIDs(ownedContent.Apps, opts.Filter)
	opts.logf("%d app entries owned (games, dlc and tools)", len(owned))
	if len(owned) == 0 {
		return summary, fmt.Errorf("no games found")
	}
	tokens := client.AppAccessTokens(ctx, owned)
	reqs := make([]apps.PICSRequest, 0, len(owned))
	for _, id := range owned {
		reqs = append(reqs, apps.NewPICSRequestWithToken(id, tokens[id]))
	}
	opts.logf("resolving product info for %d apps", len(owned))
	infos, err := client.fetchApps(ctx, reqs)
	if err != nil {
		opts.logf("product info batch failed: %v", err)
	}
	opts.logf("product info for %d of %d apps", len(infos), len(owned))
	var kept []keptApp
	skipped := map[string]int{}
	skippedTotal := 0
	for _, appID := range owned {
		info, ok := infos[appID]
		if !ok || info == nil {
			opts.logf("app %d: product info missing", appID)
			continue
		}
		kv := info.KeyValues
		if appType := AppType(kv); appType != "" && !KeepAppType(appType) {
			name := strings.ToLower(appType)
			skipped[name]++
			skippedTotal++
			continue
		}
		kept = append(kept, keptApp{id: appID, kv: kv})
	}
	if len(kept) == 0 {
		return summary, fmt.Errorf("no games found")
	}
	if skippedTotal > 0 {
		names := make([]string, 0, len(skipped))
		for name, count := range skipped {
			names = append(names, fmt.Sprintf("%s=%d", name, count))
		}
		sort.Strings(names)
		opts.logf("skipping %d non-game apps (%s)", skippedTotal, strings.Join(names, ", "))
	}
	keptIDs := make([]uint32, 0, len(kept))
	for _, app := range kept {
		keptIDs = append(keptIDs, app.id)
	}
	store := library.NewClient(library.ClientConfig{}).FetchStoreMap(ctx, keptIDs)
	free := map[uint32]bool{}
	for id, info := range store {
		if info.Known && info.Free {
			free[id] = true
		}
	}
	unknown := 0
	for _, id := range keptIDs {
		if _, ok := store[id]; !ok {
			unknown++
		}
	}
	if unknown > 0 {
		opts.logf("store lookup missed %d apps, free status unknown", unknown)
	}
	if attachedDepots, attachedGames := attachDLCApps(kept, infos, store); attachedDepots > 0 {
		opts.logf("attached %d dlc depots to %d games", attachedDepots, attachedGames)
	}
	file := File{Version: 1, Generator: "shino-dump", SteamID: client.SteamID()}
	manifests := map[string][]byte{}
	freeSkipped := 0
	for _, app := range kept {
		if free[app.id] && len(app.extra) == 0 {
			freeSkipped++
		}
	}
	var mu sync.Mutex
	var done atomic.Uint64
	var skippedNoLicense atomic.Uint64
	var failedTransient atomic.Uint64
	var reused atomic.Uint64
	var wg sync.WaitGroup
	sem := make(chan struct{}, 4)
	for _, app := range kept {
		wg.Add(1)
		go func(app keptApp) {
			defer wg.Done()
			defer func() {
				opts.logf("app %d done (%d/%d)", app.id, done.Add(1), len(kept))
			}()
			candidates := appCandidates(app, free[app.id])
			if len(candidates) == 0 {
				if !free[app.id] {
					opts.logf("app %d: no depots listed", app.id)
				}
				return
			}
			select {
			case <-ctx.Done():
				return
			case sem <- struct{}{}:
			}
			defer func() { <-sem }()
			if ctx.Err() != nil {
				return
			}
			entry := AppEntry{ID: app.id, Name: AppName(app.kv), DLCAppIDs: app.dlcIDs}
			local := map[string][]byte{}
			seenDepots := map[uint32]bool{}
			for _, cand := range candidates {
				if ctx.Err() != nil {
					return
				}
				if seenDepots[cand.depot.ID] {
					continue
				}
				seenDepots[cand.depot.ID] = true
				if filterDepots && !ownedContent.Depots[cand.depot.ID] && !ownedAppIDs[cand.depot.ID] {
					skippedNoLicense.Add(1)
					continue
				}
				key, result, err := client.DepotKey(ctx, cand.depot.ID, cand.appID)
				if err != nil {
					if ctx.Err() != nil {
						return
					}
					failedTransient.Add(1)
					opts.logf("app %d depot %d: key request failed (%v)", app.id, cand.depot.ID, err)
					continue
				}
				if result != steamlang.EResult_OK {
					if result == steamlang.EResult_AccessDenied {
						skippedNoLicense.Add(1)
					} else {
						failedTransient.Add(1)
						opts.logf("app %d depot %d: key failed (%v)", app.id, cand.depot.ID, result)
					}
					continue
				}
				cand.depot.Key = key
				gid, hasManifest := PublicManifest(cand.depot)
				if !hasManifest {
					continue
				}
				var data []byte
				if cache != nil {
					cached, ok := cache.load(cand.depot.ID, gid)
					if ok {
						data = cached
						reused.Add(1)
					}
				}
				if data == nil {
					data, err = client.FetchManifest(ctx, cand.appID, cand.depot.ID, gid)
					if err != nil {
						if ctx.Err() != nil {
							return
						}
						if IsAccessDenied(err) {
							skippedNoLicense.Add(1)
						} else {
							failedTransient.Add(1)
							opts.logf("app %d depot %d: manifest failed: %v", app.id, cand.depot.ID, err)
						}
						continue
					}
					if cache != nil {
						if err := cache.store(cand.depot.ID, gid, data); err != nil {
							opts.logf("manifest cache write failed: %v", err)
						}
					}
				}
				local[manifestKey(cand.depot.ID, gid)] = data
				entry.Depots = append(entry.Depots, cand.depot)
			}
			mu.Lock()
			if len(entry.Depots) > 0 {
				file.Apps = append(file.Apps, entry)
			}
			for k, v := range local {
				manifests[k] = v
			}
			mu.Unlock()
		}(app)
	}
	wg.Wait()
	if ctx.Err() != nil {
		return summary, ctx.Err()
	}
	sort.Slice(file.Apps, func(i, j int) bool { return file.Apps[i].ID < file.Apps[j].ID })
	if len(file.Apps) == 0 {
		return summary, fmt.Errorf("no depot keys retrieved")
	}
	written, err := WriteOutput(opts.OutputDir, file, manifests)
	if err != nil {
		return summary, err
	}
	for _, app := range file.Apps {
		summary.Depots += len(app.Depots)
	}
	for _, w := range written {
		summary.Manifests += len(w.Manifests)
	}
	summary.Apps = len(written)
	summary.OutputDir = opts.OutputDir
	summary.Archive = filepath.Join(opts.OutputDir, ArchiveName)
	summary.Reused = int(reused.Load())
	summary.FreeSkipped = freeSkipped
	summary.SkippedNoLicense = int(skippedNoLicense.Load())
	summary.FailedTransient = int(failedTransient.Load())
	if summary.Reused > 0 {
		opts.logf("reused %d manifests from cache", summary.Reused)
	}
	if freeSkipped > 0 {
		opts.logf("wrote %d apps, %d depots, %d manifests, skipped %d free", summary.Apps, summary.Depots, summary.Manifests, freeSkipped)
	} else {
		opts.logf("wrote %d apps, %d depots, %d manifests", summary.Apps, summary.Depots, summary.Manifests)
	}
	if skippedNoLicense.Load() > 0 {
		opts.logf("skipped %d depots your account has no license for", skippedNoLicense.Load())
	}
	if failedTransient.Load() > 0 {
		opts.logf("%d depot requests failed transiently (rate limited or network), rerun to retry them", failedTransient.Load())
	}
	opts.logf("packed upload archive %s", summary.Archive)
	return summary, nil
}
