package lua

import (
	"bufio"
	"io"
	"os"
	"regexp"
	"strconv"
	"strings"
)

type Depot struct {
	ID          uint32
	Key         string
	ManifestID  uint64
	HasManifest bool
	OS          string
}

type Config struct {
	AppID  uint32
	Depots []Depot
}

var (
	addAppIDRx    = regexp.MustCompile(`(?i)addappid\s*\(\s*(\d+)\s*(?:,\s*\d+\s*(?:,\s*"([0-9a-fA-F]+)"\s*)?)?\)`)
	setManifestRx = regexp.MustCompile(`(?i)setmanifestid\s*\(\s*(\d+)\s*,\s*"(\d+)"\s*(?:,\s*\d+\s*)?\)`)
	setOSRx       = regexp.MustCompile(`(?i)setoslist\s*\(\s*(\d+)\s*,\s*"([^"]*)"\s*\)`)
	pinRx         = regexp.MustCompile(`(?i)pinapp\s*\(\s*(\d+)\s*\)`)
)

func Parse(r io.Reader) (*Config, error) {
	cfg := &Config{}
	byID := map[uint32]*Depot{}
	order := []uint32{}
	ensure := func(id uint32) *Depot {
		d, ok := byID[id]
		if !ok {
			d = &Depot{ID: id}
			byID[id] = d
			order = append(order, id)
		}
		if cfg.AppID == 0 {
			cfg.AppID = id
		}
		return d
	}
	scanner := bufio.NewScanner(r)
	scanner.Buffer(make([]byte, 1024*1024), 1024*1024)
	for scanner.Scan() {
		line := stripComment(scanner.Text())
		if m := addAppIDRx.FindStringSubmatch(line); m != nil {
			id := mustUint32(m[1])
			d := ensure(id)
			if len(m) > 2 && m[2] != "" {
				d.Key = strings.ToLower(m[2])
			}
			continue
		}
		if m := setManifestRx.FindStringSubmatch(line); m != nil {
			d := ensure(mustUint32(m[1]))
			d.ManifestID = mustUint64(m[2])
			d.HasManifest = true
			continue
		}
		if m := setOSRx.FindStringSubmatch(line); m != nil {
			d := ensure(mustUint32(m[1]))
			d.OS = strings.TrimSpace(m[2])
			continue
		}
		if m := pinRx.FindStringSubmatch(line); m != nil {
			ensure(mustUint32(m[1]))
		}
	}
	if err := scanner.Err(); err != nil {
		return nil, err
	}
	for _, id := range order {
		d := byID[id]
		if id == cfg.AppID && (d.Key == "" || !d.HasManifest) {
			continue
		}
		cfg.Depots = append(cfg.Depots, *d)
	}
	return cfg, nil
}

func SplitOSList(raw string) []string {
	parts := strings.FieldsFunc(strings.ToLower(raw), func(r rune) bool {
		return r == ',' || r == ';' || r == '|' || r == ' ' || r == '+'
	})
	seen := map[string]bool{}
	out := make([]string, 0, len(parts))
	for _, p := range parts {
		p = strings.TrimSpace(p)
		switch p {
		case "mac", "osx", "macosx":
			p = "macos"
		case "win", "win32", "win64", "windows32", "windows64":
			p = "windows"
		case "linux32", "linux64":
			p = "linux"
		}
		if p != "windows" && p != "macos" && p != "linux" {
			continue
		}
		if !seen[p] {
			seen[p] = true
			out = append(out, p)
		}
	}
	return out
}

func NormalizeOSList(raw string) string {
	return strings.Join(SplitOSList(raw), ",")
}

func stripComment(line string) string {
	inString := false
	for i := 0; i+1 < len(line); i++ {
		c := line[i]
		if c == '"' {
			inString = !inString
		}
		if !inString && c == '-' && line[i+1] == '-' {
			return line[:i]
		}
	}
	return line
}

func mustUint32(s string) uint32 {
	n, _ := strconv.ParseUint(s, 10, 32)
	return uint32(n)
}

func mustUint64(s string) uint64 {
	n, _ := strconv.ParseUint(s, 10, 64)
	return n
}

func LoadFile(path string) (*Config, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	return Parse(f)
}
