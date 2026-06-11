package hook

import (
	"context"
	"sort"
	"sync"

	"znt/internal/contracts"
)

type InMemoryStore struct {
	mu        sync.RWMutex
	providers map[string]Provider
	manifests map[string]HookManifest
	versions  map[string]HookManifest
	bindings  map[string]Binding
	events    []HookEvent
}

func NewInMemoryStore() *InMemoryStore {
	return &InMemoryStore{
		providers: map[string]Provider{},
		manifests: map[string]HookManifest{},
		versions:  map[string]HookManifest{},
		bindings:  map[string]Binding{},
		events:    []HookEvent{},
	}
}

func (s *InMemoryStore) UpsertProvider(_ context.Context, provider Provider) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.providers[providerKey(provider.TenantID, provider.ProviderID)] = provider
	return nil
}

func (s *InMemoryStore) GetProvider(_ context.Context, tenantID contracts.TenantID, providerID string) (Provider, bool, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	provider, ok := s.providers[providerKey(tenantID, providerID)]
	if ok {
		return provider, true, nil
	}
	if tenantID != "" {
		provider, ok = s.providers[providerKey("", providerID)]
	}
	return provider, ok, nil
}

func (s *InMemoryStore) ListProviders(_ context.Context, tenantID contracts.TenantID) ([]Provider, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	byID := map[string]Provider{}
	for _, provider := range s.providers {
		if provider.TenantID != "" && provider.TenantID != tenantID {
			continue
		}
		existing, ok := byID[provider.ProviderID]
		if ok && existing.TenantID == tenantID {
			continue
		}
		byID[provider.ProviderID] = provider
	}
	out := make([]Provider, 0, len(byID))
	for _, provider := range byID {
		out = append(out, provider)
	}
	sort.SliceStable(out, func(i, j int) bool {
		return out[i].ProviderID < out[j].ProviderID
	})
	return out, nil
}

func (s *InMemoryStore) UpsertManifest(_ context.Context, manifest HookManifest) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.manifests[manifestKey(manifest.TenantID, manifest.HookID)] = manifest
	s.versions[manifestVersionKey(manifest.TenantID, manifest.HookID, manifest.Version)] = manifest
	return nil
}

func (s *InMemoryStore) GetManifest(_ context.Context, tenantID contracts.TenantID, hookID string) (HookManifest, bool, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	manifest, ok := s.manifests[manifestKey(tenantID, hookID)]
	if ok {
		return manifest, true, nil
	}
	if tenantID != "" {
		manifest, ok = s.manifests[manifestKey("", hookID)]
	}
	return manifest, ok, nil
}

func (s *InMemoryStore) GetManifestVersion(_ context.Context, tenantID contracts.TenantID, hookID string, version string) (HookManifest, bool, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	manifest, ok := s.versions[manifestVersionKey(tenantID, hookID, version)]
	if ok {
		return manifest, true, nil
	}
	if tenantID != "" {
		manifest, ok = s.versions[manifestVersionKey("", hookID, version)]
	}
	return manifest, ok, nil
}

func (s *InMemoryStore) ListManifestVersions(_ context.Context, tenantID contracts.TenantID, hookID string) ([]HookManifest, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	byVersion := map[string]HookManifest{}
	for _, manifest := range s.versions {
		if manifest.HookID != hookID {
			continue
		}
		if manifest.TenantID != "" && manifest.TenantID != tenantID {
			continue
		}
		existing, ok := byVersion[manifest.Version]
		if ok && existing.TenantID == tenantID {
			continue
		}
		byVersion[manifest.Version] = manifest
	}
	out := make([]HookManifest, 0, len(byVersion))
	for _, manifest := range byVersion {
		out = append(out, manifest)
	}
	sort.SliceStable(out, func(i, j int) bool {
		return out[i].Version < out[j].Version
	})
	return out, nil
}

func (s *InMemoryStore) ListManifests(_ context.Context, tenantID contracts.TenantID) ([]HookManifest, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	byID := map[string]HookManifest{}
	for _, manifest := range s.manifests {
		if manifest.TenantID != "" && manifest.TenantID != tenantID {
			continue
		}
		existing, ok := byID[manifest.HookID]
		if ok && existing.TenantID == tenantID {
			continue
		}
		byID[manifest.HookID] = manifest
	}
	out := make([]HookManifest, 0, len(byID))
	for _, manifest := range byID {
		out = append(out, manifest)
	}
	sort.SliceStable(out, func(i, j int) bool {
		if out[i].Phase == out[j].Phase {
			return out[i].HookID < out[j].HookID
		}
		return out[i].Phase < out[j].Phase
	})
	return out, nil
}

func (s *InMemoryStore) UpsertBinding(_ context.Context, binding Binding) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.bindings[bindingKey(binding.TenantID, binding.AgentID, binding.AgentVersion, binding.HookID, binding.Phase)] = binding
	return nil
}

func (s *InMemoryStore) ListBindings(_ context.Context, tenantID contracts.TenantID, agentID contracts.AgentID, agentVersion contracts.AgentVersion) ([]Binding, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	byID := map[string]Binding{}
	for _, binding := range s.bindings {
		if binding.AgentID != agentID {
			continue
		}
		if binding.TenantID != "" && binding.TenantID != tenantID {
			continue
		}
		if binding.AgentVersion != "" && binding.AgentVersion != agentVersion {
			continue
		}
		key := binding.HookID + "\x00" + string(binding.Phase)
		existing, ok := byID[key]
		if ok && bindingSpecificity(existing, tenantID, agentVersion) >= bindingSpecificity(binding, tenantID, agentVersion) {
			continue
		}
		byID[key] = binding
	}
	out := make([]Binding, 0, len(byID))
	for _, binding := range byID {
		out = append(out, binding)
	}
	sort.SliceStable(out, func(i, j int) bool {
		if out[i].Phase == out[j].Phase {
			return out[i].HookID < out[j].HookID
		}
		return out[i].Phase < out[j].Phase
	})
	return out, nil
}

func (s *InMemoryStore) SaveEvent(_ context.Context, event HookEvent) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.events = append(s.events, event)
	return nil
}

func (s *InMemoryStore) ListEvents(_ context.Context, tenantID contracts.TenantID, traceID contracts.TraceID) ([]HookEvent, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	out := make([]HookEvent, 0, len(s.events))
	for _, event := range s.events {
		if event.TenantID != tenantID {
			continue
		}
		if traceID != "" && event.TraceID != traceID {
			continue
		}
		out = append(out, event)
	}
	return out, nil
}

func providerKey(tenantID contracts.TenantID, providerID string) string {
	return string(tenantID) + "\x00" + providerID
}

func manifestKey(tenantID contracts.TenantID, hookID string) string {
	return string(tenantID) + "\x00" + hookID
}

func manifestVersionKey(tenantID contracts.TenantID, hookID string, version string) string {
	return manifestKey(tenantID, hookID) + "\x00" + version
}

func bindingKey(tenantID contracts.TenantID, agentID contracts.AgentID, version contracts.AgentVersion, hookID string, phase HookPoint) string {
	return string(tenantID) + "\x00" + string(agentID) + "@" + string(version) + "\x00" + hookID + "\x00" + string(phase)
}

func bindingSpecificity(binding Binding, tenantID contracts.TenantID, agentVersion contracts.AgentVersion) int {
	score := 0
	if binding.TenantID == tenantID && tenantID != "" {
		score += 2
	}
	if binding.AgentVersion == agentVersion && agentVersion != "" {
		score++
	}
	return score
}
