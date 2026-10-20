package download

import (
	"context"
	"testing"

	"github.com/Bink-lab/Shino/internal/lua"
)

func TestGuessOSFromFilenames(t *testing.T) {
	cases := []struct {
		name  string
		files []string
		want  string
	}{
		{"windows", []string{"game.exe", "steam_api.dll"}, "windows"},
		{"linux", []string{"game.x86_64", "libsteam_api.so"}, "linux"},
		{"macos", []string{"Game.app/Contents/MacOS/Game"}, "macos"},
		{"shared", []string{"data.bin", "texture.pak"}, ""},
	}
	for _, tc := range cases {
		if got := GuessOSFromFilenames(tc.files); got != tc.want {
			t.Fatalf("%s: OS = %q, want %q", tc.name, got, tc.want)
		}
	}
}

func TestMatchesCurrentOS(t *testing.T) {
	cases := []struct {
		raw     string
		current string
		want    bool
	}{
		{"", "linux", true},
		{"windows,macos", "linux", false},
		{"win,osx", "macos", true},
		{"steamos", "windows", true},
	}
	for _, tc := range cases {
		if got := MatchesCurrentOS(tc.raw, tc.current); got != tc.want {
			t.Fatalf("MatchesCurrentOS(%q, %q) = %t, want %t", tc.raw, tc.current, got, tc.want)
		}
	}
}

func TestResolveDepotOSUsesKnown(t *testing.T) {
	depots := []lua.Depot{{ID: 10}, {ID: 11}}
	known := map[uint32]string{10: "windows", 11: "linux"}
	got := ResolveDepotOS(context.Background(), 100, depots, "", known)
	if got[10] != "windows" || got[11] != "linux" {
		t.Fatalf("resolved = %v", got)
	}
}
