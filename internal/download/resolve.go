package download

import (
	"fmt"
	"path/filepath"

	"gitee.com/zhReimu/steam-go/application/content/depot"

	"github.com/Bink-lab/Shino/internal/manifest"
)

func resolveManifest(dir string, depotID uint32, gid uint64) (*depot.DepotManifest, error) {
	if dir == "" {
		return nil, fmt.Errorf("depot %d: no manifests directory", depotID)
	}
	path := filepath.Join(dir, manifestFileName(depotID, gid))
	m, err := manifest.LoadFile(path)
	if err != nil {
		return nil, fmt.Errorf("depot %d: manifest %d: %w", depotID, gid, err)
	}
	return m, nil
}
