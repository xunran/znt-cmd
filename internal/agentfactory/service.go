package agentfactory

import (
	"context"
	"fmt"
	"strings"
	"sync"
	"time"

	agentpackage "znt/internal/agentdef/package"
	"znt/internal/contracts"
	"znt/internal/governance/audit"
	"znt/internal/governance/trace"
	"znt/pkg/idgen"
)

type PermissionChecker interface {
	Check(ctx context.Context, input contracts.PermissionCheckInput) (contracts.PermissionDecision, error)
}

type Store interface {
	Save(ctx context.Context, request contracts.AgentDraftRequest) error
	Get(ctx context.Context, requestID contracts.AgentDraftRequestID) (contracts.AgentDraftRequest, bool, error)
}

type Service struct {
	mu          sync.RWMutex
	requests    map[contracts.AgentDraftRequestID]contracts.AgentDraftRequest
	Store       Store
	Packages    *agentpackage.Service
	Permissions PermissionChecker
	Audit       audit.Logger
	Trace       trace.Recorder
	Now         func() time.Time
}

type CreateDraftInput struct {
	TenantID    contracts.TenantID
	GroupID     contracts.GroupID
	RequestedBy string
	Roles       []string
	AgentID     contracts.AgentID
	Name        string
	Objective   string
	TraceID     contracts.TraceID
	TaskID      contracts.TaskID
	RunID       contracts.AgentRunID
}

type CreateDraftResult struct {
	Request contracts.AgentDraftRequest `json:"request"`
	Draft   *agentpackage.Draft         `json:"draft,omitempty"`
}

func NewService(packages *agentpackage.Service, permissions PermissionChecker, auditLogger audit.Logger, traceRecorder trace.Recorder) *Service {
	return NewServiceWithStore(nil, packages, permissions, auditLogger, traceRecorder)
}

func NewServiceWithStore(store Store, packages *agentpackage.Service, permissions PermissionChecker, auditLogger audit.Logger, traceRecorder trace.Recorder) *Service {
	return &Service{
		requests:    map[contracts.AgentDraftRequestID]contracts.AgentDraftRequest{},
		Store:       store,
		Packages:    packages,
		Permissions: permissions,
		Audit:       auditLogger,
		Trace:       traceRecorder,
		Now:         func() time.Time { return time.Now().UTC() },
	}
}

func (s *Service) CreateDraft(ctx context.Context, input CreateDraftInput) (CreateDraftResult, error) {
	if input.TenantID == "" || input.GroupID == "" || input.RequestedBy == "" || input.Objective == "" {
		return CreateDraftResult{}, fmt.Errorf("tenant_id, group_id, requested_by and objective are required")
	}
	decision := contracts.PermissionDecision{Decision: contracts.PermissionDecisionAllowed}
	if s.Permissions != nil {
		var err error
		decision, err = s.Permissions.Check(ctx, contracts.PermissionCheckInput{
			TenantID:     input.TenantID,
			GroupID:      input.GroupID,
			ActorID:      input.RequestedBy,
			ActorType:    "member",
			Roles:        input.Roles,
			Action:       contracts.PermissionActionAgentPackageCreate,
			ResourceType: "agent_package",
			ResourceID:   string(input.AgentID),
			TraceID:      input.TraceID,
			TaskID:       input.TaskID,
			RunID:        input.RunID,
		})
		if err != nil {
			return CreateDraftResult{}, err
		}
		if decision.Decision != contracts.PermissionDecisionAllowed {
			return CreateDraftResult{}, contracts.NewRuntimeError(contracts.CodeToolPolicyDenied, decision.Reason, map[string]any{"reason_code": decision.ReasonCode})
		}
	}
	agentID := input.AgentID
	if agentID == "" {
		agentID = contracts.AgentID(slug(input.Name))
	}
	if agentID == "" {
		agentID = contracts.AgentID(idgen.New("agent"))
	}
	name := input.Name
	if name == "" {
		name = string(agentID)
	}
	now := s.Now()
	request := contracts.AgentDraftRequest{
		RequestID:   contracts.AgentDraftRequestID(idgen.New("agentdraftreq")),
		TenantID:    input.TenantID,
		GroupID:     input.GroupID,
		RequestedBy: input.RequestedBy,
		AgentID:     agentID,
		Name:        name,
		Objective:   input.Objective,
		Status:      "draft_created",
		CreatedAt:   now,
		UpdatedAt:   now,
	}
	var draft *agentpackage.Draft
	if s.Packages != nil {
		source := agentpackage.AgentPackageSource{
			AgentsMD: fmt.Sprintf("# %s\n\n%s\n", name, input.Objective),
			Prompt:   fmt.Sprintf("你是%s。只处理与以下目标直接相关的任务：%s。输出必须清晰、可验证。", name, input.Objective),
			ToolBindings: contracts.AgentToolsConfig{
				AllowedToolIDs: []string{},
				ExposedToolIDs: []string{},
				DeniedToolIDs:  []string{},
			},
			Metadata: map[string]any{
				"system_prompt":    "你是一个 CleanCore 专业智能体草稿，必须遵守上游协调智能体的任务边界。",
				"developer_prompt": "只完成被委派的专业任务；缺信息时说明缺口，不编造。",
			},
		}
		created, err := s.Packages.CreateDraft(ctx, input.TenantID, agentID, "v1", source, input.RequestedBy)
		if err != nil {
			return CreateDraftResult{}, err
		}
		request.DraftID = created.DraftID
		draft = &created
	}
	s.mu.Lock()
	s.requests[request.RequestID] = request
	s.mu.Unlock()
	if s.Store != nil {
		if err := s.Store.Save(ctx, request); err != nil {
			return CreateDraftResult{}, err
		}
	}
	s.record(ctx, input, request, decision)
	return CreateDraftResult{Request: request, Draft: draft}, nil
}

func (s *Service) Get(ctx context.Context, requestID contracts.AgentDraftRequestID) (contracts.AgentDraftRequest, bool, error) {
	if s.Store != nil {
		request, ok, err := s.Store.Get(ctx, requestID)
		if err != nil || ok {
			return request, ok, err
		}
	}
	s.mu.RLock()
	defer s.mu.RUnlock()
	request, ok := s.requests[requestID]
	return request, ok, nil
}

func (s *Service) record(ctx context.Context, input CreateDraftInput, request contracts.AgentDraftRequest, decision contracts.PermissionDecision) {
	if s.Trace != nil && input.TraceID != "" {
		_ = s.Trace.Record(ctx, contracts.TraceEvent{
			TraceID:  input.TraceID,
			TenantID: input.TenantID,
			SpanID:   contracts.SpanID(idgen.New("span")),
			RunID:    input.RunID,
			TaskID:   input.TaskID,
			Type:     contracts.TraceAgentFactoryDraftCreated,
			Payload: map[string]any{
				"request_id": request.RequestID,
				"draft_id":   request.DraftID,
				"agent_id":   request.AgentID,
				"decision":   decision.Decision,
			},
			CreatedAt: s.Now(),
		})
	}
	if s.Audit != nil {
		_ = s.Audit.Log(ctx, contracts.AuditEvent{
			AuditID:      idgen.New("audit"),
			TenantID:     input.TenantID,
			ActorID:      input.RequestedBy,
			ActorType:    "member",
			Action:       contracts.AuditAgentDraftCreated,
			ResourceType: "agent_package",
			ResourceID:   string(request.AgentID),
			Decision:     decision.Decision,
			TraceID:      input.TraceID,
			TaskID:       input.TaskID,
			RunID:        input.RunID,
			CreatedAt:    s.Now(),
		})
	}
}

func slug(value string) string {
	value = strings.ToLower(strings.TrimSpace(value))
	replacer := strings.NewReplacer(" ", "-", "_", "-", "/", "-", "\\", "-", ".", "-")
	value = replacer.Replace(value)
	var b strings.Builder
	for _, r := range value {
		if (r >= 'a' && r <= 'z') || (r >= '0' && r <= '9') || r == '-' {
			b.WriteRune(r)
		}
	}
	return strings.Trim(b.String(), "-")
}
