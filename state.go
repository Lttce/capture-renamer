// 状態管理（prefix、連番カウンタ、既知ファイル一覧）。
// ファイルシステム操作は行わない純粋な状態管理。
// Poll() と stdin 入力が別goroutineで同時にアクセスするため、全フィールドを mutex で保護。
package main

import "sync"

type State struct {
	mu         sync.Mutex
	prefix     string          // 現在のリネームprefix
	counter    int             // 次に発行する連番
	knownFiles map[string]bool // 既に存在する／処理済みのファイル名
}

func NewState(initialPrefix string) *State {
	return &State{
		prefix:     initialPrefix,
		counter:    1,
		knownFiles: make(map[string]bool),
	}
}

// 現在のprefixを返す。
func (s *State) Prefix() string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.prefix
}

// prefixを変更し、同時に連番を1にリセットする。
func (s *State) SetPrefix(p string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.prefix = p
	s.counter = 1
}

// 次の連番を払い出し、カウンタを進める。
func (s *State) NextSequence() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	n := s.counter
	s.counter++
	return n
}

// ファイル名が既知（リネーム処理済み or 起動時から存在）か判定。
func (s *State) IsKnown(name string) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.knownFiles[name]
}

// ファイル名を既知リストに追加する。
func (s *State) MarkKnown(name string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.knownFiles[name] = true
}
