package tui

import (
	"context"
	"fmt"
	"maps"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/charmbracelet/bubbles/textinput"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"

	"github.com/Bink-lab/Shino/internal/download"
	"github.com/Bink-lab/Shino/internal/dump"
	"github.com/Bink-lab/Shino/internal/library"
	"github.com/Bink-lab/Shino/internal/lua"
)

const (
	stageSource = iota
	stageFields
	stageSummary
	stageDownload
	stageFinish
	stageLibrary
	stageProvider
	stageLibraryKey
	stageLibraryDownload
	stageDonateForm
	stageDonateRun
	stageDonateDone
	stageDonateQR
)

const (
	inputLua = iota
	inputZip
	inputManifests
	inputOutput
)

type sourceMode int

const (
	modeFiles sourceMode = iota
	modeZip
	modeLibrary
)

const (
	baseWindowW = 76
	maxWindowH  = 26
	minWindowW  = 52
	minWindowH  = 14
)

var fieldLabels = []string{"lua file", "zip file", "manifests dir", "output dir"}
var fieldPlaceholders = []string{`C:\...\app_123456.lua`, `C:\...\shino_bundle.zip`, `C:\...\manifests`, `C:\...\output`}
var sourceOptions = []string{"lua + manifests", "zip bundle", "donate via qr"}
var sourceDescriptions = []string{"a .lua config plus a manifest folder", "one .zip containing the .lua and manifests", "scan with the steam mobile app"}

type dlProgressMsg struct {
	depotID uint32
	done    uint64
	total   uint64
}

type dlLogMsg struct {
	line string
}

type dlFinishedMsg struct {
	warnings []string
	err      error
}

type browseResultMsg struct {
	field int
	path  string
	err   error
}

type frameMsg struct{}

type layout struct {
	w, h  int
	vis   int
	left  int
	top   int
	bodyW int
	bodyH int
}

type Model struct {
	stage  int
	pos    int
	mode   sourceMode
	inputs []textinput.Model

	sourceChoice int
	dialogOpen   bool

	cfg      *lua.Config
	tempDir  string
	errMsg   string
	errFlash int

	depotCursor  int
	depotScroll  int
	depotEnabled map[uint32]bool
	depotTouched map[uint32]bool
	depotOS      map[uint32]string
	osLoading    bool
	currentOS    string

	glow   float64
	frame  int
	intro  float64
	reveal float64

	width, height int
	mx, my        int
	hovering      bool

	ch     chan tea.Msg
	cancel context.CancelFunc

	depotDone  map[uint32]uint64
	depotTotal map[uint32]uint64
	logLines   []string
	logOffset  int
	displayPct float64
	status     string
	started    time.Time

	finished bool
	warnings []string
	dlErr    error
	elapsed  time.Duration

	lib          libraryPanel
	libCtx       context.Context
	libCancel    context.CancelFunc
	donate       donateForm
	donateFields donateFormFields
	qr           qrPanel
	version      string
}

func New() Model {
	inputs := make([]textinput.Model, len(fieldLabels))
	for i := range inputs {
		ti := textinput.New()
		ti.Placeholder = fieldPlaceholders[i]
		ti.Prompt = "❯ "
		ti.PromptStyle = stPrompt
		ti.TextStyle = stText
		ti.PlaceholderStyle = stPlaceholder
		ti.Cursor.Style = stPrompt
		inputs[i] = ti
	}
	keyStores := make([]library.KeyStore, len(providerNames))
	for i, file := range []string{library.RyuuKeyFile, library.HubcapKeyFile, library.ShinoKeyFile} {
		store, err := library.NewKeyStore(file)
		if err != nil {
			store = library.KeyStore{}
		}
		keyStores[i] = store
	}
	return Model{
		inputs:       inputs,
		depotDone:    map[uint32]uint64{},
		depotTotal:   map[uint32]uint64{},
		depotEnabled: map[uint32]bool{},
		depotTouched: map[uint32]bool{},
		depotOS:      map[uint32]string{},
		currentOS:    download.CurrentOS(),
		status:       "idle",
		version:      dump.BuildTag(),
		lib: libraryPanel{
			client:    library.NewClient(library.ClientConfig{ShinoWebURL: os.Getenv("SHINOWEB_URL")}),
			search:    newLibrarySearchInput(),
			keyInput:  newLibraryKeyInput(),
			images:    map[uint32][]string{},
			imgFailed: map[uint32]bool{},
			imgBusy:   map[uint32]bool{},
			keyStores: keyStores,
			savedKeys: make([]string, len(providerNames)),
		},
	}
}

func (m Model) Init() tea.Cmd {
	return tea.Batch(textinput.Blink, tick(33*time.Millisecond))
}

func tick(d time.Duration) tea.Cmd {
	return tea.Tick(d, func(time.Time) tea.Msg { return frameMsg{} })
}

func (m Model) fieldOrder() []int {
	switch m.mode {
	case modeZip:
		return []int{inputZip, inputOutput}
	case modeLibrary:
		return []int{inputOutput}
	}
	return []int{inputLua, inputManifests, inputOutput}
}

func (m Model) currentInput() int {
	order := m.fieldOrder()
	pos := clampInt(m.pos, 0, len(order)-1)
	return order[pos]
}

func (m Model) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		m.width, m.height = msg.Width, msg.Height
		m.resizeLibraryInputs()
		return m, nil
	case tea.MouseMsg:
		return m.mouse(msg)
	case frameMsg:
		return m.advanceFrame()
	case catalogLoadedMsg:
		return m.catalogLoaded(msg)
	case libraryImageLoadedMsg:
		m.imageLoaded(msg)
		return m, nil
	case providerBundleLoadedMsg:
		return m.providerBundleLoaded(msg)
	case depotOSLoadedMsg:
		if m.cfg != nil && msg.appID == m.cfg.AppID {
			m.applyDepotOS(msg.os)
		}
		return m, nil
	case browseResultMsg:
		m.dialogOpen = false
		if msg.err != nil {
			m.errMsg = msg.err.Error()
			return m, nil
		}
		if msg.path != "" {
			m.inputs[msg.field].SetValue(msg.path)
			m.errMsg = ""
		}
		return m, nil
	case dlLogMsg:
		m.appendLog(msg.line)
		return m, waitCh(m.ch)
	case dlProgressMsg:
		m.depotDone[msg.depotID] = msg.done
		m.depotTotal[msg.depotID] = msg.total
		return m, waitCh(m.ch)
	case dlFinishedMsg:
		m.finish(msg)
		return m, nil
	case donateLogMsg:
		m.donateLog(msg.line)
		return m, waitCh(m.donate.ch)
	case donateDoneMsg:
		m.donateFinished(msg)
		return m, nil
	case donateUploadedMsg:
		m.donateUploaded(msg)
		return m, nil
	case qrChallengeMsg:
		m.qr.lines = msg.lines
		m.qr.status = "waiting for approval"
		return m, waitCh(m.qr.ch)
	case qrStatusMsg:
		m.qr.status = msg.line
		return m, waitCh(m.qr.ch)
	case qrDoneMsg:
		return m.qrDone(msg)
	case tea.KeyMsg:
		if msg.String() == "ctrl+c" {
			return m.quit()
		}
		if m.stage == stageLibrary {
			return m.libraryKey(msg)
		}
		return m.stageKey(msg)
	}
	if m.stage == stageFields {
		idx := m.currentInput()
		var cmd tea.Cmd
		m.inputs[idx], cmd = m.inputs[idx].Update(msg)
		return m, cmd
	}
	return m, nil
}

func (m Model) advanceFrame() (tea.Model, tea.Cmd) {
	m.frame++
	if m.intro < 1 {
		m.intro = clampF(m.intro+0.08, 0, 1)
	}
	if m.glow < 1 {
		m.glow = clampF(m.glow+0.14, 0, 1)
	}
	if m.errFlash > 0 {
		m.errFlash--
	}
	switch m.stage {
	case stageSummary:
		if m.reveal < float64(len(m.depots())+1) {
			m.reveal += 0.5
		}
	case stageDownload:
		if !m.finished {
			m.displayPct += (m.overallPct() - m.displayPct) * 0.14
		}
	}
	return m, tick(m.frameDelay())
}

func (m Model) frameDelay() time.Duration {
	switch {
	case m.intro < 1:
		return 33 * time.Millisecond
	case m.stage == stageDownload:
		return 50 * time.Millisecond
	case m.stage == stageFinish:
		return 120 * time.Millisecond
	}
	return 80 * time.Millisecond
}

func (m Model) mouse(msg tea.MouseMsg) (tea.Model, tea.Cmd) {
	m.mx, m.my = msg.X, msg.Y
	m.hovering = true
	if msg.Button == tea.MouseButtonLeft && msg.Action == tea.MouseActionPress && !m.compact() && m.closeHit() {
		return m.quit()
	}
	if msg.Action == tea.MouseActionPress && m.stage == stageLibrary {
		switch msg.Button {
		case tea.MouseButtonLeft:
			if row := m.libraryRowAt(msg.Y); row >= 0 {
				m.lib.sel = row
				m.clampLibScroll()
				return m, m.ensureVisibleImages()
			}
		case tea.MouseButtonWheelUp:
			m.moveLibSel(-3)
			return m, m.ensureVisibleImages()
		case tea.MouseButtonWheelDown:
			m.moveLibSel(3)
			return m, m.ensureVisibleImages()
		}
	}
	if msg.Action == tea.MouseActionPress {
		if m.stage == stageDownload || m.stage == stageFinish {
			switch msg.Button {
			case tea.MouseButtonWheelUp:
				m.logScroll(3)
			case tea.MouseButtonWheelDown:
				m.logScroll(-3)
			}
		}
	}
	return m, nil
}

func (m Model) closeHit() bool {
	l := m.layout()
	if m.my != l.top+1 {
		return false
	}
	x0 := l.left + l.w - 4
	return m.mx >= x0 && m.mx < x0+3
}

func (m Model) quit() (tea.Model, tea.Cmd) {
	if m.cancel != nil {
		m.cancel()
	}
	if m.libCancel != nil {
		m.libCancel()
	}
	if m.donate.cancel != nil {
		m.donate.cancel()
	}
	if m.donate.qrClient != nil {
		m.donate.qrClient.Close()
	}
	if m.qr.cancel != nil {
		m.qr.cancel()
	}
	m.cleanupBundle()
	return m, tea.Quit
}

func (m *Model) cleanupBundle() {
	if m.tempDir != "" {
		os.RemoveAll(m.tempDir)
		m.tempDir = ""
	}
}

func (m Model) stageKey(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	switch m.stage {
	case stageSource:
		return m.sourceKey(msg)
	case stageFields:
		return m.fieldKey(msg)
	case stageSummary:
		switch msg.String() {
		case "q":
			return m.quit()
		case "esc":
			m.stage = stageFields
			m.pos = len(m.fieldOrder()) - 1
			m.glow = 0
			m.inputs[m.currentInput()].Focus()
			return m, nil
		case "d":
			return m.startDownload()
		case "up", "k":
			m.moveDepotCursor(-1)
			return m, nil
		case "down", "j":
			m.moveDepotCursor(1)
			return m, nil
		case "pgup":
			m.moveDepotCursor(-m.summaryAvailRows())
			return m, nil
		case "pgdown":
			m.moveDepotCursor(m.summaryAvailRows())
			return m, nil
		case " ", "enter", "x":
			if n := len(m.depots()); n > 0 {
				m.depotCursor = clampInt(m.depotCursor, 0, n-1)
				m.toggleDepot(m.depots()[m.depotCursor].ID)
			}
			return m, nil
		case "a":
			m.setAllDepots(m.selectedCount() != len(m.depots()))
			return m, nil
		case "w":
			m.selectCurrentOS()
			return m, nil
		}
	case stageDownload:
		switch msg.String() {
		case "q", "esc":
			return m.quit()
		case "up", "k":
			m.logScroll(1)
		case "down", "j":
			m.logScroll(-1)
		case "pgup":
			m.logScroll(m.logHeight())
		case "pgdown":
			m.logScroll(-m.logHeight())
		}
	case stageFinish:
		switch msg.String() {
		case "q", "esc":
			return m.quit()
		}
	case stageProvider:
		return m.providerKey(msg)
	case stageLibraryKey:
		return m.libraryKeyKey(msg)
	case stageLibraryDownload:
		return m.libraryDownloadKey(msg)
	case stageDonateForm:
		return m.donateFormKey(msg)
	case stageDonateRun:
		switch msg.String() {
		case "q", "esc":
			return m.quit()
		}
	case stageDonateDone:
		switch msg.String() {
		case "q", "esc":
			return m.quit()
		case "enter", " ":
			return m.enterDonateQR()
		case "u":
			if m.donate.err == nil && !m.donate.uploading && m.donate.summary.Archive != "" {
				return m.launchDonateUpload()
			}
		}
	case stageDonateQR:
		return m.qrKey(msg)
	}
	return m, nil
}

func (m Model) sourceKey(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	switch msg.String() {
	case "q", "esc":
		return m.quit()
	case "up", "k", "left", "h":
		m.sourceChoice = max(0, m.sourceChoice-1)
		return m, nil
	case "down", "j", "right", "l":
		m.sourceChoice = min(len(sourceOptions)-1, m.sourceChoice+1)
		return m, nil
	case "1":
		m.sourceChoice = 0
		return m, nil
	case "2":
		m.sourceChoice = min(1, len(sourceOptions)-1)
		return m, nil
	case "3":
		if len(sourceOptions) > 2 {
			m.sourceChoice = 2
		}
		return m, nil
	case "tab":
		return m.enterLibrary()
	case "enter", " ":
		if m.sourceChoice == 2 && len(sourceOptions) > 2 {
			m.donateFields = newDonateFormFields()
			m.stage = stageDonateForm
			m.glow = 0
			m.errMsg = ""
			return m, textinput.Blink
		}
		m.mode = sourceMode(m.sourceChoice)
		m.stage = stageFields
		m.pos = 0
		m.glow = 0
		m.errMsg = ""
		m.inputs[m.currentInput()].Focus()
		return m, nil
	}
	return m, nil
}

func (m Model) fieldKey(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	order := m.fieldOrder()
	idx := m.currentInput()
	switch msg.String() {
	case "esc":
		m.inputs[idx].Blur()
		m.errMsg = ""
		m.glow = 0
		if m.pos == 0 {
			if m.mode == modeLibrary {
				m.cleanupBundle()
				m.cfg = nil
				m.mode = modeFiles
				return m.enterLibrary()
			}
			m.stage = stageSource
			return m, nil
		}
		m.pos--
		m.inputs[m.currentInput()].Focus()
		return m, nil
	case "tab":
		if m.dialogOpen {
			return m, nil
		}
		m.dialogOpen = true
		return m, browseCmd(idx, m.inputs[idx].Value())
	case "enter":
		if m.pos < len(order)-1 {
			m.inputs[idx].Blur()
			m.pos++
			m.inputs[m.currentInput()].Focus()
			m.glow = 0
			return m, nil
		}
		return m.advance()
	}
	var cmd tea.Cmd
	m.inputs[idx], cmd = m.inputs[idx].Update(msg)
	return m, cmd
}

func (m Model) advance() (tea.Model, tea.Cmd) {
	var cfg *lua.Config
	var err error
	switch m.mode {
	case modeZip:
		var b *bundle
		b, err = loadBundle(strings.TrimSpace(m.inputs[inputZip].Value()))
		if err == nil {
			m.cleanupBundle()
			m.tempDir = b.manifestsDir
			cfg = b.cfg
		}
	case modeLibrary:
		cfg = m.cfg
	default:
		m.cleanupBundle()
		cfg, err = lua.LoadFile(strings.TrimSpace(m.inputs[inputLua].Value()))
	}
	if err != nil {
		m.errMsg = err.Error()
		m.errFlash = 10
		return m, nil
	}
	oldAppID := uint32(0)
	if m.cfg != nil {
		oldAppID = m.cfg.AppID
	}
	oldEnabled := maps.Clone(m.depotEnabled)
	oldTouched := maps.Clone(m.depotTouched)
	m.cfg = cfg
	m.errMsg = ""
	m.stage = stageSummary
	m.reveal = 0
	m.glow = 0
	initDepotSelection(&m)
	if oldAppID == cfg.AppID && len(oldTouched) > 0 {
		for _, d := range m.depots() {
			if v, ok := oldEnabled[d.ID]; ok {
				m.depotEnabled[d.ID] = v
			}
			if oldTouched[d.ID] {
				m.depotTouched[d.ID] = true
			}
		}
	}
	return m, fetchDepotOSCmd(cfg.AppID, cfg.Depots, m.manifestsDir(), m.depotOS)
}

func (m Model) manifestsDir() string {
	if m.mode == modeZip || m.mode == modeLibrary {
		return m.tempDir
	}
	return strings.TrimSpace(m.inputs[inputManifests].Value())
}

func (m Model) startDownload() (tea.Model, tea.Cmd) {
	if m.osLoading {
		m.errMsg = "resolving depot os, try again in a moment"
		m.errFlash = 10
		return m, nil
	}
	selected := m.selectedDepots()
	if len(selected) == 0 {
		m.errMsg = "no depots selected (space toggles, a selects all)"
		m.errFlash = 10
		return m, nil
	}
	filtered := &lua.Config{AppID: m.cfg.AppID, Depots: selected}
	m.stage = stageDownload
	m.started = time.Now()
	m.displayPct = 0
	m.logLines = nil
	m.logOffset = 0
	m.depotDone = map[uint32]uint64{}
	m.depotTotal = map[uint32]uint64{}
	m.status = "connecting to steam"
	ctx, cancel := context.WithCancel(context.Background())
	m.cancel = cancel
	ch := make(chan tea.Msg, 256)
	m.ch = ch
	go func() {
		warnings, err := download.DownloadAll(ctx, filtered,
			m.manifestsDir(),
			strings.TrimSpace(m.inputs[inputOutput].Value()),
			16,
			func(depotID uint32, done, total uint64) {
				ch <- dlProgressMsg{depotID, done, total}
			},
			func(line string) {
				ch <- dlLogMsg{line}
			})
		ch <- dlFinishedMsg{warnings, err}
		close(ch)
	}()
	return m, waitCh(ch)
}

func (m *Model) finish(msg dlFinishedMsg) {
	m.finished = true
	m.stage = stageFinish
	m.warnings = msg.warnings
	m.dlErr = msg.err
	m.elapsed = time.Since(m.started)
	if m.cancel != nil {
		m.cancel()
		m.cancel = nil
	}
}

func (m *Model) appendLog(line string) {
	m.logLines = append(m.logLines, time.Now().Format("15:04:05")+"  "+line)
	if len(m.logLines) > 500 {
		m.logLines = m.logLines[len(m.logLines)-500:]
	}
	m.status = line
}

func (m *Model) logScroll(delta int) {
	h := m.logHeight()
	m.logOffset = clampInt(m.logOffset+delta, 0, max(0, len(m.logLines)-h))
}

func (m Model) logHeight() int {
	l := m.layout()
	return max(2, l.bodyH-11)
}

func (m Model) visibleLog(h int) []string {
	end := min(len(m.logLines), len(m.logLines)-m.logOffset)
	start := max(0, end-h)
	return m.logLines[start:end]
}

func waitCh(ch <-chan tea.Msg) tea.Cmd {
	return func() tea.Msg {
		msg, ok := <-ch
		if !ok {
			return nil
		}
		return msg
	}
}

func browseCmd(field int, value string) tea.Cmd {
	start := dialogStartDir(value)
	return func() tea.Msg {
		var path string
		var err error
		switch field {
		case inputLua:
			path, err = pickFile("Select Shino lua config", "lua config", "*.lua", start)
		case inputZip:
			path, err = pickFile("Select Shino zip bundle", "zip bundle", "*.zip", start)
		case inputManifests:
			path, err = pickFolder("Select manifests folder", start)
		case inputOutput:
			path, err = pickFolder("Select output folder", start)
		}
		return browseResultMsg{field: field, path: path, err: err}
	}
}

func dialogStartDir(value string) string {
	value = strings.TrimSpace(strings.Trim(value, `"`))
	if value == "" {
		return ""
	}
	if fi, err := os.Stat(value); err == nil {
		if fi.IsDir() {
			return value
		}
		return filepath.Dir(value)
	}
	dir := filepath.Dir(value)
	if fi, err := os.Stat(dir); err == nil && fi.IsDir() {
		return dir
	}
	return ""
}

func (m Model) depots() []lua.Depot {
	if m.cfg == nil {
		return nil
	}
	return m.cfg.Depots
}

func (m Model) overallBytes() (uint64, uint64) {
	var done, total uint64
	for _, d := range m.depots() {
		done += m.depotDone[d.ID]
		total += m.depotTotal[d.ID]
	}
	return done, total
}

func (m Model) overallPct() float64 {
	done, total := m.overallBytes()
	if total == 0 {
		return 0
	}
	return float64(done) / float64(total)
}

func (m Model) activeDepot() (uint32, bool) {
	for _, d := range m.depots() {
		if m.depotTotal[d.ID] > 0 && m.depotDone[d.ID] < m.depotTotal[d.ID] {
			return d.ID, true
		}
	}
	return 0, false
}

func (m Model) termSize() (int, int) {
	w, h := m.width, m.height
	if w <= 0 {
		w = 80
	}
	if h <= 0 {
		h = 24
	}
	return w, h
}

func (m Model) compact() bool {
	w, h := m.termSize()
	return w < minWindowW+6 || h < minWindowH+3
}

func (m Model) layout() layout {
	tw, th := m.termSize()
	w := min(baseWindowW, tw-6)
	if w < 44 {
		w = tw - 2
	}
	h := min(maxWindowH, th-3)
	if h < 10 {
		h = th - 1
	}
	left := max(0, (tw-w)/2)
	targetTop := max(0, (th-(h+1))/2)
	e := easeOutCubic(m.intro)
	top := int(float64(targetTop) * e)
	vis := h
	if m.intro < 1 {
		vis = clampInt(2+int(float64(h-2)*e), 2, h)
	}
	return layout{w: w, h: h, vis: vis, left: left, top: top, bodyW: w - 4, bodyH: h - 4}
}

func (m Model) View() string {
	if m.compact() {
		return m.viewSmall()
	}
	l := m.layout()
	rows := m.windowRows(l)
	if l.vis < len(rows) {
		rows = rows[:l.vis]
	}
	pad := strings.Repeat(" ", l.left)
	var b strings.Builder
	for range l.top {
		b.WriteByte('\n')
	}
	for i, row := range rows {
		b.WriteString(pad)
		b.WriteString(row)
		if i < len(rows)-1 {
			b.WriteByte('\n')
		}
	}
	return b.String()
}

func (m Model) windowRows(l layout) []string {
	rows := make([]string, 0, l.h)
	rows = append(rows, stBorder.Render("╭"+strings.Repeat("─", l.w-2)+"╮"))
	rows = append(rows, m.titleRow(l))
	rows = append(rows, stBorder.Render("├"+strings.Repeat("─", l.w-2)+"┤"))
	for _, line := range m.bodyLines(l) {
		rows = append(rows, stBorder.Render("│")+" "+line+" "+stBorder.Render("│"))
	}
	rows = append(rows, stBorder.Render("╰"+strings.Repeat("─", l.w-2)+"╯"))
	return rows
}

func (m Model) titleRow(l layout) string {
	glyph := stTitleGlyph.Render("◆")
	switch m.stage {
	case stageDownload:
		glyph = stTitleGlyph.Render(spinnerFrames[m.frame%len(spinnerFrames)])
	case stageDonateRun:
		glyph = stTitleGlyph.Render(spinnerFrames[m.frame%len(spinnerFrames)])
	case stageDonateQR:
		glyph = stTitleGlyph.Render(spinnerFrames[m.frame%len(spinnerFrames)])
	case stageLibrary:
		if m.lib.loading {
			glyph = stTitleGlyph.Render(spinnerFrames[m.frame%len(spinnerFrames)])
		}
	case stageLibraryDownload:
		glyph = stTitleGlyph.Render(spinnerFrames[m.frame%len(spinnerFrames)])
	case stageFinish:
		if m.dlErr != nil {
			glyph = stRed.Background(cTitleBg).Render("×")
		} else {
			glyph = lipgloss.NewStyle().Foreground(gradAt(float64(m.frame) * 0.03)).Background(cTitleBg).Bold(true).Render("✓")
		}
	case stageDonateDone:
		if m.donate.err != nil {
			glyph = stRed.Background(cTitleBg).Render("×")
		} else {
			glyph = lipgloss.NewStyle().Foreground(gradAt(float64(m.frame) * 0.03)).Background(cTitleBg).Bold(true).Render("✓")
		}
	}
	left := " " + glyph + " " + stTitleName.Render("Shino") + stTitleMeta.Render(" · "+m.stageLabel()+" · "+m.version)
	right := m.controls()
	fill := l.w - 2 - lipgloss.Width(left) - lipgloss.Width(right)
	if fill < 1 {
		fill = 1
	}
	return stBorder.Render("│") + left + stTitleFill.Render(strings.Repeat(" ", fill)) + right + stBorder.Render("│")
}

func (m Model) controls() string {
	closeCell := stCtrl.Render("×")
	if m.hovering && m.closeHit() {
		closeCell = stCtrlHot.Render("×")
	}
	return stCtrl.Render("─") + stTitleFill.Render("   ") +
		stCtrl.Render("□") + stTitleFill.Render("   ") +
		closeCell + stTitleFill.Render(" ")
}

func (m Model) stageLabel() string {
	switch m.stage {
	case stageSource:
		return "source"
	case stageFields:
		return "config"
	case stageSummary:
		return "depots"
	case stageDownload:
		return "downloading"
	case stageFinish:
		if m.dlErr != nil {
			return "failed"
		}
		return "complete"
	case stageLibrary:
		return "library"
	case stageProvider:
		return "provider"
	case stageLibraryKey:
		return "credential"
	case stageLibraryDownload:
		return "importing"
	case stageDonateForm:
		return "donation"
	case stageDonateRun:
		return "dumping"
	case stageDonateDone:
		if m.donate.err != nil {
			return "failed"
		}
		return "donated"
	case stageDonateQR:
		return "qr login"
	}
	return ""
}

func (m Model) bodyLines(l layout) []string {
	w := l.bodyW
	var lines []string
	switch m.stage {
	case stageSource:
		lines = m.sourceBody(w, l.bodyH)
	case stageFields:
		lines = m.formBody(w, l.bodyH)
	case stageSummary:
		lines = m.summaryBody(w, l.bodyH)
	case stageDownload:
		lines = m.downloadBody(w, l.bodyH)
	case stageFinish:
		lines = m.finishBody(w, l.bodyH)
	case stageLibrary:
		lines = m.libraryBody(w, l.bodyH)
	case stageProvider:
		lines = m.providerBody(w, l.bodyH)
	case stageLibraryKey:
		lines = m.libraryKeyBody(w, l.bodyH)
	case stageLibraryDownload:
		lines = m.libraryDownloadBody(w, l.bodyH)
	case stageDonateForm:
		lines = m.donateFormBody(w, l.bodyH)
	case stageDonateRun:
		lines = m.donateRunBody(w, l.bodyH)
	case stageDonateDone:
		lines = m.donateDoneBody(w)
	case stageDonateQR:
		lines = m.donateQRBody(w)
	}
	if len(lines) > l.bodyH {
		lines = lines[:l.bodyH]
	}
	top := (l.bodyH - len(lines)) / 2
	out := make([]string, 0, l.bodyH)
	for range top {
		out = append(out, strings.Repeat(" ", w))
	}
	for _, line := range lines {
		out = append(out, padLine(line, w))
	}
	for len(out) < l.bodyH {
		out = append(out, strings.Repeat(" ", w))
	}
	return out
}

func (m Model) stepsLine() string {
	steps := []string{"source"}
	for _, idx := range m.fieldOrder() {
		steps = append(steps, fieldLabels[idx])
	}
	active := len(steps)
	switch m.stage {
	case stageSource:
		active = 0
	case stageFields:
		active = m.pos + 1
	}
	parts := make([]string, len(steps))
	for i, label := range steps {
		switch {
		case i == active:
			parts[i] = stChipOn.Render(" " + label + " ")
		case i < active:
			parts[i] = stChipDone.Render("✓ " + label)
		default:
			parts[i] = stChipOff.Render(label)
		}
	}
	return strings.Join(parts, stDim.Render("  ─  "))
}

func (m Model) sourceBody(w, h int) []string {
	blank := strings.Repeat(" ", w)
	var lines []string
	lines = append(lines, blank)
	lines = append(lines, centerLine(m.tabBar(), w))
	lines = append(lines, blank)
	rows := make([]string, len(sourceOptions))
	maxW := 0
	for i, opt := range sourceOptions {
		marker := stDim.Render("○")
		nameStyle := stMuted
		if i == m.sourceChoice {
			marker = stPink.Render("●")
			nameStyle = stPinkSoft
		}
		rows[i] = marker + " " + nameStyle.Bold(true).Render(opt) + "  " + stDim.Render(sourceDescriptions[i])
		maxW = max(maxW, lipgloss.Width(rows[i]))
	}
	inset := strings.Repeat(" ", max(0, (w-maxW)/2))
	for _, row := range rows {
		lines = append(lines, inset+truncate(row, w))
	}
	lines = append(lines, blank)
	if m.errMsg != "" {
		lines = append(lines, centerLine(stRed.Render("✖ ")+stText.Render(truncate(m.errMsg, w-8)), w))
		lines = append(lines, blank)
	}
	lines = append(lines, helpLine(w,
		[2]string{"↑↓", "choose"},
		[2]string{"enter", "continue"},
		[2]string{"tab", "library"},
		[2]string{"esc", "quit"}))
	return lines
}

func (m Model) formBody(w, h int) []string {
	blank := strings.Repeat(" ", w)
	phase := float64(m.frame) * 0.03
	var lines []string
	switch {
	case h >= 22:
		lines = append(lines, blank)
		for _, bannerLine := range strings.Split(renderBanner(phase), "\n") {
			lines = append(lines, centerLine(bannerLine, w))
		}
		lines = append(lines, blank)
		lines = append(lines, centerLine(m.stepsLine(), w))
		lines = append(lines, blank)
		lines = append(lines, m.fieldBoxes(w)...)
	case h >= 18:
		lines = append(lines, blank)
		lines = append(lines, centerLine(renderGradientText("S H I N O", phase), w))
		lines = append(lines, blank)
		lines = append(lines, centerLine(m.stepsLine(), w))
		lines = append(lines, blank)
		lines = append(lines, m.fieldBoxes(w)...)
	default:
		lines = append(lines, centerLine(renderGradientText("S H I N O", phase), w))
		lines = append(lines, blank)
		for _, idx := range m.fieldOrder() {
			marker := "  "
			if idx == m.currentInput() {
				marker = stPink.Render("▸ ")
			}
			line := marker + stMuted.Render(fieldLabels[idx]) + "  " + m.inputs[idx].View()
			lines = append(lines, truncate(line, w))
		}
	}
	lines = append(lines, blank)
	if m.errMsg != "" {
		lines[len(lines)-1] = centerLine(stRed.Render("✖ ")+stText.Render(truncate(m.errMsg, w-8)), w)
	}
	lines = append(lines, helpLine(w,
		[2]string{"enter", "continue"},
		[2]string{"tab", "browse"},
		[2]string{"esc", "back"}))
	return lines
}

func (m Model) fieldBoxes(w int) []string {
	var lines []string
	blank := strings.Repeat(" ", w)
	bw := min(56, w-4)
	inset := strings.Repeat(" ", (w-bw)/2)
	order := m.fieldOrder()
	for i, idx := range order {
		for _, boxLine := range m.fieldBox(idx, bw) {
			lines = append(lines, inset+boxLine)
		}
		if i < len(order)-1 {
			lines = append(lines, blank)
		}
	}
	return lines
}

func (m Model) fieldBox(idx, bw int) []string {
	focused := m.stage == stageFields && m.currentInput() == idx
	col := cBorder
	if focused {
		col = lerpColor(cBorder, cPink, m.glow)
	}
	flashIdx := inputLua
	if m.mode == modeZip {
		flashIdx = inputZip
	}
	if m.errFlash > 0 && idx == flashIdx {
		col = cRed
	}
	return inputBoxLines(fieldLabels[idx], m.inputs[idx].View(), bw, col, focused)
}

func (m Model) summaryBody(w, h int) []string {
	blank := strings.Repeat(" ", w)
	depots := m.depots()
	var lines []string
	lines = append(lines, blank)
	osNote := ""
	if m.osLoading {
		osNote = "  ·  resolving os " + spinnerFrames[m.frame%len(spinnerFrames)]
	} else {
		osNote = "  ·  os: " + m.currentOS
	}
	head := renderGradientText(fmt.Sprintf("app %d", m.cfg.AppID), float64(m.frame)*0.02) +
		stMuted.Render(fmt.Sprintf("  ·  %d/%d selected%s", m.selectedCount(), len(depots), osNote))
	lines = append(lines, centerLine(head, w))
	lines = append(lines, blank)

	indent := strings.Repeat(" ", max(2, (w-62)/2))
	lines = append(lines, indent+stMuted.Render(padLine("", 4)+padLine("depot", 11)+padLine("os", 11)+padLine("key", 6)+"manifest"))
	lines = append(lines, indent+stDim.Render(strings.Repeat("─", min(58, w-8))))

	avail := max(0, h-10)
	if avail > len(depots) {
		avail = len(depots)
	}
	start := clampInt(m.depotScroll, 0, max(0, len(depots)-max(1, avail)))
	end := min(len(depots), start+max(0, avail))
	for i := start; i < end; i++ {
		d := depots[i]
		t := clampF(m.reveal-float64(i), 0, 1)
		if t <= 0 {
			break
		}
		raw := m.depotOS[d.ID]
		if raw == "" {
			raw = d.OS
		}
		label := download.OSLabel(raw)
		osStyle := stMuted
		if len(lua.SplitOSList(raw)) > 0 {
			if download.MatchesCurrentOS(raw, m.currentOS) {
				osStyle = stGreen
			} else {
				osStyle = stAmber
			}
		}
		if m.osLoading && raw == "" {
			label = "…"
			osStyle = stDim
		}
		checked := "[ ]"
		checkStyle := stDim
		if m.depotEnabled[d.ID] {
			checked = "[x]"
			checkStyle = stGreen
		}
		cursor := "  "
		idStyle := lipgloss.NewStyle().Foreground(lerpColor(cDim, cPinkSoft, t))
		if i == m.depotCursor {
			cursor = stPink.Render("▸ ")
			idStyle = idStyle.Bold(true)
		}
		keyBadge := stGreen.Render("✓")
		if d.Key == "" {
			keyBadge = stRed.Render("×")
		}
		manifest := stMuted.Render("none")
		if d.HasManifest {
			manifest = stMuted.Render(strconv.FormatUint(d.ManifestID, 10))
		}
		lines = append(lines, indent+cursor+padLine(checkStyle.Render(checked), 4)+
			padLine(idStyle.Render(strconv.FormatUint(uint64(d.ID), 10)), 11)+
			padLine(osStyle.Render(label), 11)+
			padLine(keyBadge, 6)+manifest)
	}
	if len(depots) == 0 {
		lines = append(lines, centerLine(stDim.Render("no depots"), w))
	}
	lines = append(lines, blank)
	if m.errMsg != "" {
		lines = append(lines, centerLine(stRed.Render("✖ ")+stText.Render(truncate(m.errMsg, w-8)), w))
		lines = append(lines, blank)
	}
	lines = append(lines, helpLine(w,
		[2]string{"↑↓", "move"},
		[2]string{"space", "toggle"},
		[2]string{"a", "all"},
		[2]string{"w", "this os"}))
	lines = append(lines, helpLine(w,
		[2]string{"d", "download"},
		[2]string{"esc", "back"},
		[2]string{"q", "quit"}))
	return lines
}

func (m Model) downloadBody(w, h int) []string {
	blank := strings.Repeat(" ", w)
	var lines []string
	lines = append(lines, blank)
	sp := spinnerFrames[m.frame%len(spinnerFrames)]
	lines = append(lines, stPink.Render(sp)+" "+stText.Render(truncate(m.status, w-8)))
	lines = append(lines, blank)

	done, total := m.overallBytes()
	right := fmt.Sprintf("%3.0f%%  %s / %s", m.displayPct*100, download.FormatBytes(done), download.FormatBytes(total))
	barW := clampInt(w-14-lipgloss.Width(right), 10, 44)
	lines = append(lines, stMuted.Render("all depots")+"  "+
		renderBar(m.displayPct, barW, float64(m.frame)*0.35)+"  "+stText.Render(right))
	lines = append(lines, blank)

	if id, ok := m.activeDepot(); ok {
		d, t := m.depotDone[id], m.depotTotal[id]
		pct := 0.0
		if t > 0 {
			pct = float64(d) / float64(t)
		}
		pfx := fmt.Sprintf("depot %d", id)
		right := fmt.Sprintf("%3.0f%%  %s / %s", pct*100, download.FormatBytes(d), download.FormatBytes(t))
		barW := clampInt(w-4-lipgloss.Width(pfx)-lipgloss.Width(right), 8, 36)
		lines = append(lines, stPinkSoft.Render(pfx)+"  "+
			renderBar(pct, barW, float64(m.frame)*0.35)+"  "+
			stMuted.Render(right))
	} else {
		lines = append(lines, blank)
	}
	lines = append(lines, blank)

	logH := max(2, h-len(lines)-4)
	lines = append(lines, stBorder.Render("╭─ ")+stMuted.Render("log")+
		stBorder.Render(" "+strings.Repeat("─", max(1, w-8))+"╮"))
	visible := m.visibleLog(logH)
	for i := range logH {
		text := ""
		if i < len(visible) {
			text = visible[i]
		}
		lines = append(lines, stBorder.Render("│")+" "+
			padLine(styledLog(text, w-4), w-4)+" "+stBorder.Render("│"))
	}
	lines = append(lines, stBorder.Render("╰"+strings.Repeat("─", max(1, w-2))+"╯"))
	lines = append(lines, blank)
	lines = append(lines, helpLine(w,
		[2]string{"q", "quit"},
		[2]string{"↑↓", "scroll log"}))
	return lines
}

func (m Model) finishBody(w, h int) []string {
	blank := strings.Repeat(" ", w)
	var lines []string
	lines = append(lines, blank)
	phase := float64(m.frame) * 0.03
	if m.dlErr != nil {
		lines = append(lines, centerLine(stRed.Bold(true).Render("× ")+renderGradientText("failed", phase), w))
	} else {
		lines = append(lines, centerLine(stGreen.Render("✓ ")+renderGradientText("complete", phase), w))
	}
	lines = append(lines, blank)
	done, _ := m.overallBytes()
	stats := fmt.Sprintf("%s written · %d depots · %s", download.FormatBytes(done), m.selectedCount(), m.elapsed.Round(time.Second))
	lines = append(lines, centerLine(stMuted.Render(stats), w))
	lines = append(lines, blank)

	var extra []string
	if m.dlErr != nil {
		for _, l := range wrapText("× "+m.dlErr.Error(), w-12) {
			extra = append(extra, centerLine(stRed.Render(l), w))
		}
	} else {
		for _, warning := range m.warnings {
			for _, l := range wrapText("⚠ "+warning, w-12) {
				extra = append(extra, centerLine(stAmber.Render(l), w))
			}
		}
	}
	extra = extra[:min(len(extra), max(0, h-9))]
	lines = append(lines, extra...)
	lines = append(lines, blank)
	lines = append(lines, helpLine(w, [2]string{"q", "quit"}))
	return lines
}

func styledLog(line string, w int) string {
	if line == "" {
		return ""
	}
	stamp, body := "", line
	if len(line) > 10 && line[8] == ' ' {
		stamp, body = line[:8], line[10:]
	}
	style := stText
	switch {
	case strings.Contains(body, "warn"):
		style = stAmber
	case strings.Contains(body, "failed"), strings.Contains(body, "error"):
		style = stRed
	case strings.Contains(body, "complete"), strings.Contains(body, "ready"):
		style = stPinkSoft
	}
	if stamp == "" {
		return style.Render(truncate(body, w))
	}
	return stDim.Render(stamp) + stMuted.Render("  ") + style.Render(truncate(body, w-10))
}

func wrapText(s string, width int) []string {
	if width < 8 {
		width = 8
	}
	words := strings.Fields(s)
	if len(words) == 0 {
		return nil
	}
	var out []string
	cur := words[0]
	for _, word := range words[1:] {
		if lipgloss.Width(cur)+1+lipgloss.Width(word) <= width {
			cur += " " + word
		} else {
			out = append(out, cur)
			cur = word
		}
	}
	return append(out, cur)
}

func (m Model) viewSmall() string {
	var b strings.Builder
	b.WriteString(renderGradientText("S H I N O", 0) + "\n\n")
	switch m.stage {
	case stageSource:
		b.WriteString("source: 1 " + sourceOptions[0] + ", 2 " + sourceOptions[1] + "\n")
		b.WriteString(stDim.Render("enter continues, esc quits") + "\n")
	case stageFields:
		for _, idx := range m.fieldOrder() {
			marker := "  "
			if idx == m.currentInput() {
				marker = "> "
			}
			b.WriteString(marker + m.inputs[idx].View() + "\n")
		}
		b.WriteString(stDim.Render("enter continues, tab browses, esc backs") + "\n")
	case stageSummary:
		b.WriteString(fmt.Sprintf("app %d · %d/%d selected · %s\n", m.cfg.AppID, m.selectedCount(), len(m.depots()), m.currentOS))
		b.WriteString(stDim.Render("space toggles, d downloads, esc backs") + "\n")
	case stageDownload:
		b.WriteString(spinnerFrames[m.frame%len(spinnerFrames)] + " " + m.status + "\n")
		b.WriteString(fmt.Sprintf("%.0f%%\n", m.displayPct*100))
		b.WriteString(stDim.Render("q quits") + "\n")
	case stageFinish:
		if m.dlErr != nil {
			b.WriteString(stRed.Render("× failed: "+m.dlErr.Error()) + "\n")
		} else {
			b.WriteString(stGreen.Render("✓ complete") + "\n")
		}
		b.WriteString(stDim.Render("q quits") + "\n")
	case stageLibrary:
		b.WriteString("library\n")
		b.WriteString(m.lib.search.View() + "\n")
		if m.lib.loading {
			b.WriteString("loading catalog\n")
		} else if m.lib.loadErr != "" {
			b.WriteString(stRed.Render("× "+m.lib.loadErr) + "\n")
		} else {
			b.WriteString(fmt.Sprintf("%d games\n", len(m.lib.filtered)))
			if m.lib.sel < len(m.lib.filtered) {
				b.WriteString(m.lib.filtered[m.lib.sel].Name + "\n")
			}
		}
		b.WriteString(stDim.Render("enter selects, tab backs") + "\n")
	case stageProvider:
		b.WriteString("provider: " + providerNames[m.lib.provider] + "\n")
		if m.lib.savedKeys[m.lib.provider] != "" {
			b.WriteString("credential saved, k replaces\n")
		}
		if m.lib.dlErr != "" {
			b.WriteString(stRed.Render("× "+m.lib.dlErr) + "\n")
		}
		b.WriteString(stDim.Render("enter continues, esc backs") + "\n")
	case stageLibraryKey:
		b.WriteString(providerTitles[m.lib.provider] + "\n")
		b.WriteString(m.lib.keyInput.View() + "\n")
		b.WriteString(stDim.Render("enter continues, esc backs") + "\n")
	case stageLibraryDownload:
		b.WriteString(spinnerFrames[m.frame%len(spinnerFrames)] + " requesting from " + providerNames[m.lib.provider] + "\n")
		b.WriteString(stDim.Render("esc cancels, q quits") + "\n")
	case stageDonateForm:
		b.WriteString("donate via qr\n")
		b.WriteString(m.donateFields.output.View() + "\n")
		b.WriteString(m.donateFields.appids.View() + "\n")
		if m.donateFields.remember {
			b.WriteString("remember on\n")
		} else {
			b.WriteString("remember off\n")
		}
		b.WriteString(stDim.Render("enter continues, esc backs") + "\n")
	case stageDonateRun:
		b.WriteString(spinnerFrames[m.frame%len(spinnerFrames)] + " " + m.donate.status + "\n")
		b.WriteString(stDim.Render("q quits") + "\n")
	case stageDonateDone:
		if m.donate.err != nil {
			b.WriteString(stRed.Render("× failed: "+m.donate.err.Error()) + "\n")
		} else {
			b.WriteString(stGreen.Render("✓ donation ready") + "\n")
		}
		b.WriteString(stDim.Render("q quits") + "\n")
	case stageDonateQR:
		b.WriteString("qr login\n")
		b.WriteString(stDim.Render("esc backs, q quits") + "\n")
	}
	if m.errMsg != "" {
		b.WriteString(stRed.Render("✖ "+m.errMsg) + "\n")
	}
	return b.String()
}
