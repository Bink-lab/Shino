package dump

import (
	"strings"
	"testing"

	"gitee.com/zhReimu/steam-go/domain/keyvalues"

	"github.com/Bink-lab/Shino/internal/lua"
)

func TestRenderLuaRoundTrip(t *testing.T) {
	key := strings.Repeat("ab", 32)
	text := RenderLua(100, []DepotEntry{{ID: 101, Key: key, Manifests: map[string]string{"public": "1234567890123456789"}, OS: "windows,macos"}})
	cfg, err := lua.Parse(strings.NewReader(text))
	if err != nil {
		t.Fatal(err)
	}
	if cfg.AppID != 100 {
		t.Fatalf("app id = %d, want 100", cfg.AppID)
	}
	if len(cfg.Depots) != 1 {
		t.Fatalf("depots = %d, want 1", len(cfg.Depots))
	}
	if cfg.Depots[0].ID != 101 || cfg.Depots[0].OS != "windows,macos" || cfg.Depots[0].Key != key {
		t.Fatalf("depot = %+v", cfg.Depots[0])
	}
}

func TestPackageDepotIDs(t *testing.T) {
	kv, err := keyvalues.ParseText([]byte(`"appinfo" { "appids" { "0" "10" "1" "11" } "depotids" { "0" "20" "1" "21" } }`))
	if err != nil {
		t.Fatal(err)
	}
	got := PackageDepotIDs(kv)
	if len(got) != 2 || got[0] != 20 || got[1] != 21 {
		t.Fatalf("depot ids = %v, want [20 21]", got)
	}
}
