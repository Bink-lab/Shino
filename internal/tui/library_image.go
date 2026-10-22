package tui

import (
	"bytes"
	"fmt"
	"image"
	_ "image/gif"
	_ "image/jpeg"
	_ "image/png"
	"strings"

	"github.com/charmbracelet/lipgloss"
	_ "golang.org/x/image/webp"
)

func thumbnailLines(data []byte, columns, rows int) []string {
	if columns <= 0 || rows <= 0 {
		return nil
	}
	const maxDimension = 4096
	cfg, _, err := image.DecodeConfig(bytes.NewReader(data))
	if err != nil || cfg.Width <= 0 || cfg.Height <= 0 || cfg.Width > maxDimension || cfg.Height > maxDimension {
		return placeholderThumbnail(columns, rows)
	}
	img, _, err := image.Decode(bytes.NewReader(data))
	if err != nil {
		return placeholderThumbnail(columns, rows)
	}
	bounds := coverBounds(img.Bounds(), columns, rows*2)
	if bounds.Dx() <= 0 || bounds.Dy() <= 0 {
		return placeholderThumbnail(columns, rows)
	}
	cellRows := rows * 2
	lines := make([]string, rows)
	for row := range rows {
		var b strings.Builder
		for col := range columns {
			top := thumbCellColor(img, bounds, col, row*2, columns, cellRows)
			bottom := thumbCellColor(img, bounds, col, row*2+1, columns, cellRows)
			b.WriteString(lipgloss.NewStyle().
				Foreground(lipgloss.Color(fmt.Sprintf("#%02x%02x%02x", top[0], top[1], top[2]))).
				Background(lipgloss.Color(fmt.Sprintf("#%02x%02x%02x", bottom[0], bottom[1], bottom[2]))).
				Render("▀"))
		}
		lines[row] = b.String()
	}
	return lines
}

func coverBounds(bounds image.Rectangle, cols, rows int) image.Rectangle {
	if cols <= 0 || rows <= 0 || bounds.Dx() <= 0 || bounds.Dy() <= 0 {
		return bounds
	}
	target := float64(cols) / float64(rows)
	source := float64(bounds.Dx()) / float64(bounds.Dy())
	if source > target {
		w := int(float64(bounds.Dy()) * target)
		if w < 1 {
			w = 1
		}
		x0 := bounds.Min.X + (bounds.Dx()-w)/2
		return image.Rect(x0, bounds.Min.Y, x0+w, bounds.Max.Y)
	}
	h := int(float64(bounds.Dx()) / target)
	if h < 1 {
		h = 1
	}
	y0 := bounds.Min.Y + (bounds.Dy()-h)/2
	return image.Rect(bounds.Min.X, y0, bounds.Max.X, y0+h)
}

func placeholderThumbnail(columns, rows int) []string {
	if columns <= 0 || rows <= 0 {
		return nil
	}
	line := stDim.Render(strings.Repeat("░", columns))
	lines := make([]string, rows)
	for i := range lines {
		lines[i] = line
	}
	return lines
}

func thumbCellColor(img image.Image, bounds image.Rectangle, col, row, cols, rows int) [3]uint8 {
	x0 := bounds.Min.X + col*bounds.Dx()/cols
	x1 := bounds.Min.X + (col+1)*bounds.Dx()/cols
	y0 := bounds.Min.Y + row*bounds.Dy()/rows
	y1 := bounds.Min.Y + (row+1)*bounds.Dy()/rows
	if x1 <= x0 {
		x1 = x0 + 1
	}
	if y1 <= y0 {
		y1 = y0 + 1
	}
	var r, g, b, n uint64
	for y := y0; y < y1; y++ {
		for x := x0; x < x1; x++ {
			cr, cg, cb, _ := img.At(x, y).RGBA()
			r += uint64(cr)
			g += uint64(cg)
			b += uint64(cb)
			n++
		}
	}
	if n == 0 {
		return [3]uint8{}
	}
	return [3]uint8{uint8(r / n >> 8), uint8(g / n >> 8), uint8(b / n >> 8)}
}
