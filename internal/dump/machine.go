package dump

import (
	"crypto/rand"
	"encoding/hex"
	"os"
	"path/filepath"
	"runtime"
	"runtime/debug"
	"strings"

	"gitee.com/zhReimu/steam-go/infrastructure/protocol/steamlang"
	ifclient "gitee.com/zhReimu/steam-go/interface/client"

	"github.com/Bink-lab/Shino/internal/library"
)

func BuildTag() string {
	if buildTagOverride != "" {
		return buildTagOverride
	}
	info, ok := debug.ReadBuildInfo()
	if !ok {
		return "dev"
	}
	for _, setting := range info.Settings {
		if setting.Key == "vcs.revision" && len(setting.Value) >= 7 {
			return setting.Value[:7]
		}
	}
	return "dev"
}

var buildTagOverride string

func osTypeForGOOS(goos string) steamlang.EOSType {
	switch goos {
	case "windows":
		return steamlang.EOSType_Win10
	case "darwin":
		return steamlang.EOSType_MacOSUnknown
	default:
		return steamlang.EOSType_LinuxUnknown
	}
}

func machineIDFile() string {
	dir, err := library.ConfigDir()
	if err != nil {
		return ""
	}
	return filepath.Join(dir, "machineid")
}

func loadMachineSeed() string {
	path := machineIDFile()
	if path == "" {
		return "shino-fallback-seed"
	}
	if data, err := os.ReadFile(path); err == nil {
		if seed := strings.TrimSpace(string(data)); seed != "" {
			return seed
		}
	}
	seed := make([]byte, 16)
	if _, err := rand.Read(seed); err != nil {
		return "shino-fallback-seed"
	}
	hexSeed := hex.EncodeToString(seed)
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err == nil {
		_ = os.WriteFile(path, []byte(hexSeed+"\n"), 0o600)
	}
	return hexSeed
}

func machineID() []byte {
	return ifclient.GenerateMachineIDFromSeed(loadMachineSeed())
}

func clientOSType() steamlang.EOSType {
	return osTypeForGOOS(runtime.GOOS)
}
