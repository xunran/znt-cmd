package migration

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
)

const ManifestSchema = "clean-core-migration-checksums.v1"

type Manifest struct {
	Schema     string          `json:"schema"`
	Migrations []ManifestEntry `json:"migrations"`
}

type ManifestEntry struct {
	Version  string `json:"version"`
	Name     string `json:"name"`
	File     string `json:"file"`
	Checksum string `json:"checksum"`
}

type ChecksumMismatchError struct {
	Version  string
	File     string
	Expected string
	Actual   string
}

func (e ChecksumMismatchError) Error() string {
	return fmt.Sprintf("migration %s (%s) checksum mismatch: manifest=%s current=%s. Applied migrations are immutable; restore the original SQL or create a new migration file.",
		e.Version, e.File, e.Expected, e.Actual)
}

func DefaultManifestPath(dir string) string {
	return filepath.Join(dir, "checksums.json")
}

func LoadManifestIfExists(path string) (Manifest, bool, error) {
	data, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		return Manifest{}, false, nil
	}
	if err != nil {
		return Manifest{}, false, err
	}
	var manifest Manifest
	if err := json.Unmarshal(data, &manifest); err != nil {
		return Manifest{}, false, err
	}
	return manifest, true, nil
}

func ValidateManifest(migrations []Migration, manifest Manifest) error {
	if manifest.Schema != ManifestSchema {
		return fmt.Errorf("migration checksum manifest schema must be %s", ManifestSchema)
	}
	expectedByVersion := make(map[string]ManifestEntry, len(manifest.Migrations))
	for _, entry := range manifest.Migrations {
		if entry.Version == "" {
			return fmt.Errorf("migration checksum manifest contains an entry without version")
		}
		if _, ok := expectedByVersion[entry.Version]; ok {
			return fmt.Errorf("migration checksum manifest contains duplicate version %s", entry.Version)
		}
		expectedByVersion[entry.Version] = entry
	}
	seen := map[string]bool{}
	for _, migration := range migrations {
		expected, ok := expectedByVersion[migration.Version]
		if !ok {
			return fmt.Errorf("migration %s is missing from checksum manifest. Add it intentionally with scripts/check_migration_checksums.ps1 -Update.", migration.Version)
		}
		file := migration.Version + "_" + migration.Name + ".sql"
		if expected.File != file || expected.Name != migration.Name {
			return fmt.Errorf("migration %s manifest metadata mismatch: manifest=%s/%s current=%s/%s",
				migration.Version, expected.File, expected.Name, file, migration.Name)
		}
		actual := checksum(migration.SQL)
		if expected.Checksum != actual {
			return ChecksumMismatchError{
				Version:  migration.Version,
				File:     file,
				Expected: expected.Checksum,
				Actual:   actual,
			}
		}
		seen[migration.Version] = true
	}
	for _, entry := range manifest.Migrations {
		if !seen[entry.Version] {
			return fmt.Errorf("migration checksum manifest references missing migration %s", entry.Version)
		}
	}
	return nil
}
