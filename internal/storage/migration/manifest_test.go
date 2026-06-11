package migration

import (
	"errors"
	"path/filepath"
	"testing"
)

func TestValidateManifest(t *testing.T) {
	migrations := []Migration{{Version: "001", Name: "base", SQL: "SELECT 1;"}}
	manifest := Manifest{
		Schema: ManifestSchema,
		Migrations: []ManifestEntry{{
			Version:  "001",
			Name:     "base",
			File:     "001_base.sql",
			Checksum: checksum("SELECT 1;"),
		}},
	}
	if err := ValidateManifest(migrations, manifest); err != nil {
		t.Fatalf("expected manifest to validate: %v", err)
	}

	manifest.Migrations[0].Checksum = checksum("SELECT 2;")
	err := ValidateManifest(migrations, manifest)
	var mismatch ChecksumMismatchError
	if !errors.As(err, &mismatch) || mismatch.Version != "001" {
		t.Fatalf("expected checksum mismatch error, got %T %v", err, err)
	}
}

func TestValidateManifestRejectsMissingMigrationFile(t *testing.T) {
	err := ValidateManifest(nil, Manifest{
		Schema: ManifestSchema,
		Migrations: []ManifestEntry{{
			Version:  "001",
			Name:     "base",
			File:     "001_base.sql",
			Checksum: "abc",
		}},
	})
	if err == nil {
		t.Fatal("expected manifest reference to missing migration to fail")
	}
}

func TestLoadManifestIfExists(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "missing.json")
	if _, ok, err := LoadManifestIfExists(path); err != nil || ok {
		t.Fatalf("expected missing manifest to return ok=false, ok=%v err=%v", ok, err)
	}
}
