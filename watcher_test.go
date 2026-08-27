// 実ファイル操作（t.TempDir() + os パッケージ）による Watcher のユニットテスト。
// 各テストは独立したテンポラリディレクトリで実行され、テスト終了時に自動削除される。
package main

import (
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
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

// createFileAsync は createFile のサブ goroutine 版。
// t.Fatal はテスト goroutine からしか呼べないため、失敗は t.Errorf で報告する。
func createFileAsync(t *testing.T, dir, name string) {
	t.Helper()
	path := filepath.Join(dir, name)
	if err := os.WriteFile(path, []byte("x"), 0644); err != nil {
		t.Errorf("write %s: %v", name, err)
		return
	}
	past := time.Now().Add(-2 * time.Second)
	if err := os.Chtimes(path, past, past); err != nil {
		t.Errorf("chtimes %s: %v", name, err)
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
	results, err := w.Poll()
	if err != nil {
		t.Fatal(err)
	}
	if n := Renamed(results); n != 1 {
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

	results, err := w.Poll()
	if err != nil {
		t.Fatal(err)
	}
	if n := Renamed(results); n != 0 {
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
	results, err := w.Poll()
	if err != nil {
		t.Fatal(err)
	}
	if n := Renamed(results); n != 1 {
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
	results, err := w.Poll()
	if err != nil {
		t.Fatal(err)
	}
	if n := Renamed(results); n != 1 {
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

	results, err := w.Poll()
	if err != nil {
		t.Fatal(err)
	}
	if n := Renamed(results); n != 0 {
		t.Fatalf("expected 0 renames in a read-only dir, got %d", n)
	}
	// 失敗も結果に載る（Err 付き・New は空）。TUI がこれを見て赤字で出す。
	if len(results) != 1 || results[0].Err == nil || results[0].Old != "fail.png" || results[0].New != "" {
		t.Fatalf("expected one failed rename for fail.png, got %+v", results)
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
	results, err := w.Poll()
	if err != nil {
		t.Fatal(err)
	}
	if n := Renamed(results); n != 1 {
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

	results, err := w.Poll()
	if err != nil {
		t.Fatal(err)
	}
	if n := Renamed(results); n != 3 {
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

// 本番と同じ goroutine 構成（Start のポーリング × stdin 側の SetPrefix/SetCounter）を
// 実際に走らせ、共有状態への競合を -race で検出させる。
// 他のテストは全て単一 goroutine で Poll を直接呼ぶため、この経路を踏むのはここだけ。
//
// 目的は -race による競合検出なので結果の中身は検証しないが、
// 「1件もリネームされないまま素通りして通過した」状態を防ぐため
// 最後にリネームが実際に起きたことだけ確認する。
func TestConcurrentPollAndPrefixSwitch(t *testing.T) {
	const numFiles = 100

	dir := t.TempDir()
	w := NewWatcher(dir, "test")
	if err := w.ScanExisting(); err != nil {
		t.Fatal(err)
	}

	// 監視ループ。Stop 後に確実に抜けたことを待つため done で同期する。
	done := make(chan struct{})
	go func() {
		defer close(done)
		w.Start(1 * time.Millisecond)
	}()

	var wg sync.WaitGroup

	// stdin 相当: prefix と連番を切り替え続ける。
	// ポーリングと重なるよう、ファイル供給と同程度の時間をかける。
	wg.Add(1)
	go func() {
		defer wg.Done()
		for i := 0; i < 1000; i++ {
			w.SetPrefix(fmt.Sprintf("p%d", i))
			w.SetCounter(i%50 + 1)
			time.Sleep(100 * time.Microsecond)
		}
	}()

	// リネーム対象を供給する（mtime は2秒前なので即座に対象になる）
	wg.Add(1)
	go func() {
		defer wg.Done()
		for i := 0; i < numFiles; i++ {
			createFileAsync(t, dir, fmt.Sprintf("f%04d.png", i))
			time.Sleep(1 * time.Millisecond)
		}
	}()

	wg.Wait()

	// 供給したファイルがリネームされきるまで待つ
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) && countUnrenamed(t, dir) > 0 {
		time.Sleep(5 * time.Millisecond)
	}

	w.Stop()
	<-done // Poll 実行中に t.TempDir() が消えないよう待つ

	if remaining := countUnrenamed(t, dir); remaining > 0 {
		t.Errorf("%d/%d files left unrenamed after deadline", remaining, numFiles)
	}
	if renamed := numFiles - countUnrenamed(t, dir); renamed == 0 {
		t.Fatal("no file was renamed; the concurrent path was never exercised")
	}
}

// countUnrenamed は元の名前（f0000.png 形式）のままのファイル数を返す。
// リネーム後は prefix と連番が前置されるため、先頭が "f" のものが未処理。
func countUnrenamed(t *testing.T, dir string) int {
	t.Helper()
	n := 0
	for _, name := range dirNames(t, dir) {
		if strings.HasPrefix(name, "f") {
			n++
		}
	}
	return n
}
