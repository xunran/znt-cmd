package array

import (
	"context"
	"sync"
	"time"

	"znt/internal/contracts"
	storagerepo "znt/internal/storage/repository"
)

type Bridge struct {
	mu        sync.Mutex
	Messages  []contracts.SendExternalMessageRequest
	Artifacts []contracts.AttachArtifactRequest
	bindings  map[string]contracts.ExternalTaskBinding
	store     BindingStore
	adapter   contracts.CollaborationProvider
	now       func() time.Time
}

type BindingStore interface {
	SaveBinding(ctx context.Context, binding contracts.ExternalTaskBinding) (contracts.ExternalTaskBinding, error)
	GetBinding(ctx context.Context, provider string, externalTaskID contracts.ExternalTaskID) (contracts.ExternalTaskBinding, bool, error)
	GetBindingByCoreTask(ctx context.Context, tenantID contracts.TenantID, coreTaskID contracts.TaskID) (contracts.ExternalTaskBinding, bool, error)
	UpdateBindingStatus(ctx context.Context, provider string, externalTaskID contracts.ExternalTaskID, status string, lastError string, updatedAt time.Time) error
}

func NewBridge() *Bridge {
	return NewBridgeWithStore(nil)
}

func NewBridgeWithStore(store BindingStore) *Bridge {
	return NewBridgeWithStoreAndAdapter(store, nil)
}

func NewBridgeWithAdapter(adapter contracts.CollaborationProvider) *Bridge {
	return NewBridgeWithStoreAndAdapter(nil, adapter)
}

func NewBridgeWithStoreAndAdapter(store BindingStore, adapter contracts.CollaborationProvider) *Bridge {
	return &Bridge{bindings: map[string]contracts.ExternalTaskBinding{}, store: store, adapter: adapter, now: func() time.Time { return time.Now().UTC() }}
}

func (b *Bridge) GetTask(ctx context.Context, ref contracts.ExternalTaskRef) (*contracts.ExternalTaskSummary, error) {
	if b.adapter != nil {
		return b.adapter.GetTask(ctx, ref)
	}
	return &contracts.ExternalTaskSummary{Ref: ref, Status: "open"}, nil
}

func (b *Bridge) GetParticipants(ctx context.Context, ref contracts.ExternalTaskRef) ([]contracts.ParticipantSummary, error) {
	if b.adapter != nil {
		return b.adapter.GetParticipants(ctx, ref)
	}
	return []contracts.ParticipantSummary{}, nil
}

func (b *Bridge) SendMessage(ctx context.Context, req contracts.SendExternalMessageRequest) error {
	if b.adapter != nil {
		if err := b.adapter.SendMessage(ctx, req); err != nil {
			_ = b.MarkWritebackFailed(ctx, req.Ref.Provider, req.Ref.ExternalTaskID, err.Error())
			return err
		}
		_ = b.MarkWritebackActive(ctx, req.Ref.Provider, req.Ref.ExternalTaskID)
		return nil
	}
	b.mu.Lock()
	defer b.mu.Unlock()
	b.Messages = append(b.Messages, req)
	return nil
}

func (b *Bridge) AttachArtifact(ctx context.Context, req contracts.AttachArtifactRequest) error {
	if b.adapter != nil {
		if err := b.adapter.AttachArtifact(ctx, req); err != nil {
			_ = b.MarkWritebackFailed(ctx, req.Ref.Provider, req.Ref.ExternalTaskID, err.Error())
			return err
		}
		_ = b.MarkWritebackActive(ctx, req.Ref.Provider, req.Ref.ExternalTaskID)
		return nil
	}
	b.mu.Lock()
	defer b.mu.Unlock()
	b.Artifacts = append(b.Artifacts, req)
	return nil
}

func (b *Bridge) CheckAccess(ctx context.Context, req contracts.CollaborationAccessRequest) (*contracts.AccessDecision, error) {
	binding, ok, err := b.GetBinding(ctx, req.Ref.Provider, req.Ref.ExternalTaskID)
	if err != nil {
		return nil, err
	}
	if ok {
		if req.TenantID != "" && binding.TenantID != req.TenantID {
			return &contracts.AccessDecision{Allowed: false, Reason: "external task binding belongs to another tenant"}, nil
		}
		if binding.Status != "active" {
			return &contracts.AccessDecision{Allowed: false, Reason: "external task binding is not active"}, nil
		}
	}
	if b.adapter != nil {
		decision, err := b.adapter.CheckAccess(ctx, req)
		if err != nil {
			return nil, err
		}
		if decision == nil {
			return &contracts.AccessDecision{Allowed: false, Reason: "external bridge returned no access decision"}, nil
		}
		return decision, nil
	}
	return &contracts.AccessDecision{Allowed: true, Reason: "array bridge stub allows access"}, nil
}

func (b *Bridge) BindTask(ctx context.Context, binding contracts.ExternalTaskBinding) (contracts.ExternalTaskBinding, error) {
	now := b.now()
	if binding.SyncMode == "" {
		binding.SyncMode = "two_way"
	}
	if binding.Status == "" {
		binding.Status = "active"
	}
	if binding.CreatedAt.IsZero() {
		binding.CreatedAt = now
	}
	binding.UpdatedAt = now
	if b.store != nil {
		return b.store.SaveBinding(ctx, binding)
	}
	b.mu.Lock()
	defer b.mu.Unlock()
	b.bindings[bindingKey(binding.Provider, binding.ExternalTaskID)] = binding
	return binding, nil
}

func (b *Bridge) GetBinding(ctx context.Context, provider string, externalTaskID contracts.ExternalTaskID) (contracts.ExternalTaskBinding, bool, error) {
	if b.store != nil {
		return b.store.GetBinding(ctx, provider, externalTaskID)
	}
	b.mu.Lock()
	defer b.mu.Unlock()
	binding, ok := b.bindings[bindingKey(provider, externalTaskID)]
	return binding, ok, nil
}

func (b *Bridge) GetBindingByCoreTask(ctx context.Context, tenantID contracts.TenantID, coreTaskID contracts.TaskID) (contracts.ExternalTaskBinding, bool, error) {
	if b.store != nil {
		return b.store.GetBindingByCoreTask(ctx, tenantID, coreTaskID)
	}
	b.mu.Lock()
	defer b.mu.Unlock()
	for _, binding := range b.bindings {
		if binding.TenantID == tenantID && binding.CoreTaskID == coreTaskID {
			return binding, true, nil
		}
	}
	return contracts.ExternalTaskBinding{}, false, nil
}

func (b *Bridge) MarkWritebackFailed(ctx context.Context, provider string, externalTaskID contracts.ExternalTaskID, message string) error {
	now := b.now()
	if b.store != nil {
		return b.store.UpdateBindingStatus(ctx, provider, externalTaskID, "writeback_failed", message, now)
	}
	b.mu.Lock()
	defer b.mu.Unlock()
	key := bindingKey(provider, externalTaskID)
	binding, ok := b.bindings[key]
	if !ok {
		return storagerepo.ErrNotFound
	}
	binding.Status = "writeback_failed"
	binding.LastError = message
	binding.UpdatedAt = now
	b.bindings[key] = binding
	return nil
}

func (b *Bridge) MarkWritebackActive(ctx context.Context, provider string, externalTaskID contracts.ExternalTaskID) error {
	now := b.now()
	if b.store != nil {
		return b.store.UpdateBindingStatus(ctx, provider, externalTaskID, "active", "", now)
	}
	b.mu.Lock()
	defer b.mu.Unlock()
	key := bindingKey(provider, externalTaskID)
	binding, ok := b.bindings[key]
	if !ok {
		return storagerepo.ErrNotFound
	}
	binding.Status = "active"
	binding.LastError = ""
	binding.UpdatedAt = now
	b.bindings[key] = binding
	return nil
}

func bindingKey(provider string, externalTaskID contracts.ExternalTaskID) string {
	return provider + "\x00" + string(externalTaskID)
}
