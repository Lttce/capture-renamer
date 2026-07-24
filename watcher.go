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

// nextSequence は次の連番を払い出し、カウンタを進める。
func (w *Watcher) nextSequence() int {
	w.mu.Lock()
	defer w.mu.Unlock()
	n := w.counter
	w.counter++
	return n
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

// Poll は一度フォルダをスキャンし、新ファイルをリネームする。
// リネーム後の名前は {prefix}_{連番}_{元のファイル名}.{拡張子} 形式。
// 既に同名ファイルが存在する場合は uniqueNewName で衝突を回避する。
// 戻り値はリネームしたファイル数。リネーム失敗（書き込み中など）の場合は
// 元ファイルを既知扱いにし、毎ポーリングで再試行しないようにする。
func (w *Watcher) Poll() (int, error) {
	entries, err := os.ReadDir(w.folder)
	if err != nil {
		return 0, err
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

	renamed := 0
	for _, name := range newFiles {
		seq := w.nextSequence()
		oldPath := filepath.Join(w.folder, name)

		ext := filepath.Ext(name)
		stem := strings.TrimSuffix(name, ext)
		p := w.getPrefix()
		newName := fmt.Sprintf("%s_%02d_%s%s", p, seq, stem, ext)
		newPath := uniqueNewName(filepath.Join(w.folder, newName))

		if err := os.Rename(oldPath, newPath); err != nil {
			log.Printf("rename failed: %s -> %s: %v", oldPath, newPath, err)
			w.markKnown(name)
			continue
		}

		log.Printf("renamed: %s -> %s", name, filepath.Base(newPath))
		w.markKnown(name)
		w.markKnown(filepath.Base(newPath))
		renamed++
	}

	return renamed, nil
}

// Start は interval 間隔で Poll を呼び続ける監視ループ。
// Stop() が呼ばれるまでブロックする。
func (w *Watcher) Start(interval time.Duration) {
	ticker := time.NewTicker(interval)
	defer ticker.Stop()

	for {
		select {
		case <-w.stop:
			return
		case <-ticker.C:
			if _, err := w.Poll(); err != nil {
				log.Printf("poll error: %v", err)
			}
		}
	}
}
