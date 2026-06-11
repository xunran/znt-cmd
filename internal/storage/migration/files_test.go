package migration

import (
	"os"
	"path/filepath"
	"testing"
)

func TestLoadDir(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "001_create.sql"), []byte("SELECT 1;"), 0o600); err != nil {
		t.Fatal(err)
	}
	migrations, err := LoadDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	if len(migrations) != 1 || migrations[0].Version != "001" || migrations[0].Name != "create" {
		t.Fatalf("unexpected migrations: %#v", migrations)
	}
}
