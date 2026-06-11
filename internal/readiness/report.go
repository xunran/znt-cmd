package readiness

import (
	"context"
	"database/sql"
	"strings"

	"znt/internal/app/core"
	"znt/internal/execution/domain"
	"znt/internal/storage/migration"
)

type CheckStatus string

const (
	CheckPass CheckStatus = "pass"
	CheckWarn CheckStatus = "warn"
	CheckFail CheckStatus = "fail"
)

type Check struct {
	Name    string      `json:"name"`
	Status  CheckStatus `json:"status"`
	Details string      `json:"details,omitempty"`
}

type Report struct {
	Status           string                    `json:"status"`
	Checks           []Check                   `json:"checks"`
	ExecutionDomains []domain.ProductionStatus `json:"execution_domains,omitempty"`
}

func Build(ctx context.Context, appCore *core.Core, migrationDir string) Report {
	executionDomains := domain.SingleNodeProductionStatuses()
	checks := []Check{
		checkConfig(appCore),
		checkDatabase(ctx, appCore),
		checkTools(appCore),
		checkExecutionDomains(executionDomains),
		checkTraceAudit(appCore),
		checkMigrations(ctx, appCore, migrationDir),
		checkReleaseSwitches(appCore),
	}
	status := "ready"
	for _, check := range checks {
		if check.Status == CheckFail {
			status = "not_ready"
			break
		}
		if check.Status == CheckWarn && status == "ready" {
			status = "degraded"
		}
	}
	select {
	case <-ctx.Done():
		status = "not_ready"
		checks = append(checks, Check{Name: "context", Status: CheckFail, Details: ctx.Err().Error()})
	default:
	}
	return Report{Status: status, Checks: checks, ExecutionDomains: executionDomains}
}

func checkDatabase(ctx context.Context, appCore *core.Core) Check {
	if appCore.Config.DatabaseURL == "" {
		return Check{Name: "database", Status: CheckPass, Details: "in-memory persistence"}
	}
	if appCore.DB == nil {
		return Check{Name: "database", Status: CheckFail, Details: "database_url configured but database handle is nil"}
	}
	db := readinessDatabase(appCore)
	if db == nil {
		return Check{Name: "database", Status: CheckFail, Details: "database_url configured but database handle is nil"}
	}
	if err := db.PingContext(ctx); err != nil {
		return Check{Name: "database", Status: CheckFail, Details: err.Error()}
	}
	return Check{Name: "database", Status: CheckPass}
}

func checkConfig(appCore *core.Core) Check {
	if !appCore.Config.Readiness {
		return Check{Name: "config.readiness", Status: CheckFail, Details: "readiness disabled by config"}
	}
	return Check{Name: "config.readiness", Status: CheckPass}
}

func checkTools(appCore *core.Core) Check {
	if appCore.Tools == nil || len(appCore.Tools.Cards()) == 0 {
		return Check{Name: "tool.registry", Status: CheckFail, Details: "no tools registered"}
	}
	return Check{Name: "tool.registry", Status: CheckPass}
}

func checkExecutionDomains(statuses []domain.ProductionStatus) Check {
	ready := []string{}
	disabled := []string{}
	for _, status := range statuses {
		if status.Enabled && status.Status == domain.ProductionReadyStatus {
			ready = append(ready, status.DomainID)
			continue
		}
		if !status.Enabled && status.Status == domain.DisabledStatus {
			disabled = append(disabled, status.DomainID)
		}
	}
	return Check{
		Name:    "execution.domains",
		Status:  CheckPass,
		Details: "production_ready=" + strings.Join(ready, ",") + "; disabled=" + strings.Join(disabled, ","),
	}
}

func checkTraceAudit(appCore *core.Core) Check {
	if appCore.Trace == nil || appCore.Audit == nil {
		return Check{Name: "governance", Status: CheckFail, Details: "trace or audit is not configured"}
	}
	return Check{Name: "governance", Status: CheckPass}
}

func checkMigrations(ctx context.Context, appCore *core.Core, dir string) Check {
	migrations, err := migration.LoadDir(dir)
	if err != nil {
		return Check{Name: "migration.files", Status: CheckFail, Details: err.Error()}
	}
	if len(migrations) == 0 {
		return Check{Name: "migration.files", Status: CheckWarn, Details: "no migration files found"}
	}
	if missing := migration.MissingRequiredObjects(migrations); len(missing) > 0 {
		return Check{Name: "migration.schema", Status: CheckFail, Details: "missing schema objects: " + strings.Join(missing, ", ")}
	}
	if db := readinessDatabase(appCore); db != nil {
		report, err := migration.ValidateLiveSchema(ctx, migration.PostgresInspector{DB: db}, migrations)
		if err != nil {
			return Check{Name: "migration.live_schema", Status: CheckFail, Details: err.Error()}
		}
		if report.Status != "ready" {
			return Check{Name: "migration.live_schema", Status: CheckFail, Details: report.Details()}
		}
		return Check{Name: "migration.live_schema", Status: CheckPass, Details: report.Details()}
	}
	return Check{Name: "migration.files", Status: CheckPass}
}

func readinessDatabase(appCore *core.Core) *sql.DB {
	if appCore == nil {
		return nil
	}
	if appCore.ReadinessDB != nil {
		return appCore.ReadinessDB
	}
	return appCore.DB
}

func checkReleaseSwitches(appCore *core.Core) Check {
	if len(appCore.Config.DisabledAgentIDs) > 0 || len(appCore.Config.DisabledToolIDs) > 0 || appCore.Config.DisableHandoff || appCore.Config.DisableExternalToolsInvoke {
		return Check{Name: "release.switches", Status: CheckWarn, Details: "one or more rollback/disable switches are active"}
	}
	return Check{Name: "release.switches", Status: CheckPass}
}
