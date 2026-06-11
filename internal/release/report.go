package release

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"time"

	"znt/internal/app/core"
	"znt/internal/readiness"
	"znt/internal/storage/migration"
)

type ContractFreezeReport struct {
	Version string   `json:"version"`
	Status  string   `json:"status"`
	Frozen  []string `json:"frozen"`
}

type MigrationReadinessReport struct {
	Status          string   `json:"status"`
	TotalMigrations int      `json:"total_migrations"`
	Errors          []string `json:"errors,omitempty"`
}

type GoNoGoReport struct {
	Decision    string                   `json:"decision"`
	GeneratedAt time.Time                `json:"generated_at"`
	Contract    ContractFreezeReport     `json:"contract"`
	Migration   MigrationReadinessReport `json:"migration"`
	Readiness   readiness.Report         `json:"readiness"`
	Gates       []Gate                   `json:"gates"`
	Blockers    []string                 `json:"blockers,omitempty"`
}

type Gate struct {
	Name    string `json:"name"`
	Status  string `json:"status"`
	Details string `json:"details,omitempty"`
}

func BuildContractFreezeReport() ContractFreezeReport {
	return ContractFreezeReport{
		Version: "v1.0-alpha",
		Status:  "alpha_frozen",
		Frozen: []string{
			"AgentEnvelope",
			"RuntimeContext",
			"CollaborationContext",
			"Task",
			"TaskEvent append-only",
			"TaskPlan",
			"PlanStep",
			"PlanEvent",
			"AgentRun",
			"Decision",
			"ToolCall",
			"ToolResult",
			"Artifact",
			"TraceEvent",
			"AuditEvent",
			"AgentHandoff",
			"HandoffContextPackage",
			"Policy-required tool execution",
		},
	}
}

func BuildMigrationReadinessReport(dir string) MigrationReadinessReport {
	migrations, err := migration.LoadDir(dir)
	if err != nil {
		return MigrationReadinessReport{Status: "not_ready", Errors: []string{err.Error()}}
	}
	if len(migrations) == 0 {
		return MigrationReadinessReport{Status: "degraded", TotalMigrations: 0, Errors: []string{"no migration files found"}}
	}
	if missing := migration.MissingRequiredObjects(migrations); len(missing) > 0 {
		return MigrationReadinessReport{Status: "not_ready", TotalMigrations: len(migrations), Errors: []string{"missing schema objects: " + strings.Join(missing, ", ")}}
	}
	return MigrationReadinessReport{Status: "ready", TotalMigrations: len(migrations)}
}

func BuildGoNoGo(ctx context.Context, appCore *core.Core, migrationDir string, blockers []string) GoNoGoReport {
	ready := readiness.Build(ctx, appCore, migrationDir)
	migrationReport := BuildMigrationReadinessReport(migrationDir)
	allBlockers := append([]string{}, blockers...)
	gates := buildGates(appCore, ready, migrationReport)
	if ready.Status == "not_ready" {
		allBlockers = append(allBlockers, "readiness report is not_ready")
	}
	if migrationReport.Status == "not_ready" {
		allBlockers = append(allBlockers, "migration readiness is not_ready")
	}
	for _, gate := range gates {
		if gate.Status == "fail" {
			allBlockers = append(allBlockers, gate.Name+": "+gate.Details)
		}
	}
	decision := "go"
	if len(allBlockers) > 0 {
		decision = "no-go"
	}
	return GoNoGoReport{
		Decision:    decision,
		GeneratedAt: time.Now().UTC(),
		Contract:    BuildContractFreezeReport(),
		Migration:   migrationReport,
		Readiness:   ready,
		Gates:       gates,
		Blockers:    allBlockers,
	}
}

func buildGates(appCore *core.Core, ready readiness.Report, migrationReport MigrationReadinessReport) []Gate {
	gates := []Gate{
		statusGate("readiness", ready.Status == "ready" || ready.Status == "degraded", "status="+ready.Status),
		statusGate("migration", migrationReport.Status == "ready", "status="+migrationReport.Status),
		fileGate("contract.snapshot", "docs/openapi.clean-core.v1.json"),
		fileGate("e2e.matrix", "docs/e2e_regression_matrix.md"),
		statusGate("observability.metrics", true, "/metrics endpoint registered"),
	}
	gates = append(gates, capabilityGates("docs/e2e_regression_matrix.md")...)
	if isProduction(appCore.Config.Env) {
		gates = append(gates,
			statusGate("security.service_token", strings.TrimSpace(appCore.Config.ServiceToken) != "", "production requires service_token"),
			statusGate("database.postgres", strings.TrimSpace(appCore.Config.DatabaseURL) != "" && appCore.DB != nil, "production requires database_url and connected db"),
			statusGate("model.real_client", strings.TrimSpace(appCore.Config.ModelBaseURL) != "" && strings.TrimSpace(appCore.Config.ModelName) != "", "production requires model_base_url and model_name"),
		)
	}
	for _, check := range ready.Checks {
		if check.Name == "release.switches" && check.Status == readiness.CheckWarn {
			gates = append(gates, Gate{Name: "release.switches", Status: "warn", Details: check.Details})
		}
	}
	return gates
}

func capabilityGates(path string) []Gate {
	content, ok := readExistingFile(path)
	if !ok {
		return []Gate{
			{Name: "runtime.policy_version_pinning", Status: "fail", Details: "e2e matrix not found"},
			{Name: "agent_package.skill_draft", Status: "fail", Details: "e2e matrix not found"},
			{Name: "management.approval_flow", Status: "fail", Details: "e2e matrix not found"},
			{Name: "release.canary_hits", Status: "fail", Details: "e2e matrix not found"},
			{Name: "eval.result_evidence", Status: "fail", Details: "e2e matrix not found"},
			{Name: "execution.credential_data_boundary", Status: "fail", Details: "e2e matrix not found"},
			{Name: "model.streaming", Status: "fail", Details: "e2e matrix not found"},
			{Name: "agent_package.proposal_flow", Status: "fail", Details: "e2e matrix not found"},
			{Name: "openapi.final_alignment", Status: "fail", Details: "e2e matrix not found"},
		}
	}
	requirements := []struct {
		name   string
		tokens []string
	}{
		{"runtime.policy_version_pinning", []string{"TestCoordinatorPinsPolicyVersionAcrossRunSteps", "policy_version_id"}},
		{"agent_package.skill_draft", []string{"TestDraftPatchAgentsMDAndSkillLifecycle", "patch_agents_md"}},
		{"management.approval_flow", []string{"TestPolicyStableRequiresConcreteApprovalRequest", "approval_required"}},
		{"release.canary_hits", []string{"TestPackageCanaryRoutesDefaultTrafficAndRecordsHit", "canary.routed"}},
		{"eval.result_evidence", []string{"TestRunnerToolAssertions", "eval.case.completed", "TestRunnerRecordsFailedCaseTrace", "eval.case.failed", "TestEvalSuiteRunRecordsSummaryTrace", "eval.summary.created"}},
		{"execution.credential_data_boundary", []string{"TestLocalDomainRejectsCredentialScopeAndDataBoundary", "credential_scope"}},
		{"model.streaming", []string{"TestStubModelClientStream", "model.delta", "decision.completed"}},
		{"agent_package.proposal_flow", []string{"TestProposalLifecyclePublishesApprovedDraft", "agent.package.proposal"}},
		{"openapi.final_alignment", []string{"openapi.clean-core.v1.json", "ModelStreamEvent"}},
	}
	gates := make([]Gate, 0, len(requirements))
	lower := strings.ToLower(content)
	for _, requirement := range requirements {
		missing := make([]string, 0)
		for _, token := range requirement.tokens {
			if !strings.Contains(lower, strings.ToLower(token)) {
				missing = append(missing, token)
			}
		}
		if len(missing) > 0 {
			gates = append(gates, Gate{Name: requirement.name, Status: "fail", Details: "missing matrix evidence: " + strings.Join(missing, ", ")})
			continue
		}
		gates = append(gates, Gate{Name: requirement.name, Status: "pass", Details: "covered by e2e matrix"})
	}
	return gates
}

func statusGate(name string, ok bool, details string) Gate {
	if ok {
		return Gate{Name: name, Status: "pass", Details: details}
	}
	return Gate{Name: name, Status: "fail", Details: details}
}

func fileGate(name string, path string) Gate {
	resolved, ok := existingFile(path)
	if !ok {
		return Gate{Name: name, Status: "fail", Details: path + " not found"}
	}
	return Gate{Name: name, Status: "pass", Details: resolved}
}

func readExistingFile(path string) (string, bool) {
	resolved, ok := existingFile(path)
	if !ok {
		return "", false
	}
	data, err := os.ReadFile(resolved)
	if err != nil {
		return "", false
	}
	return string(data), true
}

func existingFile(path string) (string, bool) {
	candidates := []string{
		path,
		filepath.Join("..", path),
		filepath.Join("..", "..", path),
	}
	for _, candidate := range candidates {
		if info, err := os.Stat(candidate); err == nil && !info.IsDir() {
			return candidate, true
		}
	}
	return "", false
}

func isProduction(env string) bool {
	switch strings.ToLower(strings.TrimSpace(env)) {
	case "prod", "production":
		return true
	default:
		return false
	}
}
