package download

import (
	"context"
	"encoding/hex"
	"fmt"
	"path/filepath"
	"runtime"
	"strings"
	"time"

	"github.com/Bink-lab/Shino/internal/lua"
	"github.com/Bink-lab/Shino/internal/manifest"
	"github.com/Bink-lab/Shino/internal/steam"
)

func CurrentOS() string {
	switch runtime.GOOS {
	case "windows":
		return "windows"
	case "darwin":
		return "macos"
	default:
		return "linux"
	}
}

func OSLabel(raw string) string {
	list := lua.SplitOSList(raw)
	if len(list) == 0 {
		return "shared"
	}
	short := map[string]string{"windows": "win", "macos": "mac", "linux": "linux"}
	parts := make([]string, len(list))
	for i, os := range list {
		parts[i] = short[os]
	}
	return strings.Join(parts, "+")
}

func MatchesCurrentOS(raw, current string) bool {
	if current == "" {
		current = CurrentOS()
	}
	list := lua.SplitOSList(raw)
	if len(list) == 0 {
		return true
	}
	for _, os := range list {
		if os == current {
			return true
		}
	}
	return false
}

func GuessOSFromFilenames(names []string) string {
	var win, mac, linux int
	for _, n := range names {
		low := strings.ToLower(filepath.ToSlash(n))
		base := filepath.Base(low)
		ext := filepath.Ext(base)
		switch ext {
		case ".exe", ".dll", ".bat", ".msi", ".sys":
			win++
		case ".dylib":
			mac++
		case ".so", ".ko":
			linux++
		}
		if strings.Contains(low, ".app/") || strings.HasSuffix(low, ".app") || strings.Contains(low, ".bundle/") || strings.Contains(low, "/macos/") || strings.Contains(low, "macos") || strings.Contains(low, "/mac/") {
			mac++
		}
		if strings.Contains(low, "windows") || strings.Contains(low, "win64") || strings.Contains(low, "win32") {
			win++
		}
		if strings.Contains(low, "linux") {
			linux++
		}
		if strings.HasSuffix(low, ".x86_64") || strings.HasSuffix(low, ".x86") {
			linux++
		}
	}
	hasWin := win > 0
	hasMac := mac > 0
	hasLinux := linux > 0
	count := 0
	if hasWin {
		count++
	}
	if hasMac {
		count++
	}
	if hasLinux {
		count++
	}
	if count == 0 {
		return ""
	}
	if count > 1 {
		var parts []string
		if hasWin {
			parts = append(parts, "windows")
		}
		if hasMac {
			parts = append(parts, "macos")
		}
		if hasLinux {
			parts = append(parts, "linux")
		}
		return strings.Join(parts, ",")
	}
	switch {
	case hasWin:
		return "windows"
	case hasMac:
		return "macos"
	default:
		return "linux"
	}
}

func InferOSFromManifest(manifestsDir string, dep lua.Depot) string {
	if manifestsDir == "" || !dep.HasManifest {
		return ""
	}
	path := filepath.Join(manifestsDir, manifestFileName(dep.ID, dep.ManifestID))
	m, err := manifest.LoadFile(path)
	if err != nil {
		return ""
	}
	if m.FilenamesEncrypted() {
		if dep.Key == "" {
			return ""
		}
		key, err := hex.DecodeString(dep.Key)
		if err != nil {
			return ""
		}
		if err := m.DecryptFilenames(key); err != nil {
			return ""
		}
	}
	names := make([]string, 0, len(m.Payload.GetMappings()))
	for _, f := range m.Payload.GetMappings() {
		name := f.GetFilename()
		if name == "" || f.GetLinktarget() != "" {
			continue
		}
		names = append(names, name)
		if len(names) >= 2000 {
			break
		}
	}
	return GuessOSFromFilenames(names)
}

func manifestFileName(depotID uint32, gid uint64) string {
	return fmt.Sprintf("%d_%d.manifest", depotID, gid)
}

func ResolveDepotOS(ctx context.Context, appID uint32, depots []lua.Depot, manifestsDir string, known map[uint32]string) map[uint32]string {
	out := make(map[uint32]string, len(depots))
	for _, d := range depots {
		if d.OS != "" {
			out[d.ID] = d.OS
			continue
		}
		if os := known[d.ID]; os != "" {
			out[d.ID] = os
		}
	}
	needRemote := false
	for _, d := range depots {
		if _, ok := out[d.ID]; !ok {
			needRemote = true
			break
		}
	}
	if needRemote {
		rctx, cancel := context.WithTimeout(ctx, 50*time.Second)
		sess, err := steam.ConnectAnonymous(rctx)
		if err == nil {
			remote, ferr := sess.FetchDepotOS(rctx, appID)
			sess.Close()
			if ferr == nil {
				for id, os := range remote {
					if _, ok := out[id]; !ok && os != "" {
						out[id] = os
					}
				}
			}
		}
		cancel()
	}
	for _, d := range depots {
		if _, ok := out[d.ID]; ok {
			continue
		}
		if guessed := InferOSFromManifest(manifestsDir, d); guessed != "" {
			out[d.ID] = guessed
		}
	}
	return out
}
