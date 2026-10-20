package tui

import (
	"archive/zip"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"

	"github.com/Bink-lab/Shino/internal/lua"
)

type bundle struct {
	cfg          *lua.Config
	manifestsDir string
}

func loadBundle(path string) (*bundle, error) {
	zr, err := zip.OpenReader(path)
	if err != nil {
		return nil, err
	}
	defer zr.Close()

	var luaFiles []*zip.File
	var manifestFiles []*zip.File
	for _, f := range zr.File {
		if f.FileInfo().IsDir() {
			continue
		}
		switch strings.ToLower(filepath.Ext(f.Name)) {
		case ".lua":
			luaFiles = append(luaFiles, f)
		case ".manifest":
			manifestFiles = append(manifestFiles, f)
		}
	}
	switch {
	case len(luaFiles) == 0:
		return nil, fmt.Errorf("zip contains no .lua file")
	case len(luaFiles) > 1:
		return nil, fmt.Errorf("zip contains %d .lua files, expected exactly one", len(luaFiles))
	}

	rc, err := luaFiles[0].Open()
	if err != nil {
		return nil, err
	}
	defer rc.Close()
	cfg, err := lua.Parse(rc)
	if err != nil {
		return nil, fmt.Errorf("%s: %w", luaFiles[0].Name, err)
	}

	dir, err := os.MkdirTemp("", "shino-manifests-*")
	if err != nil {
		return nil, err
	}
	for _, f := range manifestFiles {
		dest := filepath.Join(dir, filepath.Base(f.Name))
		if err := extractZipFile(f, dest); err != nil {
			os.RemoveAll(dir)
			return nil, fmt.Errorf("%s: %w", f.Name, err)
		}
	}
	return &bundle{cfg: cfg, manifestsDir: dir}, nil
}

func extractZipFile(f *zip.File, dest string) error {
	rc, err := f.Open()
	if err != nil {
		return err
	}
	defer rc.Close()
	out, err := os.Create(dest)
	if err != nil {
		return err
	}
	if _, err := io.Copy(out, io.LimitReader(rc, 256<<20)); err != nil {
		out.Close()
		return err
	}
	return out.Close()
}
