package steam

import (
	"bytes"
	"compress/gzip"
	"context"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	steam "gitee.com/zhReimu/steam-go"
	"gitee.com/zhReimu/steam-go/application/apps"
	"gitee.com/zhReimu/steam-go/domain/keyvalues"

	"github.com/Bink-lab/Shino/internal/lua"
)

func fetchDepotOSLists(ctx context.Context, c *steam.CompositeClient, appID uint32) (map[uint32]string, error) {
	tctx, cancel := context.WithTimeout(ctx, 45*time.Second)
	defer cancel()
	job := c.Apps.PICSGetProductInfo([]apps.PICSRequest{apps.NewPICSRequest(appID)}, nil, false)
	raw, err := c.AwaitJob(tctx, job)
	if err != nil {
		return nil, err
	}
	ev, ok := raw.(*apps.PICSProductInfoResponseEvent)
	if !ok {
		return nil, fmt.Errorf("unexpected response %T", raw)
	}
	for range 20 {
		if !ev.ResponsePending {
			break
		}
		raw, err = c.AwaitJob(tctx, job)
		if err != nil {
			break
		}
		if next, ok := raw.(*apps.PICSProductInfoResponseEvent); ok {
			for id, info := range next.ParsedApps {
				if ev.ParsedApps == nil {
					ev.ParsedApps = map[uint32]*apps.PICSProductInfo{}
				}
				ev.ParsedApps[id] = info
			}
			ev.ResponsePending = next.ResponsePending
			if len(next.HTTPURLs) > 0 {
				if ev.HTTPURLs == nil {
					ev.HTTPURLs = map[uint32]string{}
				}
				for id, u := range next.HTTPURLs {
					ev.HTTPURLs[id] = u
				}
			}
			ev.UseHTTP = ev.UseHTTP || next.UseHTTP
		}
	}
	info := ev.ParsedApps[appID]
	if info == nil {
		if u := ev.HTTPURLs[appID]; u != "" {
			kv, ferr := fetchAppInfoHTTP(tctx, u)
			if ferr != nil {
				return nil, ferr
			}
			return depotOSFromKV(kv), nil
		}
		return nil, fmt.Errorf("no product info for app %d", appID)
	}
	kv := info.KeyValues
	if kv == nil && info.UseHTTP && info.HTTPURL != "" {
		fetched, ferr := fetchAppInfoHTTP(tctx, info.HTTPURL)
		if ferr != nil {
			return nil, ferr
		}
		kv = fetched
	}
	if kv == nil {
		return nil, fmt.Errorf("no appinfo for app %d", appID)
	}
	return depotOSFromKV(kv), nil
}

func fetchAppInfoHTTP(ctx context.Context, url string) (*keyvalues.KeyValues, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return nil, err
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return nil, fmt.Errorf("appinfo request failed with status %s", resp.Status)
	}
	raw, err := io.ReadAll(io.LimitReader(resp.Body, 32<<20+1))
	if err != nil {
		return nil, err
	}
	gz, err := gzip.NewReader(bytes.NewReader(raw))
	if err != nil {
		return nil, err
	}
	defer gz.Close()
	text, err := io.ReadAll(io.LimitReader(gz, 64<<20+1))
	if err != nil {
		return nil, err
	}
	return keyvalues.ParseText(text)
}

func depotOSFromKV(kv *keyvalues.KeyValues) map[uint32]string {
	out := map[uint32]string{}
	root := kv
	if root.GetChild("common") == nil && root.GetChild("depots") == nil {
		if appinfo := root.GetChild("appinfo"); appinfo != nil {
			root = appinfo
		}
	}
	depots := root.GetChild("depots")
	if depots == nil {
		return out
	}
	for name, kids := range depots.Children {
		var id uint32
		if _, err := fmt.Sscanf(strings.TrimSpace(name), "%d", &id); err != nil || id == 0 || len(kids) == 0 {
			continue
		}
		for _, node := range kids {
			cfg := node.GetChild("config")
			if cfg == nil {
				continue
			}
			raw := strings.TrimSpace(cfg.GetString("oslist", ""))
			if raw == "" {
				continue
			}
			out[id] = lua.NormalizeOSList(raw)
			break
		}
	}
	return out
}
