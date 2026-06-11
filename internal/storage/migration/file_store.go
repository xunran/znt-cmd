package migration

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"sync"
)

type FileStore struct {
	path string
	mu   sync.Mutex
}

func NewFileStore(path string) *FileStore {
	return &FileStore{path: path}
}

func (s *FileStore) Applied(ctx context.Context) (map[string]AppliedMigration, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.read(ctx)
}

func (s *FileStore) MarkApplied(ctx context.Context, migration AppliedMigration) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	applied, err := s.read(ctx)
	if err != nil {
		return err
	}
	if existing, ok := applied[migration.Version]; ok && existing.Checksum != migration.Checksum {
		return errors.New("migration checksum mismatch")
	}
	applied[migration.Version] = migration
	return s.write(applied)
}

func (s *FileStore) read(ctx context.Context) (map[string]AppliedMigration, error) {
	select {
	case <-ctx.Done():
		return nil, ctx.Err()
	default:
	}
	data, err := os.ReadFile(s.path)
	if errors.Is(err, os.ErrNotExist) {
		return map[string]AppliedMigration{}, nil
	}
	if err != nil {
		return nil, err
	}
	if len(data) == 0 {
		return map[string]AppliedMigration{}, nil
	}
	var applied map[string]AppliedMigration
	if err := json.Unmarshal(data, &applied); err != nil {
		return nil, err
	}
	if applied == nil {
		applied = map[string]AppliedMigration{}
	}
	return applied, nil
}

func (s *FileStore) write(applied map[string]AppliedMigration) error {
	if err := os.MkdirAll(filepath.Dir(s.path), 0o755); err != nil && filepath.Dir(s.path) != "." {
		return err
	}
	data, err := json.MarshalIndent(applied, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(s.path, data, 0o600)
}
