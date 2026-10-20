package dump

import (
	"archive/zip"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"
)

const (
	DepotsName  = "depots.json"
	ArchiveName = "shino-donation.zip"
)

func RenderLua(appID uint32, depots []DepotEntry) string {
	var b strings.Builder
	fmt.Fprintf(&b, "addappid(%d)\n", appID)
	sorted := append([]DepotEntry(nil), depots...)
	sort.Slice(sorted, func(i, j int) bool { return sorted[i].ID < sorted[j].ID })
	for _, d := range sorted {
		if d.Key == "" {
			continue
		}
		gid, ok := PublicManifest(d)
		if !ok {
			continue
		}
		fmt.Fprintf(&b, "addappid(%d, 0, \"%s\")\n", d.ID, strings.ToLower(d.Key))
		fmt.Fprintf(&b, "setManifestid(%d, \"%d\")\n", d.ID, gid)
		if d.OS != "" {
			fmt.Fprintf(&b, "setoslist(%d, \"%s\")\n", d.ID, d.OS)
		}
	}
	return b.String()
}

func ManifestFileName(depotID uint32, gid uint64) string {
	return fmt.Sprintf("%d_%d.manifest", depotID, gid)
}

func LuaFileName(appID uint32) string {
	return fmt.Sprintf("%d.lua", appID)
}

type WrittenApp struct {
	AppID     uint32
	LuaPath   string
	Manifests []string
}

func WriteOutput(root string, file File, manifests map[string][]byte) ([]WrittenApp, error) {
	if err := os.MkdirAll(root, 0o755); err != nil {
		return nil, err
	}
	luaDir := filepath.Join(root, "lua")
	manifestDir := filepath.Join(root, "manifests")
	for _, dir := range []string{luaDir, manifestDir} {
		if err := os.MkdirAll(dir, 0o755); err != nil {
			return nil, err
		}
	}
	if file.Timestamp == "" {
		file.Timestamp = time.Now().UTC().Format(time.RFC3339)
	}
	raw, err := json.MarshalIndent(file, "", "  ")
	if err != nil {
		return nil, err
	}
	if err := os.WriteFile(filepath.Join(root, DepotsName), raw, 0o644); err != nil {
		return nil, err
	}
	var written []WrittenApp
	for _, app := range file.Apps {
		luaText := RenderLua(app.ID, app.Depots)
		luaPath := filepath.Join(luaDir, LuaFileName(app.ID))
		if err := os.WriteFile(luaPath, []byte(luaText), 0o644); err != nil {
			return nil, err
		}
		entry := WrittenApp{AppID: app.ID, LuaPath: luaPath}
		for _, d := range app.Depots {
			gid, ok := PublicManifest(d)
			if !ok {
				continue
			}
			name := ManifestFileName(d.ID, gid)
			data := manifests[manifestKey(d.ID, gid)]
			if len(data) == 0 {
				continue
			}
			dest := filepath.Join(manifestDir, name)
			if err := os.WriteFile(dest, data, 0o644); err != nil {
				return nil, err
			}
			entry.Manifests = append(entry.Manifests, dest)
		}
		written = append(written, entry)
	}
	if err := writeArchive(root, raw, written); err != nil {
		return nil, err
	}
	return written, nil
}

func manifestKey(depotID uint32, gid uint64) string {
	return fmt.Sprintf("%d/%d", depotID, gid)
}

func ManifestMapKey(depotID uint32, gid uint64) string {
	return manifestKey(depotID, gid)
}

func writeArchive(root string, depots []byte, apps []WrittenApp) error {
	out, err := os.Create(filepath.Join(root, ArchiveName))
	if err != nil {
		return err
	}
	w := zip.NewWriter(out)
	fail := func(err error) error {
		w.Close()
		out.Close()
		return err
	}
	if err := writeZipBytes(w, DepotsName, depots); err != nil {
		return fail(err)
	}
	for _, app := range apps {
		lua, err := os.ReadFile(app.LuaPath)
		if err != nil {
			return fail(err)
		}
		if err := writeZipBytes(w, "lua/"+filepath.Base(app.LuaPath), lua); err != nil {
			return fail(err)
		}
	}
	for _, app := range apps {
		for _, path := range app.Manifests {
			if err := writeZipFile(w, "manifests/"+filepath.Base(path), path); err != nil {
				return fail(err)
			}
		}
	}
	if err := w.Close(); err != nil {
		out.Close()
		return err
	}
	return out.Close()
}

func writeZipBytes(w *zip.Writer, name string, data []byte) error {
	entry, err := w.Create(name)
	if err != nil {
		return err
	}
	_, err = entry.Write(data)
	return err
}

func writeZipFile(w *zip.Writer, name, path string) error {
	src, err := os.Open(path)
	if err != nil {
		return err
	}
	defer src.Close()
	entry, err := w.Create(name)
	if err != nil {
		return err
	}
	_, err = io.Copy(entry, src)
	return err
}
