package process

import (
	"context"
	"fmt"
	"sort"
	"time"

	"znt/internal/contracts"
	"znt/internal/governance/audit"
	"znt/internal/governance/trace"
	"znt/pkg/idgen"
)

type UpsertTemplateInput struct {
	TenantID   contracts.TenantID
	TemplateID contracts.GovernanceProcessTemplateID
	Name       string
	Version    string
	Status     string
	Gates      []contracts.GovernanceGateDefinition
	Metadata   map[string]any
	ActorID    string
}

type StartRunInput struct {
	TenantID    contracts.TenantID
	TemplateID  contracts.GovernanceProcessTemplateID
	SubjectType string
	SubjectID   string
	TaskID      contracts.TaskID
	AgentRunID  contracts.AgentRunID
	TraceID     contracts.TraceID
	PolicySetID contracts.PolicySetID
	Actors      []contracts.GovernanceActorRef
	Gates       []contracts.GovernanceGateDefinition
	Metadata    map[string]any
	ActorID     string
}

type OpenGateInput struct {
	TenantID     contracts.TenantID
	ProcessRunID contracts.GovernanceProcessRunID
	GateRunID    contracts.GovernanceGateRunID
	GateID       string
	ArtifactRefs []contracts.GovernanceArtifactRef
	EvidenceRefs []contracts.GovernanceEvidenceRef
	Metadata     map[string]any
	ActorID      string
}

type ReviewInput struct {
	TenantID     contracts.TenantID
	GateRunID    contracts.GovernanceGateRunID
	ReviewerID   string
	ReviewerType string
	Decision     contracts.GovernanceReviewDecision
	Reason       string
	EvidenceRefs []contracts.GovernanceEvidenceRef
	Independent  bool
}

type EscalateInput struct {
	TenantID    contracts.TenantID
	GateRunID   contracts.GovernanceGateRunID
	Issue       string
	Arguments   []contracts.GovernanceEvidenceRef
	EscalatedTo string
	ActorID     string
}

type ArbitrateInput struct {
	TenantID     contracts.TenantID
	ConflictID   contracts.GovernanceConflictID
	Decision     contracts.GovernanceReviewDecision
	Reason       string
	ArbitratorID string
}

type Service struct {
	Store Store
	Audit audit.Logger
	Trace trace.Recorder
	Now   func() time.Time
}

func NewService(store Store, auditLogger audit.Logger, traceRecorder trace.Recorder) *Service {
	if store == nil {
		store = NewInMemoryStore()
	}
	return &Service{
		Store: store,
		Audit: auditLogger,
		Trace: traceRecorder,
		Now:   func() time.Time { return time.Now().UTC() },
	}
}

func (s *Service) UpsertTemplate(ctx context.Context, input UpsertTemplateInput) (contracts.GovernanceProcessTemplate, error) {
	if input.TenantID == "" || input.Name == "" {
		return contracts.GovernanceProcessTemplate{}, fmt.Errorf("governance template requires tenant_id and name")
	}
	if input.TemplateID == "" {
		input.TemplateID = contracts.GovernanceProcessTemplateID(idgen.New("govtpl"))
	}
	if input.Version == "" {
		input.Version = "v1"
	}
	if input.Status == "" {
		input.Status = "active"
	}
	gates, err := normalizeGates(input.Gates)
	if err != nil {
		return contracts.GovernanceProcessTemplate{}, err
	}
	now := s.now()
	existing, ok, err := s.Store.GetTemplate(ctx, input.TenantID, input.TemplateID)
	if err != nil {
		return contracts.GovernanceProcessTemplate{}, err
	}
	createdAt := now
	createdBy := input.ActorID
	if ok {
		createdAt = existing.CreatedAt
		createdBy = existing.CreatedBy
	}
	template := contracts.GovernanceProcessTemplate{
		TemplateID: input.TemplateID,
		TenantID:   input.TenantID,
		Name:       input.Name,
		Version:    input.Version,
		Status:     input.Status,
		Gates:      gates,
		Metadata:   input.Metadata,
		CreatedBy:  createdBy,
		CreatedAt:  createdAt,
		UpdatedAt:  now,
	}
	if err := s.Store.UpsertTemplate(ctx, template); err != nil {
		return contracts.GovernanceProcessTemplate{}, err
	}
	s.audit(ctx, input.TenantID, input.ActorID, contracts.AuditGovernanceTemplateUpserted, "governance_template", string(template.TemplateID), "allowed", "", "")
	return template, nil
}

func (s *Service) StartRun(ctx context.Context, input StartRunInput) (contracts.GovernanceProcessSnapshot, error) {
	if input.TenantID == "" || input.SubjectType == "" || input.SubjectID == "" {
		return contracts.GovernanceProcessSnapshot{}, fmt.Errorf("governance process run requires tenant_id, subject_type, and subject_id")
	}
	gates := input.Gates
	if input.TemplateID != "" {
		template, ok, err := s.Store.GetTemplate(ctx, input.TenantID, input.TemplateID)
		if err != nil {
			return contracts.GovernanceProcessSnapshot{}, err
		}
		if !ok {
			return contracts.GovernanceProcessSnapshot{}, fmt.Errorf("governance template %s not found", input.TemplateID)
		}
		gates = template.Gates
	}
	normalized, err := normalizeGates(gates)
	if err != nil {
		return contracts.GovernanceProcessSnapshot{}, err
	}
	now := s.now()
	run := contracts.GovernanceProcessRun{
		RunID:       contracts.GovernanceProcessRunID(idgen.New("govrun")),
		TenantID:    input.TenantID,
		TemplateID:  input.TemplateID,
		Status:      contracts.GovernanceRunActive,
		SubjectType: input.SubjectType,
		SubjectID:   input.SubjectID,
		TaskID:      input.TaskID,
		AgentRunID:  input.AgentRunID,
		TraceID:     input.TraceID,
		PolicySetID: input.PolicySetID,
		Actors:      input.Actors,
		Metadata:    input.Metadata,
		CreatedBy:   input.ActorID,
		CreatedAt:   now,
		UpdatedAt:   now,
	}
	if len(normalized) == 0 {
		run.Status = contracts.GovernanceRunCompleted
		run.CompletedAt = &now
	}
	gateRuns := make([]contracts.GovernanceGateRun, 0, len(normalized))
	for _, gate := range normalized {
		gateRuns = append(gateRuns, contracts.GovernanceGateRun{
			GateRunID:         contracts.GovernanceGateRunID(idgen.New("govgate")),
			ProcessRunID:      run.RunID,
			TenantID:          input.TenantID,
			GateID:            gate.GateID,
			Name:              gate.Name,
			Status:            contracts.GovernanceGatePending,
			SubjectType:       gate.SubjectType,
			SubjectID:         input.SubjectID,
			ReviewMode:        gate.ReviewMode,
			ConsensusPolicy:   gate.ConsensusPolicy,
			EscalationPolicy:  gate.EscalationPolicy,
			RequiredReviewers: gate.RequiredReviewers,
			MinApprovals:      gate.MinApprovals,
			ReviewerRoles:     gate.ReviewerRoles,
			EvidenceTypes:     gate.EvidenceTypes,
			Metadata:          gate.Metadata,
			CreatedAt:         now,
			UpdatedAt:         now,
		})
	}
	if err := s.Store.CreateRun(ctx, run, gateRuns); err != nil {
		return contracts.GovernanceProcessSnapshot{}, err
	}
	s.audit(ctx, input.TenantID, input.ActorID, contracts.AuditGovernanceProcessStarted, "governance_process_run", string(run.RunID), "allowed", "", input.TraceID)
	s.traceEvent(ctx, run, contracts.TraceGovernanceProcessStarted, map[string]any{
		"process_run_id": run.RunID,
		"template_id":    run.TemplateID,
		"subject_type":   run.SubjectType,
		"subject_id":     run.SubjectID,
		"gate_count":     len(gateRuns),
	})
	return s.Snapshot(ctx, input.TenantID, run.RunID)
}

func (s *Service) OpenGate(ctx context.Context, input OpenGateInput) (contracts.GovernanceProcessSnapshot, error) {
	gate, err := s.findGate(ctx, input.TenantID, input.ProcessRunID, input.GateRunID, input.GateID)
	if err != nil {
		return contracts.GovernanceProcessSnapshot{}, err
	}
	if gate.Status != contracts.GovernanceGatePending && gate.Status != contracts.GovernanceGateOpen {
		return contracts.GovernanceProcessSnapshot{}, fmt.Errorf("cannot open governance gate %s from status %s", gate.GateRunID, gate.Status)
	}
	run, ok, err := s.Store.GetRun(ctx, input.TenantID, gate.ProcessRunID)
	if err != nil || !ok {
		return contracts.GovernanceProcessSnapshot{}, fmt.Errorf("governance process run %s not found", gate.ProcessRunID)
	}
	now := s.now()
	if gate.OpenedAt == nil {
		gate.OpenedAt = &now
	}
	gate.ArtifactRefs = append(gate.ArtifactRefs, input.ArtifactRefs...)
	gate.EvidenceRefs = append(gate.EvidenceRefs, stampEvidence(input.EvidenceRefs, input.ActorID, now)...)
	if input.Metadata != nil {
		gate.Metadata = mergeMetadata(gate.Metadata, input.Metadata)
	}
	if gate.ReviewMode == contracts.GovernanceReviewNone {
		gate.Status = contracts.GovernanceGatePassed
		gate.ResolvedAt = &now
		gate.ResolvedBy = input.ActorID
		gate.ResolutionDecision = contracts.GovernanceReviewApprove
		gate.ResolutionReason = "review_mode none"
	} else {
		gate.Status = contracts.GovernanceGateOpen
	}
	gate.UpdatedAt = now
	if err := s.Store.UpdateGate(ctx, gate); err != nil {
		return contracts.GovernanceProcessSnapshot{}, err
	}
	s.audit(ctx, input.TenantID, input.ActorID, contracts.AuditGovernanceGateOpened, "governance_gate_run", string(gate.GateRunID), string(gate.Status), gate.ResolutionReason, run.TraceID)
	s.traceEvent(ctx, run, contracts.TraceGovernanceGateOpened, map[string]any{
		"gate_run_id": gate.GateRunID,
		"gate_id":     gate.GateID,
		"status":      gate.Status,
	})
	if err := s.refreshRunStatus(ctx, run); err != nil {
		return contracts.GovernanceProcessSnapshot{}, err
	}
	return s.Snapshot(ctx, input.TenantID, gate.ProcessRunID)
}

func (s *Service) SubmitReview(ctx context.Context, input ReviewInput) (contracts.GovernanceProcessSnapshot, error) {
	if input.TenantID == "" || input.GateRunID == "" || input.ReviewerID == "" || input.Decision == "" {
		return contracts.GovernanceProcessSnapshot{}, fmt.Errorf("governance review requires tenant_id, gate_run_id, reviewer_id, and decision")
	}
	if err := validateReviewDecision(input.Decision); err != nil {
		return contracts.GovernanceProcessSnapshot{}, err
	}
	gate, ok, err := s.Store.GetGate(ctx, input.TenantID, input.GateRunID)
	if err != nil || !ok {
		return contracts.GovernanceProcessSnapshot{}, fmt.Errorf("governance gate %s not found", input.GateRunID)
	}
	if gate.Status != contracts.GovernanceGateOpen {
		return contracts.GovernanceProcessSnapshot{}, fmt.Errorf("cannot review governance gate %s from status %s", gate.GateRunID, gate.Status)
	}
	reviews, err := s.Store.ListReviewsByGate(ctx, input.TenantID, input.GateRunID)
	if err != nil {
		return contracts.GovernanceProcessSnapshot{}, err
	}
	for _, review := range reviews {
		if review.ReviewerID == input.ReviewerID && review.ReviewerType == input.ReviewerType {
			return contracts.GovernanceProcessSnapshot{}, fmt.Errorf("reviewer %s already reviewed gate %s", input.ReviewerID, input.GateRunID)
		}
	}
	run, ok, err := s.Store.GetRun(ctx, input.TenantID, gate.ProcessRunID)
	if err != nil || !ok {
		return contracts.GovernanceProcessSnapshot{}, fmt.Errorf("governance process run %s not found", gate.ProcessRunID)
	}
	now := s.now()
	review := contracts.GovernanceReview{
		ReviewID:     contracts.GovernanceReviewID(idgen.New("govreview")),
		GateRunID:    gate.GateRunID,
		ProcessRunID: gate.ProcessRunID,
		TenantID:     input.TenantID,
		ReviewerID:   input.ReviewerID,
		ReviewerType: input.ReviewerType,
		Decision:     input.Decision,
		Reason:       input.Reason,
		EvidenceRefs: stampEvidence(input.EvidenceRefs, input.ReviewerID, now),
		Independent:  input.Independent,
		CreatedAt:    now,
	}
	if review.ReviewerType == "" {
		review.ReviewerType = "agent"
	}
	if err := s.Store.SaveReview(ctx, review); err != nil {
		return contracts.GovernanceProcessSnapshot{}, err
	}
	reviews = append(reviews, review)
	gate, err = s.applyConsensus(ctx, run, gate, reviews, input.ReviewerID)
	if err != nil {
		return contracts.GovernanceProcessSnapshot{}, err
	}
	s.audit(ctx, input.TenantID, input.ReviewerID, contracts.AuditGovernanceReviewSubmitted, "governance_gate_run", string(gate.GateRunID), string(review.Decision), review.Reason, run.TraceID)
	s.traceEvent(ctx, run, contracts.TraceGovernanceReviewSubmitted, map[string]any{
		"gate_run_id": gate.GateRunID,
		"review_id":   review.ReviewID,
		"reviewer_id": review.ReviewerID,
		"decision":    review.Decision,
		"gate_status": gate.Status,
	})
	if err := s.refreshRunStatus(ctx, run); err != nil {
		return contracts.GovernanceProcessSnapshot{}, err
	}
	return s.Snapshot(ctx, input.TenantID, gate.ProcessRunID)
}

func (s *Service) Escalate(ctx context.Context, input EscalateInput) (contracts.GovernanceProcessSnapshot, error) {
	if input.TenantID == "" || input.GateRunID == "" || input.Issue == "" {
		return contracts.GovernanceProcessSnapshot{}, fmt.Errorf("governance escalation requires tenant_id, gate_run_id, and issue")
	}
	gate, ok, err := s.Store.GetGate(ctx, input.TenantID, input.GateRunID)
	if err != nil || !ok {
		return contracts.GovernanceProcessSnapshot{}, fmt.Errorf("governance gate %s not found", input.GateRunID)
	}
	run, ok, err := s.Store.GetRun(ctx, input.TenantID, gate.ProcessRunID)
	if err != nil || !ok {
		return contracts.GovernanceProcessSnapshot{}, fmt.Errorf("governance process run %s not found", gate.ProcessRunID)
	}
	now := s.now()
	if input.EscalatedTo == "" && gate.EscalationPolicy == contracts.GovernanceEscalationOrchestrator {
		input.EscalatedTo = "orchestrator"
	}
	conflict := contracts.GovernanceConflict{
		ConflictID:   contracts.GovernanceConflictID(idgen.New("govconflict")),
		GateRunID:    gate.GateRunID,
		ProcessRunID: gate.ProcessRunID,
		TenantID:     input.TenantID,
		Status:       "open",
		Issue:        input.Issue,
		Arguments:    stampEvidence(input.Arguments, input.ActorID, now),
		EscalatedTo:  input.EscalatedTo,
		CreatedBy:    input.ActorID,
		CreatedAt:    now,
	}
	if err := s.Store.SaveConflict(ctx, conflict); err != nil {
		return contracts.GovernanceProcessSnapshot{}, err
	}
	gate.Status = contracts.GovernanceGateEscalationPending
	gate.UpdatedAt = now
	if err := s.Store.UpdateGate(ctx, gate); err != nil {
		return contracts.GovernanceProcessSnapshot{}, err
	}
	s.audit(ctx, input.TenantID, input.ActorID, contracts.AuditGovernanceConflictEscalated, "governance_conflict", string(conflict.ConflictID), "pending", input.Issue, run.TraceID)
	s.traceEvent(ctx, run, contracts.TraceGovernanceConflictEscalated, map[string]any{
		"gate_run_id":  gate.GateRunID,
		"conflict_id":  conflict.ConflictID,
		"issue":        conflict.Issue,
		"escalated_to": conflict.EscalatedTo,
	})
	return s.Snapshot(ctx, input.TenantID, gate.ProcessRunID)
}

func (s *Service) Arbitrate(ctx context.Context, input ArbitrateInput) (contracts.GovernanceProcessSnapshot, error) {
	if input.TenantID == "" || input.ConflictID == "" || input.Decision == "" || input.ArbitratorID == "" {
		return contracts.GovernanceProcessSnapshot{}, fmt.Errorf("governance arbitration requires tenant_id, conflict_id, decision, and arbitrator_id")
	}
	if err := validateReviewDecision(input.Decision); err != nil {
		return contracts.GovernanceProcessSnapshot{}, err
	}
	conflict, ok, err := s.Store.GetConflict(ctx, input.TenantID, input.ConflictID)
	if err != nil || !ok {
		return contracts.GovernanceProcessSnapshot{}, fmt.Errorf("governance conflict %s not found", input.ConflictID)
	}
	if conflict.Status != "open" {
		return contracts.GovernanceProcessSnapshot{}, fmt.Errorf("governance conflict %s is not open", input.ConflictID)
	}
	gate, ok, err := s.Store.GetGate(ctx, input.TenantID, conflict.GateRunID)
	if err != nil || !ok {
		return contracts.GovernanceProcessSnapshot{}, fmt.Errorf("governance gate %s not found", conflict.GateRunID)
	}
	run, ok, err := s.Store.GetRun(ctx, input.TenantID, conflict.ProcessRunID)
	if err != nil || !ok {
		return contracts.GovernanceProcessSnapshot{}, fmt.Errorf("governance process run %s not found", conflict.ProcessRunID)
	}
	now := s.now()
	conflict.Status = "resolved"
	conflict.ArbitrationDecision = input.Decision
	conflict.ResolutionReason = input.Reason
	conflict.ResolvedAt = &now
	if err := s.Store.UpdateConflict(ctx, conflict); err != nil {
		return contracts.GovernanceProcessSnapshot{}, err
	}
	gate.Status = contracts.GovernanceGateArbitrated
	gate.ResolvedAt = &now
	gate.ResolvedBy = input.ArbitratorID
	gate.ResolutionDecision = input.Decision
	gate.ResolutionReason = input.Reason
	gate.UpdatedAt = now
	if err := s.Store.UpdateGate(ctx, gate); err != nil {
		return contracts.GovernanceProcessSnapshot{}, err
	}
	s.audit(ctx, input.TenantID, input.ArbitratorID, contracts.AuditGovernanceConflictArbitrated, "governance_conflict", string(conflict.ConflictID), string(input.Decision), input.Reason, run.TraceID)
	s.traceEvent(ctx, run, contracts.TraceGovernanceConflictArbitrated, map[string]any{
		"gate_run_id": gate.GateRunID,
		"conflict_id": conflict.ConflictID,
		"decision":    input.Decision,
		"reason":      input.Reason,
	})
	if err := s.refreshRunStatus(ctx, run); err != nil {
		return contracts.GovernanceProcessSnapshot{}, err
	}
	return s.Snapshot(ctx, input.TenantID, gate.ProcessRunID)
}

func (s *Service) Snapshot(ctx context.Context, tenantID contracts.TenantID, runID contracts.GovernanceProcessRunID) (contracts.GovernanceProcessSnapshot, error) {
	run, ok, err := s.Store.GetRun(ctx, tenantID, runID)
	if err != nil {
		return contracts.GovernanceProcessSnapshot{}, err
	}
	if !ok {
		return contracts.GovernanceProcessSnapshot{}, fmt.Errorf("governance process run %s not found", runID)
	}
	gates, err := s.Store.ListGates(ctx, tenantID, runID)
	if err != nil {
		return contracts.GovernanceProcessSnapshot{}, err
	}
	reviews, err := s.Store.ListReviews(ctx, tenantID, runID)
	if err != nil {
		return contracts.GovernanceProcessSnapshot{}, err
	}
	conflicts, err := s.Store.ListConflicts(ctx, tenantID, runID)
	if err != nil {
		return contracts.GovernanceProcessSnapshot{}, err
	}
	sort.Slice(gates, func(i, j int) bool {
		if gates[i].CreatedAt.Equal(gates[j].CreatedAt) {
			return gates[i].GateRunID < gates[j].GateRunID
		}
		return gates[i].CreatedAt.Before(gates[j].CreatedAt)
	})
	sort.Slice(reviews, func(i, j int) bool { return reviews[i].CreatedAt.Before(reviews[j].CreatedAt) })
	sort.Slice(conflicts, func(i, j int) bool { return conflicts[i].CreatedAt.Before(conflicts[j].CreatedAt) })
	return contracts.GovernanceProcessSnapshot{ProcessRun: run, Gates: gates, Reviews: reviews, Conflicts: conflicts}, nil
}

func (s *Service) findGate(ctx context.Context, tenantID contracts.TenantID, processRunID contracts.GovernanceProcessRunID, gateRunID contracts.GovernanceGateRunID, gateID string) (contracts.GovernanceGateRun, error) {
	if gateRunID != "" {
		gate, ok, err := s.Store.GetGate(ctx, tenantID, gateRunID)
		if err != nil || !ok {
			return contracts.GovernanceGateRun{}, fmt.Errorf("governance gate %s not found", gateRunID)
		}
		return gate, nil
	}
	if processRunID == "" || gateID == "" {
		return contracts.GovernanceGateRun{}, fmt.Errorf("opening a governance gate requires gate_run_id or process_run_id plus gate_id")
	}
	gate, ok, err := s.Store.GetGateByDefinition(ctx, tenantID, processRunID, gateID)
	if err != nil || !ok {
		return contracts.GovernanceGateRun{}, fmt.Errorf("governance gate %s not found on run %s", gateID, processRunID)
	}
	return gate, nil
}

func (s *Service) applyConsensus(ctx context.Context, run contracts.GovernanceProcessRun, gate contracts.GovernanceGateRun, reviews []contracts.GovernanceReview, actorID string) (contracts.GovernanceGateRun, error) {
	approvals, negatives, abstentions := countReviews(reviews)
	required := gate.RequiredReviewers
	if required <= 0 {
		required = defaultRequiredReviewers(gate.ReviewMode)
	}
	minApprovals := gate.MinApprovals
	if minApprovals <= 0 {
		minApprovals = defaultMinApprovals(required, gate.ConsensusPolicy)
	}
	if len(reviews) < required {
		return gate, nil
	}
	status := gate.Status
	reason := ""
	switch gate.ConsensusPolicy {
	case contracts.GovernanceConsensusAny:
		if approvals > 0 {
			status = contracts.GovernanceGatePassed
			reason = "any approval reached"
		} else if negatives > 0 {
			status = contracts.GovernanceGateRejected
			reason = "negative review reached"
		}
	case contracts.GovernanceConsensusAll:
		if approvals >= required && negatives == 0 && abstentions == 0 {
			status = contracts.GovernanceGatePassed
			reason = "unanimous approval reached"
		} else {
			status = escalationOrRejected(gate)
			reason = "unanimous consensus not reached"
		}
	default:
		if approvals >= minApprovals && approvals > negatives {
			status = contracts.GovernanceGatePassed
			reason = "majority approval reached"
		} else if negatives > approvals {
			status = contracts.GovernanceGateRejected
			reason = "majority rejection reached"
		} else {
			status = escalationOrRejected(gate)
			reason = "majority consensus not reached"
		}
	}
	if status == gate.Status {
		return gate, nil
	}
	now := s.now()
	gate.Status = status
	gate.UpdatedAt = now
	if status == contracts.GovernanceGatePassed || status == contracts.GovernanceGateRejected {
		gate.ResolvedAt = &now
		gate.ResolvedBy = actorID
		if status == contracts.GovernanceGatePassed {
			gate.ResolutionDecision = contracts.GovernanceReviewApprove
		} else {
			gate.ResolutionDecision = contracts.GovernanceReviewReject
		}
	}
	gate.ResolutionReason = reason
	if err := s.Store.UpdateGate(ctx, gate); err != nil {
		return contracts.GovernanceGateRun{}, err
	}
	s.audit(ctx, gate.TenantID, actorID, contracts.AuditGovernanceGateResolved, "governance_gate_run", string(gate.GateRunID), string(gate.Status), reason, run.TraceID)
	s.traceEvent(ctx, run, contracts.TraceGovernanceGateResolved, map[string]any{
		"gate_run_id": gate.GateRunID,
		"status":      gate.Status,
		"reason":      reason,
		"approvals":   approvals,
		"negatives":   negatives,
		"abstentions": abstentions,
	})
	return gate, nil
}

func (s *Service) refreshRunStatus(ctx context.Context, run contracts.GovernanceProcessRun) error {
	gates, err := s.Store.ListGates(ctx, run.TenantID, run.RunID)
	if err != nil {
		return err
	}
	if len(gates) == 0 {
		return nil
	}
	allTerminal := true
	blocked := false
	for _, gate := range gates {
		switch gate.Status {
		case contracts.GovernanceGateRejected:
			blocked = true
		case contracts.GovernanceGateArbitrated:
			if gate.ResolutionDecision == contracts.GovernanceReviewReject || gate.ResolutionDecision == contracts.GovernanceReviewRevise {
				blocked = true
			}
		case contracts.GovernanceGatePassed, contracts.GovernanceGateSkipped:
		default:
			allTerminal = false
		}
	}
	now := s.now()
	updated := false
	if blocked && run.Status != contracts.GovernanceRunBlocked {
		run.Status = contracts.GovernanceRunBlocked
		run.UpdatedAt = now
		updated = true
	} else if allTerminal && run.Status != contracts.GovernanceRunCompleted {
		run.Status = contracts.GovernanceRunCompleted
		run.CompletedAt = &now
		run.UpdatedAt = now
		updated = true
	}
	if updated {
		return s.Store.UpdateRun(ctx, run)
	}
	return nil
}

func normalizeGates(gates []contracts.GovernanceGateDefinition) ([]contracts.GovernanceGateDefinition, error) {
	out := make([]contracts.GovernanceGateDefinition, 0, len(gates))
	seen := map[string]struct{}{}
	for i, gate := range gates {
		if gate.GateID == "" {
			gate.GateID = fmt.Sprintf("gate_%d", i+1)
		}
		if _, ok := seen[gate.GateID]; ok {
			return nil, fmt.Errorf("duplicate governance gate_id %s", gate.GateID)
		}
		seen[gate.GateID] = struct{}{}
		if gate.Name == "" {
			gate.Name = gate.GateID
		}
		if gate.ReviewMode == "" {
			gate.ReviewMode = contracts.GovernanceReviewNone
		}
		if err := validateReviewMode(gate.ReviewMode); err != nil {
			return nil, err
		}
		if gate.ConsensusPolicy == "" {
			if gate.ReviewMode == contracts.GovernanceReviewSingle {
				gate.ConsensusPolicy = contracts.GovernanceConsensusAny
			} else {
				gate.ConsensusPolicy = contracts.GovernanceConsensusMajority
			}
		}
		if err := validateConsensusPolicy(gate.ConsensusPolicy); err != nil {
			return nil, err
		}
		if gate.EscalationPolicy == "" {
			gate.EscalationPolicy = contracts.GovernanceEscalationNone
		}
		if err := validateEscalationPolicy(gate.EscalationPolicy); err != nil {
			return nil, err
		}
		if gate.RequiredReviewers <= 0 {
			gate.RequiredReviewers = defaultRequiredReviewers(gate.ReviewMode)
		}
		if gate.ReviewMode == contracts.GovernanceReviewMulti && gate.RequiredReviewers < 2 {
			gate.RequiredReviewers = 2
		}
		if gate.MinApprovals <= 0 {
			gate.MinApprovals = defaultMinApprovals(gate.RequiredReviewers, gate.ConsensusPolicy)
		}
		out = append(out, gate)
	}
	return out, nil
}

func validateReviewMode(mode contracts.GovernanceReviewMode) error {
	switch mode {
	case contracts.GovernanceReviewNone, contracts.GovernanceReviewSingle, contracts.GovernanceReviewMulti:
		return nil
	default:
		return fmt.Errorf("unknown governance review_mode %q", mode)
	}
}

func validateConsensusPolicy(policy contracts.GovernanceConsensusPolicy) error {
	switch policy {
	case contracts.GovernanceConsensusAny, contracts.GovernanceConsensusMajority, contracts.GovernanceConsensusAll:
		return nil
	default:
		return fmt.Errorf("unknown governance consensus_policy %q", policy)
	}
}

func validateEscalationPolicy(policy contracts.GovernanceEscalationPolicy) error {
	switch policy {
	case contracts.GovernanceEscalationNone, contracts.GovernanceEscalationOrchestrator:
		return nil
	default:
		return fmt.Errorf("unknown governance escalation_policy %q", policy)
	}
}

func validateReviewDecision(decision contracts.GovernanceReviewDecision) error {
	switch decision {
	case contracts.GovernanceReviewApprove, contracts.GovernanceReviewReject, contracts.GovernanceReviewRevise, contracts.GovernanceReviewAbstain:
		return nil
	default:
		return fmt.Errorf("unknown governance review decision %q", decision)
	}
}

func defaultRequiredReviewers(mode contracts.GovernanceReviewMode) int {
	switch mode {
	case contracts.GovernanceReviewSingle:
		return 1
	case contracts.GovernanceReviewMulti:
		return 2
	default:
		return 0
	}
}

func defaultMinApprovals(required int, policy contracts.GovernanceConsensusPolicy) int {
	if required <= 0 {
		return 0
	}
	switch policy {
	case contracts.GovernanceConsensusAny:
		return 1
	case contracts.GovernanceConsensusAll:
		return required
	default:
		return required/2 + 1
	}
}

func countReviews(reviews []contracts.GovernanceReview) (int, int, int) {
	approvals, negatives, abstentions := 0, 0, 0
	for _, review := range reviews {
		switch review.Decision {
		case contracts.GovernanceReviewApprove:
			approvals++
		case contracts.GovernanceReviewReject, contracts.GovernanceReviewRevise:
			negatives++
		default:
			abstentions++
		}
	}
	return approvals, negatives, abstentions
}

func escalationOrRejected(gate contracts.GovernanceGateRun) contracts.GovernanceGateStatus {
	if gate.EscalationPolicy == contracts.GovernanceEscalationOrchestrator {
		return contracts.GovernanceGateEscalationPending
	}
	return contracts.GovernanceGateRejected
}

func stampEvidence(evidence []contracts.GovernanceEvidenceRef, actorID string, now time.Time) []contracts.GovernanceEvidenceRef {
	out := make([]contracts.GovernanceEvidenceRef, 0, len(evidence))
	for _, ref := range evidence {
		if ref.SubmittedBy == "" {
			ref.SubmittedBy = actorID
		}
		if ref.SubmittedAt == nil {
			t := now
			ref.SubmittedAt = &t
		}
		out = append(out, ref)
	}
	return out
}

func mergeMetadata(base map[string]any, patch map[string]any) map[string]any {
	if len(base) == 0 {
		base = map[string]any{}
	}
	for key, value := range patch {
		base[key] = value
	}
	return base
}

func (s *Service) audit(ctx context.Context, tenantID contracts.TenantID, actorID string, action string, resourceType string, resourceID string, decision string, reason string, traceID contracts.TraceID) {
	if s.Audit == nil {
		return
	}
	if actorID == "" {
		actorID = "system"
	}
	_ = s.Audit.Log(ctx, contracts.AuditEvent{
		AuditID:      idgen.New("audit"),
		TenantID:     tenantID,
		ActorID:      actorID,
		ActorType:    "governance",
		Action:       action,
		ResourceType: resourceType,
		ResourceID:   resourceID,
		Decision:     decision,
		Reason:       reason,
		TraceID:      traceID,
		CreatedAt:    s.now(),
	})
}

func (s *Service) traceEvent(ctx context.Context, run contracts.GovernanceProcessRun, eventType string, payload map[string]any) {
	if s.Trace == nil || run.TraceID == "" {
		return
	}
	if payload == nil {
		payload = map[string]any{}
	}
	payload["process_run_id"] = run.RunID
	_ = s.Trace.Record(ctx, contracts.TraceEvent{
		TraceID:   run.TraceID,
		TenantID:  run.TenantID,
		SpanID:    contracts.SpanID(idgen.New("span")),
		RunID:     run.AgentRunID,
		TaskID:    run.TaskID,
		Type:      eventType,
		Payload:   payload,
		CreatedAt: s.now(),
	})
}

func (s *Service) now() time.Time {
	if s.Now != nil {
		return s.Now().UTC()
	}
	return time.Now().UTC()
}
