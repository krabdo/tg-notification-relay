package main

import (
	"encoding/json"
	"os"
	"path/filepath"
	"sync"
)

type pushState struct {
	mu         sync.RWMutex
	enabled    bool
	generation uint64
	path       string
}

func openState(path string) (*pushState, error) {
	s := &pushState{enabled: true, path: path}
	b, err := os.ReadFile(path)
	if os.IsNotExist(err) {
		return s, nil
	}
	if err != nil {
		return s, err
	}
	v := struct {
		Enabled *bool `json:"push_enabled"`
	}{}
	if err := json.Unmarshal(b, &v); err != nil {
		return s, err
	}
	if v.Enabled != nil {
		s.enabled = *v.Enabled
	}
	return s, nil
}

func (s *pushState) snapshot() (bool, uint64) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.enabled, s.generation
}
func (s *pushState) permits(g uint64) bool { on, current := s.snapshot(); return on && g == current }
func (s *pushState) set(on bool) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	b, _ := json.Marshal(struct {
		Enabled bool `json:"push_enabled"`
	}{on})
	if err := atomicWrite(s.path, b); err != nil {
		return err
	}
	s.enabled = on
	s.generation++
	return nil
}

func atomicWrite(path string, b []byte) error {
	if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
		return err
	}
	f, err := os.CreateTemp(filepath.Dir(path), ".relay-*")
	if err != nil {
		return err
	}
	defer os.Remove(f.Name())
	if err = f.Chmod(0600); err == nil {
		_, err = f.Write(b)
	}
	if err == nil {
		err = f.Sync()
	}
	closeErr := f.Close()
	if err != nil {
		return err
	}
	if closeErr != nil {
		return closeErr
	}
	return os.Rename(f.Name(), path)
}
