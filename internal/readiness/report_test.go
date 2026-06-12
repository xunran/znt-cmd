package readiness

import (
	"context"
	"database/sql"
	"database/sql/driver"
	"errors"
	"io"
	"sort"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"znt/internal/app/config"
	"znt/internal/app/core"
	"znt/internal/storage/migration"
)

func TestBuildReadinessReport(t *testing.T) {
	appCore, err := core.New(config.Config{ServiceName: "clean-core", Version: "test", Env: "test", HTTPAddr: ":0", Readiness: true})
	if err != nil {
		t.Fatal(err)
	}
	report := Build(context.Background(), appCore, "../../migrations")
	if report.Status != "ready" {
		t.Fatalf("expected ready, got %#v", report)
	}
	statuses := map[string]string{}
	for _, status := range report.ExecutionDomains {
		if status.Enabled {
			statuses[status.DomainID] = string(status.Status)
			continue
		}
		statuses[status.DomainID] = "disabled:" + status.Details
	}
	for _, domainID := range []string{"local", "http", "agent_tool"} {
		if statuses[domainID] != "production_ready" {
			t.Fatalf("expected %s production ready in structured readiness output, got %#v", domainID, report.ExecutionDomains)
		}
	}
	for _, domainID := range []string{"database", "worker", "sandbox", "managed"} {
		if !strings.HasPrefix(statuses[domainID], "disabled:") {
			t.Fatalf("expected %s disabled in structured readiness output, got %#v", domainID, report.ExecutionDomains)
		}
	}
	var foundExecutionDomains bool
	for _, check := range report.Checks {
		if check.Name == "execution.domains" {
			foundExecutionDomains = true
			if check.Status != CheckPass || !strings.Contains(check.Details, "production_ready=local,http,agent_tool") || !strings.Contains(check.Details, "disabled=database,worker,sandbox,managed") {
				t.Fatalf("unexpected execution domain check: %#v", check)
			}
		}
	}
	if !foundExecutionDomains {
		t.Fatalf("expected execution domain readiness check, got %#v", report.Checks)
	}
}

func TestBuildReadinessFailsWhenDatabasePingFails(t *testing.T) {
	appCore := newReadinessTestCore(t)
	appCore.Config.DatabaseURL = "postgres://configured"
	appCore.DB = openReadinessTestDB(t, readinessDBScenario{pingErr: errors.New("database offline")})
	defer appCore.DB.Close()

	report := Build(context.Background(), appCore, "../../migrations")
	if report.Status != "not_ready" {
		t.Fatalf("expected not_ready when database ping fails, got %#v", report)
	}
	check := readinessCheck(t, report, "database")
	if check.Status != CheckFail || !strings.Contains(check.Details, "database offline") {
		t.Fatalf("expected database fail check, got %#v", check)
	}
}

func TestBuildReadinessUsesDedicatedReadinessDatabase(t *testing.T) {
	migrations := loadReadinessMigrations(t)
	appCore := newReadinessTestCore(t)
	appCore.Config.DatabaseURL = "postgres://configured"
	appCore.DB = openReadinessTestDB(t, readinessDBScenario{pingErr: errors.New("business pool exhausted")})
	defer appCore.DB.Close()
	appCore.ReadinessDB = openReadinessTestDB(t, completeReadinessDBScenario(migrations))
	defer appCore.ReadinessDB.Close()

	report := Build(context.Background(), appCore, "../../migrations")
	if report.Status != "ready" {
		t.Fatalf("expected dedicated readiness db to keep report ready, got %#v", report)
	}
	if check := readinessCheck(t, report, "database"); check.Status != CheckPass {
		t.Fatalf("expected database check to use dedicated readiness db, got %#v", check)
	}
	if check := readinessCheck(t, report, "migration.live_schema"); check.Status != CheckPass {
		t.Fatalf("expected migration check to use dedicated readiness db, got %#v", check)
	}
}

func TestBuildReadinessFailsWhenLiveSchemaIsMissing(t *testing.T) {
	migrations := loadReadinessMigrations(t)
	appCore := newReadinessTestCore(t)
	appCore.Config.DatabaseURL = "postgres://configured"
	appCore.DB = openReadinessTestDB(t, readinessDBScenario{
		tables:  map[string]bool{"clean_core_schema_migrations": true},
		columns: map[string]map[string]migration.ColumnInfo{},
		indexes: map[string]bool{},
		applied: appliedReadinessMigrations(migrations),
	})
	defer appCore.DB.Close()

	report := Build(context.Background(), appCore, "../../migrations")
	if report.Status != "not_ready" {
		t.Fatalf("expected not_ready when live schema is missing, got %#v", report)
	}
	check := readinessCheck(t, report, "migration.live_schema")
	if check.Status != CheckFail || !strings.Contains(check.Details, "missing_tables=") {
		t.Fatalf("expected live schema missing table failure, got %#v", check)
	}
}

func TestBuildReadinessFailsWhenMigrationChecksumMismatches(t *testing.T) {
	migrations := loadReadinessMigrations(t)
	if len(migrations) == 0 {
		t.Fatal("expected migrations for checksum mismatch test")
	}
	scenario := completeReadinessDBScenario(migrations)
	first := migrations[0]
	record := scenario.applied[first.Version]
	record.Checksum = migration.ChecksumSQL("edited migration sql")
	scenario.applied[first.Version] = record

	appCore := newReadinessTestCore(t)
	appCore.Config.DatabaseURL = "postgres://configured"
	appCore.DB = openReadinessTestDB(t, scenario)
	defer appCore.DB.Close()

	report := Build(context.Background(), appCore, "../../migrations")
	if report.Status != "not_ready" {
		t.Fatalf("expected not_ready when migration checksum mismatches, got %#v", report)
	}
	check := readinessCheck(t, report, "migration.live_schema")
	if check.Status != CheckFail || !strings.Contains(check.Details, "checksum_mismatches=") || !strings.Contains(check.Details, first.Version) {
		t.Fatalf("expected checksum mismatch failure, got %#v", check)
	}
}

func newReadinessTestCore(t *testing.T) *core.Core {
	t.Helper()
	appCore, err := core.New(config.Config{ServiceName: "clean-core", Version: "test", Env: "test", HTTPAddr: ":0", Readiness: true})
	if err != nil {
		t.Fatal(err)
	}
	return appCore
}

func readinessCheck(t *testing.T, report Report, name string) Check {
	t.Helper()
	for _, check := range report.Checks {
		if check.Name == name {
			return check
		}
	}
	t.Fatalf("missing readiness check %s in %#v", name, report.Checks)
	return Check{}
}

func loadReadinessMigrations(t *testing.T) []migration.Migration {
	t.Helper()
	migrations, err := migration.LoadDir("../../migrations")
	if err != nil {
		t.Fatal(err)
	}
	return migrations
}

func completeReadinessDBScenario(migrations []migration.Migration) readinessDBScenario {
	tables := map[string]bool{}
	for _, table := range migration.RequiredLiveTables {
		tables[table] = true
	}
	columns := map[string]map[string]migration.ColumnInfo{}
	for _, requirement := range migration.RequiredLiveColumns {
		if columns[requirement.Table] == nil {
			columns[requirement.Table] = map[string]migration.ColumnInfo{}
		}
		columns[requirement.Table][requirement.Column] = migration.ColumnInfo{Name: requirement.Column, NotNull: requirement.NotNull, DataType: "text"}
	}
	indexes := map[string]bool{}
	for _, index := range migration.RequiredLiveIndexes {
		indexes[index] = true
	}
	return readinessDBScenario{
		tables:  tables,
		columns: columns,
		indexes: indexes,
		applied: appliedReadinessMigrations(migrations),
	}
}

func appliedReadinessMigrations(migrations []migration.Migration) map[string]migration.AppliedMigration {
	applied := map[string]migration.AppliedMigration{}
	for _, item := range migrations {
		applied[item.Version] = migration.AppliedMigration{
			Version:   item.Version,
			Name:      item.Name,
			Checksum:  migration.ChecksumSQL(item.SQL),
			AppliedAt: time.Unix(1, 0).UTC(),
		}
	}
	return applied
}

type readinessDBScenario struct {
	pingErr error
	tables  map[string]bool
	columns map[string]map[string]migration.ColumnInfo
	indexes map[string]bool
	applied map[string]migration.AppliedMigration
}

var readinessDBDriverOnce sync.Once
var readinessDBScenarioSeq atomic.Int64
var readinessDBScenarios sync.Map

func openReadinessTestDB(t *testing.T, scenario readinessDBScenario) *sql.DB {
	t.Helper()
	readinessDBDriverOnce.Do(func() {
		sql.Register("znt_readiness_test", readinessTestDriver{})
	})
	name := "scenario_" + strings.ReplaceAll(t.Name(), "/", "_") + "_" + strconv.FormatInt(readinessDBScenarioSeq.Add(1), 10)
	readinessDBScenarios.Store(name, &scenario)
	t.Cleanup(func() {
		readinessDBScenarios.Delete(name)
	})
	db, err := sql.Open("znt_readiness_test", name)
	if err != nil {
		t.Fatal(err)
	}
	return db
}

type readinessTestDriver struct{}

func (readinessTestDriver) Open(name string) (driver.Conn, error) {
	value, ok := readinessDBScenarios.Load(name)
	if !ok {
		return nil, errors.New("unknown readiness test db scenario")
	}
	return &readinessTestConn{scenario: value.(*readinessDBScenario)}, nil
}

type readinessTestConn struct {
	scenario *readinessDBScenario
}

func (c *readinessTestConn) Prepare(string) (driver.Stmt, error) {
	return nil, errors.New("readiness test driver does not support prepared statements")
}

func (c *readinessTestConn) Close() error {
	return nil
}

func (c *readinessTestConn) Begin() (driver.Tx, error) {
	return nil, errors.New("readiness test driver does not support transactions")
}

func (c *readinessTestConn) Ping(context.Context) error {
	return c.scenario.pingErr
}

func (c *readinessTestConn) QueryContext(_ context.Context, query string, args []driver.NamedValue) (driver.Rows, error) {
	lower := strings.ToLower(query)
	switch {
	case strings.Contains(lower, "information_schema.tables"):
		return readinessRows([]string{"table_name"}, tableRows(c.scenario.tables)), nil
	case strings.Contains(lower, "information_schema.columns"):
		table := ""
		if len(args) > 0 {
			table = stringValue(args[0].Value)
		}
		return readinessRows([]string{"column_name", "is_nullable", "data_type"}, columnRows(c.scenario.columns[table])), nil
	case strings.Contains(lower, "pg_indexes"):
		return readinessRows([]string{"indexname"}, tableRows(c.scenario.indexes)), nil
	case strings.Contains(lower, "clean_core_schema_migrations"):
		return readinessRows([]string{"version", "name", "checksum", "applied_at"}, migrationRows(c.scenario.applied)), nil
	default:
		return nil, errors.New("unsupported readiness test query")
	}
}

func stringValue(value any) string {
	text, ok := value.(string)
	if ok {
		return text
	}
	return ""
}

func tableRows(values map[string]bool) [][]driver.Value {
	names := make([]string, 0, len(values))
	for name, present := range values {
		if present {
			names = append(names, name)
		}
	}
	sort.Strings(names)
	rows := make([][]driver.Value, 0, len(names))
	for _, name := range names {
		rows = append(rows, []driver.Value{name})
	}
	return rows
}

func columnRows(values map[string]migration.ColumnInfo) [][]driver.Value {
	names := make([]string, 0, len(values))
	for name := range values {
		names = append(names, name)
	}
	sort.Strings(names)
	rows := make([][]driver.Value, 0, len(names))
	for _, name := range names {
		column := values[name]
		nullable := "YES"
		if column.NotNull {
			nullable = "NO"
		}
		dataType := column.DataType
		if dataType == "" {
			dataType = "text"
		}
		rows = append(rows, []driver.Value{column.Name, nullable, dataType})
	}
	return rows
}

func migrationRows(values map[string]migration.AppliedMigration) [][]driver.Value {
	versions := make([]string, 0, len(values))
	for version := range values {
		versions = append(versions, version)
	}
	sort.Strings(versions)
	rows := make([][]driver.Value, 0, len(versions))
	for _, version := range versions {
		record := values[version]
		rows = append(rows, []driver.Value{record.Version, record.Name, record.Checksum, record.AppliedAt})
	}
	return rows
}

func readinessRows(columns []string, values [][]driver.Value) driver.Rows {
	return &readinessTestRows{columns: columns, values: values}
}

type readinessTestRows struct {
	columns []string
	values  [][]driver.Value
	index   int
}

func (r *readinessTestRows) Columns() []string {
	return r.columns
}

func (r *readinessTestRows) Close() error {
	return nil
}

func (r *readinessTestRows) Next(dest []driver.Value) error {
	if r.index >= len(r.values) {
		return io.EOF
	}
	copy(dest, r.values[r.index])
	r.index++
	return nil
}
