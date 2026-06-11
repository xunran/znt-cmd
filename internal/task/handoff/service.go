package handoff

import (
	"context"
	"fmt"
	"time"

	"znt/internal/asset/artifact"
	"znt/internal/bridge/array"
	"znt/internal/context/handoffpkg"
	"znt/internal/contracts"
	"znt/internal/governance/audit"
	"znt/internal/governance/trace"
	handoffpolicy "znt/internal/policy/handoff"
	taskrepo "znt/internal/task/repository"
	taskruntime "znt/internal/task/runtime"
	"znt/pkg/idgen"
)

type Service struct {
	Repository Repository

	Tasks           *taskruntime.Service
	TaskRepo        taskrepo.TaskRepository
	Events          taskrepo.EventRepository
	Audit           audit.Logger
	Trace           trace.Recorder
	Policy          handoffpolicy.Evaluator
	Builder         handoffpkg.Builder
	ContextPackages artifact.ContextPackageStore
	ExternalSync    array.Syncer
	ExternalBinding func(context.Context, contracts.TenantID, contracts.TaskID) (*contracts.ExternalTaskBinding, bool)
	Now             func() time.Time
}

type Repository interface {
	Save(ctx context.Context, handoff contracts.AgentHandoff, pkg contracts.HandoffContextPackage) error
	Get(ctx context.Context, handoffID contracts.HandoffID) (contracts.AgentHandoff, bool, error)
	Update(ctx context.Context, handoff contracts.AgentHandoff) error
}

type AtomicRepository interface {
	CreateWithChild(ctx context.Context, handoff contracts.AgentHandoff, pkg contracts.HandoffContextPackage, childTask *contracts.Task, childEvent *contracts.TaskEvent, parentEvent contracts.TaskEvent) error
}

type CreateInput struct {
	TenantID          contracts.TenantID
	TraceID           contracts.TraceID
	ParentTaskID      contracts.TaskID
	SourceRunID       contracts.AgentRunID
	FromAgentID       contracts.AgentID
	ToAgentID         contracts.AgentID
	ToAgentVersion    contracts.AgentVersion
	ToPolicySetID     contracts.PolicySetID
	TargetTenantID    contracts.TenantID
	Objective         string
	Reason            string
	Mode              contracts.HandoffMode
	ArtifactRefs      []contracts.ArtifactRef
	MemoryRefs        []contracts.MemoryID
	ExpectedOutput    contracts.ExpectedOutput
	Policy            contracts.HandoffPolicy
	ToAgentExists     bool
	CapabilityMatched bool
	CapabilityChecked bool
	ActorID           string
}

type CreateResult struct {
	Handoff   contracts.AgentHandoff          `json:"handoff"`
	Package   contracts.HandoffContextPackage `json:"package"`
	Decision  contracts.PolicyDecision        `json:"decision"`
	ChildTask *contracts.Task                 `json:"child_task,omitempty"`
}

func NewService(tasks *taskruntime.Service, taskRepo taskrepo.TaskRepository, events taskrepo.EventRepository) *Service {
	return &Service{
		Repository: NewInMemoryRepository(),
		Tasks:      tasks,
		TaskRepo:   taskRepo,
		Events:     events,
		Policy:     handoffpolicy.New(),
		Builder:    handoffpkg.NewBuilder(),
		Now:        func() time.Time { return time.Now().UTC() },
	}
}

func (s *Service) Create(ctx context.Context, input CreateInput) (CreateResult, error) {
	if input.ParentTaskID == "" || input.ToAgentID == "" || input.Objective == "" {
		return CreateResult{}, contracts.NewRuntimeError(contracts.CodeDecisionSchemaError, "handoff requires parent_task_id, to_agent_id, and objective", nil)
	}
	if s.TaskRepo != nil {
		parent, err := s.TaskRepo.Get(ctx, input.ParentTaskID)
		if err != nil {
			return CreateResult{}, err
		}
		if parent.TenantID != input.TenantID {
			return CreateResult{}, contracts.NewRuntimeError(contracts.CodeToolPolicyDenied, "parent task tenant does not match handoff tenant", nil)
		}
	}
	mode := input.Mode
	if mode == "" {
		mode = input.Policy.DefaultMode
	}
	if mode == "" {
		mode = contracts.HandoffHybrid
	}
	capabilityMatched := true
	if input.CapabilityChecked {
		capabilityMatched = input.CapabilityMatched
	}
	decision, err := s.Policy.EvaluateRequest(ctx, handoffpolicy.EvaluateRequest{
		Policy:            input.Policy,
		Mode:              mode,
		TenantID:          input.TenantID,
		TargetTenantID:    input.TargetTenantID,
		FromAgentID:       input.FromAgentID,
		ToAgentID:         input.ToAgentID,
		ToAgentExists:     input.ToAgentExists || input.ToAgentID != "",
		CapabilityMatched: capabilityMatched,
		ArtifactRefs:      input.ArtifactRefs,
		MemoryRefs:        input.MemoryRefs,
	})
	s.auditEvent(ctx, input, contracts.AuditHandoffPolicyChecked, "", string(decision.Decision), decision.Reason)
	s.traceEvent(ctx, input, contracts.TraceHandoffPolicyChecked, "", map[string]any{
		"from_agent_id": input.FromAgentID,
		"to_agent_id":   input.ToAgentID,
		"mode":          mode,
		"decision":      decision.Decision,
		"reason":        decision.Reason,
	})
	if err != nil {
		return CreateResult{Decision: decision}, err
	}
	pkg, err := s.Builder.Build(ctx, handoffpkg.Input{
		TenantID:       input.TenantID,
		ParentTaskID:   input.ParentTaskID,
		SourceRunID:    input.SourceRunID,
		FromAgentID:    input.FromAgentID,
		ToAgentID:      input.ToAgentID,
		Objective:      input.Objective,
		Reason:         input.Reason,
		Summary:        input.Objective,
		ArtifactRefs:   input.ArtifactRefs,
		MemoryRefs:     input.MemoryRefs,
		ExpectedOutput: input.ExpectedOutput,
		Mode:           mode,
		Policy:         input.Policy,
	})
	if err != nil {
		return CreateResult{}, err
	}
	s.traceEvent(ctx, input, contracts.TraceHandoffPackaged, "", map[string]any{
		"package_id":     pkg.PackageID,
		"package_hash":   pkg.Hash,
		"allowed_scopes": pkg.AllowedContextScopes,
		"denied_scopes":  pkg.DeniedContextScopes,
	})
	status := contracts.HandoffAccepted
	if decision.Decision == contracts.PolicyDecisionApprovalRequired {
		status = contracts.HandoffCreated
	}
	h := contracts.AgentHandoff{
		HandoffID:         contracts.HandoffID(idgen.New("handoff")),
		TenantID:          input.TenantID,
		ParentTaskID:      input.ParentTaskID,
		FromAgentID:       input.FromAgentID,
		ToAgentID:         input.ToAgentID,
		Objective:         input.Objective,
		Reason:            input.Reason,
		ContextPackageRef: pkg.PackageID,
		ArtifactRefs:      input.ArtifactRefs,
		ExpectedOutput:    input.ExpectedOutput,
		Status:            status,
		CreatedAt:         s.Now(),
	}
	var childTask *contracts.Task
	if decision.Decision == contracts.PolicyDecisionAllowed {
		agentVersion := input.ToAgentVersion
		if agentVersion == "" {
			agentVersion = "v1"
		}
		policySetID := input.ToPolicySetID
		if policySetID == "" {
			policySetID = "policy_default"
		}
		task := taskrepo.NewTask(contracts.TaskID(idgen.New("task")), input.TenantID, input.ToAgentID, agentVersion, policySetID, "handoff", input.Objective, s.Now())
		task.ParentTaskID = &input.ParentTaskID
		task.SourceHandoffID = &h.HandoffID
		h.ChildTaskID = &task.TaskID
		h.Status = contracts.HandoffRunning
		childTask = &task
	}
	parentEvent := contracts.TaskEvent{
		EventID:   contracts.TaskEventID(idgen.New("taskevt")),
		TaskID:    input.ParentTaskID,
		TenantID:  input.TenantID,
		Type:      "handoff.created",
		ActorID:   input.ActorID,
		ActorType: "agent",
		Payload: map[string]any{
			"handoff_id":  h.HandoffID,
			"to_agent_id": h.ToAgentID,
			"decision":    decision.Decision,
		},
		RunID:     input.SourceRunID,
		CreatedAt: s.Now(),
	}
	if atomicRepo, ok := s.Repository.(AtomicRepository); ok {
		childEvent := taskCreatedEvent(input, childTask, s.Now())
		if err := atomicRepo.CreateWithChild(ctx, h, pkg, childTask, childEvent, parentEvent); err != nil {
			return CreateResult{}, err
		}
		s.auditEvent(ctx, input, contracts.AuditHandoffCreated, string(h.HandoffID), "allowed", "")
		s.traceEvent(ctx, input, contracts.TraceHandoffCreated, string(h.HandoffID), map[string]any{
			"handoff_id":     h.HandoffID,
			"context_ref":    h.ContextPackageRef,
			"child_task_id":  h.ChildTaskID,
			"handoff_status": h.Status,
		})
		s.syncHandoffCreated(ctx, input, h)
		return CreateResult{Handoff: h, Package: pkg, Decision: decision, ChildTask: childTask}, nil
	}
	if childTask != nil {
		created, err := s.Tasks.CreateTask(ctx, *childTask, input.ActorID, "agent")
		if err != nil {
			return CreateResult{}, err
		}
		childTask = &created
	}
	if err := s.Repository.Save(ctx, h, pkg); err != nil {
		s.cancelCreatedChild(ctx, input, childTask, "handoff save failed")
		return CreateResult{}, err
	}
	if s.ContextPackages != nil {
		if err := s.ContextPackages.SaveContextPackage(ctx, pkg); err != nil {
			s.failCreatedChild(ctx, input, h, childTask, "context package save failed")
			return CreateResult{}, err
		}
	}
	if err := s.Events.Append(ctx, parentEvent); err != nil {
		s.failCreatedChild(ctx, input, h, childTask, "handoff event append failed")
		return CreateResult{}, err
	}
	s.auditEvent(ctx, input, contracts.AuditHandoffCreated, string(h.HandoffID), "allowed", "")
	s.traceEvent(ctx, input, contracts.TraceHandoffCreated, string(h.HandoffID), map[string]any{
		"handoff_id":     h.HandoffID,
		"context_ref":    h.ContextPackageRef,
		"child_task_id":  h.ChildTaskID,
		"handoff_status": h.Status,
	})
	s.syncHandoffCreated(ctx, input, h)
	return CreateResult{Handoff: h, Package: pkg, Decision: decision, ChildTask: childTask}, nil
}

func taskCreatedEvent(input CreateInput, childTask *contracts.Task, now time.Time) *contracts.TaskEvent {
	if childTask == nil {
		return nil
	}
	return &contracts.TaskEvent{
		EventID:   contracts.TaskEventID(idgen.New("taskevt")),
		TaskID:    childTask.TaskID,
		TenantID:  childTask.TenantID,
		Type:      "task.created",
		ActorID:   input.ActorID,
		ActorType: "agent",
		Payload:   map[string]any{"status": childTask.Status},
		CreatedAt: now,
	}
}

func (s *Service) Get(handoffID contracts.HandoffID) (contracts.AgentHandoff, bool) {
	h, ok, err := s.Repository.Get(context.Background(), handoffID)
	if err != nil {
		return contracts.AgentHandoff{}, false
	}
	return h, ok
}

func (s *Service) failCreatedChild(ctx context.Context, input CreateInput, h contracts.AgentHandoff, childTask *contracts.Task, reason string) {
	s.cancelCreatedChild(ctx, input, childTask, reason)
	h.Status = contracts.HandoffFailed
	_ = s.Repository.Update(ctx, h)
}

func (s *Service) cancelCreatedChild(ctx context.Context, input CreateInput, childTask *contracts.Task, reason string) {
	if childTask == nil {
		return
	}
	_, _, _, _ = s.Tasks.ApplyCommand(ctx, taskruntime.CommandInput{
		TaskID:    childTask.TaskID,
		Command:   contracts.CmdCancel,
		ActorID:   input.ActorID,
		ActorType: "agent",
		RunID:     input.SourceRunID,
		Payload:   map[string]any{"reason": reason},
	})
}

func (s *Service) Complete(ctx context.Context, handoffID contracts.HandoffID, actorID string, traceID contracts.TraceID, output map[string]any) (contracts.AgentHandoff, error) {
	h, ok, err := s.Repository.Get(ctx, handoffID)
	if err != nil {
		return contracts.AgentHandoff{}, err
	}
	if !ok {
		return contracts.AgentHandoff{}, fmt.Errorf("handoff %s not found", handoffID)
	}
	now := s.Now()
	h.Status = contracts.HandoffCompleted
	h.CompletedAt = &now
	if err := s.Repository.Update(ctx, h); err != nil {
		return contracts.AgentHandoff{}, err
	}
	_ = s.Events.Append(ctx, contracts.TaskEvent{
		EventID:   contracts.TaskEventID(idgen.New("taskevt")),
		TaskID:    h.ParentTaskID,
		TenantID:  h.TenantID,
		Type:      "handoff.completed",
		ActorID:   actorID,
		ActorType: "agent",
		Payload:   map[string]any{"handoff_id": h.HandoffID, "output": output},
		CreatedAt: now,
	})
	if s.Audit != nil {
		_ = s.Audit.Log(ctx, contracts.AuditEvent{
			TenantID:     h.TenantID,
			ActorID:      actorID,
			ActorType:    "agent",
			Action:       "handoff.completed",
			ResourceType: "handoff",
			ResourceID:   string(h.HandoffID),
			Decision:     "allowed",
			TaskID:       h.ParentTaskID,
			CreatedAt:    now,
		})
	}
	s.traceHandoff(ctx, h, traceID, contracts.TraceHandoffCompleted, map[string]any{"handoff_id": h.HandoffID, "output": output})
	s.syncHandoffMessage(ctx, h, traceID, "", actorID, "agent", "handoff.completed", fmt.Sprintf("handoff %s completed", h.HandoffID))
	return h, nil
}

func (s *Service) Transition(ctx context.Context, handoffID contracts.HandoffID, next contracts.HandoffStatus, actorID string, actorType string, traceID contracts.TraceID, reason string) (contracts.AgentHandoff, error) {
	h, ok, err := s.Repository.Get(ctx, handoffID)
	if err != nil {
		return contracts.AgentHandoff{}, err
	}
	if !ok {
		return contracts.AgentHandoff{}, fmt.Errorf("handoff %s not found", handoffID)
	}
	if err := validateTransition(h.Status, next); err != nil {
		return contracts.AgentHandoff{}, err
	}
	now := s.Now()
	h.Status = next
	if next == contracts.HandoffCompleted || next == contracts.HandoffFailed || next == contracts.HandoffCancelled || next == contracts.HandoffRejected {
		h.CompletedAt = &now
	}
	if err := s.Repository.Update(ctx, h); err != nil {
		return contracts.AgentHandoff{}, err
	}
	eventType := "handoff." + string(next)
	if s.Events != nil {
		_ = s.Events.Append(ctx, contracts.TaskEvent{
			EventID:   contracts.TaskEventID(idgen.New("taskevt")),
			TaskID:    h.ParentTaskID,
			TenantID:  h.TenantID,
			Type:      eventType,
			ActorID:   actorID,
			ActorType: actorType,
			Payload:   map[string]any{"handoff_id": h.HandoffID, "status": h.Status, "reason": reason},
			CreatedAt: now,
		})
	}
	if s.Audit != nil {
		_ = s.Audit.Log(ctx, contracts.AuditEvent{
			AuditID:      idgen.New("audit"),
			TenantID:     h.TenantID,
			ActorID:      actorID,
			ActorType:    actorType,
			Action:       eventType,
			ResourceType: "handoff",
			ResourceID:   string(h.HandoffID),
			Decision:     "allowed",
			Reason:       reason,
			TaskID:       h.ParentTaskID,
			CreatedAt:    now,
		})
	}
	s.traceHandoff(ctx, h, traceID, eventType, map[string]any{"handoff_id": h.HandoffID, "status": h.Status, "reason": reason})
	s.syncHandoffMessage(ctx, h, traceID, "", actorID, actorType, eventType, reason)
	return h, nil
}

func (s *Service) syncHandoffCreated(ctx context.Context, input CreateInput, h contracts.AgentHandoff) {
	if binding, ok := s.externalBinding(ctx, h.TenantID, h.ParentTaskID); ok {
		s.ExternalSync.HandoffCreatedWithContext(ctx, binding, array.SyncContext{
			TenantID:  h.TenantID,
			TraceID:   input.TraceID,
			RunID:     input.SourceRunID,
			TaskID:    h.ParentTaskID,
			ActorID:   input.ActorID,
			ActorType: "agent",
		}, h)
	}
}

func (s *Service) syncHandoffMessage(ctx context.Context, h contracts.AgentHandoff, traceID contracts.TraceID, runID contracts.AgentRunID, actorID string, actorType string, eventType string, message string) {
	if binding, ok := s.externalBinding(ctx, h.TenantID, h.ParentTaskID); ok {
		s.ExternalSync.ReplyWithContext(ctx, binding, array.SyncContext{
			TenantID:  h.TenantID,
			TraceID:   traceID,
			RunID:     runID,
			TaskID:    h.ParentTaskID,
			ActorID:   actorID,
			ActorType: actorType,
		}, "["+eventType+"] "+message)
	}
}

func (s *Service) externalBinding(ctx context.Context, tenantID contracts.TenantID, taskID contracts.TaskID) (*contracts.ExternalTaskBinding, bool) {
	if s.ExternalBinding == nil || taskID == "" {
		return nil, false
	}
	return s.ExternalBinding(ctx, tenantID, taskID)
}

func validateTransition(current contracts.HandoffStatus, next contracts.HandoffStatus) error {
	if current == next {
		return nil
	}
	switch next {
	case contracts.HandoffAccepted:
		if current == contracts.HandoffCreated {
			return nil
		}
	case contracts.HandoffRejected:
		if current == contracts.HandoffCreated {
			return nil
		}
	case contracts.HandoffRunning:
		if current == contracts.HandoffAccepted || current == contracts.HandoffCreated {
			return nil
		}
	case contracts.HandoffCompleted:
		if current == contracts.HandoffRunning || current == contracts.HandoffAccepted {
			return nil
		}
	case contracts.HandoffFailed, contracts.HandoffCancelled:
		if current != contracts.HandoffCompleted && current != contracts.HandoffRejected {
			return nil
		}
	}
	return fmt.Errorf("invalid handoff transition from %s to %s", current, next)
}

func (s *Service) auditEvent(ctx context.Context, input CreateInput, action string, resourceID string, decision string, reason string) {
	if s.Audit == nil {
		return
	}
	if resourceID == "" {
		resourceID = string(input.ParentTaskID)
	}
	_ = s.Audit.Log(ctx, contracts.AuditEvent{
		TenantID:     input.TenantID,
		ActorID:      input.ActorID,
		ActorType:    "agent",
		Action:       action,
		ResourceType: "handoff",
		ResourceID:   resourceID,
		Decision:     decision,
		Reason:       reason,
		TaskID:       input.ParentTaskID,
		RunID:        input.SourceRunID,
		CreatedAt:    s.Now(),
	})
}

func (s *Service) traceEvent(ctx context.Context, input CreateInput, eventType string, handoffID string, payload map[string]any) {
	if s.Trace == nil || input.TraceID == "" {
		return
	}
	if payload == nil {
		payload = map[string]any{}
	}
	if handoffID != "" {
		payload["handoff_id"] = handoffID
	}
	_ = s.Trace.Record(ctx, contracts.TraceEvent{
		TraceID:   input.TraceID,
		TenantID:  input.TenantID,
		SpanID:    contracts.SpanID(idgen.New("span")),
		RunID:     input.SourceRunID,
		TaskID:    input.ParentTaskID,
		Type:      eventType,
		Payload:   payload,
		CreatedAt: s.Now(),
	})
}

func (s *Service) traceHandoff(ctx context.Context, h contracts.AgentHandoff, traceID contracts.TraceID, eventType string, payload map[string]any) {
	if s.Trace == nil || traceID == "" {
		return
	}
	if payload == nil {
		payload = map[string]any{}
	}
	_ = s.Trace.Record(ctx, contracts.TraceEvent{
		TraceID:   traceID,
		TenantID:  h.TenantID,
		SpanID:    contracts.SpanID(idgen.New("span")),
		TaskID:    h.ParentTaskID,
		Type:      eventType,
		Payload:   payload,
		CreatedAt: s.Now(),
	})
}
