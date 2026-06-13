package core

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"

	"znt/internal/agentdef/loader"
	agentpackage "znt/internal/agentdef/package"
	"znt/internal/app/config"
	contextconversation "znt/internal/context/conversation"
	"znt/internal/contracts"
	"znt/internal/knowledge"
	modelclient "znt/internal/model/client"
	"znt/internal/runtime/kernel"
)

func TestConfigureConversationJudgeModes(t *testing.T) {
	coordinator := kernel.Coordinator{}
	configureConversationJudge(&coordinator, config.Config{
		ConversationJudgeMode:        "model",
		ConversationJudgeTimeoutMS:   1200,
		ConversationRetrievalEnabled: false,
	}, modelclient.StubModelClient{})
	if judge, ok := coordinator.AddressingJudge.(contextconversation.ModelJudge); !ok {
		t.Fatalf("expected model addressing judge, got %T", coordinator.AddressingJudge)
	} else if judge.Timeout != 1200*time.Millisecond {
		t.Fatalf("expected model judge timeout, got %s", judge.Timeout)
	}
	if _, ok := coordinator.SufficiencyJudge.(contextconversation.ModelJudge); !ok {
		t.Fatalf("expected model sufficiency judge, got %T", coordinator.SufficiencyJudge)
	}
	if coordinator.DisableConversationRetrieval {
		t.Fatal("expected zero-value config to keep conversation retrieval enabled")
	}

	dir := t.TempDir()
	path := filepath.Join(dir, "config.json")
	if err := os.WriteFile(path, []byte(`{"service_name":"clean-core","version":"test","http_addr":":0","conversation_retrieval_enabled":false}`), 0o600); err != nil {
		t.Fatal(err)
	}
	cfg, err := config.Load(path)
	if err != nil {
		t.Fatal(err)
	}
	coordinator = kernel.Coordinator{}
	configureConversationJudge(&coordinator, cfg, modelclient.StubModelClient{})
	if !coordinator.DisableConversationRetrieval {
		t.Fatal("expected loaded config to disable conversation retrieval")
	}

	coordinator = kernel.Coordinator{}
	configureConversationJudge(&coordinator, config.Config{ConversationJudgeMode: "hybrid"}, modelclient.StubModelClient{})
	if _, ok := coordinator.AddressingJudge.(contextconversation.HybridJudge); !ok {
		t.Fatalf("expected hybrid addressing judge, got %T", coordinator.AddressingJudge)
	}
	if _, ok := coordinator.SufficiencyJudge.(contextconversation.ModelJudge); !ok {
		t.Fatalf("expected model sufficiency judge, got %T", coordinator.SufficiencyJudge)
	}
}

func TestCoreInitializesGroupExtensionServicesAndTools(t *testing.T) {
	core, err := New(config.Config{})
	if err != nil {
		t.Fatal(err)
	}
	if core.Identity == nil || core.GroupPermissions == nil || core.Knowledge == nil || core.CrossGroups == nil || core.TaskProgress == nil || core.AgentFactory == nil || core.Tones == nil || core.GovernanceProcesses == nil {
		t.Fatalf("expected group extension services to be initialized")
	}
	for _, toolID := range []string{
		"origin.identity.resolve_member",
		"origin.permission.check",
		"origin.skill.propose_update",
		"origin.agent.create_draft",
		"origin.agent.progress_query",
		"origin.knowledge.search",
		"origin.cross_group.search",
		"origin.memory.share",
	} {
		if _, ok := core.Tools.Get(toolID); !ok {
			t.Fatalf("expected tool %s to be registered", toolID)
		}
	}
}

func TestRestorePersistedAgentDefinitionsRestoresTenantDefaults(t *testing.T) {
	ctx := context.Background()
	registry := loader.NewStaticLoader()
	store := restorePackageStore{
		definitions: []contracts.AgentDefinition{
			{
				TenantID:         "tenant_1",
				AgentID:          "test-agent",
				Version:          "v2",
				PackageVersionID: "pkg_v2",
				Name:             "Restored Agent",
				IdentityPrompt:   "restored v2",
			},
		},
		assets: []agentpackage.AgentAsset{
			{
				TenantID:       "tenant_1",
				AgentID:        "test-agent",
				Status:         agentpackage.AgentAssetActive,
				ActiveVersion:  "v2",
				DefaultVersion: "v2",
			},
		},
	}
	if err := restorePersistedAgentDefinitions(ctx, registry, store); err != nil {
		t.Fatal(err)
	}
	loaded, err := registry.Load(ctx, "tenant_1", "test-agent", "")
	if err != nil {
		t.Fatal(err)
	}
	if loaded.Version != "v2" || loaded.PackageVersionID != "pkg_v2" {
		t.Fatalf("expected restored default v2, got %#v", loaded)
	}
	if got := registry.DefaultVersionForTenant("tenant_1", "test-agent"); got != "v2" {
		t.Fatalf("expected tenant default v2, got %s", got)
	}
}

func TestRestorePersistedAgentDefinitionsSkipsMissingAssetDefault(t *testing.T) {
	ctx := context.Background()
	registry := loader.NewStaticLoader()
	store := restorePackageStore{
		definitions: []contracts.AgentDefinition{
			{
				TenantID:       "tenant_1",
				AgentID:        "test-agent",
				Version:        "v1",
				IdentityPrompt: "restored v1",
			},
		},
		assets: []agentpackage.AgentAsset{
			{
				TenantID:       "tenant_1",
				AgentID:        "test-agent",
				Status:         agentpackage.AgentAssetActive,
				ActiveVersion:  "v_missing",
				DefaultVersion: "v_missing",
			},
		},
	}
	if err := restorePersistedAgentDefinitions(ctx, registry, store); err != nil {
		t.Fatal(err)
	}
	if got := registry.DefaultVersionForTenant("tenant_1", "test-agent"); got != "" {
		t.Fatalf("expected missing asset default to be skipped, got %s", got)
	}
	loaded, err := registry.Load(ctx, "tenant_1", "test-agent", "v1")
	if err != nil {
		t.Fatal(err)
	}
	if loaded.IdentityPrompt != "restored v1" {
		t.Fatalf("expected restored definition to remain explicitly loadable, got %#v", loaded)
	}
}

func TestCoreCrossGroupToolRequiresExplicitPermission(t *testing.T) {
	ctx := context.Background()
	core, err := New(config.Config{})
	if err != nil {
		t.Fatal(err)
	}
	base, err := core.Knowledge.CreateKnowledgeBase(ctx, knowledgeCreateInput("group-a"))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := core.Knowledge.IngestDocument(ctx, contracts.KnowledgeDocument{
		TenantID:        "tenant",
		KnowledgeBaseID: base.KnowledgeBaseID,
		SourceGroupID:   "group-a",
		Title:           "共享摘要",
		Content:         "A 群允许共享的上线摘要。",
		Visibility:      contracts.VisibilityShared,
	}); err != nil {
		t.Fatal(err)
	}
	tool, ok := core.Tools.Get("origin.cross_group.search")
	if !ok {
		t.Fatal("origin.cross_group.search not registered")
	}
	_, _, err = tool.Executor.Execute(ctx, contracts.ToolCall{
		TenantID: "tenant",
		ToolID:   "origin.cross_group.search",
		Arguments: map[string]any{
			"request_group_id": "group-b",
			"source_group_id":  "group-a",
			"requested_by":     "bob",
			"roles":            []any{"admin"},
			"query":            "上线摘要",
		},
	})
	if err == nil {
		t.Fatal("expected explicit cross-group permission to be required")
	}
	if err := core.GroupPermissions.UpsertPolicy(ctx, contracts.GroupPermissionPolicy{
		TenantID:       "tenant",
		GroupID:        "group-b",
		SubjectType:    contracts.PermissionSubjectRole,
		SubjectID:      "admin",
		Actions:        []string{contracts.PermissionActionCrossGroupSearch, contracts.PermissionActionKnowledgeSearch},
		ResourceScopes: []string{"group-a"},
	}); err != nil {
		t.Fatal(err)
	}
	if _, err := core.CrossGroups.UpsertSharePolicy(ctx, contracts.CrossGroupSharePolicy{
		TenantID:        "tenant",
		SourceGroupID:   "group-a",
		TargetGroupID:   "group-b",
		RedactionPolicy: contracts.RedactionPolicySummaryOnly,
		Status:          contracts.CrossGroupShareEnabled,
		CreatedBy:       "alice",
	}); err != nil {
		t.Fatal(err)
	}
	out, _, err := tool.Executor.Execute(ctx, contracts.ToolCall{
		TenantID: "tenant",
		ToolID:   "origin.cross_group.search",
		Arguments: map[string]any{
			"request_group_id": "group-b",
			"source_group_id":  "group-a",
			"requested_by":     "bob",
			"roles":            []any{"admin"},
			"query":            "上线摘要",
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	results, ok := out["results"].([]contracts.KnowledgeSearchResult)
	if !ok || len(results) != 1 {
		t.Fatalf("expected one cross-group result, got %#v", out)
	}
}

type restorePackageStore struct {
	definitions []contracts.AgentDefinition
	assets      []agentpackage.AgentAsset
}

func (s restorePackageStore) ListAgentDefinitions(context.Context) ([]contracts.AgentDefinition, error) {
	return s.definitions, nil
}

func (s restorePackageStore) ListAgentAssets(_ context.Context, tenantID contracts.TenantID) ([]agentpackage.AgentAsset, error) {
	out := make([]agentpackage.AgentAsset, 0, len(s.assets))
	for _, asset := range s.assets {
		if tenantID != "" && asset.TenantID != tenantID {
			continue
		}
		out = append(out, asset)
	}
	return out, nil
}

func knowledgeCreateInput(groupID contracts.GroupID) knowledge.CreateKnowledgeBaseInput {
	return knowledge.CreateKnowledgeBaseInput{
		TenantID:    "tenant",
		GroupID:     groupID,
		RequestedBy: "alice",
		Roles:       []string{"admin"},
		Name:        "共享库",
		Visibility:  contracts.VisibilityShared,
	}
}
