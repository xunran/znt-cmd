package crossgroup

import (
	"context"
	"testing"

	"znt/internal/contracts"
	"znt/internal/knowledge"
	"znt/internal/permission"
)

func TestCrossGroupSearchDefaultDenyThenAllowed(t *testing.T) {
	ctx := context.Background()
	permissions := permission.NewInMemoryService(nil, nil)
	if err := permissions.UpsertPolicy(ctx, contracts.GroupPermissionPolicy{
		TenantID:    "tenant",
		GroupID:     "group-b",
		SubjectType: contracts.PermissionSubjectRole,
		SubjectID:   "admin",
		Actions:     []string{contracts.PermissionActionKnowledgeSearch},
	}); err != nil {
		t.Fatal(err)
	}
	knowledgeSvc := knowledge.NewInMemoryService(nil, nil, nil)
	base, err := knowledgeSvc.CreateKnowledgeBase(ctx, knowledge.CreateKnowledgeBaseInput{
		TenantID:   "tenant",
		GroupID:    "group-a",
		Name:       "共享知识",
		Visibility: contracts.VisibilityShared,
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := knowledgeSvc.IngestDocument(ctx, contracts.KnowledgeDocument{
		TenantID:        "tenant",
		KnowledgeBaseID: base.KnowledgeBaseID,
		SourceGroupID:   "group-a",
		Title:           "发布窗口",
		Content:         "B 群可以查询发布窗口摘要。",
		Visibility:      contracts.VisibilityShared,
	}); err != nil {
		t.Fatal(err)
	}
	svc := NewService(knowledgeSvc, permissions, nil, nil)
	_, err = svc.Search(ctx, SearchInput{
		TenantID:       "tenant",
		RequestGroupID: "group-b",
		SourceGroupID:  "group-a",
		RequestedBy:    "bob",
		Roles:          []string{"admin"},
		Query:          "发布窗口",
	})
	if err == nil {
		t.Fatal("expected cross-group search to be denied by default")
	}
	if err := permissions.UpsertPolicy(ctx, contracts.GroupPermissionPolicy{
		TenantID:       "tenant",
		GroupID:        "group-b",
		SubjectType:    contracts.PermissionSubjectRole,
		SubjectID:      "admin",
		Actions:        []string{contracts.PermissionActionCrossGroupSearch},
		ResourceScopes: []string{"group-a"},
	}); err != nil {
		t.Fatal(err)
	}
	_, err = svc.Search(ctx, SearchInput{
		TenantID:       "tenant",
		RequestGroupID: "group-b",
		SourceGroupID:  "group-a",
		RequestedBy:    "bob",
		Roles:          []string{"admin"},
		Query:          "发布窗口",
	})
	if err == nil {
		t.Fatal("expected share policy to be required even after search permission")
	}
	if _, err := svc.UpsertSharePolicy(ctx, contracts.CrossGroupSharePolicy{
		TenantID:        "tenant",
		SourceGroupID:   "group-a",
		TargetGroupID:   "group-b",
		RedactionPolicy: contracts.RedactionPolicySummaryOnly,
		Status:          contracts.CrossGroupShareEnabled,
		CreatedBy:       "alice",
	}); err != nil {
		t.Fatal(err)
	}
	results, err := svc.Search(ctx, SearchInput{
		TenantID:       "tenant",
		RequestGroupID: "group-b",
		SourceGroupID:  "group-a",
		RequestedBy:    "bob",
		Roles:          []string{"admin"},
		Query:          "发布窗口",
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(results) != 1 {
		t.Fatalf("expected one result, got %#v", results)
	}
	if !results[0].Redacted || results[0].RedactionPolicy != contracts.RedactionPolicySummaryOnly {
		t.Fatalf("expected redacted result, got %#v", results[0])
	}
}
