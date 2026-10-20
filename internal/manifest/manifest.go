package manifest

import (
	"fmt"
	"os"

	"gitee.com/zhReimu/steam-go/application/content/depot"
)

func FileName(depotID uint32, gid uint64) string {
	return fmt.Sprintf("%d_%d.manifest", depotID, gid)
}

func LoadFile(path string) (*depot.DepotManifest, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	return depot.Deserialize(data)
}
