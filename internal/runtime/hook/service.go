package hook

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"sort"
	"strings"
	"sync"
	"time"

	"znt/internal/contracts"
	tooldiscovery "znt/internal/discovery/tool"
	"znt/internal/governance/audit"
	"znt/internal/governance/trace"
	serviceconnection "znt/internal/serviceconnection"
	"znt/pkg/idgen"
)

type ProviderType string

const (
	ProviderTypeGo             ProviderType = "go"
	ProviderTypeStaticHookHost ProviderType = "static_hook_host"
)

const (
	StatusEnabled  = "enabled"
	StatusDisabled = "disabled"

	HealthUnknown   = "unknown"
	HealthHealthy   = "healthy"
	HealthUnhealthy = "unhealthy"
)

const (
	maxHookContextBlocks      = 8
	maxHookMemoryWriteIntents = 8
	maxHookPlannerHints       = 8
	maxHookTextBytes          = 8192
)

type Provider struct {
	TenantID            contracts.TenantID `json:"tenant_id,omitempty"`
	ProviderID          string             `json:"provider_id"`
	Name                string             `json:"name"`
	Description         string             `json:"description,omitempty"`
	ProviderType        ProviderType       `json:"provider_type"`
	ServiceConnectionID string             `json:"service_connection_id,omitempty"`
	Endpoint            string             `json:"endpoint,omitempty"`
	Status              string             `json:"status"`
	HealthStatus        string             `json:"health_status,omitempty"`
	LastHealthCheckAt   *time.Time         `json:"last_health_check_at,omitempty"`
	LastHealthError     string             `json:"last_health_error,omitempty"`
	Version             string             `json:"version,omitempty"`
	Config              map[string]any     `json:"config,omitempty"`
}

type ProviderCatalog struct {
	ProviderID string         `json:"provider_id,omitempty"`
	Version    string         `json:"version,omitempty"`
	Hooks      []HookManifest `json:"hooks"`
}

type HookManifest struct {
	TenantID         contracts.TenantID `json:"tenant_id,omitempty"`
	HookID           string             `json:"hook_id"`
	ProviderID       string             `json:"provider_id,omitempty"`
	Name             string             `json:"name"`
	Description      string             `json:"description,omitempty"`
	Phase            HookPoint          `json:"phase"`
	Status           string             `json:"status,omitempty"`
	Version          string             `json:"version,omitempty"`
	TimeoutMS        int                `json:"timeout_ms,omitempty"`
	FailurePolicy    string             `json:"failure_policy,omitempty"`
	RequiresApproval bool               `json:"requires_approval,omitempty"`
	ApprovalPolicy   ApprovalPolicy     `json:"approval_policy,omitempty"`
	ConfigSchema     map[string]any     `json:"config_schema,omitempty"`
	RequestSchema    map[string]any     `json:"request_schema,omitempty"`
	PatchSchema      map[string]any     `json:"patch_schema,omitempty"`
}

type HookManifestVersion struct {
	TenantID contracts.TenantID `json:"tenant_id,omitempty"`
	HookID   string             `json:"hook_id"`
	Version  string             `json:"version"`
	Status   string             `json:"status,omitempty"`
	Active   bool               `json:"active"`
	Manifest HookManifest       `json:"manifest"`
}

type Binding struct {
	TenantID         contracts.TenantID     `json:"tenant_id,omitempty"`
	AgentID          contracts.AgentID      `json:"agent_id"`
	AgentVersion     contracts.AgentVersion `json:"agent_version,omitempty"`
	HookID           string                 `json:"hook_id"`
	ProviderType     ProviderType           `json:"provider_type,omitempty"`
	ProviderID       string                 `json:"provider_id,omitempty"`
	Phase            HookPoint              `json:"phase"`
	Version          string                 `json:"version,omitempty"`
	Enabled          bool                   `json:"enabled"`
	TimeoutMS        int                    `json:"timeout_ms,omitempty"`
	FailurePolicy    string                 `json:"failure_policy,omitempty"`
	RequiresApproval bool                   `json:"requires_approval,omitempty"`
	ApprovalPolicy   ApprovalPolicy         `json:"approval_policy,omitempty"`
	Config           map[string]any         `json:"config,omitempty"`
}

type ApprovalPolicy struct {
	RequireApproval bool           `json:"require_approval,omitempty"`
	ProviderTypes   []ProviderType `json:"provider_types,omitempty"`
	Phases          []HookPoint    `json:"phases,omitempty"`
	FailurePolicies []string       `json:"failure_policies,omitempty"`
}

type HookEvent struct {
	EventID      string               `json:"event_id"`
	TenantID     contracts.TenantID   `json:"tenant_id,omitempty"`
	TraceID      contracts.TraceID    `json:"trace_id,omitempty"`
	RunID        contracts.AgentRunID `json:"run_id,omitempty"`
	TaskID       contracts.TaskID     `json:"task_id,omitempty"`
	AgentID      contracts.AgentID    `json:"agent_id,omitempty"`
	HookID       string               `json:"hook_id"`
	ProviderID   string               `json:"provider_id,omitempty"`
	ProviderType ProviderType         `json:"provider_type,omitempty"`
	Phase        HookPoint            `json:"phase"`
	Status       string               `json:"status"`
	Reason       string               `json:"reason,omitempty"`
	LatencyMS    int                  `json:"latency_ms,omitempty"`
	Patch        Patch                `json:"patch,omitempty"`
	CreatedAt    time.Time            `json:"created_at"`
}

type HookEventFilter struct {
	TraceID    contracts.TraceID
	ProviderID string
	HookID     string
	Phase      HookPoint
	Status     string
	From       *time.Time
	To         *time.Time
	Interval   time.Duration
	IntervalID string
}

type HookGovernanceBucket struct {
	ProviderID       string       `json:"provider_id,omitempty"`
	ProviderType     ProviderType `json:"provider_type,omitempty"`
	HookID           string       `json:"hook_id,omitempty"`
	Phase            HookPoint    `json:"phase,omitempty"`
	Status           string       `json:"status"`
	Count            int          `json:"count"`
	LatencyMS        int          `json:"latency_ms,omitempty"`
	AverageLatencyMS int          `json:"average_latency_ms,omitempty"`
}

type HookGovernanceProviderMatrix struct {
	ProviderID       string                               `json:"provider_id,omitempty"`
	ProviderType     ProviderType                         `json:"provider_type,omitempty"`
	TotalEvents      int                                  `json:"total_events"`
	OKEvents         int                                  `json:"ok_events_total"`
	NoOpEvents       int                                  `json:"no_op_events_total"`
	RejectedEvents   int                                  `json:"rejected_events_total"`
	FailedEvents     int                                  `json:"failed_events_total"`
	TimeoutEvents    int                                  `json:"timeout_events_total"`
	LatencyMS        int                                  `json:"latency_ms,omitempty"`
	AverageLatencyMS int                                  `json:"average_latency_ms,omitempty"`
	FailureRate      int                                  `json:"failure_rate_percent,omitempty"`
	Buckets          []HookGovernanceProviderMatrixBucket `json:"buckets"`
}

type HookGovernanceProviderMatrixBucket struct {
	Phase            HookPoint `json:"phase,omitempty"`
	Status           string    `json:"status"`
	Count            int       `json:"count"`
	LatencyMS        int       `json:"latency_ms,omitempty"`
	AverageLatencyMS int       `json:"average_latency_ms,omitempty"`
}

type HookGovernanceSummary struct {
	TenantID         contracts.TenantID             `json:"tenant_id,omitempty"`
	TraceID          contracts.TraceID              `json:"trace_id,omitempty"`
	ProviderID       string                         `json:"provider_id,omitempty"`
	HookID           string                         `json:"hook_id,omitempty"`
	Phase            HookPoint                      `json:"phase,omitempty"`
	Status           string                         `json:"status,omitempty"`
	WindowStart      *time.Time                     `json:"window_start,omitempty"`
	WindowEnd        *time.Time                     `json:"window_end,omitempty"`
	TrendInterval    string                         `json:"trend_interval,omitempty"`
	TotalEvents      int                            `json:"total_events"`
	OKEvents         int                            `json:"ok_events_total"`
	NoOpEvents       int                            `json:"no_op_events_total"`
	RejectedEvents   int                            `json:"rejected_events_total"`
	FailedEvents     int                            `json:"failed_events_total"`
	TimeoutEvents    int                            `json:"timeout_events_total"`
	LatencyMS        int                            `json:"latency_ms,omitempty"`
	AverageLatencyMS int                            `json:"average_latency_ms,omitempty"`
	FailureRate      int                            `json:"failure_rate_percent,omitempty"`
	Buckets          []HookGovernanceBucket         `json:"buckets"`
	Trend            []HookGovernancePoint          `json:"trend,omitempty"`
	ProviderMatrix   []HookGovernanceProviderMatrix `json:"provider_matrix"`
}

type HookGovernancePoint struct {
	WindowStart      time.Time `json:"window_start"`
	WindowEnd        time.Time `json:"window_end"`
	TotalEvents      int       `json:"total_events"`
	OKEvents         int       `json:"ok_events_total"`
	NoOpEvents       int       `json:"no_op_events_total"`
	RejectedEvents   int       `json:"rejected_events_total"`
	FailedEvents     int       `json:"failed_events_total"`
	TimeoutEvents    int       `json:"timeout_events_total"`
	LatencyMS        int       `json:"latency_ms,omitempty"`
	AverageLatencyMS int       `json:"average_latency_ms,omitempty"`
	FailureRate      int       `json:"failure_rate_percent,omitempty"`
}

type Store interface {
	UpsertProvider(ctx context.Context, provider Provider) error
	GetProvider(ctx context.Context, tenantID contracts.TenantID, providerID string) (Provider, bool, error)
	ListProviders(ctx context.Context, tenantID contracts.TenantID) ([]Provider, error)
	UpsertManifest(ctx context.Context, manifest HookManifest) error
	GetManifest(ctx context.Context, tenantID contracts.TenantID, hookID string) (HookManifest, bool, error)
	ListManifests(ctx context.Context, tenantID contracts.TenantID) ([]HookManifest, error)
	GetManifestVersion(ctx context.Context, tenantID contracts.TenantID, hookID string, version string) (HookManifest, bool, error)
	ListManifestVersions(ctx context.Context, tenantID contracts.TenantID, hookID string) ([]HookManifest, error)
	UpsertBinding(ctx context.Context, binding Binding) error
	ListBindings(ctx context.Context, tenantID contracts.TenantID, agentID contracts.AgentID, agentVersion contracts.AgentVersion) ([]Binding, error)
	SaveEvent(ctx context.Context, event HookEvent) error
	ListEvents(ctx context.Context, tenantID contracts.TenantID, traceID contracts.TraceID) ([]HookEvent, error)
}

type ServiceConnectionResolver interface {
	Get(ctx context.Context, tenantID contracts.TenantID, connectionID string) (serviceconnection.ServiceConnection, bool, error)
}

type Service struct {
	mu                 sync.RWMutex
	store              Store
	trace              trace.Recorder
	audit              audit.Logger
	now                func() time.Time
	clients            map[string]*http.Client
	serviceConnections ServiceConnectionResolver
}

type InvokeRequest struct {
	TenantID contracts.TenantID
	TraceID  contracts.TraceID
	RunID    contracts.AgentRunID
	TaskID   contracts.TaskID
	Agent    contracts.AgentDefinition
	Policy   contracts.PolicySet
	Phase    HookPoint
	Payload  map[string]any
}

type InvokeResult struct {
	Status string `json:"status"`
	Reason string `json:"reason,omitempty"`
	Patch  Patch  `json:"patch,omitempty"`
}

func NewService(store Store, traceRecorder trace.Recorder, auditLogger audit.Logger) *Service {
	return &Service{
		store:   store,
		trace:   traceRecorder,
		audit:   auditLogger,
		now:     func() time.Time { return time.Now().UTC() },
		clients: map[string]*http.Client{},
	}
}

func (s *Service) SetServiceConnections(resolver ServiceConnectionResolver) {
	if s == nil {
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	s.serviceConnections = resolver
}

func (s *Service) Observe(ctx context.Context, observation Observation) {
	if s == nil {
		return
	}
	event := HookEvent{
		EventID:   idgen.New("hookevt"),
		TenantID:  observation.TenantID,
		TraceID:   observation.TraceID,
		RunID:     observation.RunID,
		TaskID:    observation.TaskID,
		AgentID:   observation.Agent.AgentID,
		HookID:    string(observation.Event),
		Phase:     HookPoint(observation.Event),
		Status:    "observed",
		Patch:     Patch{},
		CreatedAt: s.now(),
	}
	if s.store != nil {
		_ = s.store.SaveEvent(ctx, event)
	}
	if s.trace != nil {
		_ = s.trace.Record(ctx, contracts.TraceEvent{
			TraceID:  observation.TraceID,
			TenantID: observation.TenantID,
			SpanID:   contracts.SpanID(idgen.New("span")),
			RunID:    observation.RunID,
			TaskID:   observation.TaskID,
			Type:     contracts.TraceRuntimeHookInvoked,
			Payload: map[string]any{
				"hook_id": string(observation.Event),
				"event":   observation.Event,
			},
			CreatedAt: s.now(),
		})
	}
	if s.audit != nil {
		_ = s.audit.Log(ctx, contracts.AuditEvent{
			AuditID:      idgen.New("audit"),
			TenantID:     observation.TenantID,
			ActorID:      string(observation.Agent.AgentID),
			ActorType:    "agent",
			Action:       contracts.AuditRuntimeHookInvoked,
			ResourceType: "runtime_hook",
			ResourceID:   string(observation.Event),
			Decision:     "allowed",
			TraceID:      observation.TraceID,
			TaskID:       observation.TaskID,
			RunID:        observation.RunID,
			CreatedAt:    s.now(),
		})
	}
}

func (s *Service) Apply(ctx context.Context, request TransformRequest) Patch {
	if s == nil {
		return Patch{}
	}
	_, result, err := s.Invoke(ctx, InvokeRequest{
		TenantID: request.TenantID,
		TraceID:  request.TraceID,
		RunID:    request.RunID,
		TaskID:   request.TaskID,
		Agent:    request.Agent,
		Policy:   request.Policy,
		Phase:    request.HookPoint,
		Payload: map[string]any{
			"objective":     request.Objective,
			"candidates":    request.Candidates,
			"work_view":     request.WorkView,
			"prompt_bundle": request.PromptBundle,
		},
	})
	_ = err
	return result.Patch
}

func (s *Service) UpsertProvider(ctx context.Context, provider Provider) error {
	provider = normalizeProvider(provider)
	if provider.ProviderID == "" {
		return contracts.NewRuntimeError(contracts.CodeDecisionSchemaError, "provider_id is required", nil)
	}
	if !validProviderType(provider.ProviderType) {
		return contracts.NewRuntimeError(contracts.CodeDecisionSchemaError, "unsupported hook provider type", map[string]any{"provider_type": provider.ProviderType})
	}
	if provider.ProviderType == ProviderTypeStaticHookHost && provider.Endpoint == "" && provider.ServiceConnectionID == "" {
		return contracts.NewRuntimeError(contracts.CodeDecisionSchemaError, "endpoint or service_connection_id is required for static_hook_host providers", nil)
	}
	if provider.ProviderType == ProviderTypeStaticHookHost && provider.ServiceConnectionID != "" {
		if _, err := s.staticProviderConnection(ctx, provider.TenantID, provider, 0); err != nil {
			return err
		}
	}
	if provider.Status != StatusEnabled && provider.Status != StatusDisabled {
		return contracts.NewRuntimeError(contracts.CodeDecisionSchemaError, "unsupported hook provider status", map[string]any{"status": provider.Status})
	}
	if !validHealthStatus(provider.HealthStatus) {
		return contracts.NewRuntimeError(contracts.CodeDecisionSchemaError, "unsupported hook provider health_status", map[string]any{"health_status": provider.HealthStatus})
	}
	if err := validateSensitiveConfig(provider.Config, "provider.config"); err != nil {
		return err
	}
	if s.store != nil {
		return s.store.UpsertProvider(ctx, provider)
	}
	return nil
}

func (s *Service) UpsertManifest(ctx context.Context, manifest HookManifest) error {
	manifest, err := normalizeHookManifest(manifest)
	if err != nil {
		return err
	}
	if manifest.ProviderID != "" {
		if _, err := s.provider(ctx, manifest.TenantID, manifest.ProviderID); err != nil {
			return err
		}
	}
	if s.store != nil {
		return s.store.UpsertManifest(ctx, manifest)
	}
	return nil
}

func (s *Service) UpsertBinding(ctx context.Context, binding Binding) error {
	binding, err := s.PrepareBinding(ctx, binding)
	if err != nil {
		return err
	}
	if s.store != nil {
		return s.store.UpsertBinding(ctx, binding)
	}
	return nil
}

func (s *Service) PrepareBinding(ctx context.Context, binding Binding) (Binding, error) {
	if binding.AgentID == "" || binding.HookID == "" {
		return Binding{}, contracts.NewRuntimeError(contracts.CodeDecisionSchemaError, "agent_id and hook_id are required", nil)
	}
	if binding.Phase == "" {
		return Binding{}, contracts.NewRuntimeError(contracts.CodeDecisionSchemaError, "phase is required", nil)
	}
	if !validTransformHookPoint(binding.Phase) {
		return Binding{}, contracts.NewRuntimeError(contracts.CodeDecisionSchemaError, "unsupported hook phase", map[string]any{"phase": binding.Phase})
	}
	if binding.ProviderType == "" {
		binding.ProviderType = ProviderTypeGo
	}
	if !validProviderType(binding.ProviderType) {
		return Binding{}, contracts.NewRuntimeError(contracts.CodeDecisionSchemaError, "unsupported hook provider type", map[string]any{"provider_type": binding.ProviderType})
	}
	if binding.ProviderType != ProviderTypeGo && binding.ProviderID == "" {
		return Binding{}, contracts.NewRuntimeError(contracts.CodeDecisionSchemaError, "provider_id is required for non-go hook bindings", nil)
	}
	if binding.TimeoutMS < 0 {
		binding.TimeoutMS = 0
	}
	if binding.FailurePolicy == "" {
		binding.FailurePolicy = "ignore"
	}
	if binding.FailurePolicy != "ignore" && binding.FailurePolicy != "reject" {
		return Binding{}, contracts.NewRuntimeError(contracts.CodeDecisionSchemaError, "unsupported hook failure policy", map[string]any{"failure_policy": binding.FailurePolicy})
	}
	approvalPolicy, err := normalizeApprovalPolicy(binding.ApprovalPolicy, "binding.approval_policy")
	if err != nil {
		return Binding{}, err
	}
	binding.ApprovalPolicy = approvalPolicy
	if err := validateSensitiveConfig(binding.Config, "binding.config"); err != nil {
		return Binding{}, err
	}
	if err := s.validateBindingManifest(ctx, binding.TenantID, binding); err != nil {
		return Binding{}, err
	}
	return binding, nil
}

func (s *Service) ListProviders(ctx context.Context, tenantID contracts.TenantID) ([]Provider, error) {
	if s == nil || s.store == nil {
		return nil, nil
	}
	return s.store.ListProviders(ctx, tenantID)
}

func (s *Service) ListManifests(ctx context.Context, tenantID contracts.TenantID, providerID string, phase HookPoint, status string) ([]HookManifest, error) {
	if s == nil || s.store == nil {
		return nil, nil
	}
	manifests, err := s.store.ListManifests(ctx, tenantID)
	if err != nil {
		return nil, err
	}
	out := make([]HookManifest, 0, len(manifests))
	for _, manifest := range manifests {
		if providerID != "" && manifest.ProviderID != providerID {
			continue
		}
		if phase != "" && manifest.Phase != phase {
			continue
		}
		if status != "" && manifest.Status != status {
			continue
		}
		out = append(out, manifest)
	}
	return out, nil
}

func (s *Service) GetManifest(ctx context.Context, tenantID contracts.TenantID, hookID string) (HookManifest, bool, error) {
	if s == nil || s.store == nil {
		return HookManifest{}, false, nil
	}
	return s.store.GetManifest(ctx, tenantID, strings.TrimSpace(hookID))
}

func (s *Service) ListManifestVersions(ctx context.Context, tenantID contracts.TenantID, hookID string) ([]HookManifestVersion, error) {
	if s == nil || s.store == nil {
		return nil, nil
	}
	hookID = strings.TrimSpace(hookID)
	if hookID == "" {
		return nil, contracts.NewRuntimeError(contracts.CodeDecisionSchemaError, "hook_id is required", nil)
	}
	current, currentOK, err := s.store.GetManifest(ctx, tenantID, hookID)
	if err != nil {
		return nil, err
	}
	manifests, err := s.store.ListManifestVersions(ctx, tenantID, hookID)
	if err != nil {
		return nil, err
	}
	if len(manifests) == 0 && currentOK {
		manifests = []HookManifest{current}
	}
	out := make([]HookManifestVersion, 0, len(manifests))
	for _, manifest := range manifests {
		if manifest.HookID != hookID {
			continue
		}
		out = append(out, hookManifestVersionView(manifest, current, currentOK))
	}
	sort.SliceStable(out, func(i, j int) bool {
		return out[i].Version < out[j].Version
	})
	return out, nil
}

func (s *Service) GetManifestVersion(ctx context.Context, tenantID contracts.TenantID, hookID string, version string) (HookManifestVersion, bool, error) {
	if s == nil || s.store == nil {
		return HookManifestVersion{}, false, nil
	}
	hookID = strings.TrimSpace(hookID)
	version = strings.TrimSpace(version)
	if hookID == "" || version == "" {
		return HookManifestVersion{}, false, contracts.NewRuntimeError(contracts.CodeDecisionSchemaError, "hook_id and version are required", nil)
	}
	current, currentOK, err := s.store.GetManifest(ctx, tenantID, hookID)
	if err != nil {
		return HookManifestVersion{}, false, err
	}
	manifest, ok, err := s.store.GetManifestVersion(ctx, tenantID, hookID, version)
	if err != nil {
		return HookManifestVersion{}, false, err
	}
	if !ok && currentOK && current.Version == version {
		manifest = current
		ok = true
	}
	if !ok {
		return HookManifestVersion{}, false, nil
	}
	return hookManifestVersionView(manifest, current, currentOK), true, nil
}

func (s *Service) ActivateManifestVersion(ctx context.Context, tenantID contracts.TenantID, hookID string, version string) (HookManifestVersion, error) {
	view, ok, err := s.GetManifestVersion(ctx, tenantID, hookID, version)
	if err != nil {
		return HookManifestVersion{}, err
	}
	if !ok {
		return HookManifestVersion{}, contracts.NewRuntimeError(contracts.CodeAgentVersionNotFound, "runtime hook manifest version not found", map[string]any{"hook_id": strings.TrimSpace(hookID), "version": strings.TrimSpace(version)})
	}
	if view.Manifest.Status != StatusEnabled {
		return HookManifestVersion{}, contracts.NewRuntimeError(contracts.CodeDecisionSchemaError, "runtime hook manifest version must be enabled before activation", map[string]any{"hook_id": view.HookID, "version": view.Version, "status": view.Manifest.Status})
	}
	manifest := view.Manifest
	manifest.TenantID = tenantID
	manifest.HookID = view.HookID
	manifest.Version = view.Version
	if err := s.UpsertManifest(ctx, manifest); err != nil {
		return HookManifestVersion{}, err
	}
	activated, _, err := s.GetManifestVersion(ctx, tenantID, hookID, version)
	if err != nil {
		return HookManifestVersion{}, err
	}
	return activated, nil
}

func (s *Service) CheckProviderHealth(ctx context.Context, tenantID contracts.TenantID, providerID string) (Provider, error) {
	return s.CheckProviderHealthForTrace(ctx, tenantID, providerID, "")
}

func (s *Service) CheckProviderHealthForTrace(ctx context.Context, tenantID contracts.TenantID, providerID string, traceID contracts.TraceID) (Provider, error) {
	provider, err := s.provider(ctx, tenantID, strings.TrimSpace(providerID))
	if err != nil {
		return Provider{}, err
	}
	checkedAt := s.now()
	started := checkedAt
	provider.HealthStatus, provider.LastHealthError = s.probeProviderHealth(ctx, provider)
	provider.LastHealthCheckAt = &checkedAt
	if s.store != nil {
		if err := s.store.UpsertProvider(ctx, provider); err != nil {
			return Provider{}, err
		}
	}
	s.recordProviderHealthTrace(ctx, tenantID, traceID, provider, int(s.now().Sub(started).Milliseconds()))
	return provider, nil
}

func (s *Service) ReadProviderCatalog(ctx context.Context, tenantID contracts.TenantID, providerID string) (Provider, ProviderCatalog, error) {
	provider, err := s.provider(ctx, tenantID, strings.TrimSpace(providerID))
	if err != nil {
		return Provider{}, ProviderCatalog{}, err
	}
	if provider.ProviderType != ProviderTypeStaticHookHost {
		return Provider{}, ProviderCatalog{}, contracts.NewRuntimeError(contracts.CodeDecisionSchemaError, "hook provider catalog is only supported for static_hook_host providers", map[string]any{"provider_id": providerID, "provider_type": provider.ProviderType})
	}
	if provider.Status != StatusEnabled {
		return Provider{}, ProviderCatalog{}, contracts.NewRuntimeError(contracts.CodeToolPolicyDenied, "hook provider is not enabled", map[string]any{"provider_id": provider.ProviderID})
	}
	if provider.HealthStatus == HealthUnhealthy {
		return Provider{}, ProviderCatalog{}, contracts.NewRuntimeError(contracts.CodeToolPolicyDenied, "hook provider is unhealthy", map[string]any{"provider_id": provider.ProviderID, "health_status": provider.HealthStatus})
	}
	catalog, err := s.fetchProviderCatalog(ctx, provider)
	if err != nil {
		return Provider{}, ProviderCatalog{}, err
	}
	catalog, err = normalizeProviderCatalog(provider, catalog)
	if err != nil {
		return Provider{}, ProviderCatalog{}, err
	}
	return provider, catalog, nil
}

func (s *Service) SyncProviderCatalog(ctx context.Context, tenantID contracts.TenantID, providerID string) (Provider, []HookManifest, error) {
	provider, catalog, err := s.ReadProviderCatalog(ctx, tenantID, providerID)
	if err != nil {
		return Provider{}, nil, err
	}
	out := make([]HookManifest, 0, len(catalog.Hooks))
	for _, manifest := range catalog.Hooks {
		manifest.TenantID = tenantID
		manifest.ProviderID = provider.ProviderID
		manifest.Status = StatusEnabled
		if err := s.UpsertManifest(ctx, manifest); err != nil {
			return Provider{}, nil, err
		}
		out = append(out, manifest)
	}
	return provider, out, nil
}

func (s *Service) ListBindings(ctx context.Context, tenantID contracts.TenantID, agentID contracts.AgentID, agentVersion contracts.AgentVersion) ([]Binding, error) {
	if s == nil || s.store == nil {
		return nil, nil
	}
	return s.store.ListBindings(ctx, tenantID, agentID, agentVersion)
}

func (s *Service) ListEvents(ctx context.Context, tenantID contracts.TenantID, traceID contracts.TraceID) ([]HookEvent, error) {
	if s == nil || s.store == nil {
		return nil, nil
	}
	return s.store.ListEvents(ctx, tenantID, traceID)
}

func (s *Service) GovernanceSummary(ctx context.Context, tenantID contracts.TenantID, filter HookEventFilter) (HookGovernanceSummary, error) {
	events, err := s.ListEvents(ctx, tenantID, filter.TraceID)
	if err != nil {
		return HookGovernanceSummary{}, err
	}
	summary := HookGovernanceSummary{
		TenantID:       tenantID,
		TraceID:        filter.TraceID,
		ProviderID:     filter.ProviderID,
		HookID:         filter.HookID,
		Phase:          filter.Phase,
		Status:         filter.Status,
		WindowStart:    filter.From,
		WindowEnd:      filter.To,
		TrendInterval:  filter.IntervalID,
		Buckets:        []HookGovernanceBucket{},
		ProviderMatrix: []HookGovernanceProviderMatrix{},
	}
	buckets := map[string]*HookGovernanceBucket{}
	providerMatrix := map[string]*HookGovernanceProviderMatrix{}
	providerMatrixBuckets := map[string]map[string]*HookGovernanceProviderMatrixBucket{}
	trend := map[time.Time]*HookGovernancePoint{}
	for _, event := range events {
		if !hookEventMatches(event, filter) {
			continue
		}
		addHookGovernanceCounts(&summary.TotalEvents, &summary.OKEvents, &summary.NoOpEvents, &summary.RejectedEvents, &summary.FailedEvents, &summary.TimeoutEvents, &summary.LatencyMS, event)
		key := hookGovernanceBucketKey(event)
		bucket, ok := buckets[key]
		if !ok {
			bucket = &HookGovernanceBucket{
				ProviderID:   event.ProviderID,
				ProviderType: event.ProviderType,
				HookID:       event.HookID,
				Phase:        event.Phase,
				Status:       event.Status,
			}
			buckets[key] = bucket
		}
		bucket.Count++
		bucket.LatencyMS += event.LatencyMS
		matrixKey := hookGovernanceProviderMatrixKey(event)
		matrix, ok := providerMatrix[matrixKey]
		if !ok {
			matrix = &HookGovernanceProviderMatrix{
				ProviderID:   event.ProviderID,
				ProviderType: event.ProviderType,
				Buckets:      []HookGovernanceProviderMatrixBucket{},
			}
			providerMatrix[matrixKey] = matrix
			providerMatrixBuckets[matrixKey] = map[string]*HookGovernanceProviderMatrixBucket{}
		}
		addHookGovernanceCounts(&matrix.TotalEvents, &matrix.OKEvents, &matrix.NoOpEvents, &matrix.RejectedEvents, &matrix.FailedEvents, &matrix.TimeoutEvents, &matrix.LatencyMS, event)
		matrixBucketKey := hookGovernanceProviderMatrixBucketKey(event)
		matrixBucket, ok := providerMatrixBuckets[matrixKey][matrixBucketKey]
		if !ok {
			matrixBucket = &HookGovernanceProviderMatrixBucket{
				Phase:  event.Phase,
				Status: event.Status,
			}
			providerMatrixBuckets[matrixKey][matrixBucketKey] = matrixBucket
		}
		matrixBucket.Count++
		matrixBucket.LatencyMS += event.LatencyMS
		if filter.Interval > 0 {
			start := hookGovernanceTrendStart(event.CreatedAt, filter)
			point, ok := trend[start]
			if !ok {
				point = &HookGovernancePoint{
					WindowStart: start,
					WindowEnd:   start.Add(filter.Interval),
				}
				trend[start] = point
			}
			addHookGovernanceCounts(&point.TotalEvents, &point.OKEvents, &point.NoOpEvents, &point.RejectedEvents, &point.FailedEvents, &point.TimeoutEvents, &point.LatencyMS, event)
		}
	}
	if summary.TotalEvents > 0 {
		finalizeHookGovernanceRates(summary.TotalEvents, summary.LatencyMS, summary.RejectedEvents, summary.FailedEvents, summary.TimeoutEvents, &summary.AverageLatencyMS, &summary.FailureRate)
	}
	for _, bucket := range buckets {
		if bucket.Count > 0 {
			bucket.AverageLatencyMS = bucket.LatencyMS / bucket.Count
		}
		summary.Buckets = append(summary.Buckets, *bucket)
	}
	sort.SliceStable(summary.Buckets, func(i, j int) bool {
		left := summary.Buckets[i]
		right := summary.Buckets[j]
		if left.ProviderID != right.ProviderID {
			return left.ProviderID < right.ProviderID
		}
		if left.HookID != right.HookID {
			return left.HookID < right.HookID
		}
		if left.Phase != right.Phase {
			return left.Phase < right.Phase
		}
		return left.Status < right.Status
	})
	for key, matrix := range providerMatrix {
		finalizeHookGovernanceRates(matrix.TotalEvents, matrix.LatencyMS, matrix.RejectedEvents, matrix.FailedEvents, matrix.TimeoutEvents, &matrix.AverageLatencyMS, &matrix.FailureRate)
		for _, bucket := range providerMatrixBuckets[key] {
			if bucket.Count > 0 {
				bucket.AverageLatencyMS = bucket.LatencyMS / bucket.Count
			}
			matrix.Buckets = append(matrix.Buckets, *bucket)
		}
		sort.SliceStable(matrix.Buckets, func(i, j int) bool {
			if matrix.Buckets[i].Phase != matrix.Buckets[j].Phase {
				return matrix.Buckets[i].Phase < matrix.Buckets[j].Phase
			}
			return matrix.Buckets[i].Status < matrix.Buckets[j].Status
		})
		summary.ProviderMatrix = append(summary.ProviderMatrix, *matrix)
	}
	sort.SliceStable(summary.ProviderMatrix, func(i, j int) bool {
		left := summary.ProviderMatrix[i]
		right := summary.ProviderMatrix[j]
		if left.ProviderID != right.ProviderID {
			return left.ProviderID < right.ProviderID
		}
		return left.ProviderType < right.ProviderType
	})
	for _, point := range trend {
		finalizeHookGovernanceRates(point.TotalEvents, point.LatencyMS, point.RejectedEvents, point.FailedEvents, point.TimeoutEvents, &point.AverageLatencyMS, &point.FailureRate)
		summary.Trend = append(summary.Trend, *point)
	}
	sort.SliceStable(summary.Trend, func(i, j int) bool {
		return summary.Trend[i].WindowStart.Before(summary.Trend[j].WindowStart)
	})
	return summary, nil
}

func hookEventMatches(event HookEvent, filter HookEventFilter) bool {
	if filter.From != nil && event.CreatedAt.Before(*filter.From) {
		return false
	}
	if filter.To != nil && event.CreatedAt.After(*filter.To) {
		return false
	}
	if filter.ProviderID != "" && event.ProviderID != filter.ProviderID {
		return false
	}
	if filter.HookID != "" && event.HookID != filter.HookID {
		return false
	}
	if filter.Phase != "" && event.Phase != filter.Phase {
		return false
	}
	if filter.Status != "" && event.Status != filter.Status {
		return false
	}
	return true
}

func hookGovernanceBucketKey(event HookEvent) string {
	return event.ProviderID + "\x00" + string(event.ProviderType) + "\x00" + event.HookID + "\x00" + string(event.Phase) + "\x00" + event.Status
}

func hookGovernanceProviderMatrixKey(event HookEvent) string {
	return event.ProviderID + "\x00" + string(event.ProviderType)
}

func hookGovernanceProviderMatrixBucketKey(event HookEvent) string {
	return string(event.Phase) + "\x00" + event.Status
}

func addHookGovernanceCounts(total, okEvents, noOpEvents, rejectedEvents, failedEvents, timeoutEvents, latencyMS *int, event HookEvent) {
	(*total)++
	*latencyMS += event.LatencyMS
	switch event.Status {
	case "ok":
		(*okEvents)++
	case "no_op":
		(*noOpEvents)++
	case "rejected":
		(*rejectedEvents)++
	case "failed":
		(*failedEvents)++
	case "timeout":
		(*timeoutEvents)++
	}
}

func finalizeHookGovernanceRates(totalEvents, latencyMS, rejectedEvents, failedEvents, timeoutEvents int, averageLatencyMS, failureRate *int) {
	if totalEvents <= 0 {
		return
	}
	*averageLatencyMS = latencyMS / totalEvents
	failures := rejectedEvents + failedEvents + timeoutEvents
	*failureRate = failures * 100 / totalEvents
}

func hookGovernanceTrendStart(createdAt time.Time, filter HookEventFilter) time.Time {
	createdAt = createdAt.UTC()
	if filter.From != nil {
		anchor := filter.From.UTC()
		if createdAt.Before(anchor) {
			return anchor
		}
		elapsed := createdAt.Sub(anchor)
		return anchor.Add((elapsed / filter.Interval) * filter.Interval)
	}
	return createdAt.Truncate(filter.Interval)
}

func (s *Service) recordProviderHealthTrace(ctx context.Context, tenantID contracts.TenantID, traceID contracts.TraceID, provider Provider, latencyMS int) {
	if s.trace == nil || traceID == "" {
		return
	}
	_ = s.trace.Record(ctx, contracts.TraceEvent{
		TraceID:  traceID,
		TenantID: tenantID,
		SpanID:   contracts.SpanID(idgen.New("span")),
		Type:     contracts.TraceRuntimeHookProviderHealthChecked,
		Payload: map[string]any{
			"provider_id":   provider.ProviderID,
			"provider_type": provider.ProviderType,
			"health_status": provider.HealthStatus,
			"latency_ms":    latencyMS,
		},
		CreatedAt: s.now(),
	})
}

func (s *Service) Preview(ctx context.Context, req InvokeRequest) (Patch, error) {
	_, result, err := s.Invoke(ctx, req)
	return result.Patch, err
}

func (s *Service) Invoke(ctx context.Context, req InvokeRequest) (HookEvent, InvokeResult, error) {
	if req.TraceID == "" {
		req.TraceID = contracts.TraceID(idgen.New("trace"))
	}
	bindings, err := s.bindings(ctx, req)
	if err != nil {
		return HookEvent{}, InvokeResult{}, err
	}
	var merged Patch
	for _, binding := range bindings {
		if !binding.Enabled || binding.Phase != req.Phase {
			continue
		}
		if binding.FailurePolicy == "" {
			binding.FailurePolicy = "ignore"
		}
		event := HookEvent{
			EventID:      idgen.New("hookevt"),
			TenantID:     req.TenantID,
			TraceID:      req.TraceID,
			RunID:        req.RunID,
			TaskID:       req.TaskID,
			AgentID:      req.Agent.AgentID,
			HookID:       binding.HookID,
			ProviderID:   binding.ProviderID,
			ProviderType: binding.ProviderType,
			Phase:        req.Phase,
			Status:       "invoked",
			CreatedAt:    s.now(),
		}
		started := event.CreatedAt
		if s.trace != nil {
			_ = s.trace.Record(ctx, contracts.TraceEvent{
				TraceID:  req.TraceID,
				TenantID: req.TenantID,
				SpanID:   contracts.SpanID(idgen.New("span")),
				RunID:    req.RunID,
				TaskID:   req.TaskID,
				Type:     contracts.TraceRuntimeHookInvoked,
				Payload: map[string]any{
					"hook_id":       binding.HookID,
					"phase":         req.Phase,
					"provider_id":   binding.ProviderID,
					"provider_type": binding.ProviderType,
				},
				CreatedAt: s.now(),
			})
		}
		if s.audit != nil {
			_ = s.audit.Log(ctx, contracts.AuditEvent{
				AuditID:      idgen.New("audit"),
				TenantID:     req.TenantID,
				ActorID:      string(req.Agent.AgentID),
				ActorType:    "agent",
				Action:       contracts.AuditRuntimeHookInvoked,
				ResourceType: "runtime_hook",
				ResourceID:   binding.HookID,
				Decision:     "allowed",
				TraceID:      req.TraceID,
				TaskID:       req.TaskID,
				RunID:        req.RunID,
				CreatedAt:    s.now(),
			})
		}
		patch, result, invokeErr := s.invokeBinding(ctx, req, binding)
		if invokeErr == nil {
			patch = annotateContextBlockSources(req, binding, patch)
			result.Patch = patch
		}
		event.Status = result.Status
		event.Reason = result.Reason
		event.Patch = patch
		latencyMS := int(s.now().Sub(started).Milliseconds())
		event.LatencyMS = latencyMS
		if s.store != nil {
			_ = s.store.SaveEvent(ctx, event)
		}
		if invokeErr != nil {
			if s.trace != nil {
				traceType := contracts.TraceRuntimeHookFailed
				if isTimeoutError(invokeErr) {
					traceType = contracts.TraceRuntimeHookTimeout
				}
				_ = s.trace.Record(ctx, contracts.TraceEvent{
					TraceID:  req.TraceID,
					TenantID: req.TenantID,
					SpanID:   contracts.SpanID(idgen.New("span")),
					RunID:    req.RunID,
					TaskID:   req.TaskID,
					Type:     traceType,
					Payload: map[string]any{
						"hook_id":       binding.HookID,
						"phase":         req.Phase,
						"provider_id":   binding.ProviderID,
						"provider_type": binding.ProviderType,
						"latency_ms":    latencyMS,
						"reason":        invokeErr.Error(),
					},
					CreatedAt: s.now(),
				})
			}
			if binding.FailurePolicy == "reject" {
				return event, result, invokeErr
			}
			continue
		}
		if hasPatch(patch) {
			merged = Merge(merged, patch)
			if s.trace != nil {
				_ = s.trace.Record(ctx, contracts.TraceEvent{
					TraceID:  req.TraceID,
					TenantID: req.TenantID,
					SpanID:   contracts.SpanID(idgen.New("span")),
					RunID:    req.RunID,
					TaskID:   req.TaskID,
					Type:     contracts.TraceRuntimeHookApplied,
					Payload: map[string]any{
						"hook_id":       binding.HookID,
						"phase":         req.Phase,
						"provider_id":   binding.ProviderID,
						"provider_type": binding.ProviderType,
						"latency_ms":    latencyMS,
					},
					CreatedAt: s.now(),
				})
			}
			if s.audit != nil {
				_ = s.audit.Log(ctx, contracts.AuditEvent{
					AuditID:      idgen.New("audit"),
					TenantID:     req.TenantID,
					ActorID:      string(req.Agent.AgentID),
					ActorType:    "agent",
					Action:       contracts.AuditRuntimeHookApplied,
					ResourceType: "runtime_hook",
					ResourceID:   binding.HookID,
					Decision:     "allowed",
					TraceID:      req.TraceID,
					TaskID:       req.TaskID,
					RunID:        req.RunID,
					CreatedAt:    s.now(),
				})
			}
		}
	}
	result := InvokeResult{Status: "ok", Patch: merged}
	if s.store != nil {
		_ = s.store.SaveEvent(ctx, HookEvent{
			EventID:   idgen.New("hookevt"),
			TenantID:  req.TenantID,
			TraceID:   req.TraceID,
			RunID:     req.RunID,
			TaskID:    req.TaskID,
			AgentID:   req.Agent.AgentID,
			Phase:     req.Phase,
			Status:    result.Status,
			Patch:     merged,
			CreatedAt: s.now(),
		})
	}
	return HookEvent{}, result, nil
}

func (s *Service) invokeBinding(ctx context.Context, req InvokeRequest, binding Binding) (Patch, InvokeResult, error) {
	if binding.ProviderType == "" {
		binding.ProviderType = ProviderTypeGo
	}
	if err := s.validateBindingManifest(ctx, req.TenantID, binding); err != nil {
		return Patch{}, InvokeResult{Status: "rejected", Reason: err.Error()}, err
	}
	switch binding.ProviderType {
	case ProviderTypeGo:
		provider := Provider{}
		if binding.ProviderID != "" {
			found, err := s.provider(ctx, req.TenantID, binding.ProviderID)
			if err != nil {
				return Patch{}, InvokeResult{Status: "rejected", Reason: err.Error()}, err
			}
			provider = found
			if provider.Status != StatusEnabled {
				err := contracts.NewRuntimeError(contracts.CodeToolPolicyDenied, "hook provider is not enabled", map[string]any{"provider_id": binding.ProviderID})
				return Patch{}, InvokeResult{Status: "rejected", Reason: err.Error()}, err
			}
			if provider.HealthStatus == HealthUnhealthy {
				err := contracts.NewRuntimeError(contracts.CodeToolPolicyDenied, "hook provider is unhealthy", map[string]any{"provider_id": binding.ProviderID, "health_status": provider.HealthStatus})
				return Patch{}, InvokeResult{Status: "rejected", Reason: err.Error()}, err
			}
		}
		config := mergeConfig(provider.Config, binding.Config)
		patch, err := patchFromConfig(config)
		if err != nil {
			return Patch{}, InvokeResult{Status: "rejected", Reason: err.Error()}, err
		}
		if err := validatePatch(req, patch); err != nil {
			return Patch{}, InvokeResult{Status: "rejected", Reason: err.Error()}, err
		}
		if !hasPatch(patch) {
			return Patch{}, InvokeResult{Status: "no_op"}, nil
		}
		return patch, InvokeResult{Status: "ok", Patch: patch}, nil
	case ProviderTypeStaticHookHost:
		if binding.ProviderID == "" {
			return Patch{}, InvokeResult{Status: "rejected", Reason: "provider_id missing"}, contracts.NewRuntimeError(contracts.CodeDecisionSchemaError, "hook provider_id is required", nil)
		}
		provider, err := s.provider(ctx, req.TenantID, binding.ProviderID)
		if err != nil {
			return Patch{}, InvokeResult{Status: "rejected", Reason: err.Error()}, err
		}
		if provider.Status != StatusEnabled {
			err := contracts.NewRuntimeError(contracts.CodeToolPolicyDenied, "hook provider is not enabled", map[string]any{"provider_id": binding.ProviderID})
			return Patch{}, InvokeResult{Status: "rejected", Reason: err.Error()}, err
		}
		if provider.HealthStatus == HealthUnhealthy {
			err := contracts.NewRuntimeError(contracts.CodeToolPolicyDenied, "hook provider is unhealthy", map[string]any{"provider_id": binding.ProviderID, "health_status": provider.HealthStatus})
			return Patch{}, InvokeResult{Status: "rejected", Reason: err.Error()}, err
		}
		return s.invokeStatic(ctx, req, binding, provider)
	default:
		return Patch{}, InvokeResult{Status: "rejected", Reason: "unsupported provider type"}, contracts.NewRuntimeError(contracts.CodeDecisionSchemaError, "unsupported hook provider type", map[string]any{"provider_type": binding.ProviderType})
	}
}

func (s *Service) validateBindingManifest(ctx context.Context, tenantID contracts.TenantID, binding Binding) error {
	if s == nil || s.store == nil || binding.HookID == "" {
		return nil
	}
	manifest, ok, err := s.store.GetManifest(ctx, tenantID, binding.HookID)
	if err != nil {
		return err
	}
	if !ok {
		if binding.ProviderType == ProviderTypeStaticHookHost && binding.ProviderID != "" {
			return contracts.NewRuntimeError(contracts.CodeToolPolicyDenied, "static hook binding requires an enabled hook manifest", map[string]any{"hook_id": binding.HookID, "provider_id": binding.ProviderID})
		}
		return nil
	}
	if binding.ProviderID == "" && manifest.ProviderID != "" {
		return nil
	}
	if binding.ProviderID != "" && manifest.ProviderID != "" && binding.ProviderID != manifest.ProviderID {
		return contracts.NewRuntimeError(contracts.CodeToolPolicyDenied, "hook binding provider does not match manifest provider", map[string]any{"hook_id": binding.HookID, "provider_id": binding.ProviderID, "manifest_provider_id": manifest.ProviderID})
	}
	if manifest.Status != StatusEnabled {
		return contracts.NewRuntimeError(contracts.CodeToolPolicyDenied, "hook manifest is not enabled", map[string]any{"hook_id": binding.HookID, "status": manifest.Status})
	}
	if manifest.Phase != "" && binding.Phase != "" && manifest.Phase != binding.Phase {
		return contracts.NewRuntimeError(contracts.CodeDecisionSchemaError, "hook binding phase does not match manifest phase", map[string]any{"hook_id": binding.HookID, "phase": binding.Phase, "manifest_phase": manifest.Phase})
	}
	return nil
}

func (s *Service) invokeStatic(ctx context.Context, req InvokeRequest, binding Binding, provider Provider) (Patch, InvokeResult, error) {
	timeout := time.Duration(binding.TimeoutMS) * time.Millisecond
	if timeout <= 0 {
		timeout = 300 * time.Millisecond
	}
	connection, err := s.staticProviderConnection(ctx, req.TenantID, provider, timeout)
	if err != nil {
		return Patch{}, InvokeResult{Status: "rejected", Reason: err.Error()}, err
	}
	client := s.clientFor(provider.ProviderID, connection.Timeout)
	payload := map[string]any{
		"hook_id": binding.HookID,
		"phase":   string(req.Phase),
		"request": map[string]any{
			"tenant_id": req.TenantID,
			"agent_id":  req.Agent.AgentID,
			"run_id":    req.RunID,
			"task_id":   req.TaskID,
			"trace_id":  req.TraceID,
			"objective": req.Payload["objective"],
			"payload":   req.Payload,
		},
	}
	body, _ := json.Marshal(payload)
	endpoint := strings.TrimRight(connection.BaseURL, "/") + "/runtime-hooks/invoke"
	attempts := connection.RetryMax + 1
	var resp *http.Response
	var lastErr error
	for attempt := 0; attempt < attempts; attempt++ {
		httpReq, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, bytes.NewReader(body))
		if err != nil {
			return Patch{}, InvokeResult{Status: "rejected", Reason: err.Error()}, err
		}
		httpReq.Header.Set("Content-Type", "application/json")
		if connection.AuthRef != "" {
			httpReq.Header.Set("X-Origin-Provider-Auth-Ref", connection.AuthRef)
		}
		resp, err = client.Do(httpReq)
		if err != nil {
			lastErr = err
			continue
		}
		if resp.StatusCode < 500 || attempt == attempts-1 {
			break
		}
		_, _ = io.Copy(io.Discard, io.LimitReader(resp.Body, 2048))
		_ = resp.Body.Close()
		resp = nil
	}
	if resp == nil {
		if lastErr == nil {
			lastErr = contracts.NewRuntimeError(contracts.CodeToolExecutionFailed, "hook provider request failed", map[string]any{"provider_id": provider.ProviderID})
		}
		status := "failed"
		if isTimeoutError(lastErr) {
			status = "timeout"
		}
		return Patch{}, InvokeResult{Status: status, Reason: lastErr.Error()}, lastErr
	}
	defer resp.Body.Close()
	if resp.StatusCode >= 400 {
		return Patch{}, InvokeResult{Status: "failed", Reason: fmt.Sprintf("hook provider returned %d", resp.StatusCode)}, contracts.NewRuntimeError(contracts.CodeModelError, "hook provider request failed", map[string]any{"status": resp.StatusCode})
	}
	var out struct {
		Status string `json:"status"`
		Reason string `json:"reason"`
		Patch  Patch  `json:"patch"`
	}
	decoder := json.NewDecoder(io.LimitReader(resp.Body, 1<<20))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&out); err != nil {
		return Patch{}, InvokeResult{Status: "failed", Reason: err.Error()}, err
	}
	if out.Status == "" {
		out.Status = "ok"
	}
	if err := validatePatch(req, out.Patch); err != nil {
		return Patch{}, InvokeResult{Status: "rejected", Reason: err.Error()}, err
	}
	return out.Patch, InvokeResult{Status: out.Status, Reason: out.Reason, Patch: out.Patch}, nil
}

func (s *Service) clientFor(providerID string, timeout time.Duration) *http.Client {
	s.mu.Lock()
	defer s.mu.Unlock()
	key := providerID + "\x00" + timeout.String()
	if client, ok := s.clients[key]; ok {
		return client
	}
	client := &http.Client{Timeout: timeout}
	s.clients[key] = client
	return client
}

type staticProviderConnection struct {
	BaseURL  string
	AuthRef  string
	Timeout  time.Duration
	RetryMax int
}

func (s *Service) staticProviderConnection(ctx context.Context, tenantID contracts.TenantID, provider Provider, requestedTimeout time.Duration) (staticProviderConnection, error) {
	if provider.ServiceConnectionID == "" {
		if provider.Endpoint == "" {
			return staticProviderConnection{}, contracts.NewRuntimeError(contracts.CodeDecisionSchemaError, "endpoint or service_connection_id is required for static_hook_host providers", map[string]any{"provider_id": provider.ProviderID})
		}
		return staticProviderConnection{
			BaseURL: provider.Endpoint,
			Timeout: defaultStaticHookTimeout(requestedTimeout),
		}, nil
	}
	resolver := s.serviceConnectionResolver()
	if resolver == nil {
		return staticProviderConnection{}, contracts.NewRuntimeError(contracts.CodeToolPolicyDenied, "service connection service is unavailable", map[string]any{"provider_id": provider.ProviderID, "connection_id": provider.ServiceConnectionID})
	}
	connection, ok, err := resolver.Get(ctx, tenantID, provider.ServiceConnectionID)
	if err != nil {
		return staticProviderConnection{}, err
	}
	if !ok {
		return staticProviderConnection{}, contracts.NewRuntimeError(contracts.CodeToolNotFound, "runtime hook service connection not found", map[string]any{"provider_id": provider.ProviderID, "connection_id": provider.ServiceConnectionID})
	}
	if connection.Status != serviceconnection.StatusEnabled {
		return staticProviderConnection{}, contracts.NewRuntimeError(contracts.CodeToolPolicyDenied, "runtime hook service connection is not enabled", map[string]any{"provider_id": provider.ProviderID, "connection_id": connection.ConnectionID, "status": connection.Status})
	}
	if connection.HealthStatus == serviceconnection.HealthUnhealthy {
		return staticProviderConnection{}, contracts.NewRuntimeError(contracts.CodeToolPolicyDenied, "runtime hook service connection is unhealthy", map[string]any{"provider_id": provider.ProviderID, "connection_id": connection.ConnectionID, "health_status": connection.HealthStatus})
	}
	if strings.TrimSpace(connection.BaseURL) == "" {
		return staticProviderConnection{}, contracts.NewRuntimeError(contracts.CodeToolArgumentInvalid, "runtime hook service connection base_url is required", map[string]any{"provider_id": provider.ProviderID, "connection_id": connection.ConnectionID})
	}
	timeout := requestedTimeout
	if connection.TimeoutMS > 0 {
		timeout = time.Duration(connection.TimeoutMS) * time.Millisecond
	}
	retryMax := connection.RetryMax
	if retryMax < 0 {
		retryMax = 0
	}
	return staticProviderConnection{
		BaseURL:  strings.TrimSpace(connection.BaseURL),
		AuthRef:  strings.TrimSpace(connection.AuthRef),
		Timeout:  defaultStaticHookTimeout(timeout),
		RetryMax: retryMax,
	}, nil
}

func (s *Service) serviceConnectionResolver() ServiceConnectionResolver {
	if s == nil {
		return nil
	}
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.serviceConnections
}

func defaultStaticHookTimeout(timeout time.Duration) time.Duration {
	if timeout > 0 {
		return timeout
	}
	return 300 * time.Millisecond
}

func (s *Service) provider(ctx context.Context, tenantID contracts.TenantID, providerID string) (Provider, error) {
	if s.store == nil {
		return Provider{}, contracts.NewRuntimeError(contracts.CodeToolNotFound, "hook provider not found", map[string]any{"provider_id": providerID})
	}
	provider, ok, err := s.store.GetProvider(ctx, tenantID, providerID)
	if err != nil {
		return Provider{}, err
	}
	if !ok {
		return Provider{}, contracts.NewRuntimeError(contracts.CodeToolNotFound, "hook provider not found", map[string]any{"provider_id": providerID})
	}
	return normalizeProvider(provider), nil
}

func validProviderType(providerType ProviderType) bool {
	switch providerType {
	case ProviderTypeGo, ProviderTypeStaticHookHost:
		return true
	default:
		return false
	}
}

func normalizeProvider(provider Provider) Provider {
	provider.ProviderID = strings.TrimSpace(provider.ProviderID)
	provider.Name = strings.TrimSpace(provider.Name)
	provider.Description = strings.TrimSpace(provider.Description)
	provider.ServiceConnectionID = strings.TrimSpace(provider.ServiceConnectionID)
	provider.Endpoint = strings.TrimSpace(provider.Endpoint)
	if provider.Name == "" {
		provider.Name = provider.ProviderID
	}
	if provider.ProviderType == "" {
		provider.ProviderType = ProviderTypeGo
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

func validHealthStatus(status string) bool {
	switch status {
	case HealthUnknown, HealthHealthy, HealthUnhealthy:
		return true
	default:
		return false
	}
}

func (s *Service) probeProviderHealth(ctx context.Context, provider Provider) (string, string) {
	if provider.ProviderType == ProviderTypeGo {
		return HealthHealthy, ""
	}
	connection, err := s.staticProviderConnection(ctx, provider.TenantID, provider, 2*time.Second)
	if err != nil {
		return HealthUnhealthy, err.Error()
	}
	healthURL := strings.TrimRight(connection.BaseURL, "/") + "/healthz"
	client := s.clientFor(provider.ProviderID, connection.Timeout)
	var lastErr string
	for attempt := 0; attempt <= connection.RetryMax; attempt++ {
		req, err := http.NewRequestWithContext(ctx, http.MethodGet, healthURL, nil)
		if err != nil {
			return HealthUnhealthy, err.Error()
		}
		if connection.AuthRef != "" {
			req.Header.Set("X-Origin-Provider-Auth-Ref", connection.AuthRef)
		}
		resp, err := client.Do(req)
		if err != nil {
			lastErr = err.Error()
			continue
		}
		_, _ = io.Copy(io.Discard, io.LimitReader(resp.Body, 2048))
		_ = resp.Body.Close()
		if resp.StatusCode >= 200 && resp.StatusCode < 300 {
			return HealthHealthy, ""
		}
		lastErr = fmt.Sprintf("healthz returned %d", resp.StatusCode)
		if resp.StatusCode < 500 {
			break
		}
	}
	return HealthUnhealthy, lastErr
}

func (s *Service) fetchProviderCatalog(ctx context.Context, provider Provider) (ProviderCatalog, error) {
	connection, err := s.staticProviderConnection(ctx, provider.TenantID, provider, 2*time.Second)
	if err != nil {
		return ProviderCatalog{}, err
	}
	catalogURL := strings.TrimRight(connection.BaseURL, "/") + "/runtime-hooks/catalog"
	client := s.clientFor(provider.ProviderID, connection.Timeout)
	var resp *http.Response
	var lastErr error
	for attempt := 0; attempt <= connection.RetryMax; attempt++ {
		req, err := http.NewRequestWithContext(ctx, http.MethodGet, catalogURL, nil)
		if err != nil {
			return ProviderCatalog{}, err
		}
		if connection.AuthRef != "" {
			req.Header.Set("X-Origin-Provider-Auth-Ref", connection.AuthRef)
		}
		resp, err = client.Do(req)
		if err != nil {
			lastErr = err
			continue
		}
		if resp.StatusCode < 500 || attempt == connection.RetryMax {
			break
		}
		_, _ = io.Copy(io.Discard, io.LimitReader(resp.Body, 2048))
		_ = resp.Body.Close()
		resp = nil
	}
	if resp == nil {
		if lastErr == nil {
			lastErr = contracts.NewRuntimeError(contracts.CodeToolExecutionFailed, "hook provider catalog request failed", map[string]any{"provider_id": provider.ProviderID})
		}
		return ProviderCatalog{}, lastErr
	}
	defer resp.Body.Close()
	if resp.StatusCode >= 400 {
		return ProviderCatalog{}, contracts.NewRuntimeError(contracts.CodeModelError, "hook provider catalog request failed", map[string]any{"status": resp.StatusCode})
	}
	var catalog ProviderCatalog
	if err := json.NewDecoder(io.LimitReader(resp.Body, 1<<20)).Decode(&catalog); err != nil {
		return ProviderCatalog{}, err
	}
	return catalog, nil
}

func normalizeProviderCatalog(provider Provider, catalog ProviderCatalog) (ProviderCatalog, error) {
	catalog.ProviderID = strings.TrimSpace(catalog.ProviderID)
	if catalog.ProviderID == "" {
		catalog.ProviderID = provider.ProviderID
	}
	if catalog.ProviderID != provider.ProviderID {
		return ProviderCatalog{}, contracts.NewRuntimeError(contracts.CodeDecisionSchemaError, "hook provider catalog provider_id does not match registered provider", map[string]any{"provider_id": provider.ProviderID, "catalog_provider_id": catalog.ProviderID})
	}
	catalog.Version = strings.TrimSpace(catalog.Version)
	if catalog.Version == "" {
		catalog.Version = provider.Version
	}
	seen := map[string]struct{}{}
	for i := range catalog.Hooks {
		hook := &catalog.Hooks[i]
		hook.TenantID = ""
		hook.ProviderID = provider.ProviderID
		hook.Status = StatusEnabled
		normalized, err := normalizeHookManifest(*hook)
		if err != nil {
			return ProviderCatalog{}, err
		}
		*hook = normalized
		if err := validateCatalogSchema(hook.ConfigSchema, fmt.Sprintf("hooks[%d].config_schema", i)); err != nil {
			return ProviderCatalog{}, err
		}
		if err := validateCatalogSchema(hook.RequestSchema, fmt.Sprintf("hooks[%d].request_schema", i)); err != nil {
			return ProviderCatalog{}, err
		}
		if err := validateCatalogSchema(hook.PatchSchema, fmt.Sprintf("hooks[%d].patch_schema", i)); err != nil {
			return ProviderCatalog{}, err
		}
		if _, ok := seen[hook.HookID]; ok {
			return ProviderCatalog{}, contracts.NewRuntimeError(contracts.CodeDecisionSchemaError, "duplicate hook catalog entry", map[string]any{"hook_id": hook.HookID})
		}
		seen[hook.HookID] = struct{}{}
	}
	return catalog, nil
}

func normalizeHookManifest(manifest HookManifest) (HookManifest, error) {
	manifest.HookID = strings.TrimSpace(manifest.HookID)
	manifest.ProviderID = strings.TrimSpace(manifest.ProviderID)
	manifest.Name = strings.TrimSpace(manifest.Name)
	manifest.Description = strings.TrimSpace(manifest.Description)
	manifest.Version = strings.TrimSpace(manifest.Version)
	manifest.Status = strings.TrimSpace(manifest.Status)
	manifest.FailurePolicy = strings.TrimSpace(manifest.FailurePolicy)
	if manifest.HookID == "" || manifest.Phase == "" {
		return HookManifest{}, contracts.NewRuntimeError(contracts.CodeDecisionSchemaError, "hook manifest requires hook_id and phase", nil)
	}
	if manifest.Name == "" {
		manifest.Name = manifest.HookID
	}
	if !validTransformHookPoint(manifest.Phase) {
		return HookManifest{}, contracts.NewRuntimeError(contracts.CodeDecisionSchemaError, "unsupported hook manifest phase", map[string]any{"hook_id": manifest.HookID, "phase": manifest.Phase})
	}
	if manifest.Status == "" {
		manifest.Status = StatusEnabled
	}
	if manifest.Status != StatusEnabled && manifest.Status != StatusDisabled {
		return HookManifest{}, contracts.NewRuntimeError(contracts.CodeDecisionSchemaError, "unsupported hook manifest status", map[string]any{"hook_id": manifest.HookID, "status": manifest.Status})
	}
	if manifest.Version == "" {
		manifest.Version = "v1"
	}
	if manifest.TimeoutMS < 0 {
		return HookManifest{}, contracts.NewRuntimeError(contracts.CodeDecisionSchemaError, "hook manifest timeout_ms cannot be negative", map[string]any{"hook_id": manifest.HookID})
	}
	if manifest.TimeoutMS == 0 {
		manifest.TimeoutMS = 300
	}
	if manifest.FailurePolicy == "" {
		manifest.FailurePolicy = "ignore"
	}
	if manifest.FailurePolicy != "ignore" && manifest.FailurePolicy != "reject" {
		return HookManifest{}, contracts.NewRuntimeError(contracts.CodeDecisionSchemaError, "unsupported hook manifest failure_policy", map[string]any{"hook_id": manifest.HookID, "failure_policy": manifest.FailurePolicy})
	}
	approvalPolicy, err := normalizeApprovalPolicy(manifest.ApprovalPolicy, "manifest.approval_policy")
	if err != nil {
		return HookManifest{}, err
	}
	manifest.ApprovalPolicy = approvalPolicy
	if err := validateCatalogSchema(manifest.ConfigSchema, "manifest.config_schema"); err != nil {
		return HookManifest{}, err
	}
	if err := validateCatalogSchema(manifest.RequestSchema, "manifest.request_schema"); err != nil {
		return HookManifest{}, err
	}
	if err := validateCatalogSchema(manifest.PatchSchema, "manifest.patch_schema"); err != nil {
		return HookManifest{}, err
	}
	return manifest, nil
}

func normalizeApprovalPolicy(policy ApprovalPolicy, path string) (ApprovalPolicy, error) {
	providerTypes := make([]ProviderType, 0, len(policy.ProviderTypes))
	for _, providerType := range policy.ProviderTypes {
		providerType = ProviderType(strings.TrimSpace(string(providerType)))
		if providerType == "" {
			continue
		}
		if !validProviderType(providerType) {
			return ApprovalPolicy{}, contracts.NewRuntimeError(contracts.CodeDecisionSchemaError, "unsupported runtime hook approval policy provider_type", map[string]any{"path": path, "provider_type": providerType})
		}
		providerTypes = append(providerTypes, providerType)
	}
	policy.ProviderTypes = providerTypes
	phases := make([]HookPoint, 0, len(policy.Phases))
	for _, phase := range policy.Phases {
		phase = HookPoint(strings.TrimSpace(string(phase)))
		if phase == "" {
			continue
		}
		if !validTransformHookPoint(phase) {
			return ApprovalPolicy{}, contracts.NewRuntimeError(contracts.CodeDecisionSchemaError, "unsupported runtime hook approval policy phase", map[string]any{"path": path, "phase": phase})
		}
		phases = append(phases, phase)
	}
	policy.Phases = phases
	failurePolicies := make([]string, 0, len(policy.FailurePolicies))
	for _, failurePolicy := range policy.FailurePolicies {
		failurePolicy = strings.TrimSpace(failurePolicy)
		if failurePolicy == "" {
			continue
		}
		if failurePolicy != "ignore" && failurePolicy != "reject" {
			return ApprovalPolicy{}, contracts.NewRuntimeError(contracts.CodeDecisionSchemaError, "unsupported runtime hook approval policy failure_policy", map[string]any{"path": path, "failure_policy": failurePolicy})
		}
		failurePolicies = append(failurePolicies, failurePolicy)
	}
	policy.FailurePolicies = failurePolicies
	return policy, nil
}

func ApprovalPolicyMatches(policy ApprovalPolicy, binding Binding) bool {
	if !policy.RequireApproval {
		return false
	}
	if len(policy.ProviderTypes) > 0 && !providerTypeIn(policy.ProviderTypes, binding.ProviderType) {
		return false
	}
	if len(policy.Phases) > 0 && !hookPointIn(policy.Phases, binding.Phase) {
		return false
	}
	if len(policy.FailurePolicies) > 0 && !stringIn(policy.FailurePolicies, binding.FailurePolicy) {
		return false
	}
	return true
}

func providerTypeIn(values []ProviderType, needle ProviderType) bool {
	for _, value := range values {
		if value == needle {
			return true
		}
	}
	return false
}

func hookPointIn(values []HookPoint, needle HookPoint) bool {
	for _, value := range values {
		if value == needle {
			return true
		}
	}
	return false
}

func stringIn(values []string, needle string) bool {
	for _, value := range values {
		if value == needle {
			return true
		}
	}
	return false
}

func hookManifestVersionView(manifest HookManifest, current HookManifest, currentOK bool) HookManifestVersion {
	return HookManifestVersion{
		TenantID: manifest.TenantID,
		HookID:   manifest.HookID,
		Version:  manifest.Version,
		Status:   manifest.Status,
		Active:   currentOK && current.HookID == manifest.HookID && current.Version == manifest.Version,
		Manifest: manifest,
	}
}

func validateCatalogSchema(schema map[string]any, path string) error {
	if len(schema) == 0 {
		return nil
	}
	return validateSensitiveConfig(schema, path)
}

func validTransformHookPoint(phase HookPoint) bool {
	switch phase {
	case BeforeContextBuild, AfterCandidateRetrieval, BeforeModelCall, BeforeMemoryWrite:
		return true
	default:
		return false
	}
}

func validatePatch(req InvokeRequest, patch Patch) error {
	if err := validatePatchQuotas(patch); err != nil {
		return err
	}
	if err := validatePatchSensitiveFields(patch); err != nil {
		return err
	}
	allowedTools := map[string]struct{}{}
	for _, tool := range req.Agent.Tools.AllowedToolIDs {
		allowedTools[tool] = struct{}{}
	}
	for _, tool := range req.Agent.Tools.ExposedToolIDs {
		allowedTools[tool] = struct{}{}
	}
	for _, tool := range req.Policy.ToolPolicy.AllowedToolIDs {
		allowedTools[tool] = struct{}{}
	}
	deniedTools := map[string]struct{}{}
	for _, tool := range req.Agent.Tools.DeniedToolIDs {
		deniedTools[tool] = struct{}{}
	}
	for _, tool := range req.Policy.ToolPolicy.DeniedToolIDs {
		deniedTools[tool] = struct{}{}
	}
	deniedGroups := map[string]struct{}{}
	for _, groupID := range req.Agent.Tools.DeniedToolGroupIDs {
		deniedGroups[groupID] = struct{}{}
	}
	for _, groupID := range req.Policy.ToolPolicy.DeniedToolGroupIDs {
		deniedGroups[groupID] = struct{}{}
	}
	candidateGroups := map[string]string{}
	if candidates, ok := candidateSet(req.Payload["candidates"]); ok {
		for _, tool := range candidates.Tools {
			if tool.ToolID == "" {
				continue
			}
			allowedTools[tool.ToolID] = struct{}{}
			candidateGroups[tool.ToolID] = tool.GroupID
		}
	}
	if len(patch.ToolRankAdjustments) > 0 {
		for _, adjustment := range patch.ToolRankAdjustments {
			if adjustment.ToolID == "" {
				return contracts.NewRuntimeError(contracts.CodeDecisionSchemaError, "tool rank adjustment requires tool_id", nil)
			}
			if _, denied := deniedTools[adjustment.ToolID]; denied {
				return contracts.NewRuntimeError(contracts.CodeToolPolicyDenied, "hook cannot rank denied tool", map[string]any{"tool_id": adjustment.ToolID})
			}
			if groupID := candidateGroups[adjustment.ToolID]; groupID != "" {
				if _, denied := deniedGroups[groupID]; denied {
					return contracts.NewRuntimeError(contracts.CodeToolPolicyDenied, "hook cannot rank tool from denied group", map[string]any{"tool_id": adjustment.ToolID, "group_id": groupID})
				}
			}
			if _, ok := allowedTools[adjustment.ToolID]; !ok {
				return contracts.NewRuntimeError(contracts.CodeToolPolicyDenied, "hook cannot rank unapproved tool", map[string]any{"tool_id": adjustment.ToolID})
			}
		}
	}
	if len(patch.DropContextRefs) > 0 {
		for _, ref := range patch.DropContextRefs {
			if ref == "policy" || ref == "safety" {
				return contracts.NewRuntimeError(contracts.CodeToolPolicyDenied, "hook cannot drop safety critical context", map[string]any{"context_ref": ref})
			}
		}
	}
	return nil
}

func validatePatchQuotas(patch Patch) error {
	if len(patch.AddContextBlocks) > maxHookContextBlocks {
		return contracts.NewRuntimeError(contracts.CodeToolPolicyDenied, "hook patch exceeds context block quota", map[string]any{
			"limit": maxHookContextBlocks,
		})
	}
	if len(patch.MemoryWriteIntents) > maxHookMemoryWriteIntents {
		return contracts.NewRuntimeError(contracts.CodeToolPolicyDenied, "hook patch exceeds memory write quota", map[string]any{
			"limit": maxHookMemoryWriteIntents,
		})
	}
	if len(patch.PlannerHints) > maxHookPlannerHints {
		return contracts.NewRuntimeError(contracts.CodeToolPolicyDenied, "hook patch exceeds planner hint quota", map[string]any{
			"limit": maxHookPlannerHints,
		})
	}
	for i, block := range patch.AddContextBlocks {
		if len([]byte(block.Content)) > maxHookTextBytes {
			return contracts.NewRuntimeError(contracts.CodeToolPolicyDenied, "hook context block exceeds text quota", map[string]any{
				"path":  fmt.Sprintf("patch.add_context_blocks[%d].content", i),
				"limit": maxHookTextBytes,
			})
		}
	}
	for i, intent := range patch.MemoryWriteIntents {
		if len([]byte(intent.Content)) > maxHookTextBytes {
			return contracts.NewRuntimeError(contracts.CodeToolPolicyDenied, "hook memory write exceeds text quota", map[string]any{
				"path":  fmt.Sprintf("patch.memory_write_intents[%d].content", i),
				"limit": maxHookTextBytes,
			})
		}
	}
	for i, hint := range patch.PlannerHints {
		if len([]byte(hint.Content)) > maxHookTextBytes {
			return contracts.NewRuntimeError(contracts.CodeToolPolicyDenied, "hook planner hint exceeds text quota", map[string]any{
				"path":  fmt.Sprintf("patch.planner_hints[%d].content", i),
				"limit": maxHookTextBytes,
			})
		}
	}
	return nil
}

func validatePatchSensitiveFields(patch Patch) error {
	for i, block := range patch.AddContextBlocks {
		prefix := fmt.Sprintf("patch.add_context_blocks[%d]", i)
		if err := validateSensitiveString(block.Title, prefix+".title", ""); err != nil {
			return err
		}
		if err := validateSensitiveString(block.Content, prefix+".content", ""); err != nil {
			return err
		}
		if err := validateSensitiveConfig(block.Metadata, prefix+".metadata"); err != nil {
			return err
		}
	}
	for i, intent := range patch.MemoryWriteIntents {
		prefix := fmt.Sprintf("patch.memory_write_intents[%d]", i)
		if err := validateSensitiveString(intent.Summary, prefix+".summary", ""); err != nil {
			return err
		}
		if err := validateSensitiveString(intent.Content, prefix+".content", ""); err != nil {
			return err
		}
		if err := validateSensitiveConfig(intent.Metadata, prefix+".metadata"); err != nil {
			return err
		}
	}
	for i, hint := range patch.PlannerHints {
		if err := validateSensitiveString(hint.Content, fmt.Sprintf("patch.planner_hints[%d].content", i), ""); err != nil {
			return err
		}
	}
	return nil
}

func validateSensitiveConfig(config map[string]any, path string) error {
	if len(config) == 0 {
		return nil
	}
	return validateSensitiveValue(config, path, "")
}

func validateSensitiveValue(value any, path string, key string) error {
	switch typed := value.(type) {
	case map[string]any:
		for childKey, childValue := range typed {
			if err := validateSensitiveValue(childValue, path+"."+childKey, childKey); err != nil {
				return err
			}
		}
	case []any:
		for i, item := range typed {
			if err := validateSensitiveValue(item, fmt.Sprintf("%s[%d]", path, i), key); err != nil {
				return err
			}
		}
	case string:
		return validateSensitiveString(typed, path, key)
	}
	return nil
}

func validateSensitiveString(value string, path string, key string) error {
	trimmed := strings.TrimSpace(value)
	if trimmed == "" {
		return nil
	}
	if looksLikeSecretValue(trimmed) || (isSensitiveKey(key) && !looksLikeSecretReference(trimmed)) {
		return contracts.NewRuntimeError(contracts.CodeToolPolicyDenied, "runtime hook contains sensitive value", map[string]any{"path": path})
	}
	return nil
}

func isSensitiveKey(key string) bool {
	normalized := strings.ToLower(strings.ReplaceAll(strings.TrimSpace(key), "-", "_"))
	if normalized == "" || normalized == "credential_ref" || normalized == "secret_ref" {
		return false
	}
	if strings.Contains(normalized, "api_key") ||
		strings.Contains(normalized, "apikey") ||
		strings.Contains(normalized, "password") ||
		strings.Contains(normalized, "private_key") ||
		strings.Contains(normalized, "access_token") ||
		strings.Contains(normalized, "refresh_token") ||
		strings.Contains(normalized, "auth_token") ||
		strings.Contains(normalized, "bearer_token") ||
		strings.Contains(normalized, "service_token") ||
		strings.Contains(normalized, "secret") ||
		strings.Contains(normalized, "credential") {
		return true
	}
	return false
}

func looksLikeSecretReference(value string) bool {
	lower := strings.ToLower(strings.TrimSpace(value))
	for _, prefix := range []string{"cred/", "credential:", "ref:", "secret://", "vault:", "kms:", "tenant:"} {
		if strings.HasPrefix(lower, prefix) {
			return true
		}
	}
	return false
}

func looksLikeSecretValue(value string) bool {
	trimmed := strings.TrimSpace(value)
	lower := strings.ToLower(trimmed)
	if strings.Contains(lower, "-----begin ") && strings.Contains(lower, "private key-----") {
		return true
	}
	for _, prefix := range []string{"sk-", "ghp_", "gho_", "ghu_", "ghs_", "ghr_", "github_pat_", "xoxb-", "xoxp-", "xoxa-", "xoxr-"} {
		if strings.HasPrefix(lower, prefix) && len(trimmed) >= len(prefix)+12 {
			return true
		}
	}
	if strings.HasPrefix(trimmed, "eyJ") && strings.Count(trimmed, ".") == 2 && len(trimmed) > 80 {
		return true
	}
	return false
}

func isTimeoutError(err error) bool {
	var timeoutErr interface {
		Timeout() bool
	}
	return errors.As(err, &timeoutErr) && timeoutErr.Timeout()
}

func candidateSet(value any) (tooldiscovery.CandidateSet, bool) {
	switch candidates := value.(type) {
	case tooldiscovery.CandidateSet:
		return candidates, true
	case *tooldiscovery.CandidateSet:
		if candidates != nil {
			return *candidates, true
		}
	}
	return tooldiscovery.CandidateSet{}, false
}

func (s *Service) bindings(ctx context.Context, req InvokeRequest) ([]Binding, error) {
	out := make([]Binding, 0)
	if s.store != nil {
		stored, err := s.store.ListBindings(ctx, req.TenantID, req.Agent.AgentID, req.Agent.Version)
		if err != nil {
			return nil, err
		}
		out = append(out, stored...)
	}
	if req.Agent.RuntimeHooks.Mode != "disabled" {
		for _, hook := range req.Agent.RuntimeHooks.Hooks {
			if strings.TrimSpace(hook.HookID) == "" {
				continue
			}
			binding := Binding{
				TenantID:         req.TenantID,
				AgentID:          req.Agent.AgentID,
				AgentVersion:     req.Agent.Version,
				HookID:           hook.HookID,
				ProviderType:     ProviderType(strings.TrimSpace(hook.ProviderType)),
				ProviderID:       hook.ProviderID,
				Phase:            HookPoint(strings.TrimSpace(hook.Phase)),
				Version:          hook.Version,
				Enabled:          hook.Enabled,
				TimeoutMS:        hook.TimeoutMS,
				FailurePolicy:    hook.FailurePolicy,
				RequiresApproval: hook.RequiresApproval,
				ApprovalPolicy:   approvalPolicyFromContract(hook.ApprovalPolicy),
				Config:           hook.Config,
			}
			if binding.ProviderType == "" {
				binding.ProviderType = ProviderTypeGo
			}
			if binding.FailurePolicy == "" {
				binding.FailurePolicy = "ignore"
			}
			out = append(out, binding)
		}
	}
	return out, nil
}

func approvalPolicyFromContract(policy contracts.RuntimeHookApprovalPolicy) ApprovalPolicy {
	out := ApprovalPolicy{
		RequireApproval: policy.RequireApproval,
		FailurePolicies: append([]string(nil), policy.FailurePolicies...),
	}
	for _, providerType := range policy.ProviderTypes {
		out.ProviderTypes = append(out.ProviderTypes, ProviderType(providerType))
	}
	for _, phase := range policy.Phases {
		out.Phases = append(out.Phases, HookPoint(phase))
	}
	return out
}

func hasPatch(patch Patch) bool {
	return len(patch.AddContextBlocks) > 0 ||
		len(patch.DropContextRefs) > 0 ||
		len(patch.ToolRankAdjustments) > 0 ||
		len(patch.MemoryWriteIntents) > 0 ||
		len(patch.PlannerHints) > 0
}

func patchFromConfig(config map[string]any) (Patch, error) {
	if len(config) == 0 {
		return Patch{}, nil
	}
	source := config
	if patchValue, ok := config["patch"]; ok {
		if patchMap, ok := patchValue.(map[string]any); ok {
			source = patchMap
		}
	}
	data, err := json.Marshal(source)
	if err != nil {
		return Patch{}, err
	}
	var patch Patch
	if err := json.Unmarshal(data, &patch); err != nil {
		return Patch{}, err
	}
	return patch, nil
}

func annotateContextBlockSources(req InvokeRequest, binding Binding, patch Patch) Patch {
	if len(patch.AddContextBlocks) == 0 {
		return patch
	}
	sourceType := "runtime_hook_context"
	if req.Agent.SourceKind == contracts.AgentSourceKindPlugin &&
		strings.TrimSpace(binding.ProviderID) != "" &&
		strings.TrimSpace(binding.ProviderID) == strings.TrimSpace(req.Agent.SourceProviderID) {
		sourceType = "agent_plugin_context"
	}
	providerID := strings.TrimSpace(binding.ProviderID)
	hookID := strings.TrimSpace(binding.HookID)
	for i := range patch.AddContextBlocks {
		block := &patch.AddContextBlocks[i]
		metadata := copyContextBlockMetadata(block.Metadata)
		metadata["source_type"] = sourceType
		metadata["trust_level"] = "untrusted_external_context"
		if providerID != "" {
			metadata["provider_id"] = providerID
		}
		if hookID != "" {
			metadata["hook_id"] = hookID
		}
		if strings.TrimSpace(metadataString(metadata, "source_ref")) == "" {
			blockID := strings.TrimSpace(block.ID)
			if blockID == "" {
				blockID = fmt.Sprintf("block_%d", i+1)
			}
			metadata["source_ref"] = strings.Join([]string{sourceType, providerID, hookID, blockID}, ":")
		}
		block.Metadata = metadata
	}
	return patch
}

func copyContextBlockMetadata(metadata map[string]any) map[string]any {
	out := make(map[string]any, len(metadata)+4)
	for key, value := range metadata {
		out[key] = value
	}
	return out
}

func metadataString(metadata map[string]any, key string) string {
	if metadata == nil {
		return ""
	}
	if value, ok := metadata[key].(string); ok {
		return strings.TrimSpace(value)
	}
	return ""
}

func mergeConfig(base map[string]any, override map[string]any) map[string]any {
	if len(base) == 0 && len(override) == 0 {
		return nil
	}
	merged := map[string]any{}
	for key, value := range base {
		merged[key] = value
	}
	for key, value := range override {
		merged[key] = value
	}
	return merged
}
