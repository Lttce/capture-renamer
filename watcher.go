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

func (w *Watcher) Stop() {
	close(w.stop)
}

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

func (w *Watcher) Poll() (int, error) {
	entries, err := w.fs.ReadDir(w.folder)
	if err != nil {
		return 0, err
	}

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
