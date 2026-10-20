package manifest

import (
	"os"

	"gitee.com/zhReimu/steam-go/application/content/depot"
)

func LoadFile(path string) (*depot.DepotManifest, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	return depot.Deserialize(data)
}
