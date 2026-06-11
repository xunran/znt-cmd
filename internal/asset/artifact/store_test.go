package artifact

import (
	"context"
	"errors"
	"testing"

	"znt/internal/contracts"
	storagerepo "znt/internal/storage/repository"
)

func TestInMemoryStoreRequiresTenant(t *testing.T) {
	store := NewInMemoryStore()
	err := store.CreateArtifact(context.Background(), contracts.Artifact{ArtifactID: "artifact_1"})
	if err == nil {
		t.Fatal("expected create without tenant to fail")
	}
	if err := store.CreateArtifact(context.Background(), contracts.Artifact{
		ArtifactID: "artifact_1",
		TenantID:   "tenant_1",
	}); err != nil {
		t.Fatal(err)
	}
	if _, err := store.GetArtifact(context.Background(), "", "artifact_1"); !errors.Is(err, storagerepo.ErrNotFound) {
		t.Fatalf("expected empty tenant read to be denied, got %v", err)
	}
	if _, err := store.GetArtifact(context.Background(), "tenant_2", "artifact_1"); !errors.Is(err, storagerepo.ErrNotFound) {
		t.Fatalf("expected cross-tenant read to be denied, got %v", err)
	}
}
