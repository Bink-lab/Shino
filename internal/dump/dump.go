package dump

import (
	"fmt"
	"sort"
	"strconv"
	"strings"

	"gitee.com/zhReimu/steam-go/domain/keyvalues"

	"github.com/Bink-lab/Shino/internal/lua"
)

type DepotEntry struct {
	ID        uint32            `json:"depot_id"`
	Key       string            `json:"key"`
	Manifests map[string]string `json:"manifests"`
	OS        string            `json:"oslist,omitempty"`
}

type AppEntry struct {
	ID        uint32       `json:"appid"`
	Name      string       `json:"name"`
	Depots    []DepotEntry `json:"depots"`
	DLCAppIDs []uint32     `json:"dlc_appids,omitempty"`
}

type File struct {
	Version   int        `json:"version"`
	Generator string     `json:"generator"`
	SteamID   string     `json:"steam_id"`
	Timestamp string     `json:"timestamp"`
	Apps      []AppEntry `json:"apps"`
}

func PackageAppIDs(kv *keyvalues.KeyValues) []uint32 {
	return packageIDs(kv, "appids")
}

func PackageDepotIDs(kv *keyvalues.KeyValues) []uint32 {
	return packageIDs(kv, "depotids")
}

func packageIDs(kv *keyvalues.KeyValues, key string) []uint32 {
	if kv == nil {
		return nil
	}
	node := kv.GetChild(key)
	if node == nil {
		for _, kids := range kv.Children {
			for _, k := range kids {
				if node = k.GetChild(key); node != nil {
					break
				}
			}
			if node != nil {
				break
			}
		}
	}
	if node == nil {
		return nil
	}
	seen := map[uint32]bool{}
	var out []uint32
	for _, kids := range node.Children {
		for _, k := range kids {
			id, err := strconv.ParseUint(strings.TrimSpace(k.Value), 10, 32)
			if err != nil || id == 0 {
				continue
			}
			if !seen[uint32(id)] {
				seen[uint32(id)] = true
				out = append(out, uint32(id))
			}
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i] < out[j] })
	return out
}

func appRoot(kv *keyvalues.KeyValues) *keyvalues.KeyValues {
	if kv == nil {
		return nil
	}
	if kv.GetChild("common") != nil || kv.GetChild("depots") != nil {
		return kv
	}
	if appinfo := kv.GetChild("appinfo"); appinfo != nil {
		return appinfo
	}
	return kv
}

func AppName(kv *keyvalues.KeyValues) string {
	common := appRoot(kv).GetChild("common")
	if common == nil {
		return ""
	}
	return strings.TrimSpace(common.GetString("name", ""))
}

func AppType(kv *keyvalues.KeyValues) string {
	common := appRoot(kv).GetChild("common")
	if common == nil {
		return ""
	}
	return strings.TrimSpace(common.GetString("type", ""))
}

func KeepAppType(appType string) bool {
	switch strings.ToLower(appType) {
	case "game", "music":
		return true
	}
	return false
}

func AppDepots(kv *keyvalues.KeyValues) []DepotEntry {
	depots := appRoot(kv).GetChild("depots")
	if depots == nil {
		return nil
	}
	var out []DepotEntry
	for name, kids := range depots.Children {
		id, err := strconv.ParseUint(strings.TrimSpace(name), 10, 32)
		if err != nil || id == 0 || len(kids) == 0 {
			continue
		}
		manifests := map[string]string{}
		oslist := ""
		for _, depotNode := range kids {
			mNode := depotNode.GetChild("manifests")
			if mNode != nil {
				for branch, bKids := range mNode.Children {
					branch = strings.TrimSpace(branch)
					if branch == "" || len(bKids) == 0 {
						continue
					}
					gidStr := strings.TrimSpace(bKids[0].GetString("gid", bKids[0].Value))
					if gidStr == "" {
						continue
					}
					gid, err := strconv.ParseUint(gidStr, 10, 64)
					if err != nil || gid == 0 || gid == ^uint64(0) {
						continue
					}
					manifests[branch] = gidStr
				}
			}
			if oslist == "" {
				oslist = DepotOSList(depotNode)
			}
		}
		if len(manifests) == 0 {
			continue
		}
		out = append(out, DepotEntry{ID: uint32(id), Manifests: manifests, OS: oslist})
	}
	return out
}

func DepotOSList(depotNode *keyvalues.KeyValues) string {
	if depotNode == nil {
		return ""
	}
	cfg := depotNode.GetChild("config")
	if cfg == nil {
		return ""
	}
	raw := strings.TrimSpace(cfg.GetString("oslist", ""))
	if raw == "" {
		raw = strings.TrimSpace(cfg.GetString("osList", ""))
	}
	if raw == "" {
		return ""
	}
	return lua.NormalizeOSList(raw)
}

func PublicManifest(d DepotEntry) (uint64, bool) {
	if gid, ok := parseManifestGID(d.Manifests["public"]); ok {
		return gid, true
	}
	keys := make([]string, 0, len(d.Manifests))
	for branch := range d.Manifests {
		keys = append(keys, branch)
	}
	sort.Strings(keys)
	for _, branch := range keys {
		if gid, ok := parseManifestGID(d.Manifests[branch]); ok {
			return gid, true
		}
	}
	return 0, false
}

func parseManifestGID(raw string) (uint64, bool) {
	gid, err := strconv.ParseUint(strings.TrimSpace(raw), 10, 64)
	if err != nil || gid == 0 || gid == ^uint64(0) {
		return 0, false
	}
	return gid, true
}

func ParseAppIDList(raw string) ([]uint32, error) {
	var out []uint32
	for _, part := range strings.Split(raw, ",") {
		part = strings.TrimSpace(part)
		if part == "" {
			continue
		}
		id, err := strconv.ParseUint(part, 10, 32)
		if err != nil || id == 0 {
			return nil, fmt.Errorf("invalid app id %q", part)
		}
		out = append(out, uint32(id))
	}
	return out, nil
}

func AppDLCs(kv *keyvalues.KeyValues) []uint32 {
	if kv == nil {
		return nil
	}
	extended := appRoot(kv).GetChild("extended")
	if extended == nil {
		return nil
	}
	raw := strings.TrimSpace(extended.GetString("listofdlc", ""))
	if raw == "" {
		return nil
	}
	var out []uint32
	seen := map[uint32]bool{}
	for _, part := range strings.Split(raw, ",") {
		id, err := strconv.ParseUint(strings.TrimSpace(part), 10, 32)
		if err != nil || id == 0 || seen[uint32(id)] {
			continue
		}
		seen[uint32(id)] = true
		out = append(out, uint32(id))
	}
	return out
}

func mergeIDs(lists ...[]uint32) []uint32 {
	seen := map[uint32]bool{}
	var out []uint32
	for _, list := range lists {
		for _, id := range list {
			if id == 0 || seen[id] {
				continue
			}
			seen[id] = true
			out = append(out, id)
		}
	}
	return out
}

func FilterAppIDs(owned []uint32, wanted []uint32) []uint32 {
	if len(wanted) == 0 {
		return owned
	}
	keep := map[uint32]bool{}
	for _, id := range wanted {
		keep[id] = true
	}
	var out []uint32
	for _, id := range owned {
		if keep[id] {
			out = append(out, id)
		}
	}
	return out
}
