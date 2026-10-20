package dump

import (
	"os"
	"path/filepath"
	"testing"
)

func TestManifestCacheMissThenHit(t *testing.T) {
	c, err := newManifestCache(t.TempDir(), "76561190000000000")
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := c.load(111, 222); ok {
		t.Fatal("expected miss on empty cache")
	}
	c.store(111, 222, []byte("manifest-bytes"))
	got, ok := c.load(111, 222)
	if !ok || string(got) != "manifest-bytes" {
		t.Fatalf("load = %q, %v", got, ok)
	}
}

func TestManifestCacheAccountSwitch(t *testing.T) {
	root := t.TempDir()
	first, err := newManifestCache(root, "76561190000000000")
	if err != nil {
		t.Fatal(err)
	}
	first.store(111, 222, []byte("one"))
	second, err := newManifestCache(root, "76561190000000001")
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := second.load(111, 222); ok {
		t.Fatal("expected cache reset on account switch")
	}
}

func TestManifestCacheCorruptEntryIsMiss(t *testing.T) {
	root := t.TempDir()
	c, err := newManifestCache(root, "76561190000000000")
	if err != nil {
		t.Fatal(err)
	}
	c.store(111, 222, []byte("good"))
	path := filepath.Join(root, "manifests", "111_222")
	if err := os.Remove(path); err != nil {
		t.Fatal(err)
	}
	if err := os.Mkdir(path, 0o755); err != nil {
		t.Fatal(err)
	}
	if _, ok := c.load(111, 222); ok {
		t.Fatal("expected miss on unreadable cache entry")
	}
}
