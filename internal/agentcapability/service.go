package agentcapability

import (
	"context"
	"fmt"
	"sort"
	"strings"
	"sync"
	"time"

	"znt/internal/contracts"
	"znt/internal/governance/trace"
	"znt/pkg/idgen"
)

type Service struct {
	mu           sync.RWMutex
	capabilities map[contracts.AgentCapabilityID]contracts.AgentCapability
	store        Store
	trace        trace.Recorder
	now          func() time.Time
}

type Store interface {
	Save(ctx context.Context, capability contracts.AgentCapability) error
	ListByTenant(ctx context.Context, tenantID contracts.TenantID) ([]contracts.AgentCapability, error)
}

func NewService(traceRecorder trace.Recorder) *Service {
	return NewServiceWithStore(nil, traceRecorder)
}

func NewServiceWithStore(store Store, traceRecorder trace.Recorder) *Service {
	return &Service{
		capabilities: map[contracts.AgentCapabilityID]contracts.AgentCapability{},
		store:        store,
		trace:        traceRecorder,
		now:          func() time.Time { return time.Now().UTC() },
	}
}

func (s *Service) Upsert(ctx context.Context, capability contracts.AgentCapability) (contracts.AgentCapability, error) {
	if capability.TenantID == "" || capability.AgentID == "" {
		return contracts.AgentCapability{}, fmt.Errorf("tenant_id and agent_id are required")
	}
	if capability.CapabilityID == "" {
		capability.CapabilityID = contracts.AgentCapabilityID(idgen.New("cap"))
	}
	if capability.CreatedAt.IsZero() {
		capability.CreatedAt = s.now()
	}
	s.mu.Lock()
	s.capabilities[capability.CapabilityID] = cloneCapability(capability)
	s.mu.Unlock()
	if s.store != nil {
		if err := s.store.Save(ctx, capability); err != nil {
			return contracts.AgentCapability{}, err
		}
	}
	return cloneCapability(capability), nil
}

func (s *Service) Match(ctx context.Context, tenantID contracts.TenantID, query string, limit int, traceID contracts.TraceID) ([]contracts.AgentCapabilityMatch, error) {
	if tenantID == "" || strings.TrimSpace(query) == "" {
		return nil, fmt.Errorf("tenant_id and query are required")
	}
	terms := tokenize(query)
	if s.store != nil {
		capabilities, err := s.store.ListByTenant(ctx, tenantID)
		if err != nil {
			return nil, err
		}
		return s.matchCapabilities(ctx, tenantID, query, terms, limit, traceID, capabilities)
	}
	s.mu.RLock()
	capabilities := make([]contracts.AgentCapability, 0, len(s.capabilities))
	for _, capability := range s.capabilities {
		capabilities = append(capabilities, capability)
	}
	s.mu.RUnlock()
	return s.matchCapabilities(ctx, tenantID, query, terms, limit, traceID, capabilities)
}

func (s *Service) matchCapabilities(ctx context.Context, tenantID contracts.TenantID, query string, terms []string, limit int, traceID contracts.TraceID, capabilities []contracts.AgentCapability) ([]contracts.AgentCapabilityMatch, error) {
	matches := make([]contracts.AgentCapabilityMatch, 0)
	for _, capability := range capabilities {
		if capability.TenantID != tenantID {
			continue
		}
		score := score(terms, capability)
		if score <= 0 {
			continue
		}
		matches = append(matches, contracts.AgentCapabilityMatch{
			Capability: cloneCapability(capability),
			Score:      score,
			Reason:     "matched capability text",
		})
	}
	sort.SliceStable(matches, func(i, j int) bool {
		if matches[i].Score == matches[j].Score {
			return matches[i].Capability.AgentID < matches[j].Capability.AgentID
		}
		return matches[i].Score > matches[j].Score
	})
	if limit <= 0 || limit > 10 {
		limit = 5
	}
	if len(matches) > limit {
		matches = matches[:limit]
	}
	if s.trace != nil && traceID != "" {
		_ = s.trace.Record(ctx, contracts.TraceEvent{
			TraceID:   traceID,
			TenantID:  tenantID,
			SpanID:    contracts.SpanID(idgen.New("span")),
			Type:      contracts.TraceAgentCapabilityMatched,
			Payload:   map[string]any{"query": query, "match_count": len(matches)},
			CreatedAt: s.now(),
		})
	}
	return matches, nil
}

func tokenize(value string) []string {
	fields := strings.Fields(strings.ToLower(value))
	out := make([]string, 0, len(fields))
	for _, field := range fields {
		field = strings.Trim(field, ".,;:!?()[]{}\"'")
		if field != "" {
			out = append(out, field)
		}
	}
	if len(out) == 0 && strings.TrimSpace(value) != "" {
		out = []string{strings.ToLower(strings.TrimSpace(value))}
	}
	return out
}

func score(terms []string, capability contracts.AgentCapability) float64 {
	text := strings.ToLower(strings.Join(append(append([]string{capability.Name, capability.Description}, capability.Tags...), capability.WhenToUse...), " "))
	var score float64
	for _, term := range terms {
		if strings.Contains(text, term) {
			score += 1
		}
	}
	return score
}

func cloneCapability(in contracts.AgentCapability) contracts.AgentCapability {
	in.Tags = append([]string(nil), in.Tags...)
	in.WhenToUse = append([]string(nil), in.WhenToUse...)
	return in
}
