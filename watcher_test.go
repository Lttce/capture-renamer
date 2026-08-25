// 実ファイル操作（t.TempDir() + os パッケージ）による Watcher のユニットテスト。
// 各テストは独立したテンポラリディレクトリで実行され、テスト終了時に自動削除される。
package main

import (
	"os"
	"path/filepath"
	"runtime"
	"testing"
	"time"
)

// createFile はテンポラリディレクトリにダミーファイルを作成するヘルパー。
// テスト用に最終編集時刻を2秒前に設定し、安定チェックを通過させる。
func createFile(t *testing.T, dir, name string) {
	t.Helper()
	path := filepath.Join(dir, name)
	if err := os.WriteFile(path, []byte("x"), 0644); err != nil {
		t.Fatal(err)
	}
	past := time.Now().Add(-2 * time.Second)
	if err := os.Chtimes(path, past, past); err != nil {
		t.Fatal(err)
	}
}

// fileExists はテンポラリディレクトリ上のファイルの存在確認。
func fileExists(dir, name string) bool {
	_, err := os.Stat(filepath.Join(dir, name))
	return err == nil
}

// dirNames はディレクトリ内のファイル名一覧を返す。
// アサーション失敗時に「実際は何があったか」を出すためのヘルパー。
func dirNames(t *testing.T, dir string) []string {
	t.Helper()
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	names := make([]string, 0, len(entries))
	for _, e := range entries {
		names = append(names, e.Name())
	}
	return names
}

// 起動時スキャンが既存ファイルを既知リストに登録することを確認。
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

// 新ファイルが {prefix}_{連番}_{元ファイル名} にリネームされることを確認。
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

// 既知ファイルがポーリングで再処理されないことを確認。
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

// prefix 切替で連番が1にリセットされることを確認。
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

// SetCounter で連番を指定した値に変更できることを確認。
func TestSetCounter(t *testing.T) {
	dir := t.TempDir()
	w := NewWatcher(dir, "test")
	w.ScanExisting()

	w.SetCounter(50)
	createFile(t, dir, "shot.png")
	n, err := w.Poll()
	if err != nil {
		t.Fatal(err)
	}
	if n != 1 {
		t.Fatalf("expected 1 rename, got %d", n)
	}
	if !fileExists(dir, "test_50_shot.png") {
		t.Error("expected test_50_shot.png (counter should be 50)")
	}
}

// releaseSequence が払い出した連番を差し戻すことを確認。
func TestReleaseSequence(t *testing.T) {
	w := NewWatcher(t.TempDir(), "test")

	n := w.nextSequence()
	w.releaseSequence(n)

	if got := w.nextSequence(); got != n {
		t.Errorf("expected released sequence %d to be reused, got %d", n, got)
	}
}

// 払い出し後に SetCounter された場合、releaseSequence が指定値を上書きしないことを確認。
func TestReleaseSequenceKeepsSetCounter(t *testing.T) {
	w := NewWatcher(t.TempDir(), "test")

	n := w.nextSequence()
	w.SetCounter(50)
	w.releaseSequence(n)

	if got := w.nextSequence(); got != 50 {
		t.Errorf("SetCounter(50) should win over releaseSequence, got %d", got)
	}
}

// rename 失敗時に連番が欠番にならないことを確認。
// 監視フォルダから書き込み権限を外して rename を失敗させる。
func TestPollKeepsSequenceOnRenameFailure(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("chmod によるディレクトリ書き込み禁止が効かないためスキップ")
	}
	if os.Geteuid() == 0 {
		t.Skip("root はパーミッションを無視して rename に成功するためスキップ")
	}

	dir := t.TempDir()
	w := NewWatcher(dir, "test")
	w.ScanExisting()

	createFile(t, dir, "fail.png")
	if err := os.Chmod(dir, 0o500); err != nil {
		t.Fatal(err)
	}
	// t.TempDir() のクリーンアップが失敗しないよう権限を戻す
	t.Cleanup(func() { os.Chmod(dir, 0o700) })

	n, err := w.Poll()
	if err != nil {
		t.Fatal(err)
	}
	if n != 0 {
		t.Fatalf("expected 0 renames in a read-only dir, got %d", n)
	}

	if err := os.Chmod(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	createFile(t, dir, "ok.png")
	if _, err := w.Poll(); err != nil {
		t.Fatal(err)
	}
	// 失敗した分で連番を消費していなければ 01 から始まる
	if !fileExists(dir, "test_01_ok.png") {
		t.Error("expected test_01_ok.png (failed rename must not consume a sequence number)")
	}
}

// validatePrefix がパス区切り文字と空文字を弾き、通常の prefix を通すことを確認。
func TestValidatePrefix(t *testing.T) {
	valid := []string{"test", "test01", "項目 A", "..", "a:b*c"}
	for _, p := range valid {
		if err := validatePrefix(p); err != nil {
			t.Errorf("validatePrefix(%q) should be valid, got %v", p, err)
		}
	}

	invalid := []string{"", "../foo", "a/b", `a\b`, "/", `\`}
	for _, p := range invalid {
		if err := validatePrefix(p); err == nil {
			t.Errorf("validatePrefix(%q) should be invalid, got nil", p)
		}
	}
}

// uniqueNewName が衝突時に (1) サフィックスを付けることを確認。
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

// リネーム先ファイル名が衝突した場合、ユニーク化されてリネームされることを確認。
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

	// shot.png は衝突回避で (1) サフィックス付きの名前になる
	if !fileExists(dir, "test_01_shot (1).png") {
		t.Errorf("expected test_01_shot (1).png, got %v", dirNames(t, dir))
	}
}

// 複数ファイルが同時に現れた場合、全てリネームされることを確認。
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

	// os.ReadDir は名前順で返すため、a→01, b→02, c→03 と連番が決まる
	for _, want := range []string{"test_01_a.png", "test_02_b.png", "test_03_c.png"} {
		if !fileExists(dir, want) {
			t.Errorf("expected %s, got %v", want, dirNames(t, dir))
		}
	}
}
