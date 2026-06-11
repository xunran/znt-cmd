package catalog

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"time"

	"znt/internal/contracts"
	"znt/internal/governance/audit"
	"znt/internal/governance/trace"
	"znt/internal/tool/registry"
	"znt/pkg/idgen"
)

const (
	ProviderTypeStaticToolHost = "static_tool_host"
	ProviderTypeMCP            = "mcp"
	ExecutorTypeHTTPDirect     = "http_direct"
	ExecutorTypeStaticToolHost = "static_tool_host"
	ExecutorTypeAgentTool      = "agent_tool"
	ExecutorTypeMCP            = "mcp"

	StatusDraft    = "draft"
	StatusEnabled  = "enabled"
	StatusDisabled = "disabled"

	HealthUnknown   = "unknown"
	HealthHealthy   = "healthy"
	HealthUnhealthy = "unhealthy"

	providerAuthRefHeader = "X-Origin-Provider-Auth-Ref"
	maxProviderTimeoutMS  = 120000
	maxProviderRetryMax   = 5
)

type ToolManifest struct {
	TenantID contracts.TenantID `json:"tenant_id,omitempty"`
	ToolID   string             `json:"tool_id"`
	GroupID  string             `json:"group_id,omitempty"`

	Name        string   `json:"name"`
	Description string   `json:"description"`
	WhenToUse   []string `json:"when_to_use,omitempty"`

	InputSchema  map[string]any `json:"input_schema"`
	OutputSchema map[string]any `json:"output_schema,omitempty"`

	RiskLevel  contracts.RiskLevel      `json:"risk_level"`
	Visibility contracts.ToolVisibility `json:"visibility"`

	ExecutionProfile string       `json:"execution_profile,omitempty"`
	Executor         ExecutorSpec `json:"executor"`

	Status  string `json:"status"`
	Version string `json:"version"`
}

type ToolGroup struct {
	TenantID    contracts.TenantID `json:"tenant_id,omitempty"`
	GroupID     string             `json:"group_id"`
	Name        string             `json:"name"`
	Description string             `json:"description,omitempty"`
	Status      string             `json:"status"`
	Version     string             `json:"version"`
}

type ExecutorSpec struct {
	Type       string            `json:"type"`
	ProviderID string            `json:"provider_id,omitempty"`
	Operation  string            `json:"operation,omitempty"`
	URL        string            `json:"url,omitempty"`
	Method     string            `json:"method,omitempty"`
	Headers    map[string]string `json:"headers,omitempty"`
}

type ToolProvider struct {
	TenantID          contracts.TenantID `json:"tenant_id,omitempty"`
	ProviderID        string             `json:"provider_id"`
	ProviderType      string             `json:"provider_type"`
	Name              string             `json:"name"`
	Description       string             `json:"description,omitempty"`
	Endpoint          string             `json:"endpoint"`
	Status            string             `json:"status"`
	HealthStatus      string             `json:"health_status,omitempty"`
	LastHealthCheckAt *time.Time         `json:"last_health_check_at,omitempty"`
	LastHealthError   string             `json:"last_health_error,omitempty"`
	AuthRef           string             `json:"auth_ref,omitempty"`
	TimeoutMS         int                `json:"timeout_ms,omitempty"`
	RetryMax          int                `json:"retry_max,omitempty"`
	Version           string             `json:"version,omitempty"`
}

type Service struct {
	mu                sync.RWMutex
	registry          registry.Registry
	store             Store
	providers         map[string]ToolProvider
	groups            map[string]ToolGroup
	manifests         map[string]ToolManifest
	client            *http.Client
	audit             audit.Logger
	trace             trace.Recorder
	now               func() time.Time
	agentToolHandler  AgentToolHandler
	DisableHTTPDirect bool
}

type AgentToolHandler interface {
	ExecuteAgentTool(ctx context.Context, call contracts.ToolCall, manifest ToolManifest) (map[string]any, []contracts.ArtifactRef, error)
}

type Store interface {
	UpsertProvider(ctx context.Context, provider ToolProvider) error
	UpsertGroup(ctx context.Context, group ToolGroup) error
	UpsertManifest(ctx context.Context, manifest ToolManifest) error
	UpsertRuntimeCache(ctx context.Context, tenantID contracts.TenantID, toolID string, version string, status string) error
	ListProviders(ctx context.Context) ([]ToolProvider, error)
	ListGroups(ctx context.Context) ([]ToolGroup, error)
	ListManifests(ctx context.Context) ([]ToolManifest, error)
}

func NewService(runtimeRegistry registry.Registry, auditLogger audit.Logger) *Service {
	return NewServiceWithStore(runtimeRegistry, auditLogger, nil)
}

func NewServiceWithStore(runtimeRegistry registry.Registry, auditLogger audit.Logger, store Store) *Service {
	return &Service{
		registry:  runtimeRegistry,
		store:     store,
		providers: map[string]ToolProvider{},
		groups:    map[string]ToolGroup{},
		manifests: map[string]ToolManifest{},
		client:    &http.Client{Timeout: 10 * time.Second},
		audit:     auditLogger,
		now:       func() time.Time { return time.Now().UTC() },
	}
}

func (s *Service) Restore(ctx context.Context) error {
	if s.store == nil {
		return nil
	}
	providers, err := s.store.ListProviders(ctx)
	if err != nil {
		return err
	}
	groups, err := s.store.ListGroups(ctx)
	if err != nil {
		return err
	}
	manifests, err := s.store.ListManifests(ctx)
	if err != nil {
		return err
	}
	s.mu.Lock()
	s.providers = map[string]ToolProvider{}
	for _, provider := range providers {
		provider = normalizeProvider(provider)
		s.providers[providerKey(provider.TenantID, provider.ProviderID)] = provider
	}
	s.groups = map[string]ToolGroup{}
	for _, group := range groups {
		group = normalizeGroup(group)
		s.groups[groupKey(group.TenantID, group.GroupID)] = group
	}
	s.manifests = map[string]ToolManifest{}
	restoredManifests := make([]ToolManifest, 0, len(manifests))
	for _, manifest := range manifests {
		manifest = normalizeManifest(manifest)
		s.manifests[manifestKey(manifest.TenantID, manifest.ToolID)] = manifest
		restoredManifests = append(restoredManifests, manifest)
	}
	s.mu.Unlock()
	return s.applyManifests(ctx, restoredManifests)
}

func (s *Service) SetAgentToolHandler(handler AgentToolHandler) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.agentToolHandler = handler
}

func (s *Service) SetTraceRecorder(recorder trace.Recorder) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.trace = recorder
}

func (s *Service) AgentToolHandler() AgentToolHandler {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.agentToolHandler
}

func (s *Service) UpsertProvider(ctx context.Context, provider ToolProvider, actorID string) (ToolProvider, error) {
	provider = normalizeProvider(provider)
	if err := validateProvider(provider); err != nil {
		return ToolProvider{}, err
	}
	if s.store != nil {
		if err := s.store.UpsertProvider(ctx, provider); err != nil {
			return ToolProvider{}, err
		}
	}
	manifests := s.storeProviderLocked(provider)
	if err := s.applyManifests(ctx, manifests); err != nil {
		return ToolProvider{}, err
	}
	s.auditEvent(ctx, provider.TenantID, actorID, "tool.provider.upsert", provider.ProviderID, "allowed", "")
	return provider, nil
}

func (s *Service) UpsertGroup(ctx context.Context, group ToolGroup, actorID string) (ToolGroup, error) {
	group = normalizeGroup(group)
	if err := validateGroup(group); err != nil {
		return ToolGroup{}, err
	}
	if s.store != nil {
		if err := s.store.UpsertGroup(ctx, group); err != nil {
			return ToolGroup{}, err
		}
	}
	manifests := s.storeGroupLocked(group)
	if err := s.applyManifests(ctx, manifests); err != nil {
		return ToolGroup{}, err
	}
	s.auditEvent(ctx, group.TenantID, actorID, "tool.group.upsert", group.GroupID, "allowed", "")
	return group, nil
}

func (s *Service) UpsertManifest(ctx context.Context, manifest ToolManifest, actorID string) (ToolManifest, error) {
	manifest = normalizeManifest(manifest)
	if err := validateManifest(manifest); err != nil {
		return ToolManifest{}, err
	}
	if err := s.validateHTTPDirectAllowed(manifest); err != nil {
		return ToolManifest{}, err
	}
	if s.store != nil {
		if err := s.store.UpsertManifest(ctx, manifest); err != nil {
			return ToolManifest{}, err
		}
	}
	s.mu.Lock()
	s.manifests[manifestKey(manifest.TenantID, manifest.ToolID)] = manifest
	s.mu.Unlock()
	if err := s.applyManifests(ctx, []ToolManifest{manifest}); err != nil {
		return ToolManifest{}, err
	}
	s.auditEvent(ctx, manifest.TenantID, actorID, "tool.manifest.upsert", manifest.ToolID, "allowed", "")
	return manifest, nil
}

func (s *Service) SyncProviderCatalog(ctx context.Context, tenantID contracts.TenantID, providerID string, actorID string) ([]ToolManifest, error) {
	provider, ok := s.provider(tenantID, providerID)
	if !ok {
		return nil, contracts.NewRuntimeError(contracts.CodeToolNotFound, "tool provider not found", map[string]any{"provider_id": providerID})
	}
	if provider.Status != StatusEnabled {
		return nil, contracts.NewRuntimeError(contracts.CodeToolPolicyDenied, "tool provider is not enabled", map[string]any{"provider_id": providerID})
	}
	if provider.HealthStatus == HealthUnhealthy {
		return nil, contracts.NewRuntimeError(contracts.CodeToolPolicyDenied, "tool provider is unhealthy", map[string]any{"provider_id": providerID, "health_status": provider.HealthStatus})
	}
	catalog, err := s.fetchCatalog(ctx, provider)
	if err != nil {
		return nil, err
	}
	out := make([]ToolManifest, 0, len(catalog.Tools))
	for _, item := range catalog.Tools {
		manifest := ToolManifest{
			TenantID:     tenantID,
			ToolID:       item.ToolID,
			GroupID:      item.GroupID,
			Name:         item.Name,
			Description:  item.Description,
			WhenToUse:    item.WhenToUse,
			InputSchema:  item.InputSchema,
			OutputSchema: item.OutputSchema,
			RiskLevel:    item.RiskLevel,
			Visibility:   item.Visibility,
			Executor: ExecutorSpec{
				Type:       executorTypeForProvider(provider.ProviderType),
				ProviderID: provider.ProviderID,
				Operation:  item.Operation,
			},
			Status:  StatusEnabled,
			Version: item.Version,
		}
		installed, err := s.UpsertManifest(ctx, manifest, actorID)
		if err != nil {
			return nil, err
		}
		out = append(out, installed)
	}
	s.auditEvent(ctx, tenantID, actorID, "tool.provider.sync", providerID, "allowed", fmt.Sprintf("tools=%d", len(out)))
	return out, nil
}

func (s *Service) CheckProviderHealth(ctx context.Context, tenantID contracts.TenantID, providerID string, actorID string) (ToolProvider, error) {
	return s.CheckProviderHealthForTrace(ctx, tenantID, providerID, actorID, "")
}

func (s *Service) CheckProviderHealthForTrace(ctx context.Context, tenantID contracts.TenantID, providerID string, actorID string, traceID contracts.TraceID) (ToolProvider, error) {
	provider, ok := s.provider(tenantID, strings.TrimSpace(providerID))
	if !ok {
		return ToolProvider{}, contracts.NewRuntimeError(contracts.CodeToolNotFound, "tool provider not found", map[string]any{"provider_id": providerID})
	}
	provider = normalizeProvider(provider)
	checkedAt := s.now()
	started := checkedAt
	provider.HealthStatus, provider.LastHealthError = s.probeProviderHealth(ctx, provider)
	provider.LastHealthCheckAt = &checkedAt
	if s.store != nil {
		if err := s.store.UpsertProvider(ctx, provider); err != nil {
			return ToolProvider{}, err
		}
	}
	manifests := s.storeProviderLocked(provider)
	if err := s.applyManifests(ctx, manifests); err != nil {
		return ToolProvider{}, err
	}
	s.auditEvent(ctx, provider.TenantID, actorID, "tool.provider.health_check", provider.ProviderID, "allowed", provider.HealthStatus)
	s.recordProviderTrace(ctx, traceID, tenantID, "", "", contracts.TraceToolProviderHealthChecked, map[string]any{
		"provider_id":   provider.ProviderID,
		"provider_type": provider.ProviderType,
		"health_status": provider.HealthStatus,
		"latency_ms":    int(s.now().Sub(started).Milliseconds()),
	})
	return provider, nil
}

func (s *Service) ListManifests(tenantID contracts.TenantID) []ToolManifest {
	s.mu.RLock()
	defer s.mu.RUnlock()
	out := make([]ToolManifest, 0, len(s.manifests))
	for _, manifest := range s.manifests {
		if manifest.TenantID == tenantID || manifest.TenantID == "" {
			out = append(out, manifest)
		}
	}
	return out
}

func (s *Service) ListProviders(tenantID contracts.TenantID) []ToolProvider {
	s.mu.RLock()
	defer s.mu.RUnlock()
	out := make([]ToolProvider, 0, len(s.providers))
	for _, provider := range s.providers {
		if provider.TenantID == tenantID || provider.TenantID == "" {
			out = append(out, provider)
		}
	}
	return out
}

func (s *Service) GetProvider(tenantID contracts.TenantID, providerID string) (ToolProvider, bool) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	provider, ok := s.providers[providerKey(tenantID, providerID)]
	if !ok && tenantID != "" {
		provider, ok = s.providers[providerKey("", providerID)]
	}
	return provider, ok
}

func (s *Service) ListGroups(tenantID contracts.TenantID) []ToolGroup {
	s.mu.RLock()
	defer s.mu.RUnlock()
	out := make([]ToolGroup, 0, len(s.groups))
	for _, group := range s.groups {
		if group.TenantID == tenantID || group.TenantID == "" {
			out = append(out, group)
		}
	}
	return out
}

func (s *Service) GetManifest(tenantID contracts.TenantID, toolID string) (ToolManifest, bool) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	manifest, ok := s.manifests[manifestKey(tenantID, toolID)]
	if !ok && tenantID != "" {
		manifest, ok = s.manifests[manifestKey("", toolID)]
	}
	return manifest, ok
}

func (s *Service) GetGroup(tenantID contracts.TenantID, groupID string) (ToolGroup, bool) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	group, ok := s.groups[groupKey(tenantID, groupID)]
	if !ok && tenantID != "" {
		group, ok = s.groups[groupKey("", groupID)]
	}
	return group, ok
}

func (s *Service) hasManifest(tenantID contracts.TenantID, toolID string) bool {
	s.mu.RLock()
	defer s.mu.RUnlock()
	_, ok := s.manifests[manifestKey(tenantID, toolID)]
	return ok
}

func (s *Service) storeGroupLocked(group ToolGroup) []ToolManifest {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.groups[groupKey(group.TenantID, group.GroupID)] = group
	out := make([]ToolManifest, 0)
	for _, manifest := range s.manifests {
		if manifest.GroupID != group.GroupID {
			continue
		}
		if manifest.TenantID != group.TenantID {
			if group.TenantID != "" {
				continue
			}
			if _, hasTenantOverride := s.groups[groupKey(manifest.TenantID, group.GroupID)]; hasTenantOverride {
				continue
			}
		}
		out = append(out, manifest)
	}
	return out
}

func (s *Service) storeProviderLocked(provider ToolProvider) []ToolManifest {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.providers[providerKey(provider.TenantID, provider.ProviderID)] = provider
	out := make([]ToolManifest, 0)
	for _, manifest := range s.manifests {
		if manifest.TenantID != provider.TenantID {
			if provider.TenantID != "" {
				continue
			}
			if _, hasTenantOverride := s.providers[providerKey(manifest.TenantID, provider.ProviderID)]; hasTenantOverride {
				continue
			}
		}
		if !executorUsesProvider(manifest.Executor.Type) || manifest.Executor.ProviderID != provider.ProviderID {
			continue
		}
		out = append(out, manifest)
	}
	return out
}

func (s *Service) applyManifests(ctx context.Context, manifests []ToolManifest) error {
	for _, manifest := range manifests {
		if !s.manifestAllowsInstall(manifest) {
			s.registry.UnregisterForTenant(manifest.TenantID, manifest.ToolID)
			if s.store != nil {
				if err := s.store.UpsertRuntimeCache(ctx, manifest.TenantID, manifest.ToolID, manifest.Version, StatusDisabled); err != nil {
					return err
				}
			}
			continue
		}
		if err := s.install(ctx, manifest); err != nil {
			return err
		}
		if s.store != nil {
			if err := s.store.UpsertRuntimeCache(ctx, manifest.TenantID, manifest.ToolID, manifest.Version, StatusEnabled); err != nil {
				return err
			}
		}
	}
	return nil
}

func (s *Service) manifestAllowsInstall(manifest ToolManifest) bool {
	return manifest.Status == StatusEnabled &&
		!(s.DisableHTTPDirect && manifest.Executor.Type == ExecutorTypeHTTPDirect) &&
		s.groupAllowsInstall(manifest.TenantID, manifest.GroupID) &&
		s.providerAllowsInstall(manifest)
}

func (s *Service) groupAllowsInstall(tenantID contracts.TenantID, groupID string) bool {
	groupID = strings.TrimSpace(groupID)
	if groupID == "" {
		return true
	}
	group, ok := s.GetGroup(tenantID, groupID)
	if !ok {
		return true
	}
	return group.Status == StatusEnabled
}

func (s *Service) providerAllowsInstall(manifest ToolManifest) bool {
	if !executorUsesProvider(manifest.Executor.Type) {
		return true
	}
	provider, ok := s.provider(manifest.TenantID, manifest.Executor.ProviderID)
	if !ok {
		return false
	}
	return provider.Status == StatusEnabled && provider.HealthStatus != HealthUnhealthy
}

func (s *Service) provider(tenantID contracts.TenantID, providerID string) (ToolProvider, bool) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	provider, ok := s.providers[providerKey(tenantID, providerID)]
	if !ok && tenantID != "" {
		provider, ok = s.providers[providerKey("", providerID)]
	}
	return provider, ok
}

func (s *Service) CheckToolAvailability(_ context.Context, tenantID contracts.TenantID, tool contracts.ToolDefinition) error {
	manifest, ok := s.GetManifest(tenantID, tool.ToolID)
	if !ok {
		return nil
	}
	if manifest.Status != StatusEnabled {
		return contracts.NewRuntimeError(contracts.CodeToolPolicyDenied, "tool manifest is not enabled", map[string]any{"tool_id": tool.ToolID, "status": manifest.Status})
	}
	if strings.TrimSpace(manifest.GroupID) != "" {
		if group, ok := s.GetGroup(manifest.TenantID, manifest.GroupID); ok && group.Status != StatusEnabled {
			return contracts.NewRuntimeError(contracts.CodeToolPolicyDenied, "tool group is not enabled", map[string]any{"tool_id": tool.ToolID, "group_id": manifest.GroupID, "status": group.Status})
		}
	}
	if executorUsesProvider(manifest.Executor.Type) {
		provider, ok := s.provider(manifest.TenantID, manifest.Executor.ProviderID)
		if !ok {
			return contracts.NewRuntimeError(contracts.CodeToolPolicyDenied, "tool provider is unavailable", map[string]any{"tool_id": tool.ToolID, "provider_id": manifest.Executor.ProviderID})
		}
		if provider.Status != StatusEnabled {
			return contracts.NewRuntimeError(contracts.CodeToolPolicyDenied, "tool provider is not enabled", map[string]any{"tool_id": tool.ToolID, "provider_id": manifest.Executor.ProviderID, "status": provider.Status})
		}
		if provider.HealthStatus == HealthUnhealthy {
			return contracts.NewRuntimeError(contracts.CodeToolPolicyDenied, "tool provider is unhealthy", map[string]any{"tool_id": tool.ToolID, "provider_id": manifest.Executor.ProviderID, "health_status": provider.HealthStatus})
		}
	}
	if manifest.Executor.Type == ExecutorTypeHTTPDirect && s.DisableHTTPDirect {
		return contracts.NewRuntimeError(contracts.CodeToolPolicyDenied, "http_direct tools are disabled by release switch", map[string]any{"tool_id": tool.ToolID})
	}
	return nil
}

func (s *Service) install(ctx context.Context, manifest ToolManifest) error {
	executor, err := s.executorFor(ctx, manifest)
	if err != nil {
		return err
	}
	return s.registry.Upsert(registry.Tool{
		Definition: contracts.ToolDefinition{
			ToolID:           manifest.ToolID,
			GroupID:          manifest.GroupID,
			Name:             manifest.Name,
			Description:      manifest.Description,
			InputSchema:      manifest.InputSchema,
			OutputSchema:     manifest.OutputSchema,
			RiskLevel:        manifest.RiskLevel,
			Visibility:       manifest.Visibility,
			ExecutionProfile: manifest.ExecutionProfile,
			Version:          manifest.Version,
		},
		Executor:  executor,
		WhenToUse: manifest.WhenToUse,
		TenantID:  manifest.TenantID,
	})
}

func (s *Service) executorFor(_ context.Context, manifest ToolManifest) (registry.Executor, error) {
	switch manifest.Executor.Type {
	case ExecutorTypeHTTPDirect:
		return HTTPExecutor{
			URL:      manifest.Executor.URL,
			Method:   manifest.Executor.Method,
			Headers:  manifest.Executor.Headers,
			Client:   s.client,
			TenantID: manifest.TenantID,
		}, nil
	case ExecutorTypeStaticToolHost:
		provider, ok := s.provider(manifest.TenantID, manifest.Executor.ProviderID)
		if !ok {
			return nil, contracts.NewRuntimeError(contracts.CodeToolNotFound, "tool provider not found", map[string]any{"provider_id": manifest.Executor.ProviderID})
		}
		if provider.Status != StatusEnabled {
			return nil, contracts.NewRuntimeError(contracts.CodeToolPolicyDenied, "tool provider is not enabled", map[string]any{"provider_id": manifest.Executor.ProviderID})
		}
		if provider.HealthStatus == HealthUnhealthy {
			return nil, contracts.NewRuntimeError(contracts.CodeToolPolicyDenied, "tool provider is unhealthy", map[string]any{"provider_id": manifest.Executor.ProviderID, "health_status": provider.HealthStatus})
		}
		return ToolHostExecutor{
			Endpoint:   provider.Endpoint,
			ProviderID: provider.ProviderID,
			Operation:  manifest.Executor.Operation,
			Headers:    providerHeaders(provider),
			TimeoutMS:  provider.TimeoutMS,
			RetryMax:   provider.RetryMax,
			Client:     s.client,
			TenantID:   manifest.TenantID,
			Trace:      s.trace,
			Now:        s.now,
		}, nil
	case ExecutorTypeAgentTool:
		return AgentToolExecutor{
			Manifest: manifest,
			Handler:  s.AgentToolHandler,
			TenantID: manifest.TenantID,
		}, nil
	case ExecutorTypeMCP:
		provider, ok := s.provider(manifest.TenantID, manifest.Executor.ProviderID)
		if !ok {
			return nil, contracts.NewRuntimeError(contracts.CodeToolNotFound, "tool provider not found", map[string]any{"provider_id": manifest.Executor.ProviderID})
		}
		if provider.Status != StatusEnabled {
			return nil, contracts.NewRuntimeError(contracts.CodeToolPolicyDenied, "tool provider is not enabled", map[string]any{"provider_id": manifest.Executor.ProviderID})
		}
		if provider.HealthStatus == HealthUnhealthy {
			return nil, contracts.NewRuntimeError(contracts.CodeToolPolicyDenied, "tool provider is unhealthy", map[string]any{"provider_id": manifest.Executor.ProviderID, "health_status": provider.HealthStatus})
		}
		return MCPExecutor{
			Endpoint:   provider.Endpoint,
			ProviderID: provider.ProviderID,
			Operation:  manifest.Executor.Operation,
			Headers:    providerHeaders(provider),
			TimeoutMS:  provider.TimeoutMS,
			RetryMax:   provider.RetryMax,
			Client:     s.client,
			TenantID:   manifest.TenantID,
			Trace:      s.trace,
			Now:        s.now,
		}, nil
	default:
		return nil, contracts.NewRuntimeError(contracts.CodeToolArgumentInvalid, "unsupported tool executor type", map[string]any{"executor_type": manifest.Executor.Type})
	}
}

func normalizeProvider(provider ToolProvider) ToolProvider {
	provider.ProviderID = strings.TrimSpace(provider.ProviderID)
	provider.ProviderType = strings.TrimSpace(provider.ProviderType)
	provider.Endpoint = strings.TrimSpace(provider.Endpoint)
	provider.AuthRef = strings.TrimSpace(provider.AuthRef)
	if provider.TimeoutMS < 0 {
		provider.TimeoutMS = 0
	}
	if provider.RetryMax < 0 {
		provider.RetryMax = 0
	}
	if provider.ProviderType == "" {
		provider.ProviderType = ProviderTypeStaticToolHost
	}
	if provider.Status == "" {
		provider.Status = StatusEnabled
	}
	if provider.HealthStatus == "" {
		provider.HealthStatus = HealthUnknown
	}
	if provider.Version == "" {
		provider.Version = "v1"
	}
	return provider
}

func normalizeGroup(group ToolGroup) ToolGroup {
	group.GroupID = strings.TrimSpace(group.GroupID)
	group.Name = strings.TrimSpace(group.Name)
	group.Description = strings.TrimSpace(group.Description)
	if group.Name == "" {
		group.Name = group.GroupID
	}
	if group.Status == "" {
		group.Status = StatusEnabled
	}
	if group.Version == "" {
		group.Version = "v1"
	}
	return group
}

func normalizeManifest(manifest ToolManifest) ToolManifest {
	manifest.ToolID = strings.TrimSpace(manifest.ToolID)
	manifest.Name = strings.TrimSpace(manifest.Name)
	manifest.Description = strings.TrimSpace(manifest.Description)
	if manifest.RiskLevel == "" {
		manifest.RiskLevel = contracts.RiskLow
	}
	if manifest.Visibility == "" {
		manifest.Visibility = contracts.ToolProtected
	}
	if manifest.Status == "" {
		manifest.Status = StatusEnabled
	}
	if manifest.Version == "" {
		manifest.Version = "v1"
	}
	manifest.Executor.Type = strings.TrimSpace(manifest.Executor.Type)
	manifest.Executor.ProviderID = strings.TrimSpace(manifest.Executor.ProviderID)
	manifest.Executor.Operation = strings.TrimSpace(manifest.Executor.Operation)
	manifest.Executor.URL = strings.TrimSpace(manifest.Executor.URL)
	manifest.Executor.Method = strings.TrimSpace(manifest.Executor.Method)
	if manifest.ExecutionProfile == "" {
		switch manifest.Executor.Type {
		case ExecutorTypeHTTPDirect, ExecutorTypeStaticToolHost, ExecutorTypeMCP:
			manifest.ExecutionProfile = `{"id":"http-direct","domain_id":"http","network_policy":{"allow_network":true}}`
		case ExecutorTypeAgentTool:
			manifest.ExecutionProfile = `{"id":"agent-tool","domain_id":"agent_tool"}`
		default:
			manifest.ExecutionProfile = "local"
		}
	}
	return manifest
}

func validateProvider(provider ToolProvider) error {
	if strings.TrimSpace(provider.ProviderID) == "" || strings.TrimSpace(provider.Endpoint) == "" {
		return contracts.NewRuntimeError(contracts.CodeToolArgumentInvalid, "provider_id and endpoint are required", nil)
	}
	if provider.ProviderType != ProviderTypeStaticToolHost && provider.ProviderType != ProviderTypeMCP {
		return contracts.NewRuntimeError(contracts.CodeToolArgumentInvalid, "unsupported provider_type", map[string]any{"provider_type": provider.ProviderType})
	}
	if provider.Status != StatusDraft && provider.Status != StatusEnabled && provider.Status != StatusDisabled {
		return contracts.NewRuntimeError(contracts.CodeToolArgumentInvalid, "unsupported provider status", map[string]any{"status": provider.Status})
	}
	if !validHealthStatus(provider.HealthStatus) {
		return contracts.NewRuntimeError(contracts.CodeToolArgumentInvalid, "unsupported provider health_status", map[string]any{"health_status": provider.HealthStatus})
	}
	if provider.TimeoutMS < 0 || provider.TimeoutMS > maxProviderTimeoutMS {
		return contracts.NewRuntimeError(contracts.CodeToolArgumentInvalid, "provider timeout_ms is out of range", map[string]any{"timeout_ms": provider.TimeoutMS, "max_timeout_ms": maxProviderTimeoutMS})
	}
	if provider.RetryMax < 0 || provider.RetryMax > maxProviderRetryMax {
		return contracts.NewRuntimeError(contracts.CodeToolArgumentInvalid, "provider retry_max is out of range", map[string]any{"retry_max": provider.RetryMax, "max_retry_max": maxProviderRetryMax})
	}
	return nil
}

func validHealthStatus(status string) bool {
	switch status {
	case HealthUnknown, HealthHealthy, HealthUnhealthy:
		return true
	default:
		return false
	}
}

func (s *Service) probeProviderHealth(ctx context.Context, provider ToolProvider) (string, string) {
	if provider.ProviderType == ProviderTypeMCP {
		if _, err := s.fetchMCPCatalog(ctx, provider); err != nil {
			return HealthUnhealthy, err.Error()
		}
		return HealthHealthy, ""
	}
	healthURL := strings.TrimRight(provider.Endpoint, "/") + "/healthz"
	if err := requestJSON(ctx, s.client, http.MethodGet, healthURL, nil, nil, nil, requestOptions{
		Headers:   providerHeaders(provider),
		TimeoutMS: provider.TimeoutMS,
		RetryMax:  provider.RetryMax,
	}); err != nil {
		return HealthUnhealthy, err.Error()
	}
	return HealthHealthy, ""
}

func (s *Service) recordProviderTrace(ctx context.Context, traceID contracts.TraceID, tenantID contracts.TenantID, runID contracts.AgentRunID, taskID contracts.TaskID, eventType string, payload map[string]any) {
	if s.trace == nil || traceID == "" {
		return
	}
	_ = s.trace.Record(ctx, contracts.TraceEvent{
		TraceID:   traceID,
		TenantID:  tenantID,
		SpanID:    contracts.SpanID(idgen.New("span")),
		RunID:     runID,
		TaskID:    taskID,
		Type:      eventType,
		Payload:   payload,
		CreatedAt: s.now(),
	})
}

func validateGroup(group ToolGroup) error {
	if strings.TrimSpace(group.GroupID) == "" {
		return contracts.NewRuntimeError(contracts.CodeToolArgumentInvalid, "group_id is required", nil)
	}
	if group.Status != StatusDraft && group.Status != StatusEnabled && group.Status != StatusDisabled {
		return contracts.NewRuntimeError(contracts.CodeToolArgumentInvalid, "unsupported group status", map[string]any{"status": group.Status})
	}
	return nil
}

func validateManifest(manifest ToolManifest) error {
	if strings.TrimSpace(manifest.ToolID) == "" || strings.TrimSpace(manifest.Name) == "" {
		return contracts.NewRuntimeError(contracts.CodeToolArgumentInvalid, "tool_id and name are required", nil)
	}
	if strings.TrimSpace(manifest.Description) == "" {
		return contracts.NewRuntimeError(contracts.CodeToolArgumentInvalid, "description is required", nil)
	}
	if manifest.InputSchema == nil {
		return contracts.NewRuntimeError(contracts.CodeToolArgumentInvalid, "input_schema is required", nil)
	}
	if err := manifest.RiskLevel.Validate(); err != nil {
		return contracts.NewRuntimeError(contracts.CodeToolArgumentInvalid, err.Error(), nil)
	}
	if !validVisibility(manifest.Visibility) {
		return contracts.NewRuntimeError(contracts.CodeToolArgumentInvalid, "unknown tool visibility", map[string]any{"visibility": manifest.Visibility})
	}
	if manifest.Status != StatusDraft && manifest.Status != StatusEnabled && manifest.Status != StatusDisabled {
		return contracts.NewRuntimeError(contracts.CodeToolArgumentInvalid, "unsupported tool status", map[string]any{"status": manifest.Status})
	}
	if manifest.Executor.Type == "" {
		return contracts.NewRuntimeError(contracts.CodeToolArgumentInvalid, "executor.type is required", nil)
	}
	if manifest.Executor.Type == ExecutorTypeHTTPDirect {
		if strings.TrimSpace(manifest.Executor.URL) == "" {
			return contracts.NewRuntimeError(contracts.CodeToolArgumentInvalid, "executor.url is required for http_direct", nil)
		}
		if manifest.RiskLevel != contracts.RiskLow {
			return contracts.NewRuntimeError(contracts.CodeToolPolicyDenied, "http_direct is limited to low risk tools", map[string]any{"tool_id": manifest.ToolID, "risk_level": manifest.RiskLevel})
		}
	}
	if manifest.Executor.Type == ExecutorTypeStaticToolHost && (strings.TrimSpace(manifest.Executor.ProviderID) == "" || strings.TrimSpace(manifest.Executor.Operation) == "") {
		return contracts.NewRuntimeError(contracts.CodeToolArgumentInvalid, "executor.provider_id and executor.operation are required for static_tool_host", nil)
	}
	if manifest.Executor.Type == ExecutorTypeMCP && (strings.TrimSpace(manifest.Executor.ProviderID) == "" || strings.TrimSpace(manifest.Executor.Operation) == "") {
		return contracts.NewRuntimeError(contracts.CodeToolArgumentInvalid, "executor.provider_id and executor.operation are required for mcp", nil)
	}
	if manifest.Executor.Type == ExecutorTypeAgentTool && strings.TrimSpace(manifest.Executor.ProviderID) == "" {
		return contracts.NewRuntimeError(contracts.CodeToolArgumentInvalid, "executor.provider_id is required for agent_tool", nil)
	}
	return nil
}

func (s *Service) validateHTTPDirectAllowed(manifest ToolManifest) error {
	if s.DisableHTTPDirect && manifest.Executor.Type == ExecutorTypeHTTPDirect {
		return contracts.NewRuntimeError(contracts.CodeToolPolicyDenied, "http_direct tools are disabled by release switch", map[string]any{"tool_id": manifest.ToolID})
	}
	return nil
}

func validVisibility(visibility contracts.ToolVisibility) bool {
	switch visibility {
	case contracts.ToolPrivate, contracts.ToolProtected, contracts.ToolExposed:
		return true
	default:
		return false
	}
}

type HTTPExecutor struct {
	URL      string
	Method   string
	Headers  map[string]string
	Client   *http.Client
	TenantID contracts.TenantID
}

func (e HTTPExecutor) NetworkTargetHost() string {
	return urlHost(e.URL)
}

func (e HTTPExecutor) Execute(ctx context.Context, call contracts.ToolCall) (map[string]any, []contracts.ArtifactRef, error) {
	if e.TenantID != "" && call.TenantID != e.TenantID {
		return nil, nil, contracts.NewRuntimeError(contracts.CodeToolPolicyDenied, "tool tenant does not match call tenant", nil)
	}
	method := e.Method
	if method == "" {
		method = http.MethodPost
	}
	payload := map[string]any{
		"tool_id":      call.ToolID,
		"tool_call_id": call.ToolCallID,
		"tenant_id":    call.TenantID,
		"arguments":    call.Arguments,
	}
	var response toolInvokeResponse
	if err := postJSON(ctx, e.Client, method, e.URL, e.Headers, payload, &response); err != nil {
		return nil, nil, err
	}
	return response.Output, response.ArtifactRefs, nil
}

type ToolHostExecutor struct {
	Endpoint   string
	ProviderID string
	Operation  string
	Headers    map[string]string
	TimeoutMS  int
	RetryMax   int
	Client     *http.Client
	TenantID   contracts.TenantID
	Trace      trace.Recorder
	Now        func() time.Time
}

func (e ToolHostExecutor) NetworkTargetHost() string {
	return urlHost(e.Endpoint)
}

func (e ToolHostExecutor) Execute(ctx context.Context, call contracts.ToolCall) (map[string]any, []contracts.ArtifactRef, error) {
	if e.TenantID != "" && call.TenantID != e.TenantID {
		return nil, nil, contracts.NewRuntimeError(contracts.CodeToolPolicyDenied, "tool tenant does not match call tenant", nil)
	}
	started := executorNow(e.Now)
	e.recordTrace(ctx, call, contracts.TraceToolProviderInvoked, map[string]any{
		"provider_id":  e.ProviderID,
		"tool_id":      call.ToolID,
		"tool_call_id": call.ToolCallID,
		"operation":    e.Operation,
	})
	payload := map[string]any{
		"tool_id":      call.ToolID,
		"operation":    e.Operation,
		"tool_call_id": call.ToolCallID,
		"tenant_id":    call.TenantID,
		"arguments":    call.Arguments,
	}
	if call.TraceID != "" {
		payload["trace_id"] = call.TraceID
	}
	if call.RunID != "" {
		payload["run_id"] = call.RunID
	}
	if call.TaskID != "" {
		payload["task_id"] = call.TaskID
	}
	if call.IdempotencyKey != "" {
		payload["idempotency_key"] = call.IdempotencyKey
	}
	var response toolInvokeResponse
	if err := postJSONWithOptions(ctx, e.Client, http.MethodPost, joinURL(e.Endpoint, "/tools/invoke"), e.Headers, payload, &response, requestOptions{
		TimeoutMS: e.TimeoutMS,
		RetryMax:  e.RetryMax,
	}); err != nil {
		e.recordTrace(ctx, call, contracts.TraceToolProviderFailed, map[string]any{
			"provider_id":  e.ProviderID,
			"tool_id":      call.ToolID,
			"tool_call_id": call.ToolCallID,
			"operation":    e.Operation,
			"latency_ms":   int(executorNow(e.Now).Sub(started).Milliseconds()),
			"error_code":   errorCode(err),
		})
		return nil, nil, err
	}
	e.recordTrace(ctx, call, contracts.TraceToolProviderCompleted, map[string]any{
		"provider_id":  e.ProviderID,
		"tool_id":      call.ToolID,
		"tool_call_id": call.ToolCallID,
		"operation":    e.Operation,
		"latency_ms":   int(executorNow(e.Now).Sub(started).Milliseconds()),
	})
	return response.Output, response.ArtifactRefs, nil
}

func (e ToolHostExecutor) recordTrace(ctx context.Context, call contracts.ToolCall, eventType string, payload map[string]any) {
	if e.Trace == nil || call.TraceID == "" {
		return
	}
	_ = e.Trace.Record(ctx, contracts.TraceEvent{
		TraceID:   call.TraceID,
		TenantID:  call.TenantID,
		SpanID:    contracts.SpanID(idgen.New("span")),
		RunID:     call.RunID,
		TaskID:    call.TaskID,
		Type:      eventType,
		Payload:   payload,
		CreatedAt: executorNow(e.Now),
	})
}

type AgentToolExecutor struct {
	Manifest ToolManifest
	Handler  func() AgentToolHandler
	TenantID contracts.TenantID
}

func (e AgentToolExecutor) Execute(ctx context.Context, call contracts.ToolCall) (map[string]any, []contracts.ArtifactRef, error) {
	if e.TenantID != "" && call.TenantID != e.TenantID {
		return nil, nil, contracts.NewRuntimeError(contracts.CodeToolPolicyDenied, "tool tenant does not match call tenant", nil)
	}
	handler := AgentToolHandler(nil)
	if e.Handler != nil {
		handler = e.Handler()
	}
	if handler == nil {
		return nil, nil, contracts.NewRuntimeError(contracts.CodeExecutionDomainUnavailable, "agent_tool execution handler is not configured", map[string]any{"tool_id": call.ToolID})
	}
	return handler.ExecuteAgentTool(ctx, call, e.Manifest)
}

type toolInvokeResponse struct {
	Output       map[string]any                `json:"output"`
	ArtifactRefs []contracts.ArtifactRef       `json:"artifact_refs,omitempty"`
	Error        *contracts.ToolExecutionError `json:"error,omitempty"`
}

type providerCatalog struct {
	Tools []providerCatalogTool `json:"tools"`
}

type providerCatalogTool struct {
	ToolID       string                   `json:"tool_id"`
	ToolKey      string                   `json:"tool_key,omitempty"`
	ToolIDCamel  string                   `json:"toolId,omitempty"`
	GroupID      string                   `json:"group_id,omitempty"`
	Operation    string                   `json:"operation"`
	Name         string                   `json:"name"`
	Description  string                   `json:"description"`
	WhenToUse    []string                 `json:"when_to_use,omitempty"`
	InputSchema  map[string]any           `json:"input_schema"`
	InputCamel   map[string]any           `json:"inputSchema,omitempty"`
	Parameters   map[string]any           `json:"parameters,omitempty"`
	OutputSchema map[string]any           `json:"output_schema,omitempty"`
	OutputCamel  map[string]any           `json:"outputSchema,omitempty"`
	RiskLevel    contracts.RiskLevel      `json:"risk_level"`
	Risk         contracts.RiskLevel      `json:"risk,omitempty"`
	RiskCamel    contracts.RiskLevel      `json:"riskLevel,omitempty"`
	Visibility   contracts.ToolVisibility `json:"visibility"`
	Version      string                   `json:"version"`
}

func (s *Service) fetchCatalog(ctx context.Context, provider ToolProvider) (providerCatalog, error) {
	if provider.ProviderType == ProviderTypeMCP {
		return s.fetchMCPCatalog(ctx, provider)
	}
	paths := []string{"/tools/catalog", "/tools", "/.well-known/agent-plugin.json"}
	var lastErr error
	for _, path := range paths {
		catalog, err := s.fetchCatalogPath(ctx, provider, path)
		if err != nil {
			lastErr = err
			continue
		}
		catalog.Tools = normalizeProviderCatalogTools(provider.ProviderID, catalog.Tools)
		if len(catalog.Tools) == 0 {
			lastErr = contracts.NewRuntimeError(contracts.CodeToolNotFound, "tool provider catalog is empty", map[string]any{"provider_id": provider.ProviderID, "path": path})
			continue
		}
		return catalog, nil
	}
	if lastErr != nil {
		return providerCatalog{}, lastErr
	}
	return providerCatalog{}, contracts.NewRuntimeError(contracts.CodeToolNotFound, "tool provider catalog is unavailable", map[string]any{"provider_id": provider.ProviderID})
}

func (s *Service) fetchCatalogPath(ctx context.Context, provider ToolProvider, path string) (providerCatalog, error) {
	var raw json.RawMessage
	if err := requestJSON(ctx, s.client, http.MethodGet, joinURL(provider.Endpoint, path), providerHeaders(provider), nil, &raw, requestOptions{
		TimeoutMS: provider.TimeoutMS,
		RetryMax:  provider.RetryMax,
	}); err != nil {
		return providerCatalog{}, err
	}
	return decodeProviderCatalog(raw)
}

func decodeProviderCatalog(raw json.RawMessage) (providerCatalog, error) {
	var catalog providerCatalog
	if err := json.Unmarshal(raw, &catalog); err == nil && len(catalog.Tools) > 0 {
		return catalog, nil
	}
	var tools []providerCatalogTool
	if err := json.Unmarshal(raw, &tools); err == nil && len(tools) > 0 {
		return providerCatalog{Tools: tools}, nil
	}
	var nested struct {
		Manifest     providerCatalog `json:"manifest"`
		Catalog      providerCatalog `json:"catalog"`
		Capabilities providerCatalog `json:"capabilities"`
	}
	if err := json.Unmarshal(raw, &nested); err != nil {
		return providerCatalog{}, err
	}
	switch {
	case len(nested.Manifest.Tools) > 0:
		return nested.Manifest, nil
	case len(nested.Catalog.Tools) > 0:
		return nested.Catalog, nil
	case len(nested.Capabilities.Tools) > 0:
		return nested.Capabilities, nil
	default:
		return providerCatalog{}, contracts.NewRuntimeError(contracts.CodeToolNotFound, "tool provider catalog has no tools", nil)
	}
}

func normalizeProviderCatalogTools(providerID string, tools []providerCatalogTool) []providerCatalogTool {
	out := make([]providerCatalogTool, 0, len(tools))
	for _, item := range tools {
		item = normalizeProviderCatalogTool(providerID, item)
		if strings.TrimSpace(item.ToolID) == "" {
			continue
		}
		out = append(out, item)
	}
	return out
}

func normalizeProviderCatalogTool(providerID string, item providerCatalogTool) providerCatalogTool {
	item.ToolID = firstCatalogString(item.ToolID, item.ToolKey, item.ToolIDCamel, item.Operation, catalogSlug(item.Name))
	if !strings.Contains(item.ToolID, ".") && providerID != "" {
		item.ToolID = providerID + "." + item.ToolID
	}
	item.Name = firstCatalogString(item.Name, item.ToolID)
	if item.Operation == "" {
		item.Operation = item.ToolID
	}
	if item.InputSchema == nil {
		item.InputSchema = item.InputCamel
	}
	if item.InputSchema == nil {
		item.InputSchema = item.Parameters
	}
	if item.InputSchema == nil {
		item.InputSchema = map[string]any{"type": "object"}
	}
	if item.OutputSchema == nil {
		item.OutputSchema = item.OutputCamel
	}
	if item.OutputSchema == nil {
		item.OutputSchema = map[string]any{"type": "object"}
	}
	item.RiskLevel = contracts.RiskLevel(firstCatalogString(string(item.RiskLevel), string(item.Risk), string(item.RiskCamel), string(contracts.RiskLow)))
	if item.Visibility == "" {
		item.Visibility = contracts.ToolProtected
	}
	item.Version = firstCatalogString(item.Version, "v1")
	return item
}

func firstCatalogString(values ...string) string {
	for _, value := range values {
		if trimmed := strings.TrimSpace(value); trimmed != "" {
			return trimmed
		}
	}
	return ""
}

func catalogSlug(value string) string {
	value = strings.ToLower(strings.TrimSpace(value))
	var b strings.Builder
	lastDash := false
	for _, r := range value {
		isAlphaNum := (r >= 'a' && r <= 'z') || (r >= '0' && r <= '9')
		if isAlphaNum {
			b.WriteRune(r)
			lastDash = false
			continue
		}
		if !lastDash && b.Len() > 0 {
			b.WriteByte('_')
			lastDash = true
		}
	}
	return strings.Trim(b.String(), "_")
}

func postJSON(ctx context.Context, client *http.Client, method string, url string, headers map[string]string, payload any, target any) error {
	return postJSONWithOptions(ctx, client, method, url, headers, payload, target, requestOptions{})
}

func postJSONWithOptions(ctx context.Context, client *http.Client, method string, url string, headers map[string]string, payload any, target any, options requestOptions) error {
	options.Headers = mergeHeaders(options.Headers, headers)
	return requestJSON(ctx, client, method, url, nil, payload, target, options)
}

type requestOptions struct {
	Headers   map[string]string
	TimeoutMS int
	RetryMax  int
}

func requestJSON(ctx context.Context, client *http.Client, method string, url string, headers map[string]string, payload any, target any, options requestOptions) error {
	options.Headers = mergeHeaders(options.Headers, headers)
	var data []byte
	if payload != nil {
		var err error
		data, err = json.Marshal(payload)
		if err != nil {
			return err
		}
	}
	if client == nil {
		client = http.DefaultClient
	}
	attempts := options.RetryMax + 1
	if attempts < 1 {
		attempts = 1
	}
	var lastErr error
	for attempt := 1; attempt <= attempts; attempt++ {
		reqCtx, cancel := contextWithTimeout(ctx, options.TimeoutMS)
		var body io.Reader
		if payload != nil {
			body = bytes.NewReader(data)
		}
		req, err := http.NewRequestWithContext(reqCtx, method, url, body)
		if err != nil {
			cancel()
			return err
		}
		if payload != nil {
			req.Header.Set("Content-Type", "application/json")
		}
		for key, value := range options.Headers {
			req.Header.Set(key, value)
		}
		resp, err := client.Do(req)
		cancel()
		if err != nil {
			lastErr = err
			if attempt < attempts {
				continue
			}
			return err
		}
		retry, err := decodeJSONResponse(resp, target)
		if err == nil {
			return nil
		}
		lastErr = err
		if !retry || attempt == attempts {
			return err
		}
	}
	return lastErr
}

func contextWithTimeout(ctx context.Context, timeoutMS int) (context.Context, context.CancelFunc) {
	if timeoutMS <= 0 {
		return ctx, func() {}
	}
	return context.WithTimeout(ctx, time.Duration(timeoutMS)*time.Millisecond)
}

func decodeJSONResponse(resp *http.Response, target any) (bool, error) {
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		body, _ := io.ReadAll(io.LimitReader(resp.Body, 2048))
		err := contracts.NewRuntimeError(contracts.CodeToolExecutionFailed, "http tool request failed", map[string]any{"status": resp.StatusCode, "body": string(body)})
		return resp.StatusCode == http.StatusTooManyRequests || resp.StatusCode >= 500, err
	}
	if target == nil {
		_, _ = io.Copy(io.Discard, resp.Body)
		return false, nil
	}
	if err := json.NewDecoder(resp.Body).Decode(target); err != nil {
		return false, err
	}
	if response, ok := target.(*toolInvokeResponse); ok && response.Error != nil {
		return false, contracts.NewRuntimeError(response.Error.Code, response.Error.Message, response.Error.Details)
	}
	return false, nil
}

func providerHeaders(provider ToolProvider) map[string]string {
	if provider.AuthRef == "" {
		return nil
	}
	return map[string]string{providerAuthRefHeader: provider.AuthRef}
}

func executorNow(now func() time.Time) time.Time {
	if now == nil {
		return time.Now().UTC()
	}
	return now()
}

func errorCode(err error) string {
	var runtimeErr *contracts.RuntimeError
	if errors.As(err, &runtimeErr) {
		return string(runtimeErr.Code)
	}
	return "tool_provider_error"
}

func mergeHeaders(first map[string]string, second map[string]string) map[string]string {
	if len(first) == 0 && len(second) == 0 {
		return nil
	}
	out := map[string]string{}
	for key, value := range first {
		out[key] = value
	}
	for key, value := range second {
		out[key] = value
	}
	return out
}

func urlHost(raw string) string {
	parsed, err := url.Parse(strings.TrimSpace(raw))
	if err != nil {
		return ""
	}
	return parsed.Hostname()
}

func joinURL(base string, path string) string {
	return strings.TrimRight(base, "/") + "/" + strings.TrimLeft(path, "/")
}

func providerKey(tenantID contracts.TenantID, providerID string) string {
	return string(tenantID) + "\x00" + providerID
}

func groupKey(tenantID contracts.TenantID, groupID string) string {
	return string(tenantID) + "\x00" + groupID
}

func manifestKey(tenantID contracts.TenantID, toolID string) string {
	return string(tenantID) + "\x00" + toolID
}

func (s *Service) auditEvent(ctx context.Context, tenantID contracts.TenantID, actorID string, action string, resourceID string, decision string, reason string) {
	if s.audit == nil {
		return
	}
	_ = s.audit.Log(ctx, contracts.AuditEvent{
		AuditID:      idgen.New("audit"),
		TenantID:     tenantID,
		ActorID:      actorID,
		ActorType:    "optimizer",
		Action:       action,
		ResourceType: "tool",
		ResourceID:   resourceID,
		Decision:     decision,
		Reason:       reason,
		CreatedAt:    s.now(),
	})
}
