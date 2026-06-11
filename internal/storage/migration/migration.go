package migration

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"sort"
	"strings"
	"sync"
	"time"
)

type Migration struct {
	Version string
	Name    string
	SQL     string
}

type AppliedMigration struct {
	Version   string    `json:"version"`
	Name      string    `json:"name"`
	Checksum  string    `json:"checksum"`
	AppliedAt time.Time `json:"applied_at"`
}

type Store interface {
	Applied(ctx context.Context) (map[string]AppliedMigration, error)
	MarkApplied(ctx context.Context, migration AppliedMigration) error
}

type Executor interface {
	Exec(ctx context.Context, sql string) error
}

type Runner struct {
	store Store
	now   func() time.Time
}

func NewRunner(store Store) Runner {
	return Runner{store: store, now: func() time.Time { return time.Now().UTC() }}
}

func (r Runner) Up(ctx context.Context, migrations []Migration) ([]AppliedMigration, error) {
	return r.up(ctx, migrations, nil)
}

func (r Runner) UpWithExecutor(ctx context.Context, migrations []Migration, executor Executor) ([]AppliedMigration, error) {
	return r.up(ctx, migrations, executor)
}

func (r Runner) up(ctx context.Context, migrations []Migration, executor Executor) ([]AppliedMigration, error) {
	applied, err := r.store.Applied(ctx)
	if err != nil {
		return nil, err
	}
	sort.Slice(migrations, func(i, j int) bool {
		return migrations[i].Version < migrations[j].Version
	})
	out := make([]AppliedMigration, 0)
	for _, migration := range migrations {
		if migration.Version == "" || migration.Name == "" {
			return nil, fmt.Errorf("invalid migration: version and name are required")
		}
		checksum := checksum(migration.SQL)
		if existing, ok := applied[migration.Version]; ok {
			if existing.Checksum != checksum {
				return nil, fmt.Errorf("migration %s checksum mismatch", migration.Version)
			}
			continue
		}
		record := AppliedMigration{
			Version:   migration.Version,
			Name:      migration.Name,
			Checksum:  checksum,
			AppliedAt: r.now(),
		}
		if executor != nil {
			if err := executor.Exec(ctx, migration.SQL); err != nil {
				return nil, err
			}
		}
		if err := r.store.MarkApplied(ctx, record); err != nil {
			return nil, err
		}
		out = append(out, record)
	}
	return out, nil
}

func (r Runner) Status(ctx context.Context, migrations []Migration) ([]AppliedMigration, error) {
	applied, err := r.store.Applied(ctx)
	if err != nil {
		return nil, err
	}
	out := make([]AppliedMigration, 0, len(applied))
	for _, migration := range migrations {
		if record, ok := applied[migration.Version]; ok {
			out = append(out, record)
		}
	}
	return out, nil
}

func checksum(sql string) string {
	sum := sha256.Sum256([]byte(normalizeSQLForChecksum(sql)))
	return hex.EncodeToString(sum[:])
}

func ChecksumSQL(sql string) string {
	return checksum(sql)
}

func normalizeSQLForChecksum(sql string) string {
	sql = strings.ReplaceAll(sql, "\r\n", "\n")
	return strings.ReplaceAll(sql, "\r", "\n")
}

type InMemoryStore struct {
	mu      sync.RWMutex
	applied map[string]AppliedMigration
}

func NewInMemoryStore() *InMemoryStore {
	return &InMemoryStore{applied: map[string]AppliedMigration{}}
}

func (s *InMemoryStore) Applied(_ context.Context) (map[string]AppliedMigration, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	out := make(map[string]AppliedMigration, len(s.applied))
	for version, migration := range s.applied {
		out[version] = migration
	}
	return out, nil
}

func (s *InMemoryStore) MarkApplied(_ context.Context, migration AppliedMigration) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if existing, ok := s.applied[migration.Version]; ok && existing.Checksum != migration.Checksum {
		return fmt.Errorf("migration %s checksum mismatch", migration.Version)
	}
	s.applied[migration.Version] = migration
	return nil
}
