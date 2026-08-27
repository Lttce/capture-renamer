// 設定定数、Watcher 構造体、リネーム処理までを一つのファイルに集約。
// このツールの実質的な中身。
package main

import (
	"fmt"
	"log"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"
)

// ---- Watcher ----

// Watcher はフォルダ監視と自動リネームを担当する。
// Poll() と SetPrefix() が別 goroutine で同時に呼ばれるため、
// prefix, counter, knownFiles は mutex で保護する。
type Watcher struct {
	mu         sync.Mutex
	folder     string          // 監視対象フォルダのパス
	prefix     string          // 現在のリネームprefix（stdinから変更可能）
	counter    int             // 次に発行する連番
	knownFiles map[string]bool // 既に存在する／処理済みのファイル名
	stop       chan struct{}   // Start() ループを停止するための通知チャネル
}

func NewWatcher(folder, initialPrefix string) *Watcher {
	return &Watcher{
		folder:     folder,
		prefix:     initialPrefix,
		counter:    1,
		knownFiles: make(map[string]bool),
		stop:       make(chan struct{}),
	}
}

// Stop は監視ループに停止を通知する。
func (w *Watcher) Stop() {
	close(w.stop)
}

// SetPrefix は prefix を変更し、連番を1にリセットする。
func (w *Watcher) SetPrefix(p string) {
	w.mu.Lock()
	defer w.mu.Unlock()
	w.prefix = p
	w.counter = 1
}

// SetCounter は連番を指定した値に設定する（prefixは変更しない）。
func (w *Watcher) SetCounter(n int) {
	w.mu.Lock()
	defer w.mu.Unlock()
	w.counter = n
}

func (w *Watcher) getPrefix() string {
	w.mu.Lock()
	defer w.mu.Unlock()
	return w.prefix
}

// Status は表示用に現在の prefix と次に払い出す連番を返す。
func (w *Watcher) Status() (prefix string, nextSeq int) {
	w.mu.Lock()
	defer w.mu.Unlock()
	return w.prefix, w.counter
}

// nextSequence は次の連番を払い出し、カウンタを進める。
func (w *Watcher) nextSequence() int {
	w.mu.Lock()
	defer w.mu.Unlock()
	n := w.counter
	w.counter++
	return n
}

// releaseSequence は nextSequence で払い出した連番 n を差し戻す。
// rename に失敗してファイル名に使われなかった番号を欠番にしないため。
// 払い出し後に SetCounter で値が変わっていた場合は、ユーザー指定を
// 上書きしないよう何もしない。
func (w *Watcher) releaseSequence(n int) {
	w.mu.Lock()
	defer w.mu.Unlock()
	if w.counter == n+1 {
		w.counter = n
	}
}

// isKnown はファイル名が既知（リネーム処理済み or 起動時から存在）か判定する。
func (w *Watcher) isKnown(name string) bool {
	w.mu.Lock()
	defer w.mu.Unlock()
	return w.knownFiles[name]
}

// markKnown はファイル名を既知リストに追加する。
func (w *Watcher) markKnown(name string) {
	w.mu.Lock()
	defer w.mu.Unlock()
	w.knownFiles[name] = true
}

// validatePrefix は prefix としてファイル名に埋め込める文字列か検証する。
// パス区切り文字を許すと filepath.Join がパスとして解決してしまい、
// `../foo` のように監視フォルダ外へファイルが出たり、存在しないディレクトリへの
// rename が失敗したりするため、ここで弾く。
func validatePrefix(p string) error {
	if p == "" {
		return fmt.Errorf("prefix must not be empty")
	}
	if strings.ContainsAny(p, `/\`) {
		return fmt.Errorf(`prefix must not contain path separators (/ or \)`)
	}
	return nil
}

// uniqueNewName は base が既に存在する場合、末尾に (1), (2), ... を付与して
// 衝突しないファイル名を返す。存在しなければ base をそのまま返す。
func uniqueNewName(base string) string {
	if _, err := os.Stat(base); os.IsNotExist(err) {
		return base
	}
	ext := filepath.Ext(base)
	stem := strings.TrimSuffix(base, ext)
	for i := 1; ; i++ {
		name := fmt.Sprintf("%s (%d)%s", stem, i, ext)
		if _, err := os.Stat(name); os.IsNotExist(err) {
			return name
		}
	}
}

// ScanExisting は起動時に既存ファイルを全て既知リストに追加する。
// これにより、既にあるファイルをリネーム対象から除外する。
func (w *Watcher) ScanExisting() error {
	entries, err := os.ReadDir(w.folder)
	if err != nil {
		return err
	}
	for _, e := range entries {
		if !e.IsDir() {
			w.markKnown(e.Name())
		}
	}
	return nil
}

// Rename は1件のリネーム結果。Err が非 nil なら失敗で、New は空になる。
// 表示（TUI）とログ（-plain）の両方がこれを使う。
type Rename struct {
	Old string // リネーム前のファイル名
	New string // リネーム後のファイル名
	Err error  // リネームに失敗した理由
}

// Poll は一度フォルダをスキャンし、新ファイルをリネームする。
// リネーム後の名前は {prefix}_{連番}_{元のファイル名}.{拡張子} 形式。
// 既に同名ファイルが存在する場合は uniqueNewName で衝突を回避する。
// 戻り値は処理したファイルの一覧（成功・失敗の両方を含む）。
// リネーム失敗（書き込み中など）の場合は元ファイルを既知扱いにし、
// 毎ポーリングで再試行しないようにする。失敗時は払い出した連番も
// 差し戻すため、番号は欠けない。
//
// 画面を壊さないため、ここではログを出さず結果を返すだけにする。
// ログ出力は -plain モードの Start が担当する。
func (w *Watcher) Poll() ([]Rename, error) {
	entries, err := os.ReadDir(w.folder)
	if err != nil {
		return nil, err
	}

	// 既知リストになく、最終編集から1秒以上経過したファイル = 安定した新ファイル
	var newFiles []string
	for _, e := range entries {
		if e.IsDir() || w.isKnown(e.Name()) {
			continue
		}
		info, err := e.Info()
		if err != nil {
			continue
		}
		if time.Since(info.ModTime()) < 1*time.Second {
			continue // 書き込み中とみなしてスキップ
		}
		newFiles = append(newFiles, e.Name())
	}

	var results []Rename
	for _, name := range newFiles {
		seq := w.nextSequence()
		oldPath := filepath.Join(w.folder, name)

		ext := filepath.Ext(name)
		stem := strings.TrimSuffix(name, ext)
		p := w.getPrefix()
		newName := fmt.Sprintf("%s_%02d_%s%s", p, seq, stem, ext)
		newPath := uniqueNewName(filepath.Join(w.folder, newName))

		if err := os.Rename(oldPath, newPath); err != nil {
			w.releaseSequence(seq)
			w.markKnown(name)
			results = append(results, Rename{Old: name, Err: err})
			continue
		}

		w.markKnown(name)
		w.markKnown(filepath.Base(newPath))
		results = append(results, Rename{Old: name, New: filepath.Base(newPath)})
	}

	return results, nil
}

// Retag は既にリネーム済みのファイル群を、新しい prefix で付け直す。
// 連番の変え忘れに気づいた時に、撮り直さずに直せるようにするためのもの。
//
// items は新しい順（先頭が最新）で渡す。連番は古い順に 1 から振り直すので、
// items の並び順がそのまま新しい連番の順になる。
// 戻り値は items と同じ順・同じ長さの一覧（付け替えに成功した分は New が新しい
// 名前に変わる）、付け替えた件数、失敗の理由。
//
// 元々リネームに失敗している要素（Err 付き）は対象外。ファイルが既に消えていた
// 場合などは、その1件だけを失敗として飛ばし、残りは処理を続ける。
func (w *Watcher) Retag(items []Rename, prefix string) ([]Rename, int, []error) {
	updated := make([]Rename, len(items))
	copy(updated, items)

	var errs []error
	seq := 1
	// 古い順（末尾）から連番を振り直す
	for i := len(items) - 1; i >= 0; i-- {
		r := items[i]
		if r.Err != nil || r.New == "" {
			continue
		}

		newName := fmt.Sprintf("%s_%02d_%s", prefix, seq, r.Old)
		newPath := uniqueNewName(filepath.Join(w.folder, newName))
		if err := os.Rename(filepath.Join(w.folder, r.New), newPath); err != nil {
			errs = append(errs, fmt.Errorf("%s: %w", r.New, err))
			continue
		}

		base := filepath.Base(newPath)
		w.markKnown(base)
		updated[i].New = base
		seq++
	}
	return updated, seq - 1, errs
}

// Renamed は成功したリネームの件数を返す。
func Renamed(results []Rename) int {
	n := 0
	for _, r := range results {
		if r.Err == nil {
			n++
		}
	}
	return n
}

// Start は interval 間隔で Poll を呼び続ける監視ループ。
// 結果を標準ログに出すため、TUI ではなく -plain モードから使う。
// Stop() が呼ばれるまでブロックする。
func (w *Watcher) Start(interval time.Duration) {
	ticker := time.NewTicker(interval)
	defer ticker.Stop()

	for {
		select {
		case <-w.stop:
			return
		case <-ticker.C:
			results, err := w.Poll()
			if err != nil {
				log.Printf("poll error: %v", err)
				continue
			}
			for _, r := range results {
				if r.Err != nil {
					log.Printf("rename failed: %s: %v", r.Old, r.Err)
					continue
				}
				log.Printf("renamed: %s -> %s", r.Old, r.New)
			}
		}
	}
}
