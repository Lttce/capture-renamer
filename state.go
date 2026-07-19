package main

import "sync"

type State struct {
	mu         sync.Mutex
	prefix     string
	counter    int
	knownFiles map[string]bool
}

func NewState(initialPrefix string) *State {
	return &State{
		prefix:     initialPrefix,
		counter:    1,
		knownFiles: make(map[string]bool),
	}
}

func (s *State) Prefix() string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.prefix
}

func (s *State) SetPrefix(p string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.prefix = p
	s.counter = 1
}

func (s *State) NextSequence() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	n := s.counter
	s.counter++
	return n
}

func (s *State) IsKnown(name string) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.knownFiles[name]
}

func (s *State) MarkKnown(name string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.knownFiles[name] = true
}
