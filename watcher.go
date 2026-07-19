// フォルダ監視と自動リネームのロジック。
// ファイル操作は FileSystem interface 経由で行うため、本番（OSFileSystem）と
// テスト（MockFS）を差し替え可能。
package main

import (
	"fmt"
	"log"
	"path/filepath"
	"time"
)

type Watcher struct {
	fs     FileSystem
	state  *State
	folder string
	stop   chan struct{}
}

func NewWatcher(fs FileSystem, state *State, folder string) *Watcher {
	return &Watcher{
		fs:     fs,
		state:  state,
		folder: folder,
		stop:   make(chan struct{}),
	}
}

// Stop は監視ループに停止を通知する。
func (w *Watcher) Stop() {
	close(w.stop)
}

// ScanExisting は起動時に既存ファイルを全て既知リストに追加する。
// これにより、既にあるファイルをリネーム対象から除外する。
func (w *Watcher) ScanExisting() error {
	entries, err := w.fs.ReadDir(w.folder)
	if err != nil {
		return err
	}
	for _, e := range entries {
		if !e.IsDir() {
			w.state.MarkKnown(e.Name())
		}
	}
	return nil
}

// Poll は一度フォルダをスキャンし、新ファイルをリネームする。
// リネーム後の名前は {prefix}_{02d}.{ext} 形式。
// 既に同名ファイルが存在する場合は UniqueNewName で衝突を回避する。
// 戻り値はリネームしたファイル数とエラー。
func (w *Watcher) Poll() (int, error) {
	entries, err := w.fs.ReadDir(w.folder)
	if err != nil {
		return 0, err
	}

	// 既知リストにない = 新ファイル
	var newFiles []string
	for _, e := range entries {
		if !e.IsDir() && !w.state.IsKnown(e.Name()) {
			newFiles = append(newFiles, e.Name())
		}
	}

	renamed := 0
	for _, name := range newFiles {
		seq := w.state.NextSequence()
		oldPath := filepath.Join(w.folder, name)

		ext := filepath.Ext(name)
		prefix := w.state.Prefix()
		newName := fmt.Sprintf("%s_%02d%s", prefix, seq, ext)
		newPath := filepath.Join(w.folder, newName)

		newPath = UniqueNewName(w.fs, newPath)

		if err := w.fs.Rename(oldPath, newPath); err != nil {
			// リネーム失敗（書き込み中など）でも元ファイルを既知扱いにし、
			// 毎ポーリングで再試行しないようにする。
			log.Printf("rename failed: %s -> %s: %v", oldPath, newPath, err)
			w.state.MarkKnown(name)
			continue
		}

		log.Printf("renamed: %s -> %s", name, filepath.Base(newPath))
		w.state.MarkKnown(name)
		w.state.MarkKnown(filepath.Base(newPath))
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
