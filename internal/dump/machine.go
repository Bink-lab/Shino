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
	if dir, err := os.UserConfigDir(); err == nil && dir != "" {
		return filepath.Join(dir, "Shino", "machineid")
	}
	if home, err := os.UserHomeDir(); err == nil && home != "" {
		if runtime.GOOS == "windows" {
			return filepath.Join(home, "AppData", "Roaming", "Shino", "machineid")
		}
		return filepath.Join(home, ".config", "Shino", "machineid")
	}
	if exe, err := os.Executable(); err == nil && exe != "" {
		if dir := filepath.Dir(exe); dir != "" {
			return filepath.Join(dir, ".shino", "machineid")
		}
	}
	return ""
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
