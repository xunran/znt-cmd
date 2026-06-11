package postgres

import (
	"errors"
	"testing"

	"github.com/jackc/pgx/v5/pgconn"

	"znt/internal/agentcapability"
	agentpackage "znt/internal/agentdef/package"
	"znt/internal/agentfactory"
	"znt/internal/asset/artifact"
	"znt/internal/crossgroup"
	"znt/internal/governance/audit"
	processgovernance "znt/internal/governance/process"
	"znt/internal/governance/trace"
	"znt/internal/identity"
	"znt/internal/knowledge"
	"znt/internal/memoryscope"
	"znt/internal/permission"
	policyengine "znt/internal/policy/engine"
	runtimehook "znt/internal/runtime/hook"
	runrepo "znt/internal/runtime/run"
	"znt/internal/skillupdate"
	taskhandoff "znt/internal/task/handoff"
	taskprogress "znt/internal/task/progress"
	taskrepo "znt/internal/task/repository"
	"znt/internal/tone"
	toolcatalog "znt/internal/tool/catalog"
	toolrepo "znt/internal/tool/repository"
)

func TestRepositoryTypesImplementRuntimeInterfaces(t *testing.T) {
	var _ taskrepo.TaskRepository = (*TaskStore)(nil)
	var _ taskrepo.EventRepository = (*TaskStore)(nil)
	var _ taskrepo.AtomicRepository = (*TaskStore)(nil)
	var _ runrepo.Repository = (*RunRepository)(nil)
	var _ toolrepo.Repository = (*ToolRepository)(nil)
	var _ toolcatalog.Store = (*ToolCatalogStore)(nil)
	var _ runtimehook.Store = (*RuntimeHookStore)(nil)
	var _ trace.Recorder = (*TraceRecorder)(nil)
	var _ audit.Logger = (*AuditLogger)(nil)
	var _ artifact.Store = (*ArtifactStore)(nil)
	var _ artifact.ContextPackageStore = (*ContextPackageStore)(nil)
	var _ taskhandoff.Repository = (*HandoffRepository)(nil)
	var _ processgovernance.Store = (*GovernanceProcessStore)(nil)
	var _ agentpackage.Store = (*PackageStore)(nil)
	var _ agentpackage.ProjectionStore = (*PackageStore)(nil)
	var _ agentpackage.PromptProfileStore = (*PackageStore)(nil)
	var _ agentpackage.SkillDefinitionStore = (*PackageStore)(nil)
	var _ agentpackage.ToolBindingStore = (*PackageStore)(nil)
	var _ agentpackage.CollaboratorStore = (*PackageStore)(nil)
	var _ agentpackage.ExportedToolStore = (*PackageStore)(nil)
	var _ policyengine.Store = (*PolicyStore)(nil)
	var _ identity.Store = (*GroupMemberStore)(nil)
	var _ permission.Store = (*GroupPermissionPolicyStore)(nil)
	var _ memoryscope.Store = (*MemoryScopeStore)(nil)
	var _ knowledge.Store = (*KnowledgeStore)(nil)
	var _ crossgroup.Store = (*CrossGroupSharePolicyStore)(nil)
	var _ taskprogress.Store = (*GroupTaskBindingStore)(nil)
	var _ skillupdate.Store = (*SkillUpdateRequestStore)(nil)
	var _ agentcapability.Store = (*AgentCapabilityStore)(nil)
	var _ agentfactory.Store = (*AgentDraftRequestStore)(nil)
	var _ tone.Store = (*TonePolicyStore)(nil)
}

func TestSchemaNotReadyErrorDetection(t *testing.T) {
	if !isSchemaNotReadyError(&pgconn.PgError{Code: "42P01"}) {
		t.Fatal("undefined table should be treated as schema not ready")
	}
	if !isSchemaNotReadyError(&pgconn.PgError{Code: "42703"}) {
		t.Fatal("undefined column should be treated as schema not ready")
	}
	if isSchemaNotReadyError(&pgconn.PgError{Code: "23505"}) {
		t.Fatal("unique violation should not be treated as schema not ready")
	}
	if isSchemaNotReadyError(errors.New("plain error")) {
		t.Fatal("plain errors should not be treated as schema not ready")
	}
}
