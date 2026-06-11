package knowledge

import (
	"context"
	"testing"

	"znt/internal/contracts"
	"znt/internal/permission"
)

func TestKnowledgeSearchRespectsGroupVisibility(t *testing.T) {
	ctx := context.Background()
	permissions := permission.NewInMemoryService(nil, nil)
	for _, action := range []string{contracts.PermissionActionKnowledgeCreate, contracts.PermissionActionKnowledgeSearch} {
		if err := permissions.UpsertPolicy(ctx, contracts.GroupPermissionPolicy{
			TenantID:    "tenant",
			GroupID:     "group-a",
			SubjectType: contracts.PermissionSubjectRole,
			SubjectID:   "admin",
			Actions:     []string{action},
		}); err != nil {
			t.Fatal(err)
		}
	}
	if err := permissions.UpsertPolicy(ctx, contracts.GroupPermissionPolicy{
		TenantID:    "tenant",
		GroupID:     "group-b",
		SubjectType: contracts.PermissionSubjectRole,
		SubjectID:   "admin",
		Actions:     []string{contracts.PermissionActionKnowledgeSearch},
	}); err != nil {
		t.Fatal(err)
	}
	svc := NewInMemoryService(permissions, nil, nil)
	base, err := svc.CreateKnowledgeBase(ctx, CreateKnowledgeBaseInput{
		TenantID:    "tenant",
		GroupID:     "group-a",
		RequestedBy: "alice",
		Roles:       []string{"admin"},
		Name:        "A 群知识库",
		Visibility:  contracts.VisibilityGroup,
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := svc.IngestDocument(ctx, contracts.KnowledgeDocument{
		TenantID:        "tenant",
		KnowledgeBaseID: base.KnowledgeBaseID,
		SourceGroupID:   "group-a",
		Title:           "上线计划",
		Content:         "上线需要通过真实模型回归和权限测试。",
	}); err != nil {
		t.Fatal(err)
	}
	results, err := svc.Search(ctx, SearchInput{
		TenantID:         "tenant",
		RequesterGroupID: "group-b",
		RequestedBy:      "bob",
		Roles:            []string{"admin"},
		Query:            "真实模型",
		AllowCrossGroup:  true,
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(results) != 0 {
		t.Fatalf("expected private group knowledge to stay hidden, got %#v", results)
	}
	base.Visibility = contracts.VisibilityShared
	svc.mu.Lock()
	svc.bases[base.KnowledgeBaseID] = base
	for id, doc := range svc.documents {
		doc.Visibility = contracts.VisibilityShared
		svc.documents[id] = doc
	}
	svc.mu.Unlock()
	results, err = svc.Search(ctx, SearchInput{
		TenantID:         "tenant",
		RequesterGroupID: "group-b",
		RequestedBy:      "bob",
		Roles:            []string{"admin"},
		Query:            "真实模型",
		AllowCrossGroup:  true,
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(results) != 1 {
		t.Fatalf("expected shared result, got %#v", results)
	}
}

func TestKnowledgeIngestionJobAndHybridSearchMode(t *testing.T) {
	ctx := context.Background()
	svc := NewInMemoryService(nil, nil, nil)
	base, err := svc.CreateKnowledgeBase(ctx, CreateKnowledgeBaseInput{
		TenantID:   "tenant",
		GroupID:    "group-a",
		Name:       "Hybrid KB",
		Visibility: contracts.VisibilityGroup,
		IndexType:  contracts.KnowledgeIndexHybrid,
	})
	if err != nil {
		t.Fatal(err)
	}
	doc, err := svc.IngestDocument(ctx, contracts.KnowledgeDocument{
		TenantID:        "tenant",
		KnowledgeBaseID: base.KnowledgeBaseID,
		SourceGroupID:   "group-a",
		Title:           "Hybrid Retrieval",
		Content:         "Hybrid retrieval combines lexical and embedding-style matching.",
	})
	if err != nil {
		t.Fatal(err)
	}
	if doc.IndexStatus != contracts.KnowledgeDocumentIndexReady || doc.IndexedAt == nil {
		t.Fatalf("expected indexed document, got %#v", doc)
	}
	jobs, err := svc.ListIngestionJobs(ctx, "tenant", base.KnowledgeBaseID)
	if err != nil {
		t.Fatal(err)
	}
	if len(jobs) != 1 || jobs[0].Status != contracts.KnowledgeIngestionCompleted || jobs[0].SearchMode != contracts.KnowledgeSearchHybrid {
		t.Fatalf("unexpected jobs: %#v", jobs)
	}
	results, err := svc.Search(ctx, SearchInput{
		TenantID:         "tenant",
		RequesterGroupID: "group-a",
		Query:            "Hybrid",
		SearchMode:       contracts.KnowledgeSearchHybrid,
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(results) != 1 || results[0].SearchMode != contracts.KnowledgeSearchHybrid {
		t.Fatalf("expected hybrid result, got %#v", results)
	}
	updated, ok, err := svc.GetKnowledgeBase(ctx, "tenant", base.KnowledgeBaseID)
	if err != nil || !ok {
		t.Fatalf("expected base, ok=%v err=%v", ok, err)
	}
	if updated.DocumentCount != 1 || updated.LastIndexedAt == nil {
		t.Fatalf("expected base index status to update, got %#v", updated)
	}
}
