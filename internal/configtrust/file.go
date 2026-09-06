// SPDX-License-Identifier: Apache-2.0
package configtrust

import (
	"crypto/rand"
	"errors"
	"io"
	"os"
	"path/filepath"
	"sync"
)

// FileStore uses a caller-owned 0700 directory and one Gate writer. It does not
// defend against root/same-user disk rollback/deletion or coordinate replica writers.
type FileStore struct {
	root *os.Root
	mu   sync.Mutex
}

func NewFileStore(dir string) (*FileStore, error) {
	if !filepath.IsAbs(dir) {
		return nil, ErrPersistence
	}
	info, e := os.Lstat(dir)
	if e != nil || !info.IsDir() || info.Mode()&os.ModeSymlink != 0 || info.Mode().Perm() != 0700 {
		return nil, ErrPersistence
	}
	root, e := os.OpenRoot(dir)
	if e != nil {
		return nil, ErrPersistence
	}
	info, e = root.Stat(".")
	if e != nil || !info.IsDir() || info.Mode().Perm() != 0700 {
		root.Close()
		return nil, ErrPersistence
	}
	return &FileStore{root: root}, nil
}
func (s *FileStore) Close() { s.mu.Lock(); defer s.mu.Unlock(); _ = s.root.Close() }
func (s *FileStore) check() error {
	dir, e := s.root.Stat(".")
	if e != nil || !dir.IsDir() || dir.Mode().Perm() != 0700 {
		return ErrPersistence
	}
	info, e := s.root.Lstat("snapshot.json")
	if errors.Is(e, os.ErrNotExist) {
		return ErrEmpty
	}
	if e != nil || !info.Mode().IsRegular() || info.Mode().Perm() != 0600 || info.Size() > MaxSnapshotBytes {
		return ErrPersistence
	}
	return nil
}
func (s *FileStore) Load() ([]byte, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if e := s.check(); e != nil {
		return nil, e
	}
	f, e := s.root.Open("snapshot.json")
	if e != nil {
		return nil, ErrPersistence
	}
	defer f.Close()
	raw, e := io.ReadAll(io.LimitReader(f, MaxSnapshotBytes+1))
	if e != nil || len(raw) > MaxSnapshotBytes {
		return nil, ErrPersistence
	}
	return raw, nil
}
func (s *FileStore) Save(raw []byte) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if len(raw) == 0 || len(raw) > MaxSnapshotBytes {
		return ErrPersistence
	}
	if e := s.check(); e != nil && !errors.Is(e, ErrEmpty) {
		return e
	}
	name := ".snapshot-" + rand.Text()
	f, e := s.root.OpenFile(name, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0600)
	if e != nil {
		return ErrPersistence
	}
	defer s.root.Remove(name)
	if _, e = f.Write(raw); e != nil {
		f.Close()
		return ErrPersistence
	}
	if e = f.Sync(); e != nil {
		f.Close()
		return ErrPersistence
	}
	if e = f.Close(); e != nil {
		return ErrPersistence
	}
	if e = s.root.Rename(name, "snapshot.json"); e != nil {
		return ErrPersistence
	}
	dir, e := s.root.Open(".")
	if e != nil {
		return ErrPersistence
	}
	defer dir.Close()
	if dir.Sync() != nil {
		return ErrPersistence
	}
	return nil
}
