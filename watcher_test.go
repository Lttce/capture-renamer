package main

import (
	"fmt"
	"os"
	"path/filepath"
	"testing"
)

type mockDirEntry struct {
	name  string
	isDir bool
}

func (m mockDirEntry) Name() string           { return m.name }
func (m mockDirEntry) IsDir() bool             { return m.isDir }
func (m mockDirEntry) Type() os.FileMode       { return 0 }
func (m mockDirEntry) Info() (os.FileInfo, error) { return nil, nil }

type MockFS struct {
	files   map[string]bool // path -> exists
	renames []struct{ old, new string }
}

func NewMockFS() *MockFS {
	return &MockFS{files: make(map[string]bool)}
}

func (m *MockFS) addFile(name string) {
	m.files[name] = true
}

func (m *MockFS) ReadDir(name string) ([]os.DirEntry, error) {
	var entries []os.DirEntry
	for path := range m.files {
		if filepath.Dir(path) == name || (name == "." && !containsSep(path)) {
			entries = append(entries, mockDirEntry{name: filepath.Base(path)})
		}
	}
	if entries == nil {
		return []os.DirEntry{}, nil
	}
	return entries, nil
}

func containsSep(s string) bool {
	for i := 0; i < len(s); i++ {
		if s[i] == filepath.Separator {
			return true
		}
	}
	return false
}

func (m *MockFS) Rename(oldPath, newPath string) error {
	if !m.files[oldPath] {
		return os.ErrNotExist
	}
	delete(m.files, oldPath)
	m.files[newPath] = true
	m.renames = append(m.renames, struct{ old, new string }{oldPath, newPath})
	return nil
}

func (m *MockFS) Stat(name string) (os.FileInfo, error) {
	if m.files[name] {
		return nil, nil
	}
	return nil, os.ErrNotExist
}

func (m *MockFS) IsNotExist(err error) bool {
	return os.IsNotExist(err)
}

func TestScanExisting(t *testing.T) {
	fs := NewMockFS()
	fs.addFile("existing_01.png")
	fs.addFile("existing_02.png")

	state := NewState("item")
	w := NewWatcher(fs, state, ".")

	if err := w.ScanExisting(); err != nil {
		t.Fatal(err)
	}

	if !state.IsKnown("existing_01.png") {
		t.Error("expected existing_01.png to be known")
	}
	if !state.IsKnown("existing_02.png") {
		t.Error("expected existing_02.png to be known")
	}
}

func TestPollRenamesNewFile(t *testing.T) {
	fs := NewMockFS()
	state := NewState("test")
	w := NewWatcher(fs, state, ".")

	fs.addFile("shot.png")

	n, err := w.Poll()
	if err != nil {
		t.Fatal(err)
	}
	if n != 1 {
		t.Fatalf("expected 1 rename, got %d", n)
	}

	if !fs.files["test_01.png"] {
		t.Error("expected test_01.png to exist after rename")
	}
	if !state.IsKnown("shot.png") {
		t.Error("expected shot.png to be known")
	}
}

func TestPollSkipsKnownFiles(t *testing.T) {
	fs := NewMockFS()
	state := NewState("test")
	w := NewWatcher(fs, state, ".")

	state.MarkKnown("known.png")
	fs.addFile("known.png")

	n, err := w.Poll()
	if err != nil {
		t.Fatal(err)
	}
	if n != 0 {
		t.Errorf("expected 0 renames, got %d", n)
	}
}

func TestPrefixSwitchResetsCounter(t *testing.T) {
	fs := NewMockFS()
	state := NewState("a")
	w := NewWatcher(fs, state, ".")

	fs.addFile("f1.png")
	w.Poll()

	state.SetPrefix("b")
	fs.addFile("f2.png")
	n, err := w.Poll()
	if err != nil {
		t.Fatal(err)
	}
	if n != 1 {
		t.Fatalf("expected 1 rename, got %d", n)
	}

	if !fs.files["b_01.png"] {
		t.Error("expected b_01.png to exist, counter should reset to 1")
	}
}

func TestUniqueNewNameOnCollision(t *testing.T) {
	fs := NewMockFS()
	fs.addFile("test_01.png")

	name := UniqueNewName(fs, "test_01.png")
	if name == "test_01.png" {
		t.Error("expected a different name when collision exists")
	}

	_, err := fs.Stat(name)
	if !fs.IsNotExist(err) {
		t.Errorf("generated name %q should not exist", name)
	}
}

func TestPollWithCollision(t *testing.T) {
	fs := NewMockFS()
	state := NewState("test")
	w := NewWatcher(fs, state, ".")

	fs.addFile("test_01.png") // pre-existing, blocks first seq
	state.MarkKnown("test_01.png")
	fs.addFile("shot.png")

	n, err := w.Poll()
	if err != nil {
		t.Fatal(err)
	}
	if n != 1 {
		t.Fatalf("expected 1 rename, got %d", n)
	}

	if fs.files["test_01.png"] && fs.files["shot.png"] {
		t.Fatal("rename did not happen")
	}

	var renamed string
	for _, r := range fs.renames {
		if r.old == "shot.png" {
			renamed = r.new
		}
	}
	if renamed == "" {
		t.Fatal("shot.png was not renamed")
	}
	if renamed == "test_01.png" {
		t.Errorf("should not overwrite existing file, got %s", renamed)
	}
	t.Logf("collision resolved: shot.png -> %s", renamed)
}

func TestPollReturnsCounts(t *testing.T) {
	fs := NewMockFS()
	state := NewState("test")
	w := NewWatcher(fs, state, ".")

	fs.addFile("a.png")
	fs.addFile("b.png")
	fs.addFile("c.png")

	n, err := w.Poll()
	if err != nil {
		t.Fatal(err)
	}
	if n != 3 {
		t.Errorf("expected 3 renames, got %d", n)
	}

	for i := 1; i <= 3; i++ {
		name := fmt.Sprintf("test_%02d.png", i)
		if !fs.files[name] {
			t.Errorf("expected %s to exist", name)
		}
	}
}
