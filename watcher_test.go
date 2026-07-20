// MockFS を使った Watcher のユニットテスト。
// MockFS は map で仮想的なファイルシステムを模擬し、実ファイルI/Oなしで
// 各シナリオ（新ファイル検出、衝突回避、prefix切替 etc.）を検証する。
package main

import (
	"os"
	"path/filepath"
	"testing"
)

// ---- MockFS ----

type mockDirEntry struct {
	name  string
	isDir bool
}

func (m mockDirEntry) Name() string               { return m.name }
func (m mockDirEntry) IsDir() bool                 { return m.isDir }
func (m mockDirEntry) Type() os.FileMode           { return 0 }
func (m mockDirEntry) Info() (os.FileInfo, error)  { return nil, nil }

// MockFS は map[string]bool でファイル名→存在有無を管理する簡易ファイルシステム。
type MockFS struct {
	files   map[string]bool
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

// ---- テストケース ----

// 起動時スキャンが既存ファイルを既知リストに登録することを確認。
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

// 新ファイルが {prefix}_{seq}.{ext} にリネームされることを確認。
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

	if !fs.files["test_01_shot.png"] {
		t.Error("expected test_01_shot.png to exist after rename")
	}
	if !state.IsKnown("shot.png") {
		t.Error("expected shot.png to be known")
	}
}

// 既知ファイルがポーリングで再処理されないことを確認。
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

// prefix切替後、新ファイルには新しいprefixが付与されることを確認。
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

	// b_01_f2 になっていればprefixが切り替わり連番がリセットされた証拠
	if !fs.files["b_01_f2.png"] {
		t.Error("expected b_01_f2.png to exist (prefix_連番_元ファイル名)")
	}
}

// UniqueNewName が衝突時に (1) サフィックスを付けることを確認。
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

// リネーム先ファイル名が衝突した場合、ユニーク化されてリネームされることを確認。
func TestPollWithCollision(t *testing.T) {
	fs := NewMockFS()
	state := NewState("test")
	w := NewWatcher(fs, state, ".")

	fs.addFile("test_01_shot.png") // 事前に存在 → test_01_shot.png をブロック
	state.MarkKnown("test_01_shot.png")
	fs.addFile("shot.png")

	n, err := w.Poll()
	if err != nil {
		t.Fatal(err)
	}
	if n != 1 {
		t.Fatalf("expected 1 rename, got %d", n)
	}

	if fs.files["test_01_shot.png"] && fs.files["shot.png"] {
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
	if renamed == "test_01_shot.png" {
		t.Errorf("should not overwrite existing file, got %s", renamed)
	}
	t.Logf("collision resolved: shot.png -> %s", renamed)
}

// 複数ファイルが同時に現れた場合、元のファイル名が保持されることを確認。
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

	// 元のファイルが全てリネームされていることだけ確認（連番の割当順は非決定的）
	for _, orig := range []string{"a.png", "b.png", "c.png"} {
		if fs.files[orig] {
			t.Errorf("original %s should have been renamed", orig)
		}
	}
	if len(fs.renames) != 3 {
		t.Errorf("expected 3 renames, got %d", len(fs.renames))
	}
}
