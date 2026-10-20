package tui

import (
	"archive/zip"
	"bytes"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"

	"github.com/Bink-lab/Shino/internal/lua"
)

const (
	maxBundleLuaBytes           = 16 << 20
	maxBundleManifestBytes      = 256 << 20
	maxBundleManifestTotalBytes = 2 << 30
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
	if luaFiles[0].UncompressedSize64 > maxBundleLuaBytes {
		return nil, fmt.Errorf("%s: lua exceeds %d bytes", luaFiles[0].Name, maxBundleLuaBytes)
	}
	var manifestBytes uint64
	for _, f := range manifestFiles {
		if f.UncompressedSize64 >= maxBundleManifestBytes {
			return nil, fmt.Errorf("%s: manifest exceeds %d bytes", f.Name, maxBundleManifestBytes)
		}
		manifestBytes += f.UncompressedSize64
		if manifestBytes > maxBundleManifestTotalBytes {
			return nil, fmt.Errorf("zip manifests exceed %d bytes", maxBundleManifestTotalBytes)
		}
	}

	rc, err := luaFiles[0].Open()
	if err != nil {
		return nil, err
	}
	defer rc.Close()
	luaData, err := io.ReadAll(io.LimitReader(rc, maxBundleLuaBytes+1))
	if err != nil {
		return nil, err
	}
	if len(luaData) > maxBundleLuaBytes {
		return nil, fmt.Errorf("%s: lua exceeds %d bytes", luaFiles[0].Name, maxBundleLuaBytes)
	}
	cfg, err := lua.Parse(bytes.NewReader(luaData))
	if err != nil {
		return nil, fmt.Errorf("%s: %w", luaFiles[0].Name, err)
	}

	dir, err := os.MkdirTemp("", "shino-manifests-*")
	if err != nil {
		return nil, err
	}
	var extracted uint64
	for _, f := range manifestFiles {
		dest := filepath.Join(dir, filepath.Base(f.Name))
		n, err := extractZipFile(f, dest)
		if err != nil {
			os.RemoveAll(dir)
			return nil, fmt.Errorf("%s: %w", f.Name, err)
		}
		extracted += uint64(n)
		if extracted > maxBundleManifestTotalBytes {
			os.RemoveAll(dir)
			return nil, fmt.Errorf("zip manifests exceed %d bytes", maxBundleManifestTotalBytes)
		}
	}
	return &bundle{cfg: cfg, manifestsDir: dir}, nil
}

func extractZipFile(f *zip.File, dest string) (int64, error) {
	rc, err := f.Open()
	if err != nil {
		return 0, err
	}
	defer rc.Close()
	out, err := os.Create(dest)
	if err != nil {
		return 0, err
	}
	n, err := io.Copy(out, io.LimitReader(rc, maxBundleManifestBytes))
	if err != nil {
		out.Close()
		return 0, err
	}
	if n >= maxBundleManifestBytes {
		out.Close()
		return 0, fmt.Errorf("manifest exceeds %d bytes", maxBundleManifestBytes)
	}
	if f.UncompressedSize64 > 0 && uint64(n) != f.UncompressedSize64 {
		out.Close()
		return 0, fmt.Errorf("manifest size mismatch: got %d, want %d", n, f.UncompressedSize64)
	}
	if err := out.Close(); err != nil {
		return 0, err
	}
	return n, nil
}
