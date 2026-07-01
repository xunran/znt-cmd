package array

import (
	"context"
	"sync"
	"time"

	"znt/internal/contracts"
	storagerepo "znt/internal/storage/repository"
	"znt/pkg/idgen"
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

type DeliveryOutboxStore interface {
	EnqueueDelivery(ctx context.Context, item contracts.ExternalDeliveryOutboxItem) (contracts.ExternalDeliveryOutboxItem, error)
	MarkDeliveryAttempt(ctx context.Context, outboxID string, status string, lastError string, nextAttemptAt time.Time, updatedAt time.Time) error
	GetDelivery(ctx context.Context, tenantID contracts.TenantID, outboxID string) (contracts.ExternalDeliveryOutboxItem, bool, error)
	ListDeliveriesDue(ctx context.Context, opts contracts.ExternalDeliveryReplayOptions, now time.Time) ([]contracts.ExternalDeliveryOutboxItem, error)
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
	item, _ := b.enqueueDelivery(ctx, req.Ref, "", "message", map[string]any{"message": req.Message}, req.IdempotencyKey)
	if b.adapter != nil {
		if err := b.adapter.SendMessage(ctx, req); err != nil {
			_ = b.markDelivery(ctx, item.OutboxID, "failed", err.Error())
			_ = b.MarkWritebackFailed(ctx, req.Ref.Provider, req.Ref.ExternalTaskID, err.Error())
			return err
		}
		_ = b.markDelivery(ctx, item.OutboxID, "delivered", "")
		_ = b.MarkWritebackActive(ctx, req.Ref.Provider, req.Ref.ExternalTaskID)
		return nil
	}
	b.mu.Lock()
	defer b.mu.Unlock()
	b.Messages = append(b.Messages, req)
	_ = b.markDelivery(ctx, item.OutboxID, "delivered", "")
	return nil
}

func (b *Bridge) AttachArtifact(ctx context.Context, req contracts.AttachArtifactRequest) error {
	item, _ := b.enqueueDelivery(ctx, req.Ref, "", "artifact", map[string]any{"artifact_ref": req.ArtifactRef}, req.IdempotencyKey)
	if b.adapter != nil {
		if err := b.adapter.AttachArtifact(ctx, req); err != nil {
			_ = b.markDelivery(ctx, item.OutboxID, "failed", err.Error())
			_ = b.MarkWritebackFailed(ctx, req.Ref.Provider, req.Ref.ExternalTaskID, err.Error())
			return err
		}
		_ = b.markDelivery(ctx, item.OutboxID, "delivered", "")
		_ = b.MarkWritebackActive(ctx, req.Ref.Provider, req.Ref.ExternalTaskID)
		return nil
	}
	b.mu.Lock()
	defer b.mu.Unlock()
	b.Artifacts = append(b.Artifacts, req)
	_ = b.markDelivery(ctx, item.OutboxID, "delivered", "")
	return nil
}

func (b *Bridge) ReplayDelivery(ctx context.Context, tenantID contracts.TenantID, outboxID string) (contracts.ExternalDeliveryOutboxItem, error) {
	store, ok := b.store.(DeliveryOutboxStore)
	if !ok || store == nil {
		return contracts.ExternalDeliveryOutboxItem{}, storagerepo.ErrNotFound
	}
	item, ok, err := store.GetDelivery(ctx, tenantID, outboxID)
	if err != nil {
		return contracts.ExternalDeliveryOutboxItem{}, err
	}
	if !ok {
		return contracts.ExternalDeliveryOutboxItem{}, storagerepo.ErrNotFound
	}
	return b.replayDeliveryItem(ctx, item)
}

func (b *Bridge) ReplayDueDeliveries(ctx context.Context, tenantID contracts.TenantID, statuses []string, limit int) ([]contracts.ExternalDeliveryOutboxItem, error) {
	return b.ReplayDueDeliveriesWithOptions(ctx, contracts.ExternalDeliveryReplayOptions{
		TenantID: tenantID,
		Statuses: statuses,
		Limit:    limit,
	})
}

func (b *Bridge) ReplayDueDeliveriesWithOptions(ctx context.Context, opts contracts.ExternalDeliveryReplayOptions) ([]contracts.ExternalDeliveryOutboxItem, error) {
	store, ok := b.store.(DeliveryOutboxStore)
	if !ok || store == nil {
		return nil, storagerepo.ErrNotFound
	}
	if opts.Limit <= 0 || opts.Limit > 100 {
		opts.Limit = 50
	}
	if len(opts.Statuses) == 0 {
		opts.Statuses = []string{"failed", "pending"}
	}
	items, err := store.ListDeliveriesDue(ctx, opts, b.now())
	if err != nil {
		return nil, err
	}
	out := make([]contracts.ExternalDeliveryOutboxItem, 0, len(items))
	for _, item := range items {
		if opts.MaxAttempts > 0 && item.AttemptCount >= opts.MaxAttempts {
			lastError := item.LastError
			if lastError == "" {
				lastError = "max delivery attempts reached"
			}
			_ = b.markDelivery(ctx, item.OutboxID, "dead_letter", lastError)
			item.Status = "dead_letter"
			item.LastError = lastError
			item.NextAttemptAt = time.Time{}
			item.UpdatedAt = b.now()
			out = append(out, item)
			continue
		}
		replayed, err := b.replayDeliveryItem(ctx, item)
		if err != nil {
			replayed = item
			replayed.Status = "failed"
			replayed.LastError = err.Error()
		}
		out = append(out, replayed)
	}
	return out, nil
}

func (b *Bridge) replayDeliveryItem(ctx context.Context, item contracts.ExternalDeliveryOutboxItem) (contracts.ExternalDeliveryOutboxItem, error) {
	if b.adapter == nil {
		if item.Channel == "message" {
			b.mu.Lock()
			b.Messages = append(b.Messages, contracts.SendExternalMessageRequest{
				Ref:            contracts.ExternalTaskRef{Provider: item.Provider, ExternalTaskID: item.ExternalTaskID},
				Message:        stringFromPayload(item.Payload, "message"),
				IdempotencyKey: item.IdempotencyKey,
			})
			b.mu.Unlock()
		}
		_ = b.markDelivery(ctx, item.OutboxID, "delivered", "")
		item.Status = "delivered"
		item.AttemptCount++
		item.LastError = ""
		item.NextAttemptAt = time.Time{}
		item.UpdatedAt = b.now()
		return item, nil
	}
	ref := contracts.ExternalTaskRef{Provider: item.Provider, ExternalTaskID: item.ExternalTaskID}
	var err error
	switch item.Channel {
	case "message":
		err = b.adapter.SendMessage(ctx, contracts.SendExternalMessageRequest{
			Ref:            ref,
			Message:        stringFromPayload(item.Payload, "message"),
			IdempotencyKey: item.IdempotencyKey,
		})
	case "artifact":
		refValue, _ := item.Payload["artifact_ref"].(contracts.ArtifactRef)
		if refValue.ArtifactID == "" {
			if raw, ok := item.Payload["artifact_ref"].(map[string]any); ok {
				refValue = contracts.ArtifactRef{
					ArtifactID: contracts.ArtifactID(stringFromPayload(raw, "artifact_id")),
					Type:       stringFromPayload(raw, "type"),
					URI:        stringFromPayload(raw, "uri"),
					Summary:    stringFromPayload(raw, "summary"),
				}
			}
		}
		err = b.adapter.AttachArtifact(ctx, contracts.AttachArtifactRequest{Ref: ref, ArtifactRef: refValue, IdempotencyKey: item.IdempotencyKey})
	default:
		err = contracts.NewRuntimeError(contracts.CodeExternalBridgeError, "unsupported external delivery channel", map[string]any{"channel": item.Channel})
	}
	if err != nil {
		_ = b.markDelivery(ctx, item.OutboxID, "failed", err.Error())
		_ = b.MarkWritebackFailed(ctx, item.Provider, item.ExternalTaskID, err.Error())
		item.Status = "failed"
		item.AttemptCount++
		item.LastError = err.Error()
		item.NextAttemptAt = b.now().Add(time.Minute)
		item.UpdatedAt = b.now()
		return item, err
	}
	_ = b.markDelivery(ctx, item.OutboxID, "delivered", "")
	_ = b.MarkWritebackActive(ctx, item.Provider, item.ExternalTaskID)
	item.Status = "delivered"
	item.AttemptCount++
	item.LastError = ""
	item.NextAttemptAt = time.Time{}
	item.UpdatedAt = b.now()
	return item, nil
}

func stringFromPayload(payload map[string]any, key string) string {
	value, _ := payload[key].(string)
	return value
}

func (b *Bridge) enqueueDelivery(ctx context.Context, ref contracts.ExternalTaskRef, coreTaskID contracts.TaskID, channel string, payload map[string]any, idempotencyKey string) (contracts.ExternalDeliveryOutboxItem, error) {
	store, ok := b.store.(DeliveryOutboxStore)
	if !ok || store == nil {
		return contracts.ExternalDeliveryOutboxItem{}, nil
	}
	binding, _, _ := b.GetBinding(ctx, ref.Provider, ref.ExternalTaskID)
	now := b.now()
	if coreTaskID == "" {
		coreTaskID = binding.CoreTaskID
	}
	if idempotencyKey == "" {
		idempotencyKey = ref.Provider + ":" + string(ref.ExternalTaskID) + ":" + channel + ":" + idgen.New("delivery")
	}
	item := contracts.ExternalDeliveryOutboxItem{
		OutboxID:       idgen.New("outbox"),
		TenantID:       binding.TenantID,
		Provider:       ref.Provider,
		ExternalTaskID: ref.ExternalTaskID,
		CoreTaskID:     coreTaskID,
		EventType:      "external_delivery",
		Channel:        channel,
		Payload:        payload,
		IdempotencyKey: idempotencyKey,
		Status:         "pending",
		CreatedAt:      now,
		UpdatedAt:      now,
	}
	return store.EnqueueDelivery(ctx, item)
}

func (b *Bridge) markDelivery(ctx context.Context, outboxID string, status string, lastError string) error {
	if outboxID == "" {
		return nil
	}
	store, ok := b.store.(DeliveryOutboxStore)
	if !ok || store == nil {
		return nil
	}
	now := b.now()
	next := time.Time{}
	if status == "failed" {
		next = now.Add(time.Minute)
	}
	return store.MarkDeliveryAttempt(ctx, outboxID, status, lastError, next, now)
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
