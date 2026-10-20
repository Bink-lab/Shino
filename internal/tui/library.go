package tui

import (
	"context"
	"fmt"
	"os"
	"strconv"
	"strings"

	"github.com/charmbracelet/bubbles/textinput"
	tea "github.com/charmbracelet/bubbletea"

	"github.com/Bink-lab/Shino/internal/library"
)

const (
	libThumbCols = 16
	libThumbRows = 4
	libRowH      = 4
	libListTop   = 4
)

const (
	providerRyuu = iota
	providerHubcap
	providerShino
)

var (
	providerNames        = []string{"ryuu", "hubcap", "shino"}
	providerDescriptions = []string{"auth code · lua + manifests", "api key · lua + manifests", "api key · cached bundle"}
	providerTitles       = []string{"ryuu auth code", "hubcap api key", "shino api key"}
	providerLabels       = []string{"auth key", "api key", "api key"}
)

type catalogLoadedMsg struct {
	apps []library.App
	err  error
}

type libraryImageLoadedMsg struct {
	appID  uint32
	lines  []string
	failed bool
}

type providerBundleLoadedMsg struct {
	bundle   *bundle
	provider int
	secret   string
	saveKey  bool
	err      error
}

type libraryPanel struct {
	client *library.Client

	search textinput.Model

	apps     []library.App
	filtered []library.App
	loaded   bool
	loading  bool
	loadErr  string

	sel    int
	scroll int

	images    map[uint32][]string
	imgFailed map[uint32]bool
	imgBusy   map[uint32]bool

	keyStores []library.KeyStore
	savedKeys []string
	provider  int
	keyInput  textinput.Model
	dlErr     string
	dlCancel  context.CancelFunc
}

func newLibrarySearchInput() textinput.Model {
	ti := textinput.New()
	ti.Placeholder = "search games or appid"
	ti.Prompt = "❯ "
	ti.PromptStyle = stPrompt
	ti.TextStyle = stText
	ti.PlaceholderStyle = stPlaceholder
	ti.Cursor.Style = stPrompt
	return ti
}

func newLibraryKeyInput() textinput.Model {
	ti := textinput.New()
	ti.Prompt = "❯ "
	ti.EchoMode = textinput.EchoPassword
	ti.PromptStyle = stPrompt
	ti.TextStyle = stText
	ti.PlaceholderStyle = stPlaceholder
	ti.Cursor.Style = stPrompt
	return ti
}

func filterApps(apps []library.App, query string) []library.App {
	query = strings.ToLower(strings.TrimSpace(query))
	if query == "" {
		return apps
	}
	out := make([]library.App, 0, len(apps))
	for _, app := range apps {
		if strings.Contains(strings.ToLower(app.Name), query) {
			out = append(out, app)
			continue
		}
		if strings.Contains(strconv.FormatUint(uint64(app.AppID), 10), query) {
			out = append(out, app)
		}
	}
	return out
}

func fetchCatalogCmd(client *library.Client, ctx context.Context) tea.Cmd {
	return func() tea.Msg {
		apps, err := client.FetchApps(ctx)
		return catalogLoadedMsg{apps: apps, err: err}
	}
}

func fetchImageCmd(client *library.Client, ctx context.Context, appID uint32) tea.Cmd {
	return func() tea.Msg {
		data, err := client.FetchImage(ctx, appID)
		if err != nil {
			return libraryImageLoadedMsg{appID: appID, failed: true}
		}
		return libraryImageLoadedMsg{appID: appID, lines: thumbnailLines(data, libThumbCols, libThumbRows)}
	}
}

func (m Model) enterLibrary() (tea.Model, tea.Cmd) {
	m.stage = stageLibrary
	m.errMsg = ""
	m.resizeLibraryInputs()
	m.lib.search.Focus()
	var cmds []tea.Cmd
	if !m.lib.loaded && !m.lib.loading {
		if m.libCtx == nil {
			m.libCtx, m.libCancel = context.WithCancel(context.Background())
		}
		m.lib.loading = true
		m.lib.loadErr = ""
		cmds = append(cmds, fetchCatalogCmd(m.lib.client, m.libCtx))
	}
	if cmd := m.ensureVisibleImages(); cmd != nil {
		cmds = append(cmds, cmd)
	}
	if len(cmds) == 0 {
		return m, nil
	}
	return m, tea.Batch(cmds...)
}

func (m Model) catalogLoaded(msg catalogLoadedMsg) (tea.Model, tea.Cmd) {
	m.lib.loading = false
	if msg.err != nil {
		m.lib.loadErr = msg.err.Error()
		return m, nil
	}
	m.lib.loaded = true
	m.lib.apps = msg.apps
	m.lib.filtered = filterApps(msg.apps, m.lib.search.Value())
	m.lib.sel = 0
	m.lib.scroll = 0
	return m, m.ensureVisibleImages()
}

func (m Model) libraryKey(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	if msg.String() == "r" && m.lib.loadErr != "" {
		m.lib.loadErr = ""
		m.lib.loading = true
		return m, fetchCatalogCmd(m.lib.client, m.libCtx)
	}
	switch msg.String() {
	case "esc", "tab":
		m.lib.search.Blur()
		m.stage = stageSource
		return m, nil
	case "up":
		m.moveLibSel(-1)
		return m, m.ensureVisibleImages()
	case "down":
		m.moveLibSel(1)
		return m, m.ensureVisibleImages()
	case "pgup":
		m.moveLibSel(-m.libVisibleRows())
		return m, m.ensureVisibleImages()
	case "pgdown":
		m.moveLibSel(m.libVisibleRows())
		return m, m.ensureVisibleImages()
	case "enter":
		if m.lib.loaded && len(m.lib.filtered) > 0 {
			m.lib.search.Blur()
			m.stage = stageProvider
			m.lib.dlErr = ""
			m.lib.provider = providerRyuu
			for i, store := range m.lib.keyStores {
				m.lib.savedKeys[i], _ = store.Load()
			}
		}
		return m, nil
	}
	var cmd tea.Cmd
	m.lib.search, cmd = m.lib.search.Update(msg)
	m.lib.filtered = filterApps(m.lib.apps, m.lib.search.Value())
	if m.lib.sel >= len(m.lib.filtered) {
		m.lib.sel = max(0, len(m.lib.filtered)-1)
	}
	m.clampLibScroll()
	return m, tea.Batch(cmd, m.ensureVisibleImages())
}

func (m *Model) moveLibSel(delta int) {
	n := len(m.lib.filtered)
	if n == 0 {
		m.lib.sel = 0
		m.lib.scroll = 0
		return
	}
	m.lib.sel = clampInt(m.lib.sel+delta, 0, n-1)
	rows := m.libVisibleRows()
	if m.lib.sel < m.lib.scroll {
		m.lib.scroll = m.lib.sel
	}
	if m.lib.sel >= m.lib.scroll+rows {
		m.lib.scroll = m.lib.sel - rows + 1
	}
	m.clampLibScroll()
}

func (m *Model) clampLibScroll() {
	n := len(m.lib.filtered)
	rows := m.libVisibleRows()
	m.lib.scroll = clampInt(m.lib.scroll, 0, max(0, n-rows))
	if m.lib.scroll > m.lib.sel {
		m.lib.scroll = m.lib.sel
	}
}

func (m Model) libVisibleRows() int {
	l := m.layout()
	return max(1, (l.bodyH-libListTop-1)/libRowH)
}

func (m Model) libVisibleRange() (int, int) {
	n := len(m.lib.filtered)
	if n == 0 {
		return 0, 0
	}
	rows := m.libVisibleRows()
	start := clampInt(m.lib.scroll, 0, max(0, n-rows))
	end := min(n, start+rows)
	return start, end
}

func (m Model) ensureVisibleImages() tea.Cmd {
	if m.stage != stageLibrary || m.lib.client == nil {
		return nil
	}
	start, end := m.libVisibleRange()
	var cmds []tea.Cmd
	for _, app := range m.lib.filtered[start:end] {
		if _, ok := m.lib.images[app.AppID]; ok {
			continue
		}
		if m.lib.imgFailed[app.AppID] || m.lib.imgBusy[app.AppID] {
			continue
		}
		m.lib.imgBusy[app.AppID] = true
		cmds = append(cmds, fetchImageCmd(m.lib.client, m.libCtx, app.AppID))
	}
	if len(cmds) == 0 {
		return nil
	}
	return tea.Batch(cmds...)
}

func (m *Model) imageLoaded(msg libraryImageLoadedMsg) {
	delete(m.lib.imgBusy, msg.appID)
	if msg.failed || len(msg.lines) == 0 {
		m.lib.imgFailed[msg.appID] = true
		return
	}
	m.lib.images[msg.appID] = msg.lines
}

func (m *Model) resizeLibraryInputs() {
	l := m.layout()
	m.lib.search.Width = max(8, l.bodyW-8)
	m.lib.keyInput.Width = max(8, min(48, l.bodyW-10))
}

func (m Model) selectedApp() (library.App, bool) {
	if m.lib.sel < 0 || m.lib.sel >= len(m.lib.filtered) {
		return library.App{}, false
	}
	return m.lib.filtered[m.lib.sel], true
}

func (m Model) providerKey(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	switch msg.String() {
	case "esc", "tab":
		m.stage = stageLibrary
		m.lib.search.Focus()
		return m, nil
	case "up":
		m.lib.provider = max(0, m.lib.provider-1)
		return m, nil
	case "down":
		m.lib.provider = min(len(providerNames)-1, m.lib.provider+1)
		return m, nil
	case "k":
		return m.openLibraryKey(), nil
	case "enter":
		if m.lib.savedKeys[m.lib.provider] != "" {
			return m.startLibraryDownload(m.lib.provider, m.lib.savedKeys[m.lib.provider], false)
		}
		return m.openLibraryKey(), nil
	}
	return m, nil
}

func (m Model) openLibraryKey() Model {
	m.lib.keyInput.SetValue("")
	m.lib.keyInput.Placeholder = providerLabels[m.lib.provider]
	m.resizeLibraryInputs()
	m.lib.keyInput.Focus()
	m.stage = stageLibraryKey
	return m
}

func (m Model) libraryKeyKey(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	switch msg.String() {
	case "esc", "tab":
		m.lib.keyInput.Blur()
		m.stage = stageProvider
		return m, nil
	case "enter":
		key := strings.TrimSpace(m.lib.keyInput.Value())
		if key == "" {
			return m, nil
		}
		m.lib.keyInput.Blur()
		return m.startLibraryDownload(m.lib.provider, key, true)
	}
	var cmd tea.Cmd
	m.lib.keyInput, cmd = m.lib.keyInput.Update(msg)
	return m, cmd
}

func (m Model) libraryDownloadKey(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	switch msg.String() {
	case "q":
		return m.quit()
	case "esc":
		if m.lib.dlCancel != nil {
			m.lib.dlCancel()
			m.lib.dlCancel = nil
		}
		m.stage = stageProvider
		return m, nil
	}
	return m, nil
}

func (m Model) startLibraryDownload(provider int, secret string, save bool) (tea.Model, tea.Cmd) {
	app, ok := m.selectedApp()
	if !ok {
		m.stage = stageLibrary
		return m, nil
	}
	if m.lib.dlCancel != nil {
		m.lib.dlCancel()
	}
	ctx, cancel := context.WithCancel(context.Background())
	m.lib.dlCancel = cancel
	m.lib.dlErr = ""
	m.stage = stageLibraryDownload
	client := m.lib.client
	appID := app.AppID
	return m, func() tea.Msg {
		f, err := os.CreateTemp("", "shino-"+providerNames[provider]+"-*.zip")
		if err != nil {
			return providerBundleLoadedMsg{provider: provider, err: fmt.Errorf("could not create temp file: %w", err)}
		}
		zipPath := f.Name()
		defer os.Remove(zipPath)
		if provider == providerHubcap {
			err = client.DownloadHubcap(ctx, appID, secret, f)
		} else if provider == providerShino {
			err = client.DownloadShinoWeb(ctx, appID, secret, f)
		} else {
			err = client.DownloadRyuu(ctx, appID, secret, f)
		}
		if err != nil {
			f.Close()
			return providerBundleLoadedMsg{provider: provider, err: err}
		}
		if err := f.Close(); err != nil {
			return providerBundleLoadedMsg{provider: provider, err: err}
		}
		b, err := loadBundle(zipPath)
		if err != nil {
			return providerBundleLoadedMsg{provider: provider, err: fmt.Errorf("invalid bundle from %s: %w", providerNames[provider], err)}
		}
		return providerBundleLoadedMsg{bundle: b, provider: provider, secret: secret, saveKey: save}
	}
}

func (m Model) providerBundleLoaded(msg providerBundleLoadedMsg) (tea.Model, tea.Cmd) {
	if m.stage != stageLibraryDownload {
		if msg.bundle != nil {
			os.RemoveAll(msg.bundle.manifestsDir)
		}
		return m, nil
	}
	if m.lib.dlCancel != nil {
		m.lib.dlCancel()
		m.lib.dlCancel = nil
	}
	if msg.err != nil {
		m.lib.dlErr = msg.err.Error()
		m.stage = stageProvider
		return m, nil
	}
	m.lib.savedKeys[msg.provider] = msg.secret
	if msg.saveKey {
		if err := m.lib.keyStores[msg.provider].Save(msg.secret); err != nil {
			m.errMsg = "could not save credential: " + err.Error()
		}
	}
	m.mode = modeLibrary
	m.cleanupBundle()
	m.cfg = msg.bundle.cfg
	m.tempDir = msg.bundle.manifestsDir
	m.stage = stageFields
	m.pos = 0
	m.glow = 0
	m.inputs[inputOutput].Focus()
	return m, nil
}

func (m Model) providerBody(w, h int) []string {
	blank := strings.Repeat(" ", w)
	var lines []string
	lines = append(lines, blank)
	lines = append(lines, centerLine(renderGradientText("choose a provider", float64(m.frame)*0.03), w))
	lines = append(lines, blank)
	for i, name := range providerNames {
		marker := stDim.Render("○")
		nameStyle := stMuted
		if i == m.lib.provider {
			marker = stPink.Render("●")
			nameStyle = stPinkSoft
		}
		row := marker + " " + nameStyle.Bold(true).Render(name) + "  " +
			stDim.Render(providerDescriptions[i])
		lines = append(lines, centerLine(row, w))
	}
	lines = append(lines, blank)
	if app, ok := m.selectedApp(); ok {
		lines = append(lines, centerLine(stText.Render(truncate(app.Name, max(4, w-16)))+
			stMuted.Render(fmt.Sprintf("  ·  appid %d", app.AppID)), w))
	}
	if m.lib.savedKeys[m.lib.provider] != "" {
		lines = append(lines, centerLine(stGreen.Render("✓ credential saved")+"  "+
			stKey.Render("k")+" "+stKeyDesc.Render("replace"), w))
	} else {
		lines = append(lines, centerLine(stMuted.Render("no saved credential"), w))
	}
	if m.lib.dlErr != "" {
		lines = append(lines, blank)
		for _, l := range wrapText("✖ "+m.lib.dlErr, max(8, w-8)) {
			lines = append(lines, centerLine(stRed.Render(l), w))
		}
	}
	lines = append(lines, blank)
	lines = append(lines, helpLine(w,
		[2]string{"↑↓", "choose"},
		[2]string{"enter", "continue"},
		[2]string{"k", "credential"},
		[2]string{"esc", "back"}))
	return lines
}

func (m Model) libraryKeyBody(w, h int) []string {
	blank := strings.Repeat(" ", w)
	var lines []string
	lines = append(lines, blank)
	lines = append(lines, centerLine(renderGradientText(providerTitles[m.lib.provider], float64(m.frame)*0.03), w))
	lines = append(lines, blank)
	bw := min(56, w-4)
	inset := strings.Repeat(" ", (w-bw)/2)
	for _, boxLine := range inputBoxLines(providerLabels[m.lib.provider], m.lib.keyInput.View(), bw, cPink, true) {
		lines = append(lines, inset+boxLine)
	}
	lines = append(lines, blank)
	lines = append(lines, centerLine(stDim.Render("saved locally after a valid download"), w))
	lines = append(lines, blank)
	lines = append(lines, helpLine(w,
		[2]string{"enter", "continue"},
		[2]string{"esc", "back"}))
	return lines
}

func (m Model) libraryDownloadBody(w, h int) []string {
	blank := strings.Repeat(" ", w)
	var lines []string
	lines = append(lines, blank)
	sp := spinnerFrames[m.frame%len(spinnerFrames)]
	lines = append(lines, centerLine(stPink.Render(sp)+" "+stText.Render("requesting bundle from "+providerNames[m.lib.provider]), w))
	lines = append(lines, blank)
	if app, ok := m.selectedApp(); ok {
		lines = append(lines, centerLine(stMuted.Render(truncate(app.Name, max(4, w-16))+
			fmt.Sprintf("  ·  appid %d", app.AppID)), w))
	}
	lines = append(lines, blank)
	lines = append(lines, helpLine(w,
		[2]string{"esc", "cancel"},
		[2]string{"q", "quit"}))
	return lines
}

func (m Model) libraryRowAt(y int) int {
	if m.stage != stageLibrary || m.compact() {
		return -1
	}
	l := m.layout()
	rel := y - (l.top + 3) - libListTop
	if rel < 0 {
		return -1
	}
	idx := rel / libRowH
	if idx < 0 || idx >= m.libVisibleRows() {
		return -1
	}
	start, _ := m.libVisibleRange()
	i := start + idx
	if i >= len(m.lib.filtered) {
		return -1
	}
	return i
}

func (m Model) tabBar() string {
	active := 0
	if m.stage == stageLibrary {
		active = 1
	}
	labels := []string{"source", "library"}
	parts := make([]string, len(labels))
	for i, label := range labels {
		if i == active {
			parts[i] = stChipOn.Render(" " + label + " ")
		} else {
			parts[i] = stChipOff.Render(label)
		}
	}
	return strings.Join(parts, stDim.Render("  "))
}

func (m Model) libraryBody(w, h int) []string {
	blank := strings.Repeat(" ", w)
	lines := make([]string, 0, h)
	lines = append(lines, centerLine(m.tabBar(), w))
	lines = append(lines, blank)
	lines = append(lines, "  "+truncate(m.lib.search.View(), max(1, w-4)))
	lines = append(lines, "  "+stDim.Render(strings.Repeat("─", max(2, w-4))))

	switch {
	case m.lib.loading:
		lines = append(lines, blank)
		lines = append(lines, centerLine(stPink.Render(spinnerFrames[m.frame%len(spinnerFrames)])+" "+stMuted.Render("loading game catalog"), w))
	case m.lib.loadErr != "":
		lines = append(lines, blank)
		lines = append(lines, centerLine(stRed.Render("✖ ")+stText.Render(truncate(m.lib.loadErr, max(4, w-6))), w))
		lines = append(lines, centerLine(stKey.Render("r")+" "+stKeyDesc.Render("retry"), w))
	case m.lib.loaded && len(m.lib.filtered) == 0:
		lines = append(lines, blank)
		if strings.TrimSpace(m.lib.search.Value()) == "" {
			lines = append(lines, centerLine(stDim.Render("catalog is empty"), w))
		} else {
			lines = append(lines, centerLine(stDim.Render("no matches for ")+stMuted.Render(truncate(m.lib.search.Value(), max(4, w-18))), w))
		}
	default:
		start, end := m.libVisibleRange()
		for i := start; i < end; i++ {
			lines = append(lines, m.libraryRow(m.lib.filtered[i], i, w)...)
		}
		if room := h - len(lines); room >= 1 {
			lines = append(lines, helpLine(w,
				[2]string{"enter", "select"},
				[2]string{"↑↓", "move"},
				[2]string{"tab", "back"}))
		}
	}
	for len(lines) < h {
		lines = append(lines, blank)
	}
	if len(lines) > h {
		lines = lines[:h]
	}
	return lines
}

func (m Model) libraryRow(app library.App, index, w int) []string {
	selected := index == m.lib.sel
	thumb := m.lib.images[app.AppID]
	if thumb == nil {
		thumb = placeholderThumbnail(libThumbCols, libThumbRows)
	}
	marker := "  "
	if selected {
		marker = stPink.Render("▌") + " "
	}
	nameStyle := stText
	if selected {
		nameStyle = stPinkSoft.Bold(true)
	}
	infoW := max(4, w-libThumbCols-6)
	lines := make([]string, libRowH)
	for i := range libRowH {
		left := ""
		if i < len(thumb) {
			left = thumb[i]
		}
		right := ""
		switch i {
		case 0:
			right = nameStyle.Render(truncate(app.Name, infoW))
		case 1:
			right = stMuted.Render(fmt.Sprintf("appid %d", app.AppID))
		}
		lines[i] = marker + padLine(left, libThumbCols) + "  " + right
	}
	return lines
}
