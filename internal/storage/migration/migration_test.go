package migration

import (
	"context"
	"testing"
	"time"
)

func TestRunnerUpIsIdempotent(t *testing.T) {
	store := NewInMemoryStore()
	runner := NewRunner(store)
	migrations := []Migration{{Version: "001", Name: "create_tasks", SQL: "CREATE TABLE tasks(id TEXT);"}}
	applied, err := runner.Up(context.Background(), migrations)
	if err != nil {
		t.Fatal(err)
	}
	if len(applied) != 1 {
		t.Fatalf("expected one applied migration, got %d", len(applied))
	}
	appliedAgain, err := runner.Up(context.Background(), migrations)
	if err != nil {
		t.Fatal(err)
	}
	if len(appliedAgain) != 0 {
		t.Fatalf("expected repeated up to skip existing migrations, got %d", len(appliedAgain))
	}
}

func TestRunnerDetectsChecksumMismatch(t *testing.T) {
	store := NewInMemoryStore()
	runner := NewRunner(store)
	if _, err := runner.Up(context.Background(), []Migration{{Version: "001", Name: "first", SQL: "A"}}); err != nil {
		t.Fatal(err)
	}
	if _, err := runner.Up(context.Background(), []Migration{{Version: "001", Name: "first", SQL: "B"}}); err == nil {
		t.Fatal("expected checksum mismatch")
	}
}

type fakeInspector struct {
	tables  map[string]bool
	columns map[string]map[string]ColumnInfo
	indexes map[string]bool
	applied map[string]AppliedMigration
}

func (f fakeInspector) Tables(context.Context) (map[string]bool, error) {
	return f.tables, nil
}

func (f fakeInspector) Columns(_ context.Context, table string) (map[string]ColumnInfo, error) {
	return f.columns[table], nil
}

func (f fakeInspector) Indexes(context.Context) (map[string]bool, error) {
	return f.indexes, nil
}

func (f fakeInspector) AppliedMigrations(context.Context) (map[string]AppliedMigration, error) {
	return f.applied, nil
}

func TestValidateLiveSchemaDetectsMissingObjects(t *testing.T) {
	report, err := ValidateLiveSchema(context.Background(), fakeInspector{
		tables:  map[string]bool{"tasks": true, "clean_core_schema_migrations": true},
		columns: map[string]map[string]ColumnInfo{"tasks": {"tenant_id": {Name: "tenant_id", NotNull: false}}},
		indexes: map[string]bool{},
		applied: map[string]AppliedMigration{},
	}, []Migration{{Version: "001", Name: "base", SQL: "CREATE TABLE tasks(id TEXT);"}})
	if err != nil {
		t.Fatal(err)
	}
	if report.Status != "not_ready" || len(report.MissingTables) == 0 || len(report.NullableColumns) == 0 || len(report.MissingMigrations) != 1 {
		t.Fatalf("expected live schema failures, got %#v", report)
	}
}

func TestValidateLiveSchemaPassesCompleteInspector(t *testing.T) {
	tables := map[string]bool{}
	for _, table := range RequiredLiveTables {
		tables[table] = true
	}
	columns := map[string]map[string]ColumnInfo{}
	for _, requirement := range RequiredLiveColumns {
		if columns[requirement.Table] == nil {
			columns[requirement.Table] = map[string]ColumnInfo{}
		}
		columns[requirement.Table][requirement.Column] = ColumnInfo{Name: requirement.Column, NotNull: requirement.NotNull}
	}
	indexes := map[string]bool{}
	for _, index := range RequiredLiveIndexes {
		indexes[index] = true
	}
	migrations := []Migration{{Version: "001", Name: "base", SQL: "CREATE TABLE tasks(id TEXT);"}}
	report, err := ValidateLiveSchema(context.Background(), fakeInspector{
		tables:  tables,
		columns: columns,
		indexes: indexes,
		applied: map[string]AppliedMigration{"001": {
			Version:   "001",
			Name:      "base",
			Checksum:  checksum(migrations[0].SQL),
			AppliedAt: time.Unix(1, 0).UTC(),
		}},
	}, migrations)
	if err != nil {
		t.Fatal(err)
	}
	if report.Status != "ready" {
		t.Fatalf("expected ready report, got %#v", report)
	}
}
