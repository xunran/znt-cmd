package migration

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

func LoadDir(dir string) ([]Migration, error) {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil, err
	}
	out := make([]Migration, 0)
	for _, entry := range entries {
		if entry.IsDir() || !strings.HasSuffix(entry.Name(), ".sql") {
			continue
		}
		name := entry.Name()
		version, rest, ok := strings.Cut(name, "_")
		if !ok {
			return nil, fmt.Errorf("migration file %s must use version_name.sql format", name)
		}
		data, err := os.ReadFile(filepath.Join(dir, name))
		if err != nil {
			return nil, err
		}
		out = append(out, Migration{
			Version: version,
			Name:    strings.TrimSuffix(rest, ".sql"),
			SQL:     string(data),
		})
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Version < out[j].Version })
	return out, nil
}
