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
	id  string
	err error
}

type donateForm struct {
	logs       []string
	status     string
	summary    dump.Summary
	err        error
	cancel     context.CancelFunc
	ch         chan tea.Msg
	started    time.Time
	elapsed    time.Duration
	qrClient   *dump.Client
	uploading  bool
	uploadLine string
	uploadFail bool
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
	m.stage = stageDonateDone
}

func (m Model) launchDonateUpload() (tea.Model, tea.Cmd) {
	store, err := library.NewKeyStore(library.ShinoKeyFile)
	if err != nil {
		store = library.KeyStore{}
	}
	apiKey, err := store.Load()
	if err != nil || apiKey == "" {
		m.donate.uploadLine = "no shinoweb api key saved, create one on the site and press k on the shino provider"
		m.donate.uploadFail = true
		return m, nil
	}
	baseURL := os.Getenv("SHINOWEB_URL")
	if baseURL == "" {
		baseURL = library.DefaultShinoWebURL
	}
	m.donate.uploading = true
	m.donate.uploadLine = ""
	m.donate.uploadFail = false
	m.donateLog("uploading donation")
	ctx, cancel := context.WithCancel(context.Background())
	m.donate.cancel = cancel
	ch := make(chan tea.Msg, 256)
	m.donate.ch = ch
	archive := m.donate.summary.Archive
	go func() {
		id, err := dump.UploadDonation(ctx, dump.UploadConfig{
			BaseURL: baseURL, APIKey: apiKey, FilePath: archive,
		}, func(done, total int) {
			select {
			case ch <- donateLogMsg{line: fmt.Sprintf("part %d/%d", done, total)}:
			case <-ctx.Done():
			}
		})
		select {
		case ch <- donateUploadedMsg{id: id, err: err}:
		case <-ctx.Done():
		}
		close(ch)
	}()
	return m, waitCh(ch)
}

func (m *Model) donateUploaded(msg donateUploadedMsg) {
	m.donate.uploading = false
	if m.donate.cancel != nil {
		m.donate.cancel()
		m.donate.cancel = nil
	}
	if msg.err != nil {
		m.donate.uploadLine = "upload failed: " + msg.err.Error()
		m.donate.uploadFail = true
	} else {
		m.donate.uploadLine = "donation " + msg.id + " uploaded"
		m.donate.uploadFail = false
	}
	m.donateLog(m.donate.uploadLine)
}

func (m Model) enterDonateQR() (tea.Model, tea.Cmd) {
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
	return m.launchDonateRun(msg.client, dump.Options{
		OutputDir: strings.TrimSpace(m.donateFields.output.Value()),
		Filter:    dump.ParseAppIDList(m.donateFields.appids.Value()),
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
		f.output, cmd = f.output.Update(msg)
	} else if f.focus == 1 {
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

func (m Model) donateDoneBody(w int) []string {
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
		stats := renderDonateSummary(m.donate.summary, m.donate.elapsed)
		lines = append(lines, centerLine(stMuted.Render(stats), w))
		if m.donate.uploading {
			sp := spinnerFrames[m.frame%len(spinnerFrames)]
			lines = append(lines, centerLine(stPink.Render(sp)+" "+stText.Render(truncate(m.donate.status, w-8)), w))
		} else if m.donate.uploadLine != "" {
			style := stGreen
			if m.donate.uploadFail {
				style = stRed
			}
			lines = append(lines, centerLine(style.Render(truncate(m.donate.uploadLine, w-8)), w))
		} else if m.donate.summary.Archive != "" {
			lines = append(lines, centerLine(stMuted.Render("u upload "+filepath.Base(m.donate.summary.Archive)), w))
		}
		lines = append(lines, centerLine(stMuted.Render(m.donate.summary.OutputDir), w))
	}
	lines = append(lines, blank)
	help := [][2]string{{"enter", "again"}}
	if m.donate.err == nil && !m.donate.uploading && m.donate.summary.Archive != "" {
		help = append(help, [2]string{"u", "upload"})
	}
	help = append(help, [2]string{"esc", "back"}, [2]string{"q", "quit"})
	lines = append(lines, helpLine(w, help...))
	return lines
}

func renderDonateSummary(summary dump.Summary, elapsed time.Duration) string {
	parts := []string{
		strconv.Itoa(summary.Apps) + " apps",
		strconv.Itoa(summary.Depots) + " depots",
		strconv.Itoa(summary.Manifests) + " manifests",
	}
	if summary.Reused > 0 {
		parts = append(parts, strconv.Itoa(summary.Reused)+" reused")
	}
	if summary.FreeSkipped > 0 {
		parts = append(parts, strconv.Itoa(summary.FreeSkipped)+" free skipped")
	}
	if summary.SkippedNoLicense > 0 {
		parts = append(parts, strconv.Itoa(summary.SkippedNoLicense)+" no license")
	}
	if summary.FailedTransient > 0 {
		parts = append(parts, strconv.Itoa(summary.FailedTransient)+" failed, rerun to retry")
	}
	parts = append(parts, elapsed.Round(time.Second).String())
	return strings.Join(parts, " · ")
}

func (m Model) donateVisibleLog(h int) []string {
	end := len(m.donate.logs)
	start := max(0, end-h)
	return m.donate.logs[start:end]
}
