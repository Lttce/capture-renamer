package main

import (
	"os"
	"path/filepath"
	"testing"
)

func createFile(t *testing.T, dir, name string) {
	t.Helper()
	if err := os.WriteFile(filepath.Join(dir, name), []byte("x"), 0644); err != nil {
		t.Fatal(err)
	}
}

func fileExists(dir, name string) bool {
	_, err := os.Stat(filepath.Join(dir, name))
	return err == nil
}

func TestScanExisting(t *testing.T) {
	dir := t.TempDir()
	createFile(t, dir, "a.png")
	createFile(t, dir, "b.png")

	w := NewWatcher(dir, "item")
	if err := w.ScanExisting(); err != nil {
		t.Fatal(err)
	}
	if !w.isKnown("a.png") || !w.isKnown("b.png") {
		t.Error("existing files should be known after ScanExisting")
	}
}

func TestPollRenamesNewFile(t *testing.T) {
	dir := t.TempDir()
	w := NewWatcher(dir, "test")
	w.ScanExisting()

	createFile(t, dir, "shot.png")
	n, err := w.Poll()
	if err != nil {
		t.Fatal(err)
	}
	if n != 1 {
		t.Fatalf("expected 1 rename, got %d", n)
	}
	if !fileExists(dir, "test_01_shot.png") {
		t.Error("expected test_01_shot.png to exist")
	}
	if fileExists(dir, "shot.png") {
		t.Error("original should be gone after rename")
	}
}

func TestPollSkipsKnownFiles(t *testing.T) {
	dir := t.TempDir()
	createFile(t, dir, "known.png")

	w := NewWatcher(dir, "test")
	w.ScanExisting()

	n, err := w.Poll()
	if err != nil {
		t.Fatal(err)
	}
	if n != 0 {
		t.Errorf("expected 0 renames, got %d", n)
	}
}

func TestPrefixSwitchResetsCounter(t *testing.T) {
	dir := t.TempDir()
	w := NewWatcher(dir, "a")
	w.ScanExisting()

	createFile(t, dir, "f1.png")
	w.Poll()

	w.SetPrefix("b")
	createFile(t, dir, "f2.png")
	n, err := w.Poll()
	if err != nil {
		t.Fatal(err)
	}
	if n != 1 {
		t.Fatalf("expected 1 rename, got %d", n)
	}
	if !fileExists(dir, "b_01_f2.png") {
		t.Error("expected b_01_f2.png (counter should reset to 1)")
	}
}

func TestUniqueNewNameOnCollision(t *testing.T) {
	dir := t.TempDir()
	createFile(t, dir, "test_01.png")

	name := uniqueNewName(filepath.Join(dir, "test_01.png"))
	if name == filepath.Join(dir, "test_01.png") {
		t.Error("expected different name when collision exists")
	}
	if fileExists(dir, filepath.Base(name)) {
		t.Errorf("generated name %q should not exist", name)
	}
}

func TestPollWithCollision(t *testing.T) {
	dir := t.TempDir()
	createFile(t, dir, "test_01_shot.png")

	w := NewWatcher(dir, "test")
	w.ScanExisting()

	createFile(t, dir, "shot.png")
	n, err := w.Poll()
	if err != nil {
		t.Fatal(err)
	}
	if n != 1 {
		t.Fatalf("expected 1 rename, got %d", n)
	}
	if !fileExists(dir, "test_01_shot.png") {
		t.Error("original collision file should still exist")
	}

	entries, _ := os.ReadDir(dir)
	found := false
	for _, e := range entries {
		if e.Name() != "test_01_shot.png" && len(e.Name()) > 13 && e.Name()[:13] == "test_01_shot " {
			found = true
			break
		}
	}
	if !found {
		t.Error("expected renamed file with '(1)' suffix, got:", dir)
	}
}

func TestPollReturnsCounts(t *testing.T) {
	dir := t.TempDir()
	w := NewWatcher(dir, "test")
	w.ScanExisting()

	createFile(t, dir, "a.png")
	createFile(t, dir, "b.png")
	createFile(t, dir, "c.png")

	n, err := w.Poll()
	if err != nil {
		t.Fatal(err)
	}
	if n != 3 {
		t.Errorf("expected 3 renames, got %d", n)
	}
	for _, orig := range []string{"a.png", "b.png", "c.png"} {
		if fileExists(dir, orig) {
			t.Errorf("original %s should have been renamed", orig)
		}
	}

	entries, _ := os.ReadDir(dir)
	count := 0
	for _, e := range entries {
		if len(e.Name()) > 5 && e.Name()[:5] == "test_" {
			count++
		}
	}
	if count != 3 {
		t.Errorf("expected 3 renamed files, got %d", count)
	}
}
