package tui

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/charmbracelet/bubbles/textinput"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"

	"github.com/Bink-lab/Shino/internal/dump"
	"github.com/Bink-lab/Shino/internal/library"
)

type donateLogMsg struct {
	line string
}

type donateDoneMsg struct {
	summary dump.Summary
	err     error
}

type donateUploadedMsg struct {
	seq int
	id  string
	err error
}

type donateUploadStartMsg struct {
	seq   int
	total int
}

type donateUploadPartMsg struct {
	seq   int
	done  int
	total int
}

type donateForm struct {
	logs          []string
	status        string
	summary       dump.Summary
	err           error
	cancel        context.CancelFunc
	ch            chan tea.Msg
	started       time.Time
	elapsed       time.Duration
	qrClient      *dump.Client
	uploading     bool
	uploadLine    string
	uploadFail    bool
	uploadSeq     int
	uploadDone    int
	uploadTotal   int
	keyInput      textinput.Model
	keySaved      bool
	keyInvalid    bool
	keyErr        string
	pendingUpload bool
}

type qrPanel struct {
	lines  []string
	status string
	err    string
	cancel context.CancelFunc
	ch     chan tea.Msg
}

type qrChallengeMsg struct {
	lines []string
}

type qrStatusMsg struct {
	line string
}

type qrDoneMsg struct {
	client *dump.Client
	err    error
}

type donateFormFields struct {
	output   textinput.Model
	appids   textinput.Model
	remember bool
	focus    int
	err      string
}

func newDonateInput() textinput.Model {
	ti := textinput.New()
	ti.Prompt = "❯ "
	ti.PromptStyle = stPrompt
	ti.TextStyle = stText
	ti.PlaceholderStyle = stPlaceholder
	ti.Cursor.Style = stPrompt
	return ti
}

func newDonateKeyInput() textinput.Model {
	ti := textinput.New()
	ti.Prompt = "❯ "
	ti.EchoMode = textinput.EchoPassword
	ti.Placeholder = "shino_..."
	ti.PromptStyle = stPrompt
	ti.TextStyle = stText
	ti.PlaceholderStyle = stPlaceholder
	ti.Cursor.Style = stPrompt
	ti.CharLimit = 128
	return ti
}

func newDonateFormFields() donateFormFields {
	output := newDonateInput()
	output.SetValue("shino-donation")
	appids := newDonateInput()
	appids.Placeholder = "comma separated, empty = all owned"
	f := donateFormFields{output: output, appids: appids}
	f.output.Focus()
	return f
}

func (f *donateFormFields) applyFocus() tea.Cmd {
	f.output.Blur()
	f.appids.Blur()
	if f.focus == 0 {
		return f.output.Focus()
	}
	if f.focus == 1 {
		return f.appids.Focus()
	}
	return nil
}

func (m Model) launchDonateRun(client *dump.Client, opts dump.Options) (tea.Model, tea.Cmd) {
	if m.donate.cancel != nil {
		m.donate.cancel()
	}
	m.stage = stageDonateRun
	m.donate.logs = nil
	m.donate.status = "connecting to steam"
	m.donate.started = time.Now()
	m.donate.err = nil
	m.donate.qrClient = client
	m.donate.uploading = false
	m.donate.uploadLine = ""
	m.donate.uploadFail = false
	m.donate.uploadDone = 0
	m.donate.uploadTotal = 0
	m.donate.pendingUpload = false
	m.donate.keyErr = ""
	m.donate.keyInvalid = false
	m.errMsg = ""
	ctx, cancel := context.WithCancel(context.Background())
	m.donate.cancel = cancel
	ch := make(chan tea.Msg, 256)
	m.donate.ch = ch
	go func() {
		inner := opts
		inner.OnLog = func(line string) {
			select {
			case ch <- donateLogMsg{line: line}:
			case <-ctx.Done():
			}
		}
		summary, err := dump.RunWithClient(ctx, client, inner)
		select {
		case ch <- donateDoneMsg{summary: summary, err: err}:
		case <-ctx.Done():
		}
		close(ch)
	}()
	return m, waitCh(ch)
}

func (m *Model) donateLog(line string) {
	m.donate.logs = append(m.donate.logs, time.Now().Format("15:04:05")+"  "+line)
	if len(m.donate.logs) > 500 {
		m.donate.logs = m.donate.logs[len(m.donate.logs)-500:]
	}
	m.donate.status = line
}

func (m *Model) donateFinished(msg donateDoneMsg) {
	m.donate.elapsed = time.Since(m.donate.started)
	if m.donate.cancel != nil {
		m.donate.cancel()
		m.donate.cancel = nil
	}
	if m.donate.qrClient != nil {
		m.donate.qrClient.Close()
		m.donate.qrClient = nil
	}
	m.donate.summary = msg.summary
	m.donate.err = msg.err
	m.donate.keySaved = false
	if _, key, err := loadShinoKey(); err == nil {
		m.donate.keySaved = key != ""
	}
	m.donate.keyInvalid = false
	m.donate.uploadDone = 0
	m.donate.uploadTotal = 0
	m.donate.pendingUpload = false
	m.stage = stageDonateDone
}

func loadShinoKey() (library.KeyStore, string, error) {
	store, err := library.NewKeyStore(library.ShinoKeyFile)
	if err != nil {
		return library.KeyStore{}, "", err
	}
	key, err := store.Load()
	if err != nil {
		return store, "", err
	}
	return store, key, nil
}

func (m *Model) resizeDonateKeyInput() {
	l := m.layout()
	m.donate.keyInput.Width = max(8, min(48, l.bodyW-10))
}

func (m Model) openDonateKey(pending bool) (tea.Model, tea.Cmd) {
	m.donate.pendingUpload = pending
	m.donate.keyErr = ""
	m.donate.keyInput.SetValue("")
	m.donate.keyInput.Placeholder = "shino_..."
	m.resizeDonateKeyInput()
	m.donate.keyInput.Focus()
	m.stage = stageDonateKey
	return m, textinput.Blink
}

func (m Model) donateKeyKey(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	switch msg.String() {
	case "esc", "tab":
		m.donate.keyInput.Blur()
		m.donate.pendingUpload = false
		m.donate.keyErr = ""
		m.stage = stageDonateDone
		return m, nil
	case "enter":
		key := strings.TrimSpace(m.donate.keyInput.Value())
		if key == "" {
			m.donate.keyErr = "paste a shinoweb api key"
			return m, nil
		}
		store, _, err := loadShinoKey()
		if err != nil {
			m.donate.keyErr = "could not open the key store: " + err.Error()
			return m, nil
		}
		if err := store.Save(key); err != nil {
			m.donate.keyErr = "could not save the key: " + err.Error()
			return m, nil
		}
		m.donate.keyInput.Blur()
		m.donate.keySaved = true
		m.donate.keyInvalid = false
		m.donate.keyErr = ""
		pending := m.donate.pendingUpload
		m.donate.pendingUpload = false
		m.stage = stageDonateDone
		if pending {
			return m.launchDonateUpload()
		}
		return m, nil
	}
	m.donate.keyErr = ""
	var cmd tea.Cmd
	m.donate.keyInput, cmd = m.donate.keyInput.Update(msg)
	return m, cmd
}

func (m Model) launchDonateUpload() (tea.Model, tea.Cmd) {
	_, apiKey, err := loadShinoKey()
	if err != nil {
		m.donate.uploadLine = "could not read the shinoweb api key: " + err.Error()
		m.donate.uploadFail = true
		return m, nil
	}
	if apiKey == "" {
		return m.openDonateKey(true)
	}
	baseURL := os.Getenv("SHINOWEB_URL")
	if baseURL == "" {
		baseURL = library.DefaultShinoWebURL
	}
	if m.donate.cancel != nil {
		m.donate.cancel()
		m.donate.cancel = nil
	}
	m.donate.uploadSeq++
	seq := m.donate.uploadSeq
	m.donate.uploading = true
	m.donate.uploadLine = ""
	m.donate.uploadFail = false
	m.donate.keySaved = true
	m.donate.keyInvalid = false
	m.donate.uploadDone = 0
	m.donate.uploadTotal = 0
	m.donateLog("uploading donation")
	ctx, cancel := context.WithCancel(context.Background())
	m.donate.cancel = cancel
	ch := make(chan tea.Msg, 256)
	m.donate.ch = ch
	archive := m.donate.summary.Archive
	go func() {
		id, err := dump.UploadDonation(ctx, dump.UploadConfig{
			BaseURL: baseURL, APIKey: apiKey, FilePath: archive,
			OnStart: func(total int) {
				select {
				case ch <- donateUploadStartMsg{seq: seq, total: total}:
				case <-ctx.Done():
				}
			},
		}, func(done, total int) {
			select {
			case ch <- donateUploadPartMsg{seq: seq, done: done, total: total}:
			case <-ctx.Done():
			}
		})
		select {
		case ch <- donateUploadedMsg{seq: seq, id: id, err: err}:
		case <-ctx.Done():
		}
		close(ch)
	}()
	return m, waitCh(ch)
}

func (m *Model) donateUploaded(msg donateUploadedMsg) {
	if msg.seq != m.donate.uploadSeq || !m.donate.uploading {
		return
	}
	m.donate.uploading = false
	if m.donate.cancel != nil {
		m.donate.cancel()
		m.donate.cancel = nil
	}
	m.donate.uploadDone = 0
	m.donate.uploadTotal = 0
	if msg.err != nil {
		m.donate.uploadFail = true
		if strings.Contains(strings.ToLower(msg.err.Error()), "unauthorized") {
			m.donate.keyInvalid = true
			m.donate.uploadLine = "shinoweb rejected the api key, press k to replace it"
		} else {
			m.donate.uploadLine = "upload failed: " + msg.err.Error()
		}
	} else {
		m.donate.uploadLine = "donation " + msg.id + " uploaded, verification runs on the site"
		m.donate.uploadFail = false
		m.donate.keyInvalid = false
	}
	m.donateLog(m.donate.uploadLine)
}

func (m Model) enterDonateQR() (tea.Model, tea.Cmd) {
	if _, err := dump.ParseAppIDList(m.donateFields.appids.Value()); err != nil {
		m.donateFields.err = "✖ " + err.Error()
		m.stage = stageDonateForm
		return m, nil
	}
	m.stage = stageDonateQR
	m.qr = qrPanel{status: "connecting to steam"}
	m.errMsg = ""
	remember := m.donateFields.remember
	ctx, cancel := context.WithCancel(context.Background())
	m.qr.cancel = cancel
	ch := make(chan tea.Msg, 16)
	m.qr.ch = ch
	go func() {
		status := func(line string) {
			select {
			case ch <- qrStatusMsg{line: line}:
			case <-ctx.Done():
			}
		}
		if client, err := dump.ConnectStored(ctx, status); err == nil {
			select {
			case ch <- qrDoneMsg{client: client}:
			case <-ctx.Done():
			}
			close(ch)
			return
		}
		client, err := dump.ConnectQR(ctx, func(url string) {
			lines, rerr := dump.RenderQR(url)
			if rerr != nil {
				lines = []string{url}
			}
			select {
			case ch <- qrChallengeMsg{lines: lines}:
			case <-ctx.Done():
			}
		}, status, func(account, refreshToken string) {
			if !remember {
				return
			}
			store := dump.NewKeyringStorePublic()
			if err := store.Save(account, refreshToken); err != nil {
				select {
				case ch <- qrStatusMsg{line: "could not remember sign-in: " + err.Error()}:
				case <-ctx.Done():
				}
				return
			}
			select {
			case ch <- qrStatusMsg{line: "sign-in saved for " + account}:
			case <-ctx.Done():
			}
		})
		select {
		case ch <- qrDoneMsg{client: client, err: err}:
		case <-ctx.Done():
		}
		close(ch)
	}()
	return m, waitCh(ch)
}

func (m Model) qrKey(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	switch msg.String() {
	case "q":
		return m.quit()
	case "esc":
		if m.qr.cancel != nil {
			m.qr.cancel()
			m.qr.cancel = nil
		}
		m.stage = stageSource
		m.glow = 0
		return m, nil
	case "enter", " ":
		if m.qr.err != "" {
			return m.enterDonateQR()
		}
	}
	return m, nil
}

func (m Model) qrDone(msg qrDoneMsg) (tea.Model, tea.Cmd) {
	if m.stage != stageDonateQR {
		if msg.client != nil {
			msg.client.Close()
		}
		return m, nil
	}
	if m.qr.cancel != nil {
		m.qr.cancel()
		m.qr.cancel = nil
	}
	if msg.err != nil || msg.client == nil {
		m.qr.err = "qr login failed"
		if msg.err != nil {
			m.qr.err = msg.err.Error()
		}
		m.qr.lines = nil
		m.qr.status = "failed"
		return m, nil
	}
	m.qr.err = ""
	filter, err := dump.ParseAppIDList(m.donateFields.appids.Value())
	if err != nil {
		m.donateFields.err = "✖ " + err.Error()
		m.stage = stageDonateForm
		return m, nil
	}
	return m.launchDonateRun(msg.client, dump.Options{
		OutputDir: strings.TrimSpace(m.donateFields.output.Value()),
		Filter:    filter,
	})
}

func (m Model) donateFormKey(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	f := &m.donateFields
	switch msg.String() {
	case "esc":
		m.stage = stageSource
		m.glow = 0
		return m, nil
	case "up":
		f.focus = max(0, f.focus-1)
		return m, f.applyFocus()
	case "down", "tab":
		f.focus = min(2, f.focus+1)
		return m, f.applyFocus()
	case " ":
		if f.focus == 2 {
			f.remember = !f.remember
			return m, nil
		}
	case "enter":
		return m.enterDonateQR()
	case "f":
		if f.focus == 2 {
			if err := dump.ForgetStoredSigninPublic(); err != nil {
				f.err = "✖ could not forget sign-in: " + err.Error()
			} else {
				f.err = "✓ stored sign-in forgotten"
			}
			return m, nil
		}
	}
	var cmd tea.Cmd
	if f.focus == 0 {
		f.err = ""
		f.output, cmd = f.output.Update(msg)
	} else if f.focus == 1 {
		f.err = ""
		f.appids, cmd = f.appids.Update(msg)
	}
	return m, cmd
}

func (m Model) donateQRBody(w int) []string {
	blank := strings.Repeat(" ", w)
	var lines []string
	lines = append(lines, blank)
	lines = append(lines, centerLine(renderGradientText("scan to sign in", float64(m.frame)*0.03), w))
	lines = append(lines, blank)
	if m.qr.err != "" {
		for _, l := range wrapText("✖ "+m.qr.err, max(8, w-8)) {
			lines = append(lines, centerLine(stRed.Render(l), w))
		}
		lines = append(lines, blank)
		lines = append(lines, helpLine(w,
			[2]string{"enter", "retry"},
			[2]string{"esc", "back"},
			[2]string{"q", "quit"}))
		return lines
	}
	if len(m.qr.lines) == 0 {
		lines = append(lines, centerLine(stPink.Render(spinnerFrames[m.frame%len(spinnerFrames)])+" "+stText.Render(m.qr.status), w))
	} else {
		for _, line := range m.qr.lines {
			lines = append(lines, centerLine(stText.Render(line), w))
		}
		lines = append(lines, blank)
		if m.qr.status == "" || m.qr.status == "waiting for approval" {
			lines = append(lines, centerLine(stMuted.Render("scan with the Steam Mobile app, then approve"), w))
		} else {
			lines = append(lines, centerLine(stPink.Render(spinnerFrames[m.frame%len(spinnerFrames)])+" "+stText.Render(m.qr.status), w))
		}
	}
	lines = append(lines, blank)
	lines = append(lines, helpLine(w,
		[2]string{"esc", "back"},
		[2]string{"q", "quit"}))
	return lines
}

func (m Model) donateFormBody(w, h int) []string {
	blank := strings.Repeat(" ", w)
	f := m.donateFields
	var lines []string
	lines = append(lines, blank)
	if h >= 16 {
		lines = append(lines, centerLine(renderGradientText("donate via qr", float64(m.frame)*0.03), w))
		lines = append(lines, blank)
	}
	bw := min(56, w-4)
	inset := strings.Repeat(" ", max(0, (w-bw)/2))
	outCol := cBorder
	if f.focus == 0 {
		outCol = lerpColor(cBorder, cPink, m.glow)
	}
	for _, l := range inputBoxLines("output dir", f.output.View(), bw, outCol, f.focus == 0) {
		lines = append(lines, inset+l)
	}
	lines = append(lines, blank)
	appCol := cBorder
	if f.focus == 1 {
		appCol = lerpColor(cBorder, cPink, m.glow)
	}
	for _, l := range inputBoxLines("appid filter", f.appids.View(), bw, appCol, f.focus == 1) {
		lines = append(lines, inset+l)
	}
	lines = append(lines, blank)
	lines = append(lines, centerLine(m.donateToggleRow(), w))
	if f.err != "" {
		style := stGreen
		if strings.HasPrefix(f.err, "✖") {
			style = stRed
		}
		lines = append(lines, blank)
		lines = append(lines, centerLine(style.Render(truncate(f.err, w-8)), w))
	}
	lines = append(lines, blank)
	lines = append(lines, helpLine(w,
		[2]string{"↑↓", "move"},
		[2]string{"space", "toggle"},
		[2]string{"enter", "sign in"},
		[2]string{"f", "forget"},
		[2]string{"esc", "back"}))
	return lines
}

func (m Model) donateToggleRow() string {
	f := m.donateFields
	box := stDim.Render("[ ]")
	if f.remember {
		box = stGreen.Render("[x]")
	}
	label := stMuted
	marker := "  "
	if f.focus == 2 {
		label = stPinkSoft
		marker = stPink.Render("▸ ")
	}
	return marker + box + " " + label.Bold(true).Render("remember sign-in in os keyring")
}

func (m Model) donateRunBody(w, h int) []string {
	blank := strings.Repeat(" ", w)
	var lines []string
	lines = append(lines, blank)
	sp := spinnerFrames[m.frame%len(spinnerFrames)]
	elapsed := time.Since(m.donate.started).Round(time.Second)
	lines = append(lines, stPink.Render(sp)+" "+stText.Render(truncate(m.donate.status, w-16))+" "+stMuted.Render(elapsed.String()))
	lines = append(lines, blank)
	logH := max(2, h-len(lines)-4)
	lines = append(lines, stBorder.Render("╭─ ")+stMuted.Render("log")+
		stBorder.Render(" "+strings.Repeat("─", max(1, w-8))+"╮"))
	visible := m.donateVisibleLog(logH)
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
	lines = append(lines, helpLine(w, [2]string{"q", "quit"}))
	return lines
}

func (m Model) donateKeyBody(w int) []string {
	blank := strings.Repeat(" ", w)
	var lines []string
	lines = append(lines, blank)
	title := "shinoweb api key"
	if m.donate.pendingUpload {
		title = "api key needed to upload"
	}
	lines = append(lines, centerLine(renderGradientText(title, float64(m.frame)*0.03), w))
	lines = append(lines, blank)
	bw := min(56, w-4)
	inset := strings.Repeat(" ", max(0, (w-bw)/2))
	for _, line := range inputBoxLines("api key", m.donate.keyInput.View(), bw, cPink, true) {
		lines = append(lines, inset+line)
	}
	lines = append(lines, blank)
	lines = append(lines, centerLine(stMuted.Render("paste a key from the shinoweb api page"), w))
	lines = append(lines, centerLine(stMuted.Render("saved locally in your shino config"), w))
	lines = append(lines, blank)
	if m.donate.keyErr != "" {
		for _, line := range wrapText("× "+m.donate.keyErr, w-8) {
			lines = append(lines, centerLine(stRed.Render(line), w))
		}
		lines = append(lines, blank)
	}
	save := "save"
	if m.donate.pendingUpload {
		save = "save and upload"
	}
	lines = append(lines, helpLine(w, [2]string{"enter", save}, [2]string{"esc", "back"}))
	return lines
}

func (m Model) donateDoneBody(w, h int) []string {
	blank := strings.Repeat(" ", w)
	var lines []string
	lines = append(lines, blank)
	phase := float64(m.frame) * 0.03
	if m.donate.err != nil {
		lines = append(lines, centerLine(stRed.Bold(true).Render("× ")+renderGradientText("failed", phase), w))
	} else {
		lines = append(lines, centerLine(stGreen.Render("✓ ")+renderGradientText("donation ready", phase), w))
	}
	lines = append(lines, blank)
	if m.donate.err != nil {
		for _, l := range wrapText("× "+m.donate.err.Error(), w-12) {
			lines = append(lines, centerLine(stRed.Render(l), w))
		}
		lines = append(lines, blank)
		tail := m.donate.logs
		if len(tail) > 6 {
			tail = tail[len(tail)-6:]
		}
		for _, l := range tail {
			lines = append(lines, centerLine(stMuted.Render(truncate(l, w-4)), w))
		}
	} else {
		if h >= 16 {
			lines = append(lines, donatePanel("donation", donateStatLines(donateStats(m.donate.summary, m.donate.elapsed), w-8), w)...)
			if m.donate.summary.FailedTransient > 0 {
				lines = append(lines, centerLine(stAmber.Render(fmt.Sprintf("%d depots failed, rerun to retry", m.donate.summary.FailedTransient)), w))
			}
		} else if h >= 12 {
			for _, line := range donateStatLines(donateStats(m.donate.summary, m.donate.elapsed), w-4) {
				lines = append(lines, centerLine(line, w))
			}
		} else {
			lines = append(lines, centerLine(stMuted.Render(truncate(donateCoreSummary(m.donate.summary, m.donate.elapsed), w-2)), w))
		}
		lines = append(lines, blank)
		lines = append(lines, m.donateKeyStatusLines(w)...)
		lines = append(lines, m.donateUploadLines(w)...)
		if h >= 16 {
			var paths []string
			if archive := m.donate.summary.Archive; archive != "" {
				paths = append(paths, stMuted.Render("archive  ")+stText.Render(truncate(filepath.Base(archive), w-12)))
			}
			if output := m.donate.summary.OutputDir; output != "" {
				paths = append(paths, stMuted.Render("folder   ")+stText.Render(tailPath(output, w-12)))
			}
			if len(paths) > 0 {
				lines = append(lines, blank)
				blockW := 0
				for _, path := range paths {
					blockW = max(blockW, lipgloss.Width(path))
				}
				for _, path := range paths {
					lines = append(lines, centerLine(path+strings.Repeat(" ", max(0, blockW-lipgloss.Width(path))), w))
				}
			}
		}
	}
	lines = append(lines, blank)
	help := [][2]string{}
	if m.donate.uploading {
		help = append(help, [2]string{"esc", "cancel"}, [2]string{"q", "quit"})
	} else if m.donate.err != nil {
		help = append(help, [2]string{"enter", "retry"}, [2]string{"esc", "back"}, [2]string{"q", "quit"})
	} else {
		help = append(help, [2]string{"enter", "again"})
		if m.donate.summary.Archive != "" {
			help = append(help, [2]string{"u", "upload"})
		}
		help = append(help, [2]string{"k", "api key"}, [2]string{"esc", "back"}, [2]string{"q", "quit"})
	}
	lines = append(lines, helpLine(w, help...))
	return lines
}

type donateStat struct {
	label string
	value string
}

func donateStats(summary dump.Summary, elapsed time.Duration) []donateStat {
	stats := []donateStat{
		{label: "apps", value: strconv.Itoa(summary.Apps)},
		{label: "depots", value: strconv.Itoa(summary.Depots)},
		{label: "manifests", value: strconv.Itoa(summary.Manifests)},
	}
	if summary.Reused > 0 {
		stats = append(stats, donateStat{label: "reused", value: strconv.Itoa(summary.Reused)})
	}
	if summary.FreeSkipped > 0 {
		stats = append(stats, donateStat{label: "free skipped", value: strconv.Itoa(summary.FreeSkipped)})
	}
	if summary.SkippedNoLicense > 0 {
		stats = append(stats, donateStat{label: "no license", value: strconv.Itoa(summary.SkippedNoLicense)})
	}
	stats = append(stats, donateStat{label: "time", value: elapsed.Round(time.Second).String()})
	return stats
}

func donateStatLines(stats []donateStat, maxW int) []string {
	labelW, valueW := 0, 0
	for _, stat := range stats {
		labelW = max(labelW, lipgloss.Width(stat.label))
		valueW = max(valueW, lipgloss.Width(stat.value))
	}
	cell := func(stat donateStat) string {
		return stMuted.Render(fmt.Sprintf("%-*s", labelW, stat.label)) + "  " +
			stText.Render(fmt.Sprintf("%-*s", valueW, stat.value))
	}
	twoCol := len(stats) > 1 && maxW >= 2*(labelW+2+valueW)+6
	if !twoCol {
		lines := make([]string, 0, len(stats))
		for _, stat := range stats {
			lines = append(lines, cell(stat))
		}
		return lines
	}
	lines := make([]string, 0, (len(stats)+1)/2)
	for i := 0; i < len(stats); i += 2 {
		line := cell(stats[i])
		if i+1 < len(stats) {
			line += "      " + cell(stats[i+1])
		}
		lines = append(lines, line)
	}
	return lines
}

func donatePanel(title string, body []string, w int) []string {
	innerW := lipgloss.Width(title) + 2
	for _, line := range body {
		innerW = max(innerW, lipgloss.Width(line))
	}
	bw := min(max(innerW+4, 30), max(20, w-2))
	inner := bw - 4
	top := stBorder.Render("╭ ") + stMuted.Render(title) +
		stBorder.Render(" "+strings.Repeat("─", max(1, bw-4-lipgloss.Width(title)))+"╮")
	lines := []string{centerLine(top, w)}
	for _, line := range body {
		lines = append(lines, centerLine(stBorder.Render("│")+" "+padLine(line, inner)+" "+stBorder.Render("│"), w))
	}
	lines = append(lines, centerLine(stBorder.Render("╰"+strings.Repeat("─", max(1, bw-2))+"╯"), w))
	return lines
}

func (m Model) donateKeyStatusLines(w int) []string {
	text := "! no shinoweb key · press k to add it, new games earn downloads"
	style := stAmber
	switch {
	case m.donate.keyInvalid:
		text = "× shinoweb key rejected · press k to replace it"
		style = stRed
	case m.donate.keySaved:
		text = "✓ shinoweb key saved · new games earn downloads"
		style = stGreen
	}
	var lines []string
	for _, line := range wrapText(text, max(8, w-4)) {
		lines = append(lines, centerLine(style.Render(line), w))
	}
	return lines
}

func (m Model) donateUploadLines(w int) []string {
	if m.donate.uploading {
		sp := spinnerFrames[m.frame%len(spinnerFrames)]
		label := "uploading donation"
		if m.donate.uploadTotal > 0 {
			label = fmt.Sprintf("uploading donation · %d/%d parts", m.donate.uploadDone, m.donate.uploadTotal)
		}
		lines := []string{centerLine(stPink.Render(sp)+" "+stText.Render(label), w)}
		if m.donate.uploadTotal > 0 {
			pct := float64(m.donate.uploadDone) / float64(m.donate.uploadTotal)
			barW := clampInt(w-24, 12, 40)
			lines = append(lines, centerLine(renderBar(pct, barW, float64(m.frame)*0.35)+"  "+
				stMuted.Render(fmt.Sprintf("%3.0f%%", pct*100)), w))
		}
		return lines
	}
	if m.donate.uploadLine != "" {
		style := stGreen
		if m.donate.uploadFail {
			style = stRed
		}
		lines := make([]string, 0, 2)
		for _, line := range wrapText(m.donate.uploadLine, max(8, w-8)) {
			lines = append(lines, centerLine(style.Render(line), w))
		}
		return lines
	}
	return nil
}

func tailPath(path string, w int) string {
	if lipgloss.Width(path) <= w {
		return path
	}
	if w < 2 {
		return "…"
	}
	runes := []rune(path)
	for len(runes) > 0 && lipgloss.Width(string(runes))+1 > w {
		runes = runes[1:]
	}
	return "…" + string(runes)
}

func donateCoreSummary(summary dump.Summary, elapsed time.Duration) string {
	return fmt.Sprintf("%d apps · %d depots · %d manifests · %s",
		summary.Apps, summary.Depots, summary.Manifests, elapsed.Round(time.Second))
}

func (m Model) donateVisibleLog(h int) []string {
	end := len(m.donate.logs)
	start := max(0, end-h)
	return m.donate.logs[start:end]
}
