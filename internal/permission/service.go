package permission

import (
	"context"
	"sort"
	"sync"
	"time"

	"znt/internal/contracts"
	"znt/internal/governance/audit"
	"znt/internal/governance/trace"
	"znt/pkg/idgen"
)

type Service interface {
	Check(ctx context.Context, input contracts.PermissionCheckInput) (contracts.PermissionDecision, error)
	UpsertPolicy(ctx context.Context, policy contracts.GroupPermissionPolicy) error
}

type Store interface {
	SavePolicy(ctx context.Context, policy contracts.GroupPermissionPolicy) error
	ListPolicies(ctx context.Context, tenantID contracts.TenantID, groupID contracts.GroupID) ([]contracts.GroupPermissionPolicy, error)
}

type InMemoryService struct {
	mu         sync.RWMutex
	policies   []contracts.GroupPermissionPolicy
	store      Store
	adminRoles map[string]struct{}
	audit      audit.Logger
	trace      trace.Recorder
	now        func() time.Time
}

func NewInMemoryService(auditLogger audit.Logger, traceRecorder trace.Recorder) *InMemoryService {
	return NewInMemoryServiceWithStore(nil, auditLogger, traceRecorder)
}

func NewInMemoryServiceWithStore(store Store, auditLogger audit.Logger, traceRecorder trace.Recorder) *InMemoryService {
	return &InMemoryService{
		store: store,
		adminRoles: map[string]struct{}{
			"owner":       {},
			"admin":       {},
			"group_admin": {},
			"agent_admin": {},
		},
		audit: auditLogger,
		trace: traceRecorder,
		now:   func() time.Time { return time.Now().UTC() },
	}
}

func (s *InMemoryService) UpsertPolicy(ctx context.Context, policy contracts.GroupPermissionPolicy) error {
	now := s.now()
	if policy.CreatedAt.IsZero() {
		policy.CreatedAt = now
	}
	policy.UpdatedAt = now
	s.mu.Lock()
	defer s.mu.Unlock()
	for i, existing := range s.policies {
		if existing.TenantID == policy.TenantID &&
			existing.GroupID == policy.GroupID &&
			existing.SubjectType == policy.SubjectType &&
			existing.SubjectID == policy.SubjectID {
			s.policies[i] = clonePolicy(policy)
			return nil
		}
	}
	s.policies = append(s.policies, clonePolicy(policy))
	sortPolicies(s.policies)
	if s.store != nil {
		return s.store.SavePolicy(ctx, policy)
	}
	return nil
}

func (s *InMemoryService) Check(ctx context.Context, input contracts.PermissionCheckInput) (contracts.PermissionDecision, error) {
	decision, err := s.evaluate(ctx, input)
	if err != nil {
		return contracts.PermissionDecision{}, err
	}
	s.record(ctx, input, decision)
	return decision, nil
}

func (s *InMemoryService) evaluate(ctx context.Context, input contracts.PermissionCheckInput) (contracts.PermissionDecision, error) {
	if input.Action == "" {
		return s.decision(contracts.PermissionDecisionDenied, "missing action", "missing_action", false, nil), nil
	}
	roles := uniqueStrings(input.Roles)
	if input.Member != nil {
		roles = uniqueStrings(append(roles, input.Member.Roles...))
	}
	if !requiresExplicitPolicy(input.Action) {
		for _, role := range roles {
			if _, ok := s.adminRoles[role]; ok {
				return s.decision(contracts.PermissionDecisionAllowed, "admin role allows action", "admin_role", false, []string{"role:" + role}), nil
			}
		}
	}

	policies, err := s.policiesFor(ctx, input.TenantID, input.GroupID)
	if err != nil {
		return contracts.PermissionDecision{}, err
	}
	for _, policy := range policies {
		if !policyMatches(input, roles, policy) {
			continue
		}
		policyID := policy.SubjectType + ":" + policy.SubjectID
		if policy.RequiresApproval {
			return s.decision(contracts.PermissionDecisionApprovalRequired, firstNonEmpty(policy.Reason, "policy requires approval"), "approval_required", true, []string{policyID}), nil
		}
		return s.decision(contracts.PermissionDecisionAllowed, firstNonEmpty(policy.Reason, "policy allows action"), "policy_allowed", false, []string{policyID}), nil
	}
	return s.decision(contracts.PermissionDecisionDenied, "no matching permission policy", "no_matching_policy", false, nil), nil
}

func (s *InMemoryService) policiesFor(ctx context.Context, tenantID contracts.TenantID, groupID contracts.GroupID) ([]contracts.GroupPermissionPolicy, error) {
	if s.store != nil {
		policies, err := s.store.ListPolicies(ctx, tenantID, groupID)
		if err != nil || len(policies) > 0 {
			return policies, err
		}
	}
	s.mu.RLock()
	defer s.mu.RUnlock()
	policies := make([]contracts.GroupPermissionPolicy, len(s.policies))
	copy(policies, s.policies)
	return policies, nil
}

func requiresExplicitPolicy(action string) bool {
	switch action {
	case contracts.PermissionActionCrossGroupSearch:
		return true
	default:
		return false
	}
}

func (s *InMemoryService) decision(decision string, reason string, reasonCode string, requiresApproval bool, policyIDs []string) contracts.PermissionDecision {
	return contracts.PermissionDecision{
		Decision:         decision,
		Reason:           reason,
		ReasonCode:       reasonCode,
		RequiresApproval: requiresApproval,
		AppliedPolicyIDs: append([]string(nil), policyIDs...),
		CheckedAt:        s.now(),
	}
}

func policyMatches(input contracts.PermissionCheckInput, roles []string, policy contracts.GroupPermissionPolicy) bool {
	if policy.TenantID != "" && policy.TenantID != input.TenantID {
		return false
	}
	if policy.GroupID != "" && policy.GroupID != input.GroupID {
		return false
	}
	if !containsAction(policy.Actions, input.Action) {
		return false
	}
	if !scopeMatches(policy.ResourceScopes, input) {
		return false
	}
	switch policy.SubjectType {
	case contracts.PermissionSubjectUser:
		if policy.SubjectID == input.ActorID {
			return true
		}
		if input.Member != nil {
			return policy.SubjectID == string(input.Member.MemberID) || policy.SubjectID == input.Member.ExternalUserID
		}
	case contracts.PermissionSubjectRole:
		return containsString(roles, policy.SubjectID)
	case contracts.PermissionSubjectAgent:
		return policy.SubjectID == input.ActorID
	}
	return false
}

func scopeMatches(scopes []string, input contracts.PermissionCheckInput) bool {
	if len(scopes) == 0 {
		return true
	}
	for _, scope := range scopes {
		switch scope {
		case "*":
			return true
		case input.ResourceScope, input.ResourceID, input.ResourceType, string(input.GroupID):
			return true
		}
	}
	return false
}

func containsAction(actions []string, action string) bool {
	for _, candidate := range actions {
		if candidate == "*" || candidate == action {
			return true
		}
	}
	return false
}

func containsString(values []string, target string) bool {
	for _, value := range values {
		if value == target {
			return true
		}
	}
	return false
}

func uniqueStrings(values []string) []string {
	seen := map[string]struct{}{}
	out := make([]string, 0, len(values))
	for _, value := range values {
		if value == "" {
			continue
		}
		if _, ok := seen[value]; ok {
			continue
		}
		seen[value] = struct{}{}
		out = append(out, value)
	}
	sort.Strings(out)
	return out
}

func firstNonEmpty(values ...string) string {
	for _, value := range values {
		if value != "" {
			return value
		}
	}
	return ""
}

func sortPolicies(policies []contracts.GroupPermissionPolicy) {
	sort.SliceStable(policies, func(i, j int) bool {
		a := policies[i]
		b := policies[j]
		if a.TenantID != b.TenantID {
			return a.TenantID < b.TenantID
		}
		if a.GroupID != b.GroupID {
			return a.GroupID < b.GroupID
		}
		if a.SubjectType != b.SubjectType {
			return a.SubjectType < b.SubjectType
		}
		return a.SubjectID < b.SubjectID
	})
}

func clonePolicy(policy contracts.GroupPermissionPolicy) contracts.GroupPermissionPolicy {
	policy.Actions = append([]string(nil), policy.Actions...)
	policy.ResourceScopes = append([]string(nil), policy.ResourceScopes...)
	return policy
}

func (s *InMemoryService) record(ctx context.Context, input contracts.PermissionCheckInput, decision contracts.PermissionDecision) {
	if s.trace != nil && input.TraceID != "" {
		_ = s.trace.Record(ctx, contracts.TraceEvent{
			TraceID:  input.TraceID,
			TenantID: input.TenantID,
			SpanID:   contracts.SpanID(idgen.New("span")),
			RunID:    input.RunID,
			TaskID:   input.TaskID,
			Type:     contracts.TracePermissionChecked,
			Payload: map[string]any{
				"group_id":    input.GroupID,
				"actor_id":    input.ActorID,
				"action":      input.Action,
				"resource_id": input.ResourceID,
				"decision":    decision.Decision,
				"reason_code": decision.ReasonCode,
			},
			CreatedAt: s.now(),
		})
	}
	if s.audit != nil {
		action := contracts.AuditPermissionChecked
		if decision.Decision == contracts.PermissionDecisionDenied {
			action = contracts.AuditPermissionDenied
		}
		_ = s.audit.Log(ctx, contracts.AuditEvent{
			AuditID:      idgen.New("audit"),
			TenantID:     input.TenantID,
			ActorID:      input.ActorID,
			ActorType:    firstNonEmpty(input.ActorType, "member"),
			Action:       action,
			ResourceType: firstNonEmpty(input.ResourceType, "permission"),
			ResourceID:   firstNonEmpty(input.ResourceID, input.Action),
			Decision:     decision.Decision,
			Reason:       decision.ReasonCode,
			TraceID:      input.TraceID,
			TaskID:       input.TaskID,
			RunID:        input.RunID,
			CreatedAt:    s.now(),
		})
	}
}
