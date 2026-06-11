package skillupdate

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

type PermissionChecker interface {
	Check(ctx context.Context, input contracts.PermissionCheckInput) (contracts.PermissionDecision, error)
}

type Store interface {
	Save(ctx context.Context, request contracts.SkillUpdateRequest) error
	Get(ctx context.Context, requestID contracts.SkillUpdateRequestID) (contracts.SkillUpdateRequest, bool, error)
}

type Service struct {
	mu          sync.RWMutex
	requests    map[contracts.SkillUpdateRequestID]contracts.SkillUpdateRequest
	store       Store
	permissions PermissionChecker
	audit       audit.Logger
	trace       trace.Recorder
	now         func() time.Time
}

type ProposeInput struct {
	TenantID      contracts.TenantID
	GroupID       contracts.GroupID
	AgentID       contracts.AgentID
	RequestedBy   string
	Roles         []string
	Objective     string
	TargetSkillID string
	ProposedPatch map[string]any
	TraceID       contracts.TraceID
	TaskID        contracts.TaskID
	RunID         contracts.AgentRunID
}

func NewService(permissions PermissionChecker, auditLogger audit.Logger, traceRecorder trace.Recorder) *Service {
	return NewServiceWithStore(nil, permissions, auditLogger, traceRecorder)
}

func NewServiceWithStore(store Store, permissions PermissionChecker, auditLogger audit.Logger, traceRecorder trace.Recorder) *Service {
	return &Service{
		requests:    map[contracts.SkillUpdateRequestID]contracts.SkillUpdateRequest{},
		store:       store,
		permissions: permissions,
		audit:       auditLogger,
		trace:       traceRecorder,
		now:         func() time.Time { return time.Now().UTC() },
	}
}

func (s *Service) Propose(ctx context.Context, input ProposeInput) (contracts.SkillUpdateRequest, contracts.PermissionDecision, error) {
	if input.TenantID == "" || input.GroupID == "" || input.AgentID == "" || input.RequestedBy == "" || input.Objective == "" {
		return contracts.SkillUpdateRequest{}, contracts.PermissionDecision{}, fmt.Errorf("tenant_id, group_id, agent_id, requested_by and objective are required")
	}
	decision := contracts.PermissionDecision{Decision: contracts.PermissionDecisionAllowed, Reason: "no permission checker configured", CheckedAt: s.now()}
	var err error
	if s.permissions != nil {
		decision, err = s.permissions.Check(ctx, contracts.PermissionCheckInput{
			TenantID:     input.TenantID,
			GroupID:      input.GroupID,
			ActorID:      input.RequestedBy,
			ActorType:    "member",
			Roles:        input.Roles,
			Action:       contracts.PermissionActionSkillProposeUpdate,
			ResourceType: "skill",
			ResourceID:   input.TargetSkillID,
			TraceID:      input.TraceID,
			TaskID:       input.TaskID,
			RunID:        input.RunID,
		})
		if err != nil {
			return contracts.SkillUpdateRequest{}, decision, err
		}
	}
	if decision.Decision == contracts.PermissionDecisionDenied {
		return contracts.SkillUpdateRequest{}, decision, contracts.NewRuntimeError(contracts.CodeToolPolicyDenied, decision.Reason, map[string]any{"reason_code": decision.ReasonCode})
	}
	status := contracts.SkillUpdateDraft
	if decision.Decision == contracts.PermissionDecisionApprovalRequired {
		status = contracts.SkillUpdateWaitingApproval
	}
	now := s.now()
	request := contracts.SkillUpdateRequest{
		RequestID:     contracts.SkillUpdateRequestID(idgen.New("skillreq")),
		TenantID:      input.TenantID,
		AgentID:       input.AgentID,
		GroupID:       input.GroupID,
		RequestedBy:   input.RequestedBy,
		Objective:     input.Objective,
		TargetSkillID: input.TargetSkillID,
		ProposedPatch: clonePatch(input.ProposedPatch),
		Status:        status,
		Reason:        decision.Reason,
		CreatedAt:     now,
		UpdatedAt:     now,
	}
	s.mu.Lock()
	s.requests[request.RequestID] = request
	s.mu.Unlock()
	if s.store != nil {
		if err := s.store.Save(ctx, request); err != nil {
			return contracts.SkillUpdateRequest{}, decision, err
		}
	}
	s.record(ctx, input, request, decision)
	return request, decision, nil
}

func (s *Service) Get(ctx context.Context, requestID contracts.SkillUpdateRequestID) (contracts.SkillUpdateRequest, bool, error) {
	if s.store != nil {
		request, ok, err := s.store.Get(ctx, requestID)
		if err != nil || ok {
			return request, ok, err
		}
	}
	s.mu.RLock()
	defer s.mu.RUnlock()
	request, ok := s.requests[requestID]
	request.ProposedPatch = clonePatch(request.ProposedPatch)
	return request, ok, nil
}

func (s *Service) record(ctx context.Context, input ProposeInput, request contracts.SkillUpdateRequest, decision contracts.PermissionDecision) {
	if s.trace != nil && input.TraceID != "" {
		_ = s.trace.Record(ctx, contracts.TraceEvent{
			TraceID:  input.TraceID,
			TenantID: input.TenantID,
			SpanID:   contracts.SpanID(idgen.New("span")),
			RunID:    input.RunID,
			TaskID:   input.TaskID,
			Type:     contracts.TraceSkillUpdateRequested,
			Payload: map[string]any{
				"request_id": request.RequestID,
				"agent_id":   request.AgentID,
				"group_id":   request.GroupID,
				"status":     request.Status,
				"decision":   decision.Decision,
			},
			CreatedAt: s.now(),
		})
	}
	if s.audit != nil {
		_ = s.audit.Log(ctx, contracts.AuditEvent{
			AuditID:      idgen.New("audit"),
			TenantID:     input.TenantID,
			ActorID:      input.RequestedBy,
			ActorType:    "member",
			Action:       contracts.AuditSkillUpdateRequested,
			ResourceType: "skill_update_request",
			ResourceID:   string(request.RequestID),
			Decision:     decision.Decision,
			Reason:       decision.Reason,
			TraceID:      input.TraceID,
			TaskID:       input.TaskID,
			RunID:        input.RunID,
			CreatedAt:    s.now(),
		})
	}
}

func clonePatch(patch map[string]any) map[string]any {
	if patch == nil {
		return nil
	}
	out := make(map[string]any, len(patch))
	for key, value := range patch {
		out[key] = value
	}
	return out
}
