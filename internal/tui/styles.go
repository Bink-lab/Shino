package tui

import (
	"math"
	"strings"

	"github.com/charmbracelet/lipgloss"
	"github.com/charmbracelet/x/ansi"
	"github.com/lucasb-eyer/go-colorful"
)

var (
	cPink     = lipgloss.Color("#ff5fa2")
	cPinkSoft = lipgloss.Color("#ff9ecb")
	cText     = lipgloss.Color("#efe7f5")
	cMuted    = lipgloss.Color("#8a7f96")
	cDim      = lipgloss.Color("#5b5266")
	cBorder   = lipgloss.Color("#6b5a80")
	cAmber    = lipgloss.Color("#f0b06a")
	cRed      = lipgloss.Color("#ff5c7a")
	cGreen    = lipgloss.Color("#8be9a8")
	cInk      = lipgloss.Color("#1a1224")
	cTitleBg  = lipgloss.Color("#2b1f3a")
)

var gradStops = []string{
	"#ff9ecb", "#ff5fa2", "#c86dd7", "#7b6cff", "#c86dd7", "#ff5fa2", "#ff9ecb",
}

var (
	stTitleName  = lipgloss.NewStyle().Foreground(cPinkSoft).Background(cTitleBg).Bold(true)
	stTitleMeta  = lipgloss.NewStyle().Foreground(cMuted).Background(cTitleBg)
	stTitleGlyph = lipgloss.NewStyle().Foreground(cPink).Background(cTitleBg)
	stTitleFill  = lipgloss.NewStyle().Background(cTitleBg)
	stCtrl       = lipgloss.NewStyle().Foreground(cMuted).Background(cTitleBg)
	stCtrlHot    = lipgloss.NewStyle().Foreground(cInk).Background(cPinkSoft).Bold(true)

	stBorder   = lipgloss.NewStyle().Foreground(cBorder)
	stText     = lipgloss.NewStyle().Foreground(cText)
	stMuted    = lipgloss.NewStyle().Foreground(cMuted)
	stDim      = lipgloss.NewStyle().Foreground(cDim)
	stPink     = lipgloss.NewStyle().Foreground(cPink)
	stPinkSoft = lipgloss.NewStyle().Foreground(cPinkSoft)
	stAmber    = lipgloss.NewStyle().Foreground(cAmber)
	stRed      = lipgloss.NewStyle().Foreground(cRed)
	stGreen    = lipgloss.NewStyle().Foreground(cGreen)

	stPrompt      = lipgloss.NewStyle().Foreground(cPink).Bold(true)
	stPlaceholder = lipgloss.NewStyle().Foreground(cDim)
	stChipOn      = lipgloss.NewStyle().Foreground(cInk).Background(cPink).Bold(true)
	stChipDone    = lipgloss.NewStyle().Foreground(cPinkSoft)
	stChipOff     = lipgloss.NewStyle().Foreground(cDim)
	stKey         = lipgloss.NewStyle().Foreground(cPink).Bold(true)
	stKeyDesc     = lipgloss.NewStyle().Foreground(cDim)
)

var spinnerFrames = []string{"⠋", "⠙", "⠹", "⠸", "⠼", "⠴", "⠦", "⠧", "⠇", "⠏"}

var bannerS = []string{"███████", "██     ", "███████", "     ██", "███████"}
var bannerH = []string{"██   ██", "██   ██", "███████", "██   ██", "██   ██"}
var bannerI = []string{"██", "██", "██", "██", "██"}
var bannerN = []string{"███    ██", "████   ██", "██ ██  ██", "██  ██ ██", "██   ████"}
var bannerO = []string{" ██████ ", "██    ██", "██    ██", "██    ██", " ██████ "}

func bannerLines() []string {
	letters := [][]string{bannerS, bannerH, bannerI, bannerN, bannerO}
	out := make([]string, 5)
	for r := range out {
		parts := make([]string, len(letters))
		for i, letter := range letters {
			parts[i] = letter[r]
		}
		out[r] = strings.Join(parts, " ")
	}
	return out
}

func renderBanner(phase float64) string {
	lines := bannerLines()
	width := float64(lipgloss.Width(lines[0]))
	var b strings.Builder
	for r, line := range lines {
		for x, ch := range line {
			if ch == ' ' {
				b.WriteByte(' ')
				continue
			}
			t := float64(x)/width*0.8 + float64(r)*0.07 + phase
			b.WriteString(lipgloss.NewStyle().Foreground(gradAt(t)).Render(string(ch)))
		}
		if r < len(lines)-1 {
			b.WriteByte('\n')
		}
	}
	return b.String()
}

func renderGradientText(s string, phase float64) string {
	var b strings.Builder
	width := float64(len([]rune(s)))
	for i, ch := range s {
		t := float64(i)/width + phase
		b.WriteString(lipgloss.NewStyle().Foreground(gradAt(t)).Bold(true).Render(string(ch)))
	}
	return b.String()
}

func gradAt(t float64) lipgloss.Color {
	t -= math.Floor(t)
	n := len(gradStops) - 1
	pos := t * float64(n)
	i := min(int(pos), n-1)
	c1, _ := colorful.Hex(gradStops[i])
	c2, _ := colorful.Hex(gradStops[i+1])
	return lipgloss.Color(c1.BlendRgb(c2, pos-float64(i)).Hex())
}

func lerpColor(a, b lipgloss.Color, t float64) lipgloss.Color {
	if t <= 0 {
		return a
	}
	if t >= 1 {
		return b
	}
	c1, _ := colorful.Hex(string(a))
	c2, _ := colorful.Hex(string(b))
	return lipgloss.Color(c1.BlendRgb(c2, t).Hex())
}

func easeOutCubic(t float64) float64 {
	if t <= 0 {
		return 0
	}
	if t >= 1 {
		return 1
	}
	return 1 - math.Pow(1-t, 3)
}

func clampF(v, lo, hi float64) float64 {
	return math.Min(hi, math.Max(lo, v))
}

func clampInt(v, lo, hi int) int {
	if hi < lo {
		return lo
	}
	return min(hi, max(lo, v))
}

func padLine(s string, w int) string {
	sw := lipgloss.Width(s)
	if sw > w {
		return ansi.Truncate(s, w, "…")
	}
	if sw < w {
		return s + strings.Repeat(" ", w-sw)
	}
	return s
}

func centerLine(s string, w int) string {
	sw := lipgloss.Width(s)
	if sw >= w {
		return s
	}
	return strings.Repeat(" ", (w-sw)/2) + s
}

func truncate(s string, w int) string {
	if lipgloss.Width(s) <= w {
		return s
	}
	return ansi.Truncate(s, w, "…")
}

func helpLine(w int, pairs ...[2]string) string {
	parts := make([]string, len(pairs))
	for i, p := range pairs {
		parts[i] = stKey.Render(p[0]) + " " + stKeyDesc.Render(p[1])
	}
	return centerLine(strings.Join(parts, stDim.Render("  ·  ")), w)
}

func inputBoxLines(label, view string, bw int, border lipgloss.Color, focused bool) []string {
	bs := lipgloss.NewStyle().Foreground(border)
	ls := stMuted
	if focused {
		ls = lipgloss.NewStyle().Foreground(cPinkSoft).Bold(true)
	}
	inner := bw - 4
	if lipgloss.Width(view) > inner {
		view = truncate(view, inner)
	}
	top := bs.Render("╭ ") + ls.Render(label) +
		bs.Render(" "+strings.Repeat("─", max(1, bw-4-lipgloss.Width(label)))+"╮")
	mid := bs.Render("│") + " " + padLine(view, inner) + " " + bs.Render("│")
	bot := bs.Render("╰" + strings.Repeat("─", bw-2) + "╯")
	return []string{top, mid, bot}
}

func renderBar(pct float64, width int, pulse float64) string {
	filled := int(pct*float64(width) + 0.5)
	if filled > width {
		filled = width
	}
	var b strings.Builder
	for i := range width {
		if i < filled {
			t := float64(i) / float64(width)
			col := gradAt(t * 0.8)
			if i == filled-1 {
				col = gradAt(t*0.8 + 0.15 + 0.1*math.Sin(pulse))
			}
			b.WriteString(lipgloss.NewStyle().Foreground(col).Render("▰"))
		} else {
			b.WriteString(stDim.Render("▱"))
		}
	}
	return b.String()
}
