package tui

import (
	"testing"

	"github.com/Bink-lab/Shino/internal/lua"
)

func TestApplyDepotOSKeepsTouchedSelection(t *testing.T) {
	m := Model{
		cfg:          &lua.Config{AppID: 1, Depots: []lua.Depot{{ID: 10}, {ID: 11}, {ID: 12}}},
		currentOS:    "windows",
		depotEnabled: map[uint32]bool{10: true, 11: true, 12: true},
		depotTouched: map[uint32]bool{},
		depotOS:      map[uint32]string{},
	}
	m.toggleDepot(10)
	m.applyDepotOS(map[uint32]string{10: "windows", 11: "macos", 12: "windows"})
	if m.depotEnabled[10] {
		t.Fatal("toggled depot 10 was re-enabled by OS resolution")
	}
	if m.depotEnabled[11] {
		t.Fatal("depot 11 is macOS and should be disabled on windows")
	}
	if !m.depotEnabled[12] {
		t.Fatal("untouched windows depot 12 should be enabled")
	}
}
