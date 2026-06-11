package crossgroup

import (
	"context"
	"fmt"
	"regexp"
	"sort"
	"strings"
	"sync"
	"time"

	"znt/internal/contracts"
	"znt/internal/governance/audit"
	"znt/internal/governance/trace"
	"znt/internal/knowledge"
	"znt/pkg/idgen"
)

type PermissionChecker interface {
	Check(ctx context.Context, input contracts.PermissionCheckInput) (contracts.PermissionDecision, error)
}

type Store interface {
	SaveSharePolicy(ctx context.Context, policy contracts.CrossGroupSharePolicy) error
	GetSharePolicy(ctx context.Context, tenantID contracts.TenantID, policyID contracts.CrossGroupSharePolicyID) (contracts.CrossGroupSharePolicy, bool, error)
	ListSharePolicies(ctx context.Context, tenantID contracts.TenantID, sourceGroupID contracts.GroupID, targetGroupID contracts.GroupID) ([]contracts.CrossGroupSharePolicy, error)
}

type Service struct {
	Knowledge   knowledge.Service
	Permissions PermissionChecker
	Store       Store
	mu          sync.RWMutex
	Policies    map[contracts.CrossGroupSharePolicyID]contracts.CrossGroupSharePolicy
	Audit       audit.Logger
	Trace       trace.Recorder
	Now         func() time.Time
}

type SearchInput struct {
	TenantID       contracts.TenantID
	RequestGroupID contracts.GroupID
	SourceGroupID  contracts.GroupID
	RequestedBy    string
	Roles          []string
	Query          string
	Limit          int
	TraceID        contracts.TraceID
	TaskID         contracts.TaskID
	RunID          contracts.AgentRunID
}

func NewService(knowledgeService knowledge.Service, permissions PermissionChecker, auditLogger audit.Logger, traceRecorder trace.Recorder) *Service {
	return NewServiceWithStore(knowledgeService, permissions, nil, auditLogger, traceRecorder)
}

func NewServiceWithStore(knowledgeService knowledge.Service, permissions PermissionChecker, store Store, auditLogger audit.Logger, traceRecorder trace.Recorder) *Service {
	return &Service{
		Knowledge:   knowledgeService,
		Permissions: permissions,
		Store:       store,
		Policies:    map[contracts.CrossGroupSharePolicyID]contracts.CrossGroupSharePolicy{},
		Audit:       auditLogger,
		Trace:       traceRecorder,
		Now:         func() time.Time { return time.Now().UTC() },
	}
}

func (s *Service) UpsertSharePolicy(ctx context.Context, policy contracts.CrossGroupSharePolicy) (contracts.CrossGroupSharePolicy, error) {
	if policy.TenantID == "" || policy.SourceGroupID == "" || policy.TargetGroupID == "" {
		return contracts.CrossGroupSharePolicy{}, fmt.Errorf("tenant_id, source_group_id and target_group_id are required")
	}
	if policy.PolicyID == "" {
		policy.PolicyID = contracts.CrossGroupSharePolicyID(idgen.New("cgshare"))
	}
	if policy.RedactionPolicy == "" {
		policy.RedactionPolicy = contracts.RedactionPolicySummaryOnly
	}
	if policy.Status == "" {
		policy.Status = contracts.CrossGroupShareEnabled
	}
	now := s.now()
	if policy.CreatedAt.IsZero() {
		policy.CreatedAt = now
	}
	policy.UpdatedAt = now
	if s.Store != nil {
		if err := s.Store.SaveSharePolicy(ctx, policy); err != nil {
			return contracts.CrossGroupSharePolicy{}, err
		}
	}
	if s.Policies == nil {
		s.Policies = map[contracts.CrossGroupSharePolicyID]contracts.CrossGroupSharePolicy{}
	}
	s.mu.Lock()
	s.Policies[policy.PolicyID] = clonePolicy(policy)
	s.mu.Unlock()
	s.auditPolicy(ctx, policy, "allowed", "policy_upsert")
	return policy, nil
}

func (s *Service) GetSharePolicy(ctx context.Context, tenantID contracts.TenantID, policyID contracts.CrossGroupSharePolicyID) (contracts.CrossGroupSharePolicy, bool, error) {
	if s.Store != nil {
		return s.Store.GetSharePolicy(ctx, tenantID, policyID)
	}
	s.mu.RLock()
	defer s.mu.RUnlock()
	policy, ok := s.Policies[policyID]
	if !ok || policy.TenantID != tenantID {
		return contracts.CrossGroupSharePolicy{}, false, nil
	}
	return clonePolicy(policy), true, nil
}

func (s *Service) ListSharePolicies(ctx context.Context, tenantID contracts.TenantID, sourceGroupID contracts.GroupID, targetGroupID contracts.GroupID) ([]contracts.CrossGroupSharePolicy, error) {
	if s.Store != nil {
		return s.Store.ListSharePolicies(ctx, tenantID, sourceGroupID, targetGroupID)
	}
	s.mu.RLock()
	defer s.mu.RUnlock()
	out := make([]contracts.CrossGroupSharePolicy, 0, len(s.Policies))
	for _, policy := range s.Policies {
		if policy.TenantID != tenantID {
			continue
		}
		if sourceGroupID != "" && policy.SourceGroupID != sourceGroupID {
			continue
		}
		if targetGroupID != "" && policy.TargetGroupID != targetGroupID {
			continue
		}
		out = append(out, clonePolicy(policy))
	}
	sort.SliceStable(out, func(i, j int) bool {
		if out[i].UpdatedAt.Equal(out[j].UpdatedAt) {
			return out[i].PolicyID < out[j].PolicyID
		}
		return out[i].UpdatedAt.After(out[j].UpdatedAt)
	})
	return out, nil
}

func (s *Service) Search(ctx context.Context, input SearchInput) ([]contracts.KnowledgeSearchResult, error) {
	if input.TenantID == "" || input.RequestGroupID == "" || input.SourceGroupID == "" || input.Query == "" {
		return nil, fmt.Errorf("tenant_id, request_group_id, source_group_id and query are required")
	}
	s.traceEvent(ctx, input, contracts.TraceCrossGroupSearchRequested, map[string]any{"query": input.Query})
	if s.Permissions != nil {
		decision, err := s.Permissions.Check(ctx, contracts.PermissionCheckInput{
			TenantID:      input.TenantID,
			GroupID:       input.RequestGroupID,
			ActorID:       input.RequestedBy,
			ActorType:     "member",
			Roles:         input.Roles,
			Action:        contracts.PermissionActionCrossGroupSearch,
			ResourceType:  "group",
			ResourceID:    string(input.SourceGroupID),
			ResourceScope: string(input.SourceGroupID),
			TraceID:       input.TraceID,
			TaskID:        input.TaskID,
			RunID:         input.RunID,
		})
		if err != nil {
			return nil, err
		}
		if decision.Decision != contracts.PermissionDecisionAllowed {
			s.traceEvent(ctx, input, contracts.TraceCrossGroupSearchDenied, map[string]any{"decision": decision.Decision, "reason_code": decision.ReasonCode})
			s.auditEvent(ctx, input, decision.Decision, decision.ReasonCode, 0)
			return nil, contracts.NewRuntimeError(contracts.CodeToolPolicyDenied, decision.Reason, map[string]any{"reason_code": decision.ReasonCode})
		}
	}
	if s.Knowledge == nil {
		return nil, fmt.Errorf("knowledge service is not configured")
	}
	policies, err := s.matchingPolicies(ctx, input)
	if err != nil {
		return nil, err
	}
	if len(policies) == 0 {
		s.traceEvent(ctx, input, contracts.TraceCrossGroupSearchDenied, map[string]any{"decision": "denied", "reason_code": "no_share_policy"})
		s.auditEvent(ctx, input, "denied", "no_share_policy", 0)
		return nil, contracts.NewRuntimeError(contracts.CodeToolPolicyDenied, "no enabled cross-group share policy", map[string]any{"reason_code": "no_share_policy"})
	}
	results, err := s.Knowledge.Search(ctx, knowledge.SearchInput{
		TenantID:         input.TenantID,
		RequesterGroupID: input.RequestGroupID,
		RequestedBy:      input.RequestedBy,
		Roles:            input.Roles,
		Query:            input.Query,
		KnowledgeBaseIDs: allowedKnowledgeBaseIDs(policies),
		SourceGroupID:    input.SourceGroupID,
		Limit:            input.Limit,
		AllowCrossGroup:  true,
		TraceID:          input.TraceID,
		TaskID:           input.TaskID,
		RunID:            input.RunID,
	})
	if err != nil {
		return nil, err
	}
	redactionPolicy := strongestRedactionPolicy(policies)
	results = redactResults(results, redactionPolicy)
	s.traceEvent(ctx, input, contracts.TraceCrossGroupSearchCompleted, map[string]any{"result_count": len(results), "share_policy_ids": sharePolicyIDs(policies), "redaction_policy": redactionPolicy})
	s.auditEvent(ctx, input, "allowed", fmt.Sprintf("results=%d policies=%s", len(results), strings.Join(sharePolicyIDs(policies), ",")), len(results))
	return results, nil
}

func (s *Service) matchingPolicies(ctx context.Context, input SearchInput) ([]contracts.CrossGroupSharePolicy, error) {
	policies, err := s.ListSharePolicies(ctx, input.TenantID, input.SourceGroupID, input.RequestGroupID)
	if err != nil {
		return nil, err
	}
	out := make([]contracts.CrossGroupSharePolicy, 0, len(policies))
	for _, policy := range policies {
		if policy.Status != contracts.CrossGroupShareEnabled {
			continue
		}
		out = append(out, policy)
	}
	return out, nil
}

func (s *Service) traceEvent(ctx context.Context, input SearchInput, eventType string, payload map[string]any) {
	if s.Trace == nil || input.TraceID == "" {
		return
	}
	if payload == nil {
		payload = map[string]any{}
	}
	payload["request_group_id"] = input.RequestGroupID
	payload["source_group_id"] = input.SourceGroupID
	_ = s.Trace.Record(ctx, contracts.TraceEvent{
		TraceID:   input.TraceID,
		TenantID:  input.TenantID,
		SpanID:    contracts.SpanID(idgen.New("span")),
		RunID:     input.RunID,
		TaskID:    input.TaskID,
		Type:      eventType,
		Payload:   payload,
		CreatedAt: s.Now(),
	})
}

func (s *Service) auditEvent(ctx context.Context, input SearchInput, decision string, reason string, resultCount int) {
	if s.Audit == nil {
		return
	}
	_ = s.Audit.Log(ctx, contracts.AuditEvent{
		AuditID:      idgen.New("audit"),
		TenantID:     input.TenantID,
		ActorID:      input.RequestedBy,
		ActorType:    "member",
		Action:       contracts.AuditCrossGroupSearch,
		ResourceType: "group",
		ResourceID:   string(input.SourceGroupID),
		Decision:     decision,
		Reason:       reason,
		TraceID:      input.TraceID,
		TaskID:       input.TaskID,
		RunID:        input.RunID,
		CreatedAt:    s.Now(),
	})
	_ = resultCount
}

func (s *Service) auditPolicy(ctx context.Context, policy contracts.CrossGroupSharePolicy, decision string, reason string) {
	if s.Audit == nil {
		return
	}
	_ = s.Audit.Log(ctx, contracts.AuditEvent{
		AuditID:      idgen.New("audit"),
		TenantID:     policy.TenantID,
		ActorID:      policy.CreatedBy,
		ActorType:    "member",
		Action:       contracts.AuditCrossGroupSearch,
		ResourceType: "cross_group_share_policy",
		ResourceID:   string(policy.PolicyID),
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

func allowedKnowledgeBaseIDs(policies []contracts.CrossGroupSharePolicy) []contracts.KnowledgeBaseID {
	seen := map[contracts.KnowledgeBaseID]struct{}{}
	out := make([]contracts.KnowledgeBaseID, 0)
	for _, policy := range policies {
		if len(policy.KnowledgeBaseIDs) == 0 {
			return nil
		}
		for _, id := range policy.KnowledgeBaseIDs {
			if id == "" {
				continue
			}
			if _, ok := seen[id]; ok {
				continue
			}
			seen[id] = struct{}{}
			out = append(out, id)
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i] < out[j] })
	return out
}

func sharePolicyIDs(policies []contracts.CrossGroupSharePolicy) []string {
	out := make([]string, 0, len(policies))
	for _, policy := range policies {
		out = append(out, string(policy.PolicyID))
	}
	sort.Strings(out)
	return out
}

func strongestRedactionPolicy(policies []contracts.CrossGroupSharePolicy) string {
	priority := map[string]int{
		contracts.RedactionPolicySummaryOnly: 1,
		contracts.RedactionPolicyMaskEmails:  2,
		contracts.RedactionPolicyMaskNumbers: 2,
		contracts.RedactionPolicyStrict:      3,
	}
	best := contracts.RedactionPolicySummaryOnly
	for _, policy := range policies {
		if priority[policy.RedactionPolicy] > priority[best] {
			best = policy.RedactionPolicy
		}
	}
	return best
}

func redactResults(results []contracts.KnowledgeSearchResult, policy string) []contracts.KnowledgeSearchResult {
	out := make([]contracts.KnowledgeSearchResult, 0, len(results))
	for _, result := range results {
		result.Redacted = true
		result.RedactionPolicy = policy
		switch policy {
		case contracts.RedactionPolicyStrict:
			result.Title = "redacted"
			result.Snippet = "[redacted shared knowledge]"
			result.SourceURI = ""
		case contracts.RedactionPolicyMaskEmails:
			result.Title = maskEmails(result.Title)
			result.Snippet = truncate(maskEmails(result.Snippet), 180)
		case contracts.RedactionPolicyMaskNumbers:
			result.Title = maskNumbers(result.Title)
			result.Snippet = truncate(maskNumbers(result.Snippet), 180)
		default:
			result.Snippet = truncate(result.Snippet, 180)
			result.SourceURI = ""
		}
		out = append(out, result)
	}
	return out
}

var emailPattern = regexp.MustCompile(`[A-Za-z0-9._%+\-]+@[A-Za-z0-9.\-]+\.[A-Za-z]{2,}`)
var numberPattern = regexp.MustCompile(`\b\d{3,}\b`)

func maskEmails(value string) string {
	return emailPattern.ReplaceAllString(value, "[redacted-email]")
}

func maskNumbers(value string) string {
	return numberPattern.ReplaceAllString(value, "[redacted-number]")
}

func truncate(value string, maxRunes int) string {
	if maxRunes <= 0 || len([]rune(value)) <= maxRunes {
		return value
	}
	return string([]rune(value)[:maxRunes])
}

func clonePolicy(policy contracts.CrossGroupSharePolicy) contracts.CrossGroupSharePolicy {
	policy.KnowledgeBaseIDs = append([]contracts.KnowledgeBaseID(nil), policy.KnowledgeBaseIDs...)
	return policy
}
