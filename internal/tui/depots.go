package tui

import (
	"context"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/Bink-lab/Shino/internal/download"
	"github.com/Bink-lab/Shino/internal/lua"
)

type depotOSLoadedMsg struct {
	appID uint32
	os    map[uint32]string
}

func fetchDepotOSCmd(ctx context.Context, appID uint32, depots []lua.Depot, manifestsDir string, cached map[uint32]string) tea.Cmd {
	cp := make([]lua.Depot, len(depots))
	copy(cp, depots)
	known := make(map[uint32]string, len(cached))
	for id, os := range cached {
		known[id] = os
	}
	return func() tea.Msg {
		osMap := download.ResolveDepotOS(ctx, appID, cp, manifestsDir, known)
		return depotOSLoadedMsg{appID: appID, os: osMap}
	}
}

func initDepotSelection(m *Model) {
	if m.depotEnabled == nil {
		m.depotEnabled = map[uint32]bool{}
	}
	if m.depotOS == nil {
		m.depotOS = map[uint32]string{}
	}
	if m.currentOS == "" {
		m.currentOS = download.CurrentOS()
	}
	m.depotCursor = 0
	m.depotScroll = 0
	m.depotTouched = map[uint32]bool{}
	m.osLoading = true
	for _, d := range m.depots() {
		raw := d.OS
		if raw != "" {
			m.depotOS[d.ID] = raw
		} else {
			raw = m.depotOS[d.ID]
		}
		if raw != "" {
			m.depotEnabled[d.ID] = download.MatchesCurrentOS(raw, m.currentOS)
		} else {
			m.depotEnabled[d.ID] = true
		}
	}
}

func (m *Model) applyDepotOS(osMap map[uint32]string) {
	if m.depotOS == nil {
		m.depotOS = map[uint32]string{}
	}
	if m.depotEnabled == nil {
		m.depotEnabled = map[uint32]bool{}
	}
	for _, d := range m.depots() {
		raw := osMap[d.ID]
		if raw == "" {
			continue
		}
		m.depotOS[d.ID] = raw
		if !m.depotTouched[d.ID] {
			m.depotEnabled[d.ID] = download.MatchesCurrentOS(raw, m.currentOS)
		}
	}
	if m.osCancel != nil {
		m.osCancel()
		m.osCancel = nil
	}
	m.osLoading = false
}

func (m Model) selectedDepots() []lua.Depot {
	if m.cfg == nil {
		return nil
	}
	if len(m.depotEnabled) == 0 {
		return m.cfg.Depots
	}
	var out []lua.Depot
	for _, d := range m.cfg.Depots {
		if m.depotEnabled[d.ID] {
			out = append(out, d)
		}
	}
	return out
}

func (m Model) selectedCount() int {
	n := 0
	for _, d := range m.depots() {
		if m.depotEnabled[d.ID] {
			n++
		}
	}
	return n
}

func (m *Model) toggleDepot(id uint32) {
	if m.depotEnabled == nil {
		m.depotEnabled = map[uint32]bool{}
	}
	if m.depotTouched == nil {
		m.depotTouched = map[uint32]bool{}
	}
	m.depotEnabled[id] = !m.depotEnabled[id]
	m.depotTouched[id] = true
	m.errMsg = ""
}

func (m *Model) setAllDepots(enabled bool) {
	if m.depotTouched == nil {
		m.depotTouched = map[uint32]bool{}
	}
	for _, d := range m.depots() {
		m.depotEnabled[d.ID] = enabled
		m.depotTouched[d.ID] = true
	}
	m.errMsg = ""
}

func (m *Model) selectCurrentOS() {
	if m.depotTouched == nil {
		m.depotTouched = map[uint32]bool{}
	}
	for _, d := range m.depots() {
		raw := m.depotOS[d.ID]
		if raw == "" {
			raw = d.OS
		}
		if raw == "" {
			continue
		}
		m.depotEnabled[d.ID] = download.MatchesCurrentOS(raw, m.currentOS)
		m.depotTouched[d.ID] = true
	}
	m.errMsg = ""
}

func (m *Model) moveDepotCursor(delta int) {
	n := len(m.depots())
	if n == 0 {
		return
	}
	m.depotCursor = clampInt(m.depotCursor+delta, 0, n-1)
	if m.depotCursor < m.depotScroll {
		m.depotScroll = m.depotCursor
	}
	avail := m.summaryAvailRows()
	if avail < 1 {
		avail = 1
	}
	if m.depotCursor >= m.depotScroll+avail {
		m.depotScroll = m.depotCursor - avail + 1
	}
}

func (m Model) summaryAvailRows() int {
	l := m.layout()
	return max(0, l.bodyH-10)
}
