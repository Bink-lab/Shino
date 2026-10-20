package dump

import (
	"os"
	"path/filepath"
	"strconv"
)

func cacheRoot() string {
	if dir, err := os.UserCacheDir(); err == nil && dir != "" {
		return filepath.Join(dir, "Shino")
	}
	if home, err := os.UserHomeDir(); err == nil && home != "" {
		return filepath.Join(home, ".cache", "Shino")
	}
	return "shino-cache"
}

type manifestCache struct {
	root    string
	steamID string
}

func newManifestCache(root string, steamID string) (*manifestCache, error) {
	c := &manifestCache{root: root, steamID: steamID}
	accountPath := filepath.Join(root, "account.txt")
	raw, err := os.ReadFile(accountPath)
	switch {
	case err == nil && string(raw) != steamID:
		if err := os.RemoveAll(filepath.Join(root, "manifests")); err != nil {
			return nil, err
		}
	case os.IsNotExist(err):
	default:
		if err != nil {
			return nil, err
		}
	}
	if err := os.MkdirAll(filepath.Join(root, "manifests"), 0o755); err != nil {
		return nil, err
	}
	if err := os.WriteFile(accountPath, []byte(steamID), 0o644); err != nil {
		return nil, err
	}
	return c, nil
}

func cachePath(root string, depotID uint32, gid uint64) string {
	return filepath.Join(root, "manifests", strconv.FormatUint(uint64(depotID), 10)+"_"+strconv.FormatUint(gid, 10))
}

func (c *manifestCache) load(depotID uint32, gid uint64) ([]byte, bool) {
	data, err := os.ReadFile(cachePath(c.root, depotID, gid))
	if err != nil || len(data) == 0 {
		return nil, false
	}
	return data, true
}

func (c *manifestCache) store(depotID uint32, gid uint64, data []byte) error {
	dir := filepath.Join(c.root, "manifests")
	tmp, err := os.CreateTemp(dir, "manifest-*")
	if err != nil {
		return err
	}
	tmpName := tmp.Name()
	if _, err := tmp.Write(data); err != nil {
		tmp.Close()
		os.Remove(tmpName)
		return err
	}
	if err := tmp.Close(); err != nil {
		os.Remove(tmpName)
		return err
	}
	if err := os.Rename(tmpName, cachePath(c.root, depotID, gid)); err != nil {
		os.Remove(tmpName)
		return err
	}
	return nil
}
