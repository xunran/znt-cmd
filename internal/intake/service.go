package intake

import (
	"context"
	"regexp"
	"sort"
	"strings"
	"sync"
	"time"

	"znt/internal/contracts"
	"znt/internal/governance/audit"
	"znt/internal/governance/trace"
	"znt/pkg/idgen"
)

const (
	StatusDraft    = "draft"
	StatusEnabled  = "enabled"
	StatusDisabled = "disabled"

	MatchAlways   = "always"
	MatchExact    = "exact"
	MatchPrefix   = "prefix"
	MatchContains = "contains"
	MatchRegex    = "regex"

	ReplyStatusUpdate   = "status_update"
	ReplyAcknowledgment = "acknowledgement"
	ReplyPolicyNotice   = "policy_notice"

	DispatchExternalChannel = "external_channel"
)

type Policy struct {
	TenantID      contracts.TenantID     `json:"tenant_id,omitempty"`
	PolicyID      string                 `json:"policy_id"`
	Name          string                 `json:"name"`
	Status        string                 `json:"status"`
	Priority      int                    `json:"priority,omitempty"`
	AgentID       contracts.AgentID      `json:"agent_id,omitempty"`
	AgentVersion  contracts.AgentVersion `json:"agent_version,omitempty"`
	Channel       string                 `json:"channel,omitempty"`
	MatchType     string                 `json:"match_type"`
	Pattern       string                 `json:"pattern,omitempty"`
	ReplyText     string                 `json:"reply_text"`
	ReplyKind     string                 `json:"reply_kind"`
	ContinueToRun bool                   `json:"continue_to_run"`
	Version       string                 `json:"version,omitempty"`
	CreatedAt     time.Time              `json:"created_at"`
	UpdatedAt     time.Time              `json:"updated_at"`
}

type EvaluateRequest struct {
	TenantID     contracts.TenantID     `json:"tenant_id,omitempty"`
	TraceID      contracts.TraceID      `json:"trace_id,omitempty"`
	RunID        contracts.AgentRunID   `json:"run_id,omitempty"`
	TaskID       contracts.TaskID       `json:"task_id,omitempty"`
	AgentID      contracts.AgentID      `json:"agent_id,omitempty"`
	AgentVersion contracts.AgentVersion `json:"agent_version,omitempty"`
	Channel      string                 `json:"channel,omitempty"`
	Input        string                 `json:"input"`
	CallerID     string                 `json:"caller_id,omitempty"`
	CallerType   string                 `json:"caller_type,omitempty"`
}

type EvaluateResult struct {
	Matched       bool           `json:"matched"`
	PolicyID      string         `json:"policy_id,omitempty"`
	ReplyText     string         `json:"reply_text,omitempty"`
	ReplyKind     string         `json:"reply_kind,omitempty"`
	ContinueToRun bool           `json:"continue_to_run"`
	Dispatch      string         `json:"dispatch"`
	Metadata      map[string]any `json:"metadata,omitempty"`
	Policy        *Policy        `json:"policy,omitempty"`
}

type Store interface {
	Upsert(ctx context.Context, policy Policy) (Policy, error)
	List(ctx context.Context, tenantID contracts.TenantID) ([]Policy, error)
	Get(ctx context.Context, tenantID contracts.TenantID, policyID string) (Policy, bool, error)
	Delete(ctx context.Context, tenantID contracts.TenantID, policyID string) (Policy, bool, error)
}

type InMemoryStore struct {
	mu       sync.Mutex
	policies map[string]Policy
}

func NewInMemoryStore() *InMemoryStore {
	return &InMemoryStore{policies: map[string]Policy{}}
}

func (s *InMemoryStore) Upsert(_ context.Context, policy Policy) (Policy, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.policies[policyKey(policy.TenantID, policy.PolicyID)] = policy
	return policy, nil
}

func (s *InMemoryStore) List(_ context.Context, tenantID contracts.TenantID) ([]Policy, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := make([]Policy, 0, len(s.policies))
	for _, policy := range s.policies {
		if policy.TenantID == tenantID || policy.TenantID == "" {
			out = append(out, policy)
		}
	}
	sortPolicies(out)
	return out, nil
}

func (s *InMemoryStore) Get(_ context.Context, tenantID contracts.TenantID, policyID string) (Policy, bool, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	policy, ok := s.policies[policyKey(tenantID, policyID)]
	if !ok && tenantID != "" {
		policy, ok = s.policies[policyKey("", policyID)]
	}
	return policy, ok, nil
}

func (s *InMemoryStore) Delete(_ context.Context, tenantID contracts.TenantID, policyID string) (Policy, bool, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	key := policyKey(tenantID, policyID)
	policy, ok := s.policies[key]
	if !ok {
		return Policy{}, false, nil
	}
	delete(s.policies, key)
	return policy, true, nil
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

func (s *Service) Upsert(ctx context.Context, policy Policy, actorID string, actorType string) (Policy, error) {
	policy = normalizePolicy(policy, s.now())
	if err := validatePolicy(policy); err != nil {
		return Policy{}, err
	}
	saved, err := s.Store.Upsert(ctx, policy)
	if err != nil {
		return Policy{}, err
	}
	s.audit(ctx, saved.TenantID, actorID, actorType, contracts.AuditIntakePolicyUpserted, saved.PolicyID, "allowed", "")
	return saved, nil
}

func (s *Service) List(ctx context.Context, tenantID contracts.TenantID) ([]Policy, error) {
	return s.Store.List(ctx, tenantID)
}

func (s *Service) Get(ctx context.Context, tenantID contracts.TenantID, policyID string) (Policy, bool, error) {
	return s.Store.Get(ctx, tenantID, strings.TrimSpace(policyID))
}

func (s *Service) Delete(ctx context.Context, tenantID contracts.TenantID, policyID string, actorID string, actorType string) (Policy, bool, error) {
	policy, ok, err := s.Store.Delete(ctx, tenantID, strings.TrimSpace(policyID))
	if err != nil || !ok {
		return policy, ok, err
	}
	s.audit(ctx, policy.TenantID, actorID, actorType, contracts.AuditIntakePolicyDeleted, policy.PolicyID, "allowed", "")
	return policy, true, nil
}

func (s *Service) Evaluate(ctx context.Context, req EvaluateRequest) (EvaluateResult, error) {
	policies, err := s.Store.List(ctx, req.TenantID)
	if err != nil {
		return EvaluateResult{}, err
	}
	sortPolicies(policies)
	result := EvaluateResult{
		ContinueToRun: true,
		Dispatch:      DispatchExternalChannel,
	}
	for _, policy := range policies {
		if !policyApplies(policy, req) {
			continue
		}
		matched, err := matchPolicy(policy, req.Input)
		if err != nil {
			return EvaluateResult{}, err
		}
		if !matched {
			continue
		}
		policyCopy := policy
		result = EvaluateResult{
			Matched:       true,
			PolicyID:      policy.PolicyID,
			ReplyText:     policy.ReplyText,
			ReplyKind:     policy.ReplyKind,
			ContinueToRun: policy.ContinueToRun,
			Dispatch:      DispatchExternalChannel,
			Metadata: map[string]any{
				"agent_id":      policy.AgentID,
				"agent_version": policy.AgentVersion,
				"channel":       policy.Channel,
				"match_type":    policy.MatchType,
			},
			Policy: &policyCopy,
		}
		break
	}
	s.traceEvaluation(ctx, req, result)
	return result, nil
}

func normalizePolicy(policy Policy, now time.Time) Policy {
	policy.PolicyID = strings.TrimSpace(policy.PolicyID)
	if policy.PolicyID == "" {
		policy.PolicyID = idgen.New("intakepol")
	}
	policy.Name = strings.TrimSpace(policy.Name)
	if policy.Name == "" {
		policy.Name = policy.PolicyID
	}
	policy.Status = strings.TrimSpace(policy.Status)
	if policy.Status == "" {
		policy.Status = StatusEnabled
	}
	policy.Channel = strings.TrimSpace(policy.Channel)
	policy.MatchType = strings.TrimSpace(policy.MatchType)
	if policy.MatchType == "" {
		policy.MatchType = MatchContains
	}
	policy.Pattern = strings.TrimSpace(policy.Pattern)
	policy.ReplyText = strings.TrimSpace(policy.ReplyText)
	policy.ReplyKind = strings.TrimSpace(policy.ReplyKind)
	if policy.ReplyKind == "" {
		policy.ReplyKind = ReplyAcknowledgment
	}
	if policy.Version == "" {
		policy.Version = "v1"
	}
	if policy.CreatedAt.IsZero() {
		policy.CreatedAt = now
	}
	policy.UpdatedAt = now
	return policy
}

func validatePolicy(policy Policy) error {
	switch policy.Status {
	case StatusDraft, StatusEnabled, StatusDisabled:
	default:
		return contracts.NewRuntimeError(contracts.CodeDecisionSchemaError, "unsupported intake policy status", map[string]any{"status": policy.Status})
	}
	switch policy.MatchType {
	case MatchAlways, MatchExact, MatchPrefix, MatchContains, MatchRegex:
	default:
		return contracts.NewRuntimeError(contracts.CodeDecisionSchemaError, "unsupported intake match_type", map[string]any{"match_type": policy.MatchType})
	}
	if policy.MatchType != MatchAlways && policy.Pattern == "" {
		return contracts.NewRuntimeError(contracts.CodeDecisionSchemaError, "intake policy pattern is required", nil)
	}
	if policy.MatchType == MatchRegex {
		if _, err := regexp.Compile(policy.Pattern); err != nil {
			return contracts.NewRuntimeError(contracts.CodeDecisionSchemaError, "invalid intake regex pattern", map[string]any{"error": err.Error()})
		}
	}
	if policy.ReplyText == "" {
		return contracts.NewRuntimeError(contracts.CodeDecisionSchemaError, "intake policy reply_text is required", nil)
	}
	switch policy.ReplyKind {
	case ReplyStatusUpdate, ReplyAcknowledgment, ReplyPolicyNotice:
	default:
		return contracts.NewRuntimeError(contracts.CodeDecisionSchemaError, "unsupported intake reply_kind", map[string]any{"reply_kind": policy.ReplyKind})
	}
	return nil
}

func policyApplies(policy Policy, req EvaluateRequest) bool {
	if policy.Status != StatusEnabled {
		return false
	}
	if policy.TenantID != "" && policy.TenantID != req.TenantID {
		return false
	}
	if policy.AgentID != "" && policy.AgentID != req.AgentID {
		return false
	}
	if policy.AgentVersion != "" && policy.AgentVersion != req.AgentVersion {
		return false
	}
	if policy.Channel != "" && !strings.EqualFold(policy.Channel, req.Channel) {
		return false
	}
	return true
}

func matchPolicy(policy Policy, input string) (bool, error) {
	switch policy.MatchType {
	case MatchAlways:
		return true, nil
	case MatchExact:
		return strings.EqualFold(strings.TrimSpace(input), strings.TrimSpace(policy.Pattern)), nil
	case MatchPrefix:
		return strings.HasPrefix(strings.ToLower(strings.TrimSpace(input)), strings.ToLower(policy.Pattern)), nil
	case MatchContains:
		return strings.Contains(strings.ToLower(input), strings.ToLower(policy.Pattern)), nil
	case MatchRegex:
		compiled, err := regexp.Compile(policy.Pattern)
		if err != nil {
			return false, contracts.NewRuntimeError(contracts.CodeDecisionSchemaError, "invalid intake regex pattern", map[string]any{"error": err.Error()})
		}
		return compiled.MatchString(input), nil
	default:
		return false, contracts.NewRuntimeError(contracts.CodeDecisionSchemaError, "unsupported intake match_type", map[string]any{"match_type": policy.MatchType})
	}
}

func sortPolicies(policies []Policy) {
	sort.SliceStable(policies, func(i, j int) bool {
		if policies[i].Priority != policies[j].Priority {
			return policies[i].Priority > policies[j].Priority
		}
		if !policies[i].UpdatedAt.Equal(policies[j].UpdatedAt) {
			return policies[i].UpdatedAt.After(policies[j].UpdatedAt)
		}
		return policies[i].PolicyID < policies[j].PolicyID
	})
}

func (s *Service) traceEvaluation(ctx context.Context, req EvaluateRequest, result EvaluateResult) {
	if s.Trace == nil || req.TraceID == "" {
		return
	}
	_ = s.Trace.Record(ctx, contracts.TraceEvent{
		TraceID:  req.TraceID,
		TenantID: req.TenantID,
		SpanID:   contracts.SpanID(idgen.New("span")),
		RunID:    req.RunID,
		TaskID:   req.TaskID,
		Type:     contracts.TraceIntakePreReplyEvaluated,
		Payload: map[string]any{
			"matched":         result.Matched,
			"policy_id":       result.PolicyID,
			"reply_kind":      result.ReplyKind,
			"continue_to_run": result.ContinueToRun,
			"dispatch":        result.Dispatch,
			"agent_id":        req.AgentID,
			"agent_version":   req.AgentVersion,
			"channel":         req.Channel,
		},
		CreatedAt: s.now(),
	})
}

func (s *Service) audit(ctx context.Context, tenantID contracts.TenantID, actorID string, actorType string, action string, resourceID string, decision string, reason string) {
	if s.Audit == nil {
		return
	}
	if actorID == "" {
		actorID = "clean-core"
	}
	if actorType == "" {
		actorType = "system"
	}
	_ = s.Audit.Log(ctx, contracts.AuditEvent{
		AuditID:      idgen.New("audit"),
		TenantID:     tenantID,
		ActorID:      actorID,
		ActorType:    actorType,
		Action:       action,
		ResourceType: "intake_policy",
		ResourceID:   resourceID,
		Decision:     decision,
		Reason:       reason,
		CreatedAt:    s.now(),
	})
}

func (s *Service) now() time.Time {
	if s.Now != nil {
		return s.Now().UTC()
	}
	return time.Now().UTC()
}

func policyKey(tenantID contracts.TenantID, policyID string) string {
	return string(tenantID) + "\x00" + strings.TrimSpace(policyID)
}
