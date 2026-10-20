package lua

import (
	"strings"
	"testing"
)

func TestParseSetOSList(t *testing.T) {
	cfg, err := Parse(strings.NewReader("addappid(100)\naddappid(200, 0, \"" + strings.Repeat("ab", 32) + "\")\nsetManifestid(200, \"1234567890123456789\")\nsetoslist(200, \"win, osx\")\n"))
	if err != nil {
		t.Fatal(err)
	}
	if cfg.AppID != 100 {
		t.Fatalf("app id = %d, want 100", cfg.AppID)
	}
	if len(cfg.Depots) != 1 {
		t.Fatalf("depots = %d, want 1", len(cfg.Depots))
	}
	if cfg.Depots[0].ID != 200 || cfg.Depots[0].OS != "win, osx" {
		t.Fatalf("depot = %+v", cfg.Depots[0])
	}
}

func TestSplitOSList(t *testing.T) {
	cases := []struct {
		raw  string
		want string
	}{
		{"", ""},
		{"windows", "windows"},
		{"Windows,OSX", "windows,macos"},
		{"win64 linux32 + macos", "windows,linux,macos"},
		{"windows,win,unknown", "windows"},
		{"steamos", ""},
	}
	for _, tc := range cases {
		got := strings.Join(SplitOSList(tc.raw), ",")
		if got != tc.want {
			t.Fatalf("SplitOSList(%q) = %q, want %q", tc.raw, got, tc.want)
		}
	}
	if got := NormalizeOSList("Windows, OSX"); got != "windows,macos" {
		t.Fatalf("NormalizeOSList = %q", got)
	}
}
