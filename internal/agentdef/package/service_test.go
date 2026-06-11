package agentpackage

import (
	"context"
	"testing"

	"znt/internal/contracts"
	"znt/internal/governance/audit"
)

func TestPackageDraftValidatePublishWritesAudit(t *testing.T) {
	auditLogger := audit.NewInMemoryLogger()
	service := NewService(auditLogger)
	draft, err := service.CreateDraft(context.Background(), "tenant_1", "agent_1", "v1", AgentPackageSource{
		AgentsMD: "agent",
		Prompt:   "prompt",
		ToolBindings: contracts.AgentToolsConfig{
			AllowedToolIDs: []string{"echo"},
		},
	}, "optimizer_1")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := service.ValidateDraft(context.Background(), draft.DraftID); err != nil {
		t.Fatal(err)
	}
	release, err := service.PublishDraft(context.Background(), draft.DraftID, "optimizer_1")
	if err != nil {
		t.Fatal(err)
	}
	if release.Status != contracts.ReleasePublished || release.SourceHash == "" || release.CompiledHash == "" {
		t.Fatalf("unexpected release: %#v", release)
	}
	events, err := auditLogger.Search(context.Background(), audit.Filter{Action: contracts.AuditAgentPackagePublish})
	if err != nil {
		t.Fatal(err)
	}
	if len(events) != 1 {
		t.Fatalf("expected publish audit, got %d", len(events))
	}
}

func TestPackageCanaryStableRollback(t *testing.T) {
	service := NewService(nil)
	draft, err := service.CreateDraft(context.Background(), "tenant_1", "agent_1", "v1", AgentPackageSource{Prompt: "prompt"}, "optimizer_1")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := service.ValidateDraft(context.Background(), draft.DraftID); err != nil {
		t.Fatal(err)
	}
	release, err := service.PublishDraft(context.Background(), draft.DraftID, "optimizer_1")
	if err != nil {
		t.Fatal(err)
	}
	canary, err := service.MarkCanary(context.Background(), release.PackageVersionID, "optimizer_1")
	if err != nil {
		t.Fatal(err)
	}
	if canary.Status != contracts.ReleaseCanary {
		t.Fatalf("expected canary, got %#v", canary)
	}
	if _, err := service.MarkStable(context.Background(), release.PackageVersionID, "optimizer_1"); err == nil {
		t.Fatal("expected stable to require eval pass")
	}
	if _, err := service.MarkEvalResult(context.Background(), release.PackageVersionID, true, "eval", "passed"); err != nil {
		t.Fatal(err)
	}
	stable, err := service.MarkStable(context.Background(), release.PackageVersionID, "optimizer_1")
	if err != nil {
		t.Fatal(err)
	}
	if stable.Status != contracts.ReleaseStable {
		t.Fatalf("expected stable, got %#v", stable)
	}
	rolledBack, err := service.Rollback(context.Background(), release.PackageVersionID, "optimizer_1", "bad eval")
	if err != nil {
		t.Fatal(err)
	}
	if rolledBack.Status != contracts.ReleaseRolledBack {
		t.Fatalf("expected rolled back, got %#v", rolledBack)
	}
	if _, err := service.MarkCanary(context.Background(), release.PackageVersionID, "optimizer_1"); err == nil {
		t.Fatal("expected rolled back release to reject canary transition")
	}
	if _, err := service.MarkEvalResult(context.Background(), release.PackageVersionID, true, "eval", "passed after rollback"); err == nil {
		t.Fatal("expected rolled back release to reject eval transition")
	}
	afterRollback, ok := service.GetRelease(release.PackageVersionID)
	if !ok {
		t.Fatal("expected release to remain available")
	}
	if afterRollback.Status != contracts.ReleaseRolledBack {
		t.Fatalf("expected release to stay rolled_back, got %s", afterRollback.Status)
	}
}

func TestPublishDraftRejectsDuplicateAgentVersion(t *testing.T) {
	service := NewService(nil)
	first, err := service.CreateDraft(context.Background(), "tenant_1", "agent_1", "v1", AgentPackageSource{Prompt: "prompt"}, "optimizer_1")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := service.ValidateDraft(context.Background(), first.DraftID); err != nil {
		t.Fatal(err)
	}
	if _, err := service.PublishDraft(context.Background(), first.DraftID, "optimizer_1"); err != nil {
		t.Fatal(err)
	}
	second, err := service.CreateDraft(context.Background(), "tenant_1", "agent_1", "v1", AgentPackageSource{Prompt: "prompt v2"}, "optimizer_1")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := service.ValidateDraft(context.Background(), second.DraftID); err != nil {
		t.Fatal(err)
	}
	if _, err := service.PublishDraft(context.Background(), second.DraftID, "optimizer_1"); err == nil {
		t.Fatal("expected duplicate agent version publish to fail")
	}
}

func TestDraftPatchAgentsMDAndSkillLifecycle(t *testing.T) {
	service := NewService(nil)
	draft, err := service.CreateDraft(context.Background(), "tenant_1", "agent_1", "v1", AgentPackageSource{Prompt: "prompt"}, "optimizer_1")
	if err != nil {
		t.Fatal(err)
	}
	draft, err = service.PatchAgentsMDForTenant(context.Background(), "tenant_1", draft.DraftID, "agent markdown", "optimizer_1")
	if err != nil {
		t.Fatal(err)
	}
	if draft.Source.AgentsMD != "agent markdown" {
		t.Fatalf("expected agents_md update, got %#v", draft.Source)
	}
	draft, err = service.PatchDeveloperPromptForTenant(context.Background(), "tenant_1", draft.DraftID, "developer strategy", "optimizer_1")
	if err != nil {
		t.Fatal(err)
	}
	draft, err = service.PatchSystemPromptForTenant(context.Background(), "tenant_1", draft.DraftID, "system contract", "optimizer_1")
	if err != nil {
		t.Fatal(err)
	}
	compiled, err := Compile(draft.AgentID, draft.Version, draft.Source)
	if err != nil {
		t.Fatal(err)
	}
	if compiled.DeveloperPrompt != "developer strategy" || compiled.SystemPrompt != "system contract" {
		t.Fatalf("expected metadata prompts to compile, got %#v", compiled)
	}
	draft, err = service.UpsertSkillForTenant(context.Background(), "tenant_1", draft.DraftID, SkillDraftInput{
		SkillID:                 "pkg.review",
		Version:                 "v1",
		Name:                    "Review",
		Instruction:             "Review with package rules.",
		RiskLevel:               contracts.RiskMedium,
		RecommendedTools:        []string{"artifact.read"},
		AllowedTools:            []string{"echo"},
		RecommendedMemoryReads:  []string{"rules"},
		RecommendedMemoryWrites: []string{"review_notes"},
		RecommendedHandoffs:     []string{"qa-agent"},
		CompletionCriteria:      []string{"review complete"},
		OutputSchema:            map[string]any{"type": "object"},
		Resources: []contracts.SkillResourceRef{{
			ResourceID: "rules",
			Type:       "artifact",
			URI:        "memory://rules",
			LoadPolicy: "reference",
		}},
	}, "optimizer_1")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := service.ValidateDraft(context.Background(), draft.DraftID); err != nil {
		t.Fatal(err)
	}
	compiled, err = Compile(draft.AgentID, draft.Version, draft.Source)
	if err != nil {
		t.Fatal(err)
	}
	if len(compiled.SkillDefinitions) != 1 || compiled.SkillDefinitions[0].Card.SkillID != "pkg.review" {
		t.Fatalf("expected skill to compile from draft, got %#v", compiled.SkillDefinitions)
	}
	if compiled.SkillDefinitions[0].OutputSchema["type"] != "object" || len(compiled.SkillDefinitions[0].RecommendedTools) != 1 {
		t.Fatalf("expected V3 skill fields to compile from draft, got %#v", compiled.SkillDefinitions[0])
	}
	draft, err = service.RemoveSkillForTenant(context.Background(), "tenant_1", draft.DraftID, "pkg.review", "v1", "optimizer_1")
	if err != nil {
		t.Fatal(err)
	}
	compiled, err = Compile(draft.AgentID, draft.Version, draft.Source)
	if err != nil {
		t.Fatal(err)
	}
	if len(compiled.SkillDefinitions) != 0 {
		t.Fatalf("expected skill removal, got %#v", compiled.SkillDefinitions)
	}
}

func TestProposalLifecyclePublishesApprovedDraft(t *testing.T) {
	auditLogger := audit.NewInMemoryLogger()
	service := NewService(auditLogger)
	draft, err := service.CreateDraft(context.Background(), "tenant_1", "agent_1", "v1", AgentPackageSource{Prompt: "prompt"}, "optimizer_1")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := service.ValidateDraftForTenant(context.Background(), "tenant_1", draft.DraftID, "optimizer_1"); err != nil {
		t.Fatal(err)
	}
	proposal, err := service.CreateProposalForTenant(context.Background(), "tenant_1", draft.DraftID, "PromptPatchProposal", "Improve prompt", "better answer quality", map[string]any{"prompt": "prompt v2"}, "agent_1")
	if err != nil {
		t.Fatal(err)
	}
	if proposal.Status != contracts.ProposalDraft || proposal.DraftID != draft.DraftID {
		t.Fatalf("unexpected proposal: %#v", proposal)
	}
	if _, err := service.PublishProposalForTenant(context.Background(), "tenant_1", proposal.ProposalID, "optimizer_1"); err == nil {
		t.Fatal("expected unapproved proposal publish to fail")
	}
	proposal, err = service.SubmitProposalForTenant(context.Background(), "tenant_1", proposal.ProposalID, "agent_1")
	if err != nil {
		t.Fatal(err)
	}
	if proposal.Status != contracts.ProposalPendingReview {
		t.Fatalf("expected pending review, got %#v", proposal)
	}
	proposal, err = service.ApproveProposalForTenant(context.Background(), "tenant_1", proposal.ProposalID, "reviewer_1")
	if err != nil {
		t.Fatal(err)
	}
	if proposal.Status != contracts.ProposalApproved || proposal.ReviewedBy != "reviewer_1" {
		t.Fatalf("expected approved proposal, got %#v", proposal)
	}
	release, err := service.PublishProposalForTenant(context.Background(), "tenant_1", proposal.ProposalID, "optimizer_1")
	if err != nil {
		t.Fatal(err)
	}
	if release.Status != contracts.ReleasePublished {
		t.Fatalf("expected published release, got %#v", release)
	}
	events, err := auditLogger.Search(context.Background(), audit.Filter{ResourceID: string(proposal.ProposalID)})
	if err != nil {
		t.Fatal(err)
	}
	if len(events) < 4 {
		t.Fatalf("expected proposal audit lifecycle, got %d", len(events))
	}
}
