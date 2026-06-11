package artifact

import (
	"context"
	"fmt"
	"sync"
	"time"

	"znt/internal/contracts"
	"znt/internal/governance/audit"
	storagerepo "znt/internal/storage/repository"
	"znt/pkg/idgen"
)

type Store interface {
	CreateArtifact(ctx context.Context, artifact contracts.Artifact) error
	GetArtifact(ctx context.Context, tenantID contracts.TenantID, artifactID contracts.ArtifactID) (contracts.Artifact, error)
	ReadArtifact(ctx context.Context, tenantID contracts.TenantID, artifactID contracts.ArtifactID, actorID string, actorType string, traceID contracts.TraceID) (contracts.Artifact, error)
	DeleteArtifact(ctx context.Context, tenantID contracts.TenantID, artifactID contracts.ArtifactID, actorID string, actorType string, traceID contracts.TraceID, reason string) error
	Summary(ctx context.Context, tenantID contracts.TenantID, artifactID contracts.ArtifactID) (contracts.ArtifactRef, error)
}

type ContentStore interface {
	CreateArtifactWithContent(ctx context.Context, artifact contracts.Artifact, content string) error
	ReadContent(ctx context.Context, tenantID contracts.TenantID, artifactID contracts.ArtifactID) (string, error)
}

type InMemoryStore struct {
	mu        sync.RWMutex
	artifacts map[contracts.ArtifactID]contracts.Artifact
	content   map[contracts.ArtifactID]string
	audit     audit.Logger
	now       func() time.Time
}

func NewInMemoryStore() *InMemoryStore {
	return &InMemoryStore{artifacts: map[contracts.ArtifactID]contracts.Artifact{}, content: map[contracts.ArtifactID]string{}, now: func() time.Time { return time.Now().UTC() }}
}

func NewInMemoryStoreWithAudit(auditLogger audit.Logger) *InMemoryStore {
	store := NewInMemoryStore()
	store.audit = auditLogger
	return store
}

func (s *InMemoryStore) CreateArtifact(_ context.Context, artifact contracts.Artifact) error {
	if artifact.TenantID == "" {
		return fmt.Errorf("artifact tenant_id is required")
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if _, ok := s.artifacts[artifact.ArtifactID]; ok {
		return storagerepo.ErrDuplicateRequest
	}
	s.artifacts[artifact.ArtifactID] = artifact
	return nil
}

func (s *InMemoryStore) CreateArtifactWithContent(_ context.Context, artifact contracts.Artifact, content string) error {
	if artifact.TenantID == "" {
		return fmt.Errorf("artifact tenant_id is required")
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if _, ok := s.artifacts[artifact.ArtifactID]; ok {
		return storagerepo.ErrDuplicateRequest
	}
	s.artifacts[artifact.ArtifactID] = artifact
	s.content[artifact.ArtifactID] = content
	return nil
}

func (s *InMemoryStore) GetArtifact(_ context.Context, tenantID contracts.TenantID, artifactID contracts.ArtifactID) (contracts.Artifact, error) {
	if tenantID == "" {
		return contracts.Artifact{}, storagerepo.ErrNotFound
	}
	s.mu.RLock()
	defer s.mu.RUnlock()
	artifact, ok := s.artifacts[artifactID]
	if !ok {
		return contracts.Artifact{}, storagerepo.ErrNotFound
	}
	if tenantID != "" && artifact.TenantID != tenantID {
		return contracts.Artifact{}, storagerepo.ErrNotFound
	}
	return artifact, nil
}

func (s *InMemoryStore) ReadArtifact(ctx context.Context, tenantID contracts.TenantID, artifactID contracts.ArtifactID, actorID string, actorType string, traceID contracts.TraceID) (contracts.Artifact, error) {
	artifact, err := s.GetArtifact(ctx, tenantID, artifactID)
	if err != nil {
		return contracts.Artifact{}, err
	}
	s.auditEvent(ctx, tenantID, actorID, actorType, "artifact.read", string(artifactID), "allowed", "", traceID)
	return artifact, nil
}

func (s *InMemoryStore) DeleteArtifact(ctx context.Context, tenantID contracts.TenantID, artifactID contracts.ArtifactID, actorID string, actorType string, traceID contracts.TraceID, reason string) error {
	if tenantID == "" {
		return storagerepo.ErrNotFound
	}
	s.mu.Lock()
	artifact, ok := s.artifacts[artifactID]
	if !ok || artifact.TenantID != tenantID {
		s.mu.Unlock()
		return storagerepo.ErrNotFound
	}
	delete(s.artifacts, artifactID)
	delete(s.content, artifactID)
	s.mu.Unlock()
	s.auditEvent(ctx, tenantID, actorID, actorType, contracts.AuditArtifactDelete, string(artifactID), "allowed", reason, traceID)
	return nil
}

func (s *InMemoryStore) ReadContent(ctx context.Context, tenantID contracts.TenantID, artifactID contracts.ArtifactID) (string, error) {
	if _, err := s.GetArtifact(ctx, tenantID, artifactID); err != nil {
		return "", err
	}
	s.mu.RLock()
	defer s.mu.RUnlock()
	content, ok := s.content[artifactID]
	if !ok {
		return "", storagerepo.ErrNotFound
	}
	return content, nil
}

func (s *InMemoryStore) auditEvent(ctx context.Context, tenantID contracts.TenantID, actorID string, actorType string, action string, resourceID string, decision string, reason string, traceID contracts.TraceID) {
	if s.audit == nil {
		return
	}
	_ = s.audit.Log(ctx, contracts.AuditEvent{
		AuditID:      idgen.New("audit"),
		TenantID:     tenantID,
		ActorID:      actorID,
		ActorType:    actorType,
		Action:       action,
		ResourceType: "artifact",
		ResourceID:   resourceID,
		Decision:     decision,
		Reason:       reason,
		TraceID:      traceID,
		CreatedAt:    s.now(),
	})
}

func (s *InMemoryStore) Summary(ctx context.Context, tenantID contracts.TenantID, artifactID contracts.ArtifactID) (contracts.ArtifactRef, error) {
	artifact, err := s.GetArtifact(ctx, tenantID, artifactID)
	if err != nil {
		return contracts.ArtifactRef{}, err
	}
	return contracts.ArtifactRef{
		ArtifactID: artifact.ArtifactID,
		Type:       artifact.Type,
		URI:        artifact.StorageURI,
		Summary:    artifact.Name,
		Hash:       artifact.Hash,
	}, nil
}
