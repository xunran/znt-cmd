package migration

import (
	"context"
	"path/filepath"
	"testing"
	"time"
)

func TestFileStorePersistsAppliedMigrations(t *testing.T) {
	path := filepath.Join(t.TempDir(), "state.json")
	store := NewFileStore(path)
	record := AppliedMigration{
		Version:   "001",
		Name:      "create",
		Checksum:  "abc",
		AppliedAt: time.Unix(1, 0).UTC(),
	}
	if err := store.MarkApplied(context.Background(), record); err != nil {
		t.Fatal(err)
	}
	reloaded, err := NewFileStore(path).Applied(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if reloaded["001"].Checksum != "abc" {
		t.Fatalf("expected persisted checksum, got %#v", reloaded)
	}
}
