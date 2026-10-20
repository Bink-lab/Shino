package library

import (
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"strings"
)

const (
	RyuuKeyFile   = "ryuu-auth-code"
	HubcapKeyFile = "hubcap-api-key"
	ShinoKeyFile  = "shino-api-key"
)

type KeyStore struct {
	path string
}

func NewKeyStore(fileName string) (KeyStore, error) {
	dir, err := ConfigDir()
	if err != nil {
		return KeyStore{}, err
	}
	return KeyStore{path: filepath.Join(dir, fileName)}, nil
}

func ConfigDir() (string, error) {
	if dir, err := os.UserConfigDir(); err == nil && dir != "" {
		return filepath.Join(dir, "Shino"), nil
	}
	if home, err := os.UserHomeDir(); err == nil && home != "" {
		if runtime.GOOS == "windows" {
			return filepath.Join(home, "AppData", "Roaming", "Shino"), nil
		}
		return filepath.Join(home, ".config", "Shino"), nil
	}
	if exe, err := os.Executable(); err == nil && exe != "" {
		if dir := filepath.Dir(exe); dir != "" {
			return filepath.Join(dir, ".shino"), nil
		}
	}
	return "", errors.New("config directory unavailable")
}

func (s KeyStore) Load() (string, error) {
	if s.path == "" {
		return "", nil
	}
	data, err := os.ReadFile(s.path)
	if errors.Is(err, os.ErrNotExist) {
		return "", nil
	}
	if err != nil {
		return "", err
	}
	return strings.TrimSpace(string(data)), nil
}

func (s KeyStore) Save(secret string) error {
	secret = strings.TrimSpace(secret)
	if secret == "" {
		return errors.New("credential is empty")
	}
	if s.path == "" {
		return errors.New("config directory unavailable")
	}
	dir := filepath.Dir(s.path)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return err
	}
	tmp, err := os.CreateTemp(dir, filepath.Base(s.path)+"-*")
	if err != nil {
		return err
	}
	tmpName := tmp.Name()
	_ = tmp.Chmod(0o600)
	if _, err := tmp.WriteString(secret); err != nil {
		tmp.Close()
		os.Remove(tmpName)
		return err
	}
	_ = tmp.Sync()
	if err := tmp.Close(); err != nil {
		os.Remove(tmpName)
		return err
	}
	if err := os.Rename(tmpName, s.path); err != nil {
		_ = os.Chmod(s.path, 0o600)
		_ = os.Remove(s.path)
		if err := os.Rename(tmpName, s.path); err != nil {
			data, rerr := os.ReadFile(tmpName)
			os.Remove(tmpName)
			if rerr != nil {
				return err
			}
			if werr := os.WriteFile(s.path, data, 0o600); werr != nil {
				return err
			}
		}
	}
	return nil
}
