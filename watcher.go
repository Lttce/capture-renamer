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

const (
	SaveFolder    = "."
	InitialPrefix = "item"
	PollInterval  = 300 * time.Millisecond
)

type Watcher struct {
	mu         sync.Mutex
	folder     string
	prefix     string
	counter    int
	knownFiles map[string]bool
	stop       chan struct{}
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

func (w *Watcher) Stop() {
	close(w.stop)
}

func (w *Watcher) SetPrefix(p string) {
	w.mu.Lock()
	defer w.mu.Unlock()
	w.prefix = p
	w.counter = 1
}

func (w *Watcher) getPrefix() string {
	w.mu.Lock()
	defer w.mu.Unlock()
	return w.prefix
}

func (w *Watcher) nextSequence() int {
	w.mu.Lock()
	defer w.mu.Unlock()
	n := w.counter
	w.counter++
	return n
}

func (w *Watcher) isKnown(name string) bool {
	w.mu.Lock()
	defer w.mu.Unlock()
	return w.knownFiles[name]
}

func (w *Watcher) markKnown(name string) {
	w.mu.Lock()
	defer w.mu.Unlock()
	w.knownFiles[name] = true
}

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

func (w *Watcher) Poll() (int, error) {
	entries, err := os.ReadDir(w.folder)
	if err != nil {
		return 0, err
	}

	var newFiles []string
	for _, e := range entries {
		if !e.IsDir() && !w.isKnown(e.Name()) {
			newFiles = append(newFiles, e.Name())
		}
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
