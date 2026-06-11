package approval

import (
	"context"
	"fmt"
	"sync"
	"time"

	"znt/internal/contracts"
	"znt/internal/governance/audit"
	"znt/internal/governance/trace"
	"znt/pkg/idgen"
)

type RequestInput struct {
	TenantID     contracts.TenantID
	ResourceType string
	ResourceID   string
	Action       string
	RiskLevel    contracts.RiskLevel
	Reason       string
	RequestedBy  string
	TraceID      contracts.TraceID
	ExpiresAt    *time.Time
}

type ListFilter struct {
	TenantID     contracts.TenantID
	ResourceType string
	ResourceID   string
	Action       string
	Status       contracts.ApprovalStatus
	TraceID      contracts.TraceID
}

type Service struct {
	mu    sync.RWMutex
	items map[contracts.ApprovalID]contracts.ApprovalRequest
	Audit audit.Logger
	Trace trace.Recorder
	Now   func() time.Time
}

func NewService(auditLogger audit.Logger, traceRecorder trace.Recorder) *Service {
	return &Service{
		items: map[contracts.ApprovalID]contracts.ApprovalRequest{},
		Audit: auditLogger,
		Trace: traceRecorder,
		Now:   func() time.Time { return time.Now().UTC() },
	}
}

func (s *Service) Request(ctx context.Context, input RequestInput) (contracts.ApprovalRequest, error) {
	if input.TenantID == "" || input.ResourceType == "" || input.ResourceID == "" || input.Action == "" {
		return contracts.ApprovalRequest{}, fmt.Errorf("approval request requires tenant, resource, and action")
	}
	now := s.now()
	req := contracts.ApprovalRequest{
		ApprovalID:   contracts.ApprovalID(idgen.New("approval")),
		TenantID:     input.TenantID,
		ResourceType: input.ResourceType,
		ResourceID:   input.ResourceID,
		Action:       input.Action,
		RiskLevel:    input.RiskLevel,
		Reason:       input.Reason,
		Status:       contracts.ApprovalPending,
		RequestedBy:  input.RequestedBy,
		CreatedAt:    now,
		ExpiresAt:    input.ExpiresAt,
		TraceID:      input.TraceID,
	}
	s.mu.Lock()
	s.items[req.ApprovalID] = req
	s.mu.Unlock()
	s.audit(ctx, req, contracts.AuditApprovalRequested, req.RequestedBy, "pending")
	s.trace(ctx, req, contracts.TraceApprovalRequested)
	return req, nil
}

func (s *Service) Approve(ctx context.Context, tenantID contracts.TenantID, approvalID contracts.ApprovalID, actorID string) (contracts.ApprovalRequest, error) {
	return s.resolve(ctx, tenantID, approvalID, actorID, contracts.ApprovalApproved)
}

func (s *Service) Reject(ctx context.Context, tenantID contracts.TenantID, approvalID contracts.ApprovalID, actorID string) (contracts.ApprovalRequest, error) {
	return s.resolve(ctx, tenantID, approvalID, actorID, contracts.ApprovalRejected)
}

func (s *Service) Consume(ctx context.Context, tenantID contracts.TenantID, approvalID contracts.ApprovalID, resourceType string, resourceID string, action string, actorID string) (contracts.ApprovalRequest, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	req, ok := s.items[approvalID]
	if !ok {
		return contracts.ApprovalRequest{}, fmt.Errorf("approval %s not found", approvalID)
	}
	if err := s.validate(req, tenantID, resourceType, resourceID, action); err != nil {
		return contracts.ApprovalRequest{}, err
	}
	now := s.now()
	req.Status = contracts.ApprovalUsed
	req.ResolvedBy = actorID
	req.ResolvedAt = &now
	s.items[approvalID] = req
	s.audit(ctx, req, contracts.AuditApprovalResolved, actorID, string(req.Status))
	s.trace(ctx, req, contracts.TraceApprovalResolved)
	return req, nil
}

func (s *Service) Validate(tenantID contracts.TenantID, approvalID contracts.ApprovalID, resourceType string, resourceID string, action string) (contracts.ApprovalRequest, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	req, ok := s.items[approvalID]
	if !ok {
		return contracts.ApprovalRequest{}, fmt.Errorf("approval %s not found", approvalID)
	}
	if err := s.validate(req, tenantID, resourceType, resourceID, action); err != nil {
		return contracts.ApprovalRequest{}, err
	}
	return req, nil
}

func (s *Service) Get(approvalID contracts.ApprovalID) (contracts.ApprovalRequest, bool) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	req, ok := s.items[approvalID]
	return req, ok
}

func (s *Service) List(filter ListFilter) []contracts.ApprovalRequest {
	s.mu.RLock()
	defer s.mu.RUnlock()
	out := make([]contracts.ApprovalRequest, 0, len(s.items))
	for _, req := range s.items {
		if filter.TenantID != "" && req.TenantID != filter.TenantID {
			continue
		}
		if filter.ResourceType != "" && req.ResourceType != filter.ResourceType {
			continue
		}
		if filter.ResourceID != "" && req.ResourceID != filter.ResourceID {
			continue
		}
		if filter.Action != "" && req.Action != filter.Action {
			continue
		}
		if filter.Status != "" && req.Status != filter.Status {
			continue
		}
		if filter.TraceID != "" && req.TraceID != filter.TraceID {
			continue
		}
		out = append(out, req)
	}
	return out
}

func (s *Service) resolve(ctx context.Context, tenantID contracts.TenantID, approvalID contracts.ApprovalID, actorID string, status contracts.ApprovalStatus) (contracts.ApprovalRequest, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	req, ok := s.items[approvalID]
	if !ok {
		return contracts.ApprovalRequest{}, fmt.Errorf("approval %s not found", approvalID)
	}
	if req.TenantID != tenantID {
		return contracts.ApprovalRequest{}, fmt.Errorf("approval tenant does not match caller tenant")
	}
	if req.Status != contracts.ApprovalPending {
		return contracts.ApprovalRequest{}, fmt.Errorf("approval %s is not pending", approvalID)
	}
	now := s.now()
	req.Status = status
	req.ResolvedBy = actorID
	req.ResolvedAt = &now
	s.items[approvalID] = req
	s.audit(ctx, req, contracts.AuditApprovalResolved, actorID, string(status))
	s.trace(ctx, req, contracts.TraceApprovalResolved)
	return req, nil
}

func (s *Service) validate(req contracts.ApprovalRequest, tenantID contracts.TenantID, resourceType string, resourceID string, action string) error {
	if req.TenantID != tenantID {
		return fmt.Errorf("approval tenant does not match caller tenant")
	}
	if req.ResourceType != resourceType || req.ResourceID != resourceID || req.Action != action {
		return fmt.Errorf("approval does not match requested resource/action")
	}
	if req.Status != contracts.ApprovalApproved {
		return fmt.Errorf("approval %s is not approved", req.ApprovalID)
	}
	if req.ExpiresAt != nil && s.now().After(*req.ExpiresAt) {
		return fmt.Errorf("approval %s is expired", req.ApprovalID)
	}
	return nil
}

func (s *Service) audit(ctx context.Context, req contracts.ApprovalRequest, action string, actorID string, decision string) {
	if s.Audit == nil {
		return
	}
	_ = s.Audit.Log(ctx, contracts.AuditEvent{
		AuditID:      idgen.New("audit"),
		TenantID:     req.TenantID,
		ActorID:      actorID,
		ActorType:    "optimizer",
		Action:       action,
		ResourceType: req.ResourceType,
		ResourceID:   req.ResourceID,
		Decision:     decision,
		Reason:       req.Reason,
		TraceID:      req.TraceID,
		CreatedAt:    s.now(),
	})
}

func (s *Service) trace(ctx context.Context, req contracts.ApprovalRequest, eventType string) {
	if s.Trace == nil || req.TraceID == "" {
		return
	}
	_ = s.Trace.Record(ctx, contracts.TraceEvent{
		TraceID:  req.TraceID,
		TenantID: req.TenantID,
		SpanID:   contracts.SpanID(idgen.New("span")),
		Type:     eventType,
		Payload: map[string]any{
			"approval_id":   req.ApprovalID,
			"resource_type": req.ResourceType,
			"resource_id":   req.ResourceID,
			"action":        req.Action,
			"status":        req.Status,
			"reason":        req.Reason,
		},
		CreatedAt: s.now(),
	})
}

func (s *Service) now() time.Time {
	if s.Now != nil {
		return s.Now().UTC()
	}
	return time.Now().UTC()
}
