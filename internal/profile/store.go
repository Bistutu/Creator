package profile

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"sync"

	"github.com/Bistutu/Creator/internal/fileutil"
)

var ErrNotFound = errors.New("找不到这个应用")

type Store struct {
	path         string
	mu           sync.Mutex
	readBuffer   bytes.Buffer
	lastData     bytes.Buffer
	lastProfiles []App
	positions    map[string]int
}

func NewStore(path string) *Store { return &Store{path: path} }

func (s *Store) List() ([]App, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	apps, err := s.read()
	return slices.Clone(apps), err
}

func (s *Store) Find(id string) (App, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	apps, err := s.read()
	if err != nil {
		return App{}, err
	}
	if index, ok := s.positions[id]; ok {
		return apps[index], nil
	}
	return App{}, ErrNotFound
}

// ConfirmAbsent uses an existing, readable profile list. A missing file can
// also mean the library needs recovery, so it cannot authorize package deletion.
func (s *Store) ConfirmAbsent(id string) (bool, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	apps, err := s.read()
	if err != nil || apps == nil || s.lastData.Len() == 0 {
		return false, err
	}
	if _, exists := s.positions[id]; exists {
		return false, nil
	}
	for saved := range s.positions {
		if !ValidID(saved) {
			return false, nil
		}
	}
	return true, nil
}

func (s *Store) Put(app App) error {
	if !ValidID(app.ID) {
		return errors.New("应用 ID 无效")
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	apps, err := s.read()
	if err != nil {
		return err
	}
	// Match the stored representation before caching this record. JSON
	// normalizes invalid UTF-8 and removes time.Time's in-process metadata.
	data, err := json.Marshal(app)
	if err != nil {
		return err
	}
	var saved App
	if err := json.Unmarshal(data, &saved); err != nil {
		return err
	}
	apps = slices.Clone(apps)
	if index, ok := s.positions[saved.ID]; ok {
		apps[index] = saved
		return s.write(apps)
	}
	return s.write(append(apps, saved))
}

func (s *Store) Delete(id string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	apps, err := s.read()
	if err != nil {
		return err
	}
	if index, ok := s.positions[id]; ok {
		return s.write(slices.Delete(slices.Clone(apps), index, index+1))
	}
	return ErrNotFound
}

func (s *Store) read() ([]App, error) {
	err := s.readFile()
	if errors.Is(err, os.ErrNotExist) {
		s.readBuffer, s.lastData = bytes.Buffer{}, bytes.Buffer{}
		s.lastProfiles, s.positions = nil, nil
		return []App{}, nil
	}
	if err != nil {
		s.readBuffer = bytes.Buffer{}
		return nil, fmt.Errorf("读取应用列表：%w", err)
	}
	data := s.readBuffer.Bytes()
	// Reuse the decoded snapshot only for identical bytes. Other writers and
	// restored files are visible even when the size and timestamps match.
	if s.lastData.Len() > 0 && bytes.Equal(data, s.lastData.Bytes()) {
		return s.lastProfiles, nil
	}
	var apps []App
	if err := json.Unmarshal(data, &apps); err != nil {
		s.readBuffer = bytes.Buffer{}
		return nil, fmt.Errorf("无法读取应用列表：%w", err)
	}
	s.adopt(apps)
	return apps, nil
}

func (s *Store) adopt(apps []App) {
	// Keep the previous buffer available for the next read. The index and
	// file contents belong to the same decoded snapshot.
	s.lastData, s.readBuffer = s.readBuffer, s.lastData
	s.lastProfiles = apps
	s.positions = make(map[string]int, len(apps))
	for index, app := range apps {
		if _, exists := s.positions[app.ID]; !exists {
			s.positions[app.ID] = index
		}
	}
}

func (s *Store) readFile() error {
	file, info, err := fileutil.OpenRegular(s.path)
	if err != nil {
		return err
	}
	defer file.Close()
	s.readBuffer.Reset()
	s.readBuffer.Grow(int(min(info.Size(), 1<<20)) + bytes.MinRead)
	_, err = s.readBuffer.ReadFrom(file)
	return err
}

func (s *Store) write(apps []App) error {
	s.readBuffer.Reset()
	if err := json.NewEncoder(&s.readBuffer).Encode(apps); err != nil {
		return err
	}
	dir := filepath.Dir(s.path)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return err
	}
	file, err := os.CreateTemp(dir, "apps-*.tmp")
	if err != nil {
		return err
	}
	defer os.Remove(file.Name())
	_, err = file.Write(s.readBuffer.Bytes())
	if err == nil {
		err = file.Sync()
	}
	if closeErr := file.Close(); err == nil {
		err = closeErr
	}
	if err != nil {
		return err
	}
	if err := replaceProfiles(file.Name(), s.path); err != nil {
		return err
	}
	s.adopt(apps)
	return nil
}
