package tone

import (
	"context"
	"strings"
	"sync"
	"time"

	"znt/internal/contracts"
	"znt/internal/governance/trace"
	"znt/pkg/idgen"
)

type Service struct {
	mu       sync.RWMutex
	policies map[string]contracts.TonePolicy
	store    Store
	trace    trace.Recorder
	now      func() time.Time
}

type Store interface {
	SavePolicy(ctx context.Context, policy contracts.TonePolicy) error
	GetPolicy(ctx context.Context, tenantID contracts.TenantID, groupID contracts.GroupID) (contracts.TonePolicy, bool, error)
}

type DecideInput struct {
	TenantID  contracts.TenantID
	GroupID   contracts.GroupID
	Signals   []string
	Addressee bool
	HighRisk  bool
	TraceID   contracts.TraceID
	TaskID    contracts.TaskID
	RunID     contracts.AgentRunID
}

func NewService(traceRecorder trace.Recorder) *Service {
	return NewServiceWithStore(nil, traceRecorder)
}

func NewServiceWithStore(store Store, traceRecorder trace.Recorder) *Service {
	return &Service{
		policies: map[string]contracts.TonePolicy{},
		store:    store,
		trace:    traceRecorder,
		now:      func() time.Time { return time.Now().UTC() },
	}
}

func (s *Service) UpsertPolicy(ctx context.Context, policy contracts.TonePolicy) {
	if policy.DefaultStyle == "" {
		policy.DefaultStyle = "concise_collaborative"
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	s.policies[key(policy.TenantID, policy.GroupID)] = clonePolicy(policy)
	if s.store != nil {
		_ = s.store.SavePolicy(ctx, policy)
	}
}

func (s *Service) Decide(ctx context.Context, input DecideInput) contracts.ToneDecision {
	policy := s.policy(input.TenantID, input.GroupID)
	style := policy.DefaultStyle
	if style == "" {
		style = "concise_collaborative"
	}
	signals := append([]string(nil), input.Signals...)
	if input.HighRisk {
		signals = append(signals, "high_risk_action")
	}
	if !input.Addressee {
		signals = append(signals, "human_to_human_discussion")
	}
	for _, signal := range signals {
		for _, rule := range policy.Rules {
			if rule.When == signal && rule.Style != "" {
				style = rule.Style
			}
		}
	}
	shouldReply := style != "silent" && input.Addressee
	if input.HighRisk && style == "formal_confirmation" {
		shouldReply = true
	}
	decision := contracts.ToneDecision{
		Style:       style,
		ShouldReply: shouldReply,
		Reasons:     unique(signals),
	}
	if s.trace != nil && input.TraceID != "" {
		_ = s.trace.Record(ctx, contracts.TraceEvent{
			TraceID:  input.TraceID,
			TenantID: input.TenantID,
			SpanID:   contracts.SpanID(idgen.New("span")),
			RunID:    input.RunID,
			TaskID:   input.TaskID,
			Type:     contracts.TraceTonePolicyApplied,
			Payload: map[string]any{
				"group_id":     input.GroupID,
				"style":        decision.Style,
				"should_reply": decision.ShouldReply,
				"signals":      decision.Reasons,
			},
			CreatedAt: s.now(),
		})
	}
	return decision
}

func (s *Service) policy(tenantID contracts.TenantID, groupID contracts.GroupID) contracts.TonePolicy {
	if s.store != nil {
		if policy, ok, err := s.store.GetPolicy(context.Background(), tenantID, groupID); err == nil && ok {
			return clonePolicy(policy)
		}
		if policy, ok, err := s.store.GetPolicy(context.Background(), tenantID, ""); err == nil && ok {
			return clonePolicy(policy)
		}
	}
	s.mu.RLock()
	defer s.mu.RUnlock()
	if policy, ok := s.policies[key(tenantID, groupID)]; ok {
		return clonePolicy(policy)
	}
	if policy, ok := s.policies[key(tenantID, "")]; ok {
		return clonePolicy(policy)
	}
	return contracts.TonePolicy{
		DefaultStyle: "concise_collaborative",
		Rules: []contracts.ToneRule{
			{When: "high_risk_action", Style: "formal_confirmation"},
			{When: "human_to_human_discussion", Style: "silent"},
			{When: "direct_question", Style: "clear_short_answer"},
		},
	}
}

func key(tenantID contracts.TenantID, groupID contracts.GroupID) string {
	return string(tenantID) + "\x00" + string(groupID)
}

func clonePolicy(policy contracts.TonePolicy) contracts.TonePolicy {
	policy.Rules = append([]contracts.ToneRule(nil), policy.Rules...)
	return policy
}

func unique(values []string) []string {
	seen := map[string]struct{}{}
	out := make([]string, 0, len(values))
	for _, value := range values {
		value = strings.TrimSpace(value)
		if value == "" {
			continue
		}
		if _, ok := seen[value]; ok {
			continue
		}
		seen[value] = struct{}{}
		out = append(out, value)
	}
	return out
}
