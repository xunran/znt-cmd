package catalog

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"sort"
	"strings"
	"sync"
	"time"

	"znt/internal/contracts"
	"znt/internal/governance/audit"
	"znt/internal/governance/trace"
	serviceconnection "znt/internal/serviceconnection"
	"znt/internal/tool/registry"
	"znt/pkg/idgen"
)

const (
	ProviderTypeStaticToolHost  = "static_tool_host"
	ProviderTypeAgentPlugin     = "agent_plugin_service"
	ProviderTypeMCP             = "mcp"
	ProviderTypeHTTPAPIAdapter  = "http_api_adapter"
	ProviderTypeDatabaseAdapter = "database_adapter"
	ManagedHTTPAPIAdapterID     = "managed_http_api_adapter"
	ManagedDatabaseAdapterID    = "managed_database_adapter"
	ExecutorTypeStaticToolHost  = "static_tool_host"
	ExecutorTypeAgentPlugin     = "agent_plugin_service"
	ExecutorTypeAgentTool       = "agent_tool"
	ExecutorTypeMCP             = "mcp"
	ExecutorTypeHTTPAPIAdapter  = "http_api_adapter"
	ExecutorTypeDatabaseAdapter = "database_adapter"

	StatusDraft    = "draft"
	StatusEnabled  = "enabled"
	StatusDisabled = "disabled"

	HealthUnknown   = "unknown"
	HealthHealthy   = "healthy"
	HealthUnhealthy = "unhealthy"

	providerAuthRefHeader = "X-Origin-Provider-Auth-Ref"

	auditResourceToolProvider         = "tool_provider"
	auditResourceToolGroup            = "tool_group"
	auditResourceToolManifest         = "tool_manifest"
	auditResourceToolAdapterOperation = "tool_adapter_operation"
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
	Type       string `json:"type"`
	ProviderID string `json:"provider_id,omitempty"`
	Operation  string `json:"operation,omitempty"`
}

type ToolProvider struct {
	TenantID            contracts.TenantID `json:"tenant_id,omitempty"`
	ProviderID          string             `json:"provider_id"`
	ProviderType        string             `json:"provider_type"`
	Name                string             `json:"name"`
	Description         string             `json:"description,omitempty"`
	ServiceConnectionID string             `json:"service_connection_id,omitempty"`
	Status              string             `json:"status"`
	HealthStatus        string             `json:"health_status,omitempty"`
	LastHealthCheckAt   *time.Time         `json:"last_health_check_at,omitempty"`
	LastHealthError     string             `json:"last_health_error,omitempty"`
	Version             string             `json:"version,omitempty"`
}

type AdapterOperation struct {
	TenantID            contracts.TenantID       `json:"tenant_id,omitempty"`
	ProviderID          string                   `json:"provider_id"`
	OperationID         string                   `json:"operation_id"`
	ToolID              string                   `json:"tool_id"`
	GroupID             string                   `json:"group_id,omitempty"`
	Name                string                   `json:"name"`
	Description         string                   `json:"description"`
	WhenToUse           []string                 `json:"when_to_use,omitempty"`
	ServiceConnectionID string                   `json:"service_connection_id"`
	Method              string                   `json:"method"`
	Path                string                   `json:"path"`
	Headers             map[string]string        `json:"headers,omitempty"`
	InputSchema         map[string]any           `json:"input_schema"`
	OutputSchema        map[string]any           `json:"output_schema,omitempty"`
	RequestMapping      map[string]any           `json:"request_mapping,omitempty"`
	ResponseMapping     map[string]any           `json:"response_mapping,omitempty"`
	ResourceID          string                   `json:"resource_id,omitempty"`
	QueryTemplate       string                   `json:"query_template,omitempty"`
	ParameterSchema     map[string]any           `json:"parameter_schema,omitempty"`
	MaxRows             int                      `json:"max_rows,omitempty"`
	RedactColumns       []string                 `json:"redact_columns,omitempty"`
	ReadOnly            bool                     `json:"read_only"`
	RiskLevel           contracts.RiskLevel      `json:"risk_level"`
	Visibility          contracts.ToolVisibility `json:"visibility"`
	Status              string                   `json:"status"`
	Version             string                   `json:"version"`
}

type AdapterOperationFromResourceRequest struct {
	ServiceConnectionID string                   `json:"service_connection_id"`
	ResourceID          string                   `json:"resource_id"`
	OperationID         string                   `json:"operation_id,omitempty"`
	ToolID              string                   `json:"tool_id,omitempty"`
	GroupID             string                   `json:"group_id,omitempty"`
	Name                string                   `json:"name,omitempty"`
	Description         string                   `json:"description,omitempty"`
	WhenToUse           []string                 `json:"when_to_use,omitempty"`
	InputSchema         map[string]any           `json:"input_schema,omitempty"`
	OutputSchema        map[string]any           `json:"output_schema,omitempty"`
	QueryTemplate       string                   `json:"query_template,omitempty"`
	ParameterSchema     map[string]any           `json:"parameter_schema,omitempty"`
	MaxRows             int                      `json:"max_rows,omitempty"`
	RedactColumns       []string                 `json:"redact_columns,omitempty"`
	RiskLevel           contracts.RiskLevel      `json:"risk_level,omitempty"`
	Visibility          contracts.ToolVisibility `json:"visibility,omitempty"`
	Status              string                   `json:"status,omitempty"`
	Version             string                   `json:"version,omitempty"`
}

type ServiceConnectionImpact struct {
	ConnectionID string             `json:"connection_id"`
	Providers    []ToolProvider     `json:"providers"`
	Operations   []AdapterOperation `json:"operations"`
	Tools        []ToolManifest     `json:"tools"`
	Summary      map[string]int     `json:"summary"`
}

type ProviderListFilter struct {
	Query          string
	ProviderType   string
	Status         string
	HealthStatus   string
	IncludeManaged bool
	PageSize       int
	Cursor         string
}

type ManifestListFilter struct {
	Query        string
	ProviderID   string
	ExecutorType string
	Status       string
	RiskLevel    contracts.RiskLevel
	Visibility   contracts.ToolVisibility
	PageSize     int
	Cursor       string
}

type Service struct {
	mu               sync.RWMutex
	registry         registry.Registry
	store            Store
	providers        map[string]ToolProvider
	operations       map[string]AdapterOperation
	groups           map[string]ToolGroup
	manifests        map[string]ToolManifest
	client           *http.Client
	openDB           func(driverName string, dataSourceName string) (*sql.DB, error)
	audit            audit.Logger
	trace            trace.Recorder
	connections      *serviceconnection.Service
	now              func() time.Time
	agentToolHandler AgentToolHandler
}

type AgentToolHandler interface {
	ExecuteAgentTool(ctx context.Context, call contracts.ToolCall, manifest ToolManifest) (map[string]any, []contracts.ArtifactRef, error)
}

type Store interface {
	UpsertProvider(ctx context.Context, provider ToolProvider) error
	UpsertAdapterOperation(ctx context.Context, operation AdapterOperation) error
	UpsertGroup(ctx context.Context, group ToolGroup) error
	UpsertManifest(ctx context.Context, manifest ToolManifest) error
	UpsertRuntimeCache(ctx context.Context, tenantID contracts.TenantID, toolID string, version string, status string) error
	ListProviders(ctx context.Context) ([]ToolProvider, error)
	ListAdapterOperations(ctx context.Context) ([]AdapterOperation, error)
	ListGroups(ctx context.Context) ([]ToolGroup, error)
	ListManifests(ctx context.Context) ([]ToolManifest, error)
}

func NewService(runtimeRegistry registry.Registry, auditLogger audit.Logger) *Service {
	return NewServiceWithStore(runtimeRegistry, auditLogger, nil)
}

func NewServiceWithStore(runtimeRegistry registry.Registry, auditLogger audit.Logger, store Store) *Service {
	return &Service{
		registry:   runtimeRegistry,
		store:      store,
		providers:  map[string]ToolProvider{},
		operations: map[string]AdapterOperation{},
		groups:     map[string]ToolGroup{},
		manifests:  map[string]ToolManifest{},
		client:     &http.Client{Timeout: 10 * time.Second},
		openDB:     sql.Open,
		audit:      auditLogger,
		now:        func() time.Time { return time.Now().UTC() },
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
	operations, err := s.store.ListAdapterOperations(ctx)
	if err != nil {
		return err
	}
	s.mu.Lock()
	s.providers = map[string]ToolProvider{}
	for _, provider := range providers {
		provider = normalizeProvider(provider)
		s.providers[providerKey(provider.TenantID, provider.ProviderID)] = provider
	}
	s.operations = map[string]AdapterOperation{}
	for _, operation := range operations {
		operation = normalizeAdapterOperation(operation)
		s.operations[operationKey(operation.TenantID, operation.ProviderID, operation.OperationID)] = operation
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

func (s *Service) SetServiceConnections(connections *serviceconnection.Service) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.connections = connections
}

func (s *Service) AgentToolHandler() AgentToolHandler {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.agentToolHandler
}

func (s *Service) UpsertProvider(ctx context.Context, provider ToolProvider, actorID string) (ToolProvider, error) {
	provider = s.normalizeProviderForUpsert(provider)
	if err := validateProvider(provider); err != nil {
		return ToolProvider{}, err
	}
	if providerIsManagedAdapter(provider.ProviderType) {
		if existing, ok := s.provider(provider.TenantID, provider.ProviderID); ok {
			existing = normalizeProvider(existing)
			if existing.ProviderType != provider.ProviderType {
				return ToolProvider{}, contracts.NewRuntimeError(contracts.CodeToolArgumentInvalid, "managed adapter provider has unexpected provider_type", map[string]any{"provider_id": provider.ProviderID, "provider_type": existing.ProviderType, "expected_provider_type": provider.ProviderType})
			}
		} else if provider, err := s.ensureManagedAdapterProvider(ctx, provider.TenantID, provider.ProviderID, actorID); err != nil {
			return ToolProvider{}, err
		} else {
			return provider, nil
		}
	}
	if providerUsesProviderConnection(provider.ProviderType) {
		if _, err := s.providerConnectionRef(ctx, provider, false); err != nil {
			return ToolProvider{}, err
		}
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
	auditReason := ""
	if providerIsManagedAdapter(provider.ProviderType) {
		auditReason = "managed_adapter_update"
	}
	s.auditEvent(ctx, provider.TenantID, actorID, contracts.AuditToolProviderUpserted, auditResourceToolProvider, provider.ProviderID, "allowed", auditReason)
	return provider, nil
}

func (s *Service) normalizeProviderForUpsert(provider ToolProvider) ToolProvider {
	provider = normalizeProvider(provider)
	if existing, ok := s.provider(provider.TenantID, provider.ProviderID); ok {
		existing = normalizeProvider(existing)
		if providerHealthScopeMatches(existing, provider) {
			provider.HealthStatus = existing.HealthStatus
			provider.LastHealthCheckAt = existing.LastHealthCheckAt
			provider.LastHealthError = existing.LastHealthError
		} else {
			provider.HealthStatus = HealthUnknown
			provider.LastHealthCheckAt = nil
			provider.LastHealthError = ""
		}
		return provider
	}
	provider.HealthStatus = HealthUnknown
	provider.LastHealthCheckAt = nil
	provider.LastHealthError = ""
	return provider
}

func providerHealthScopeMatches(existing ToolProvider, next ToolProvider) bool {
	return strings.TrimSpace(existing.ProviderType) == strings.TrimSpace(next.ProviderType) &&
		strings.TrimSpace(existing.ServiceConnectionID) == strings.TrimSpace(next.ServiceConnectionID)
}

func (s *Service) UpsertAdapterOperation(ctx context.Context, operation AdapterOperation, actorID string) (AdapterOperation, error) {
	operation = normalizeAdapterOperation(operation)
	if _, err := s.ensureManagedAdapterProvider(ctx, operation.TenantID, operation.ProviderID, actorID); err != nil {
		return AdapterOperation{}, err
	}
	if err := s.validateAdapterOperation(ctx, operation, false); err != nil {
		return AdapterOperation{}, err
	}
	if s.store != nil {
		if err := s.store.UpsertAdapterOperation(ctx, operation); err != nil {
			return AdapterOperation{}, err
		}
	}
	manifests := s.storeOperationLocked(operation)
	if err := s.applyManifests(ctx, manifests); err != nil {
		return AdapterOperation{}, err
	}
	s.auditEvent(ctx, operation.TenantID, actorID, contracts.AuditToolAdapterOperationUpserted, auditResourceToolAdapterOperation, operation.OperationID, "allowed", "")
	return operation, nil
}

func (s *Service) UpsertAdapterOperationFromResource(ctx context.Context, tenantID contracts.TenantID, providerID string, request AdapterOperationFromResourceRequest, actorID string) (AdapterOperation, error) {
	if s.connections == nil {
		return AdapterOperation{}, contracts.NewRuntimeError(contracts.CodeToolPolicyDenied, "service connection service is unavailable", nil)
	}
	connectionID := strings.TrimSpace(request.ServiceConnectionID)
	resourceID := strings.TrimSpace(request.ResourceID)
	if connectionID == "" || resourceID == "" {
		return AdapterOperation{}, contracts.NewRuntimeError(contracts.CodeToolArgumentInvalid, "service_connection_id and resource_id are required", nil)
	}
	connection, ok, err := s.connections.Get(ctx, tenantID, connectionID)
	if err != nil {
		return AdapterOperation{}, err
	}
	if !ok {
		return AdapterOperation{}, contracts.NewRuntimeError(contracts.CodeToolNotFound, "service connection not found", map[string]any{"connection_id": connectionID})
	}
	resource, ok, err := s.findConnectionResource(ctx, tenantID, connectionID, resourceID)
	if err != nil {
		return AdapterOperation{}, err
	}
	if !ok {
		return AdapterOperation{}, contracts.NewRuntimeError(contracts.CodeToolNotFound, "service connection resource not found", map[string]any{"connection_id": connectionID, "resource_id": resourceID})
	}
	var operation AdapterOperation
	switch providerID {
	case ManagedHTTPAPIAdapterID:
		if connection.ConnectionType != serviceconnection.TypeHTTPAPI {
			return AdapterOperation{}, contracts.NewRuntimeError(contracts.CodeToolArgumentInvalid, "http_api operation generation requires http_api service connection", map[string]any{"connection_id": connectionID, "connection_type": connection.ConnectionType})
		}
		operation, err = s.httpAPIAdapterOperationFromResource(tenantID, providerID, connection, resource, request)
	case ManagedDatabaseAdapterID:
		if connection.ConnectionType != serviceconnection.TypeDatabase {
			return AdapterOperation{}, contracts.NewRuntimeError(contracts.CodeToolArgumentInvalid, "database operation generation requires database service connection", map[string]any{"connection_id": connectionID, "connection_type": connection.ConnectionType})
		}
		operation, err = s.databaseAdapterOperationFromResource(tenantID, providerID, connection, resource, request)
	default:
		return AdapterOperation{}, contracts.NewRuntimeError(contracts.CodeToolArgumentInvalid, "adapter operation generation requires a managed adapter provider", map[string]any{"provider_id": providerID})
	}
	if err != nil {
		return AdapterOperation{}, err
	}
	return s.UpsertAdapterOperation(ctx, operation, actorID)
}

func (s *Service) UpsertHTTPAPIAdapterOperationFromResource(ctx context.Context, tenantID contracts.TenantID, providerID string, request AdapterOperationFromResourceRequest, actorID string) (AdapterOperation, error) {
	if providerID != ManagedHTTPAPIAdapterID {
		return AdapterOperation{}, contracts.NewRuntimeError(contracts.CodeToolArgumentInvalid, "http_api operation generation requires managed_http_api_adapter provider", map[string]any{"provider_id": providerID})
	}
	return s.UpsertAdapterOperationFromResource(ctx, tenantID, providerID, request, actorID)
}

func (s *Service) PublishAdapterOperation(ctx context.Context, tenantID contracts.TenantID, providerID string, operationID string, actorID string) (ToolManifest, error) {
	operation, ok := s.adapterOperation(tenantID, providerID, operationID)
	if !ok {
		return ToolManifest{}, contracts.NewRuntimeError(contracts.CodeToolNotFound, "adapter operation not found", map[string]any{"provider_id": providerID, "operation_id": operationID})
	}
	if err := s.validateAdapterOperation(ctx, operation, true); err != nil {
		return ToolManifest{}, err
	}
	manifest, ok := s.manifestForAdapterOperation(operation)
	if !ok {
		return ToolManifest{}, contracts.NewRuntimeError(contracts.CodeToolPolicyDenied, "adapter operation is not enabled", map[string]any{"provider_id": providerID, "operation_id": operationID, "status": operation.Status})
	}
	published, err := s.upsertManifest(ctx, manifest, actorID, true)
	if err != nil {
		return ToolManifest{}, err
	}
	s.auditEvent(ctx, operation.TenantID, actorID, contracts.AuditToolAdapterOperationPublished, auditResourceToolAdapterOperation, operation.OperationID, "allowed", published.ToolID)
	return published, nil
}

func (s *Service) TestAdapterOperation(ctx context.Context, tenantID contracts.TenantID, operation AdapterOperation, arguments map[string]any, actorID string) (map[string]any, error) {
	operation.TenantID = tenantID
	operation = normalizeAdapterOperation(operation)
	if err := s.validateAdapterOperation(ctx, operation, true); err != nil {
		s.auditEvent(ctx, tenantID, actorID, contracts.AuditToolAdapterOperationTested, auditResourceToolAdapterOperation, operation.OperationID, "failed", errorCode(err))
		return nil, err
	}
	connection, err := s.operationConnection(ctx, operation, true)
	if err != nil {
		s.auditEvent(ctx, tenantID, actorID, contracts.AuditToolAdapterOperationTested, auditResourceToolAdapterOperation, operation.OperationID, "failed", errorCode(err))
		return nil, err
	}
	provider, ok := s.provider(tenantID, operation.ProviderID)
	if !ok {
		err := contracts.NewRuntimeError(contracts.CodeToolNotFound, "tool provider not found", map[string]any{"provider_id": operation.ProviderID})
		s.auditEvent(ctx, tenantID, actorID, contracts.AuditToolAdapterOperationTested, auditResourceToolAdapterOperation, operation.OperationID, "failed", errorCode(err))
		return nil, err
	}
	var executor registry.Executor
	switch provider.ProviderType {
	case ProviderTypeHTTPAPIAdapter:
		executor = HTTPAPIAdapterExecutor{
			Endpoint:   connection.BaseURL,
			ProviderID: operation.ProviderID,
			Operation:  operation,
			Headers:    mergeHeaders(connectionHeaders(connection), operation.Headers),
			TimeoutMS:  connection.TimeoutMS,
			RetryMax:   connection.RetryMax,
			Client:     s.client,
			TenantID:   tenantID,
			Trace:      s.trace,
			Now:        s.now,
		}
	case ProviderTypeDatabaseAdapter:
		executor = DatabaseAdapterExecutor{
			Connection: connection,
			ProviderID: provider.ProviderID,
			Operation:  operation,
			TenantID:   tenantID,
			Trace:      s.trace,
			Now:        s.now,
			OpenDB:     s.openDB,
		}
	default:
		err := contracts.NewRuntimeError(contracts.CodeToolArgumentInvalid, "unsupported adapter provider_type", map[string]any{"provider_type": provider.ProviderType})
		s.auditEvent(ctx, tenantID, actorID, contracts.AuditToolAdapterOperationTested, auditResourceToolAdapterOperation, operation.OperationID, "failed", errorCode(err))
		return nil, err
	}
	output, _, err := executor.Execute(ctx, contracts.ToolCall{
		TenantID:  tenantID,
		ToolID:    operation.ToolID,
		Arguments: arguments,
	})
	if err != nil {
		s.auditEvent(ctx, tenantID, actorID, contracts.AuditToolAdapterOperationTested, auditResourceToolAdapterOperation, operation.OperationID, "failed", errorCode(err))
		return nil, err
	}
	s.auditEvent(ctx, tenantID, actorID, contracts.AuditToolAdapterOperationTested, auditResourceToolAdapterOperation, operation.OperationID, "allowed", "")
	return output, nil
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
	s.auditEvent(ctx, group.TenantID, actorID, contracts.AuditToolGroupUpserted, auditResourceToolGroup, group.GroupID, "allowed", "")
	return group, nil
}

func (s *Service) UpsertManifest(ctx context.Context, manifest ToolManifest, actorID string) (ToolManifest, error) {
	return s.upsertManifest(ctx, manifest, actorID, false)
}

func (s *Service) upsertManifest(ctx context.Context, manifest ToolManifest, actorID string, allowManagedInternal bool) (ToolManifest, error) {
	manifest = normalizeManifest(manifest)
	var err error
	manifest, err = s.manifestWithConnectionProfile(ctx, manifest)
	if err != nil {
		return ToolManifest{}, err
	}
	if err := validateManifest(manifest, allowManagedInternal); err != nil {
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
	s.auditEvent(ctx, manifest.TenantID, actorID, contracts.AuditToolManifestUpserted, auditResourceToolManifest, manifest.ToolID, "allowed", "")
	return manifest, nil
}

func (s *Service) manifestWithConnectionProfile(ctx context.Context, manifest ToolManifest) (ToolManifest, error) {
	if manifest.ExecutionProfile != "" {
		return manifest, nil
	}
	if !executorUsesProvider(manifest.Executor.Type) {
		return normalizeManifest(manifest), nil
	}
	var connection serviceconnection.ServiceConnection
	var err error
	switch manifest.Executor.Type {
	case ExecutorTypeStaticToolHost, ExecutorTypeAgentPlugin, ExecutorTypeMCP:
		provider, ok := s.provider(manifest.TenantID, manifest.Executor.ProviderID)
		if !ok || strings.TrimSpace(provider.ServiceConnectionID) == "" {
			return normalizeManifest(manifest), nil
		}
		connection, err = s.providerConnectionRef(ctx, provider, false)
	case ExecutorTypeHTTPAPIAdapter:
		operation, ok := s.adapterOperation(manifest.TenantID, manifest.Executor.ProviderID, manifest.Executor.Operation)
		if !ok || strings.TrimSpace(operation.ServiceConnectionID) == "" {
			return normalizeManifest(manifest), nil
		}
		connection, err = s.operationConnection(ctx, operation, false)
	case ExecutorTypeDatabaseAdapter:
		return normalizeManifest(manifest), nil
	default:
		return normalizeManifest(manifest), nil
	}
	if err != nil {
		return ToolManifest{}, err
	}
	return normalizeManifestWithConnection(manifest, connection), nil
}

func (s *Service) SyncProviderCatalog(ctx context.Context, tenantID contracts.TenantID, providerID string, actorID string) ([]ToolManifest, error) {
	provider, ok := s.provider(tenantID, providerID)
	if !ok {
		return nil, contracts.NewRuntimeError(contracts.CodeToolNotFound, "tool provider not found", map[string]any{"provider_id": providerID})
	}
	if provider.Status != StatusEnabled {
		return nil, contracts.NewRuntimeError(contracts.CodeToolPolicyDenied, "tool provider is not enabled", map[string]any{"provider_id": providerID})
	}
	if !providerIsManagedAdapter(provider.ProviderType) && provider.HealthStatus == HealthUnhealthy {
		return nil, contracts.NewRuntimeError(contracts.CodeToolPolicyDenied, "tool provider is unhealthy", map[string]any{"provider_id": providerID, "health_status": provider.HealthStatus})
	}
	if providerIsManagedAdapter(provider.ProviderType) {
		operations := s.ListAdapterOperations(tenantID, providerID)
		out := make([]ToolManifest, 0, len(operations))
		for _, operation := range operations {
			if operation.Status != StatusEnabled {
				continue
			}
			manifest, ok := s.manifestForAdapterOperation(operation)
			if !ok {
				continue
			}
			installed, err := s.upsertManifest(ctx, manifest, actorID, true)
			if err != nil {
				return nil, err
			}
			out = append(out, installed)
		}
		s.auditEvent(ctx, tenantID, actorID, contracts.AuditToolProviderSynced, auditResourceToolProvider, providerID, "allowed", fmt.Sprintf("operations=%d", len(out)))
		return out, nil
	}
	catalog, err := s.fetchCatalog(ctx, provider)
	if err != nil {
		return nil, err
	}
	out := make([]ToolManifest, 0, len(catalog.Tools))
	executorType := executorTypeForProvider(provider.ProviderType)
	if executorType == "" {
		return nil, contracts.NewRuntimeError(contracts.CodeToolArgumentInvalid, "unsupported provider_type", map[string]any{"provider_type": provider.ProviderType})
	}
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
				Type:       executorType,
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
	s.auditEvent(ctx, tenantID, actorID, contracts.AuditToolProviderSynced, auditResourceToolProvider, providerID, "allowed", fmt.Sprintf("tools=%d", len(out)))
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
	healthStatus, healthError := s.probeProviderHealth(ctx, provider)
	provider, err := s.storeProviderHealth(ctx, provider, checkedAt, healthStatus, healthError)
	if err != nil {
		return ToolProvider{}, err
	}
	s.auditEvent(ctx, provider.TenantID, actorID, contracts.AuditToolProviderHealthChecked, auditResourceToolProvider, provider.ProviderID, "allowed", provider.HealthStatus)
	s.recordProviderTrace(ctx, traceID, tenantID, "", "", contracts.TraceToolProviderHealthChecked, map[string]any{
		"provider_id":   provider.ProviderID,
		"provider_type": provider.ProviderType,
		"health_status": provider.HealthStatus,
		"latency_ms":    int(s.now().Sub(started).Milliseconds()),
	})
	return provider, nil
}

func (s *Service) setProviderHealth(ctx context.Context, tenantID contracts.TenantID, providerID string, status string, healthError string) (ToolProvider, error) {
	provider, ok := s.provider(tenantID, providerID)
	if !ok {
		return ToolProvider{}, contracts.NewRuntimeError(contracts.CodeToolNotFound, "tool provider not found", map[string]any{"provider_id": providerID})
	}
	return s.storeProviderHealth(ctx, provider, s.now(), status, healthError)
}

func (s *Service) storeProviderHealth(ctx context.Context, provider ToolProvider, checkedAt time.Time, status string, healthError string) (ToolProvider, error) {
	provider = normalizeProvider(provider)
	status = strings.TrimSpace(status)
	if !validHealthStatus(status) {
		return ToolProvider{}, contracts.NewRuntimeError(contracts.CodeToolArgumentInvalid, "unsupported provider health_status", map[string]any{"health_status": status})
	}
	provider.HealthStatus = status
	provider.LastHealthError = strings.TrimSpace(healthError)
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
	return provider, nil
}

func (s *Service) ListManifests(tenantID contracts.TenantID) []ToolManifest {
	return s.ListManifestsFiltered(tenantID, ManifestListFilter{})
}

func (s *Service) ListManifestsFiltered(tenantID contracts.TenantID, filter ManifestListFilter) []ToolManifest {
	filter = normalizeManifestListFilter(filter)
	s.mu.RLock()
	defer s.mu.RUnlock()
	out := make([]ToolManifest, 0, len(s.manifests))
	for _, manifest := range s.manifests {
		if manifest.TenantID != tenantID && manifest.TenantID != "" {
			continue
		}
		if !manifestMatchesFilter(manifest, filter) {
			continue
		}
		out = append(out, manifest)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].ToolID < out[j].ToolID })
	return paginateManifests(out, filter.Cursor, filter.PageSize)
}

func (s *Service) ListProviders(tenantID contracts.TenantID) []ToolProvider {
	return s.ListProvidersFiltered(tenantID, ProviderListFilter{IncludeManaged: true})
}

func (s *Service) ListProvidersFiltered(tenantID contracts.TenantID, filter ProviderListFilter) []ToolProvider {
	filter = normalizeProviderListFilter(filter)
	s.mu.RLock()
	defer s.mu.RUnlock()
	out := make([]ToolProvider, 0, len(s.providers))
	for _, provider := range s.providers {
		if provider.TenantID != tenantID && provider.TenantID != "" {
			continue
		}
		if !providerMatchesFilter(provider, filter) {
			continue
		}
		out = append(out, provider)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].ProviderID < out[j].ProviderID })
	return paginateProviders(out, filter.Cursor, filter.PageSize)
}

func normalizeProviderListFilter(filter ProviderListFilter) ProviderListFilter {
	filter.Query = strings.TrimSpace(filter.Query)
	filter.ProviderType = strings.TrimSpace(filter.ProviderType)
	filter.Status = strings.TrimSpace(filter.Status)
	filter.HealthStatus = strings.TrimSpace(filter.HealthStatus)
	filter.Cursor = strings.TrimSpace(filter.Cursor)
	filter.PageSize = normalizePageSize(filter.PageSize)
	return filter
}

func normalizeManifestListFilter(filter ManifestListFilter) ManifestListFilter {
	filter.Query = strings.TrimSpace(filter.Query)
	filter.ProviderID = strings.TrimSpace(filter.ProviderID)
	filter.ExecutorType = strings.TrimSpace(filter.ExecutorType)
	filter.Status = strings.TrimSpace(filter.Status)
	filter.RiskLevel = contracts.RiskLevel(strings.TrimSpace(string(filter.RiskLevel)))
	filter.Visibility = contracts.ToolVisibility(strings.TrimSpace(string(filter.Visibility)))
	filter.Cursor = strings.TrimSpace(filter.Cursor)
	filter.PageSize = normalizePageSize(filter.PageSize)
	return filter
}

func providerMatchesFilter(provider ToolProvider, filter ProviderListFilter) bool {
	if !filter.IncludeManaged && providerIsManagedAdapter(provider.ProviderType) {
		return false
	}
	if filter.ProviderType != "" && provider.ProviderType != filter.ProviderType {
		return false
	}
	if filter.Status != "" && provider.Status != filter.Status {
		return false
	}
	if filter.HealthStatus != "" && provider.HealthStatus != filter.HealthStatus {
		return false
	}
	if !matchesTextQuery(filter.Query, provider.ProviderID, provider.Name, provider.Description, provider.ServiceConnectionID) {
		return false
	}
	return true
}

func manifestMatchesFilter(manifest ToolManifest, filter ManifestListFilter) bool {
	if filter.ProviderID != "" && manifest.Executor.ProviderID != filter.ProviderID {
		return false
	}
	if filter.ExecutorType != "" && manifest.Executor.Type != filter.ExecutorType {
		return false
	}
	if filter.Status != "" && manifest.Status != filter.Status {
		return false
	}
	if filter.RiskLevel != "" && manifest.RiskLevel != filter.RiskLevel {
		return false
	}
	if filter.Visibility != "" && manifest.Visibility != filter.Visibility {
		return false
	}
	if !matchesTextQuery(filter.Query, manifest.ToolID, manifest.Name, manifest.Description, manifest.GroupID, manifest.Executor.ProviderID, manifest.Executor.Operation, strings.Join(manifest.WhenToUse, " ")) {
		return false
	}
	return true
}

func paginateProviders(providers []ToolProvider, cursor string, pageSize int) []ToolProvider {
	if cursor != "" {
		out := providers[:0]
		for _, provider := range providers {
			if provider.ProviderID > cursor {
				out = append(out, provider)
			}
		}
		providers = out
	}
	if pageSize > 0 && len(providers) > pageSize {
		return providers[:pageSize]
	}
	return providers
}

func paginateManifests(manifests []ToolManifest, cursor string, pageSize int) []ToolManifest {
	if cursor != "" {
		out := manifests[:0]
		for _, manifest := range manifests {
			if manifest.ToolID > cursor {
				out = append(out, manifest)
			}
		}
		manifests = out
	}
	if pageSize > 0 && len(manifests) > pageSize {
		return manifests[:pageSize]
	}
	return manifests
}

func normalizePageSize(pageSize int) int {
	if pageSize <= 0 {
		return 0
	}
	if pageSize > 200 {
		return 200
	}
	return pageSize
}

func matchesTextQuery(query string, values ...string) bool {
	query = strings.ToLower(strings.TrimSpace(query))
	if query == "" {
		return true
	}
	for _, value := range values {
		if strings.Contains(strings.ToLower(value), query) {
			return true
		}
	}
	return false
}

func (s *Service) ListAdapterOperations(tenantID contracts.TenantID, providerID string) []AdapterOperation {
	s.mu.RLock()
	defer s.mu.RUnlock()
	out := make([]AdapterOperation, 0, len(s.operations))
	for _, operation := range s.operations {
		if operation.TenantID != tenantID && operation.TenantID != "" {
			continue
		}
		if strings.TrimSpace(providerID) != "" && operation.ProviderID != providerID {
			continue
		}
		out = append(out, operation)
	}
	return out
}

func (s *Service) ServiceConnectionImpact(tenantID contracts.TenantID, connectionID string) ServiceConnectionImpact {
	connectionID = strings.TrimSpace(connectionID)
	s.mu.RLock()
	defer s.mu.RUnlock()

	providers := make([]ToolProvider, 0)
	operations := make([]AdapterOperation, 0)
	tools := make([]ToolManifest, 0)
	seenProviders := map[string]struct{}{}
	directProviders := map[string]struct{}{}
	impactedOperations := map[string]struct{}{}

	for _, provider := range s.providers {
		if provider.TenantID != tenantID && provider.TenantID != "" {
			continue
		}
		if provider.ServiceConnectionID != connectionID {
			continue
		}
		providers = append(providers, provider)
		seenProviders[providerKey(provider.TenantID, provider.ProviderID)] = struct{}{}
		seenProviders[providerKey(tenantID, provider.ProviderID)] = struct{}{}
		directProviders[providerKey(provider.TenantID, provider.ProviderID)] = struct{}{}
		directProviders[providerKey(tenantID, provider.ProviderID)] = struct{}{}
	}
	for _, operation := range s.operations {
		if operation.TenantID != tenantID && operation.TenantID != "" {
			continue
		}
		if operation.ServiceConnectionID != connectionID {
			continue
		}
		operations = append(operations, operation)
		impactedOperations[operationKey(operation.TenantID, operation.ProviderID, operation.OperationID)] = struct{}{}
		impactedOperations[operationKey(tenantID, operation.ProviderID, operation.OperationID)] = struct{}{}
		if _, seen := seenProviders[providerKey(operation.TenantID, operation.ProviderID)]; !seen {
			if provider, ok := s.providers[providerKey(operation.TenantID, operation.ProviderID)]; ok {
				providers = append(providers, provider)
				seenProviders[providerKey(operation.TenantID, operation.ProviderID)] = struct{}{}
				seenProviders[providerKey(tenantID, operation.ProviderID)] = struct{}{}
			} else if provider, ok := s.providers[providerKey("", operation.ProviderID)]; ok {
				providers = append(providers, provider)
				seenProviders[providerKey("", operation.ProviderID)] = struct{}{}
				seenProviders[providerKey(tenantID, operation.ProviderID)] = struct{}{}
			}
		}
	}
	for _, manifest := range s.manifests {
		if manifest.TenantID != tenantID && manifest.TenantID != "" {
			continue
		}
		if provider, ok := s.providers[providerKey(manifest.TenantID, manifest.Executor.ProviderID)]; ok {
			if manifest.Executor.Type == executorTypeForProvider(provider.ProviderType) {
				if _, direct := directProviders[providerKey(manifest.TenantID, manifest.Executor.ProviderID)]; direct {
					tools = append(tools, manifest)
					continue
				}
			}
		} else if provider, ok := s.providers[providerKey("", manifest.Executor.ProviderID)]; ok {
			if manifest.Executor.Type == executorTypeForProvider(provider.ProviderType) {
				if _, direct := directProviders[providerKey(tenantID, manifest.Executor.ProviderID)]; direct {
					tools = append(tools, manifest)
					continue
				}
			}
		}
		if executorIsManagedAdapter(manifest.Executor.Type) {
			if _, ok := impactedOperations[operationKey(manifest.TenantID, manifest.Executor.ProviderID, manifest.Executor.Operation)]; ok {
				tools = append(tools, manifest)
				continue
			}
			if _, ok := impactedOperations[operationKey(tenantID, manifest.Executor.ProviderID, manifest.Executor.Operation)]; ok {
				tools = append(tools, manifest)
			}
		}
	}
	return ServiceConnectionImpact{
		ConnectionID: connectionID,
		Providers:    providers,
		Operations:   operations,
		Tools:        tools,
		Summary: map[string]int{
			"providers_total":  len(providers),
			"operations_total": len(operations),
			"tools_total":      len(tools),
		},
	}
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

func (s *Service) GetAdapterOperation(tenantID contracts.TenantID, providerID string, operationID string) (AdapterOperation, bool) {
	return s.adapterOperation(tenantID, providerID, operationID)
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

func (s *Service) storeOperationLocked(operation AdapterOperation) []ToolManifest {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.operations[operationKey(operation.TenantID, operation.ProviderID, operation.OperationID)] = operation
	out := make([]ToolManifest, 0)
	for _, manifest := range s.manifests {
		if !executorIsManagedAdapter(manifest.Executor.Type) ||
			manifest.Executor.ProviderID != operation.ProviderID ||
			manifest.Executor.Operation != operation.OperationID {
			continue
		}
		if manifest.TenantID != operation.TenantID {
			if operation.TenantID != "" {
				continue
			}
			if _, hasTenantOverride := s.operations[operationKey(manifest.TenantID, operation.ProviderID, operation.OperationID)]; hasTenantOverride {
				continue
			}
		}
		out = append(out, manifest)
	}
	return out
}

func (s *Service) applyManifests(ctx context.Context, manifests []ToolManifest) error {
	for _, manifest := range manifests {
		if !s.manifestAllowsInstall(ctx, manifest) {
			s.registry.UnregisterForTenant(manifest.TenantID, manifest.ToolID)
			if s.store != nil {
				if err := s.store.UpsertRuntimeCache(ctx, manifest.TenantID, manifest.ToolID, manifest.Version, StatusDisabled); err != nil {
					return err
				}
			}
			continue
		}
		prepared, err := s.manifestWithConnectionProfile(ctx, manifest)
		if err != nil {
			return err
		}
		manifest = prepared
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

func (s *Service) manifestAllowsInstall(ctx context.Context, manifest ToolManifest) bool {
	return manifest.Status == StatusEnabled &&
		s.groupAllowsInstall(manifest.TenantID, manifest.GroupID) &&
		s.providerAllowsInstall(ctx, manifest)
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

func (s *Service) providerAllowsInstall(ctx context.Context, manifest ToolManifest) bool {
	if !executorUsesProvider(manifest.Executor.Type) {
		return true
	}
	provider, ok := s.provider(manifest.TenantID, manifest.Executor.ProviderID)
	if !ok {
		return false
	}
	if provider.Status != StatusEnabled {
		return false
	}
	switch manifest.Executor.Type {
	case ExecutorTypeStaticToolHost, ExecutorTypeAgentPlugin, ExecutorTypeMCP:
		if provider.HealthStatus == HealthUnhealthy {
			return false
		}
		_, err := s.providerConnection(ctx, provider)
		return err == nil
	case ExecutorTypeHTTPAPIAdapter, ExecutorTypeDatabaseAdapter:
		operation, ok := s.adapterOperation(manifest.TenantID, manifest.Executor.ProviderID, manifest.Executor.Operation)
		if !ok || operation.Status != StatusEnabled {
			return false
		}
		return s.validateAdapterOperation(ctx, operation, true) == nil
	default:
		return false
	}
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

func (s *Service) adapterOperation(tenantID contracts.TenantID, providerID string, operationID string) (AdapterOperation, bool) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	operation, ok := s.operations[operationKey(tenantID, providerID, operationID)]
	if !ok && tenantID != "" {
		operation, ok = s.operations[operationKey("", providerID, operationID)]
	}
	return operation, ok
}

func (s *Service) providerConnection(ctx context.Context, provider ToolProvider) (serviceconnection.ServiceConnection, error) {
	return s.providerConnectionRef(ctx, provider, true)
}

func (s *Service) providerConnectionRef(ctx context.Context, provider ToolProvider, requireEnabled bool) (serviceconnection.ServiceConnection, error) {
	if s.connections == nil {
		return serviceconnection.ServiceConnection{}, contracts.NewRuntimeError(contracts.CodeToolPolicyDenied, "service connection service is unavailable", nil)
	}
	connection, ok, err := s.connections.Get(ctx, provider.TenantID, provider.ServiceConnectionID)
	if err != nil {
		return serviceconnection.ServiceConnection{}, err
	}
	if !ok {
		return serviceconnection.ServiceConnection{}, contracts.NewRuntimeError(contracts.CodeToolNotFound, "service connection not found", map[string]any{"connection_id": provider.ServiceConnectionID, "provider_id": provider.ProviderID})
	}
	if requireEnabled && connection.Status != serviceconnection.StatusEnabled {
		return serviceconnection.ServiceConnection{}, contracts.NewRuntimeError(contracts.CodeToolPolicyDenied, "service connection is not enabled", map[string]any{"connection_id": provider.ServiceConnectionID, "status": connection.Status})
	}
	if strings.TrimSpace(connection.BaseURL) == "" {
		return serviceconnection.ServiceConnection{}, contracts.NewRuntimeError(contracts.CodeToolArgumentInvalid, "service connection base_url is required", map[string]any{"connection_id": provider.ServiceConnectionID})
	}
	return connection, nil
}

func (s *Service) operationConnection(ctx context.Context, operation AdapterOperation, requireEnabled bool) (serviceconnection.ServiceConnection, error) {
	if s.connections == nil {
		return serviceconnection.ServiceConnection{}, contracts.NewRuntimeError(contracts.CodeToolPolicyDenied, "service connection service is unavailable", nil)
	}
	connection, ok, err := s.connections.Get(ctx, operation.TenantID, operation.ServiceConnectionID)
	if err != nil {
		return serviceconnection.ServiceConnection{}, err
	}
	if !ok {
		return serviceconnection.ServiceConnection{}, contracts.NewRuntimeError(contracts.CodeToolNotFound, "service connection not found", map[string]any{"connection_id": operation.ServiceConnectionID, "provider_id": operation.ProviderID, "operation_id": operation.OperationID})
	}
	if requireEnabled && connection.Status != serviceconnection.StatusEnabled {
		return serviceconnection.ServiceConnection{}, contracts.NewRuntimeError(contracts.CodeToolPolicyDenied, "service connection is not enabled", map[string]any{"connection_id": operation.ServiceConnectionID, "status": connection.Status})
	}
	if serviceConnectionRequiresBaseURL(connection.ConnectionType) && strings.TrimSpace(connection.BaseURL) == "" {
		return serviceconnection.ServiceConnection{}, contracts.NewRuntimeError(contracts.CodeToolArgumentInvalid, "service connection base_url is required", map[string]any{"connection_id": operation.ServiceConnectionID})
	}
	return connection, nil
}

func (s *Service) findConnectionResource(ctx context.Context, tenantID contracts.TenantID, connectionID string, resourceID string) (serviceconnection.ServiceConnectionResource, bool, error) {
	resources, err := s.connections.ListResources(ctx, tenantID, connectionID)
	if err != nil {
		return serviceconnection.ServiceConnectionResource{}, false, err
	}
	for _, resource := range resources {
		if resource.ResourceID == resourceID {
			return resource, true, nil
		}
	}
	return serviceconnection.ServiceConnectionResource{}, false, nil
}

func (s *Service) httpAPIAdapterOperationFromResource(tenantID contracts.TenantID, providerID string, _ serviceconnection.ServiceConnection, resource serviceconnection.ServiceConnectionResource, request AdapterOperationFromResourceRequest) (AdapterOperation, error) {
	if resource.ResourceType != "http_operation" {
		return AdapterOperation{}, contracts.NewRuntimeError(contracts.CodeToolArgumentInvalid, "service connection resource must be an http_operation", map[string]any{"resource_id": resource.ResourceID, "resource_type": resource.ResourceType})
	}
	method := firstCatalogString(metadataString(resource.Metadata, "method"), metadataString(resource.Schema, "method"))
	path := firstCatalogString(metadataString(resource.Metadata, "path"), metadataString(resource.Schema, "path"))
	if method == "" || path == "" {
		method, path = parseHTTPResourceID(resource.ResourceID)
	}
	if method == "" || path == "" {
		return AdapterOperation{}, contracts.NewRuntimeError(contracts.CodeToolArgumentInvalid, "http_operation resource must include method and path", map[string]any{"resource_id": resource.ResourceID})
	}
	baseID := firstCatalogString(request.OperationID, metadataString(resource.Metadata, "operation_id"), metadataString(resource.Schema, "operation_id"), method+" "+path)
	baseSlug := catalogSlug(baseID)
	if baseSlug == "" {
		baseSlug = catalogSlug(resource.ResourceID)
	}
	if baseSlug == "" {
		return AdapterOperation{}, contracts.NewRuntimeError(contracts.CodeToolArgumentInvalid, "cannot derive operation_id from resource", map[string]any{"resource_id": resource.ResourceID})
	}
	inputSchema, requestMapping := openAPIResourceInputSchemaAndMapping(resource)
	outputSchema := openAPIResourceOutputSchema(resource)
	if request.InputSchema != nil {
		inputSchema = request.InputSchema
	}
	if request.OutputSchema != nil {
		outputSchema = request.OutputSchema
	}
	description := firstCatalogString(request.Description, metadataString(resource.Metadata, "description"), metadataString(resource.Schema, "description"), resource.Name, "Generated HTTP API operation.")
	name := firstCatalogString(request.Name, resource.Name, metadataString(resource.Metadata, "summary"), metadataString(resource.Schema, "summary"), baseSlug)
	operation := AdapterOperation{
		TenantID:            tenantID,
		ProviderID:          providerID,
		OperationID:         firstCatalogString(request.OperationID, baseSlug),
		ToolID:              firstCatalogString(request.ToolID, baseSlug),
		GroupID:             strings.TrimSpace(request.GroupID),
		Name:                name,
		Description:         description,
		WhenToUse:           request.WhenToUse,
		ServiceConnectionID: strings.TrimSpace(request.ServiceConnectionID),
		Method:              method,
		Path:                path,
		InputSchema:         inputSchema,
		OutputSchema:        outputSchema,
		RequestMapping:      requestMapping,
		ResponseMapping:     map[string]any{},
		RiskLevel:           request.RiskLevel,
		Visibility:          request.Visibility,
		Status:              firstCatalogString(request.Status, StatusDraft),
		Version:             request.Version,
	}
	return operation, nil
}

func (s *Service) databaseAdapterOperationFromResource(tenantID contracts.TenantID, providerID string, _ serviceconnection.ServiceConnection, resource serviceconnection.ServiceConnectionResource, request AdapterOperationFromResourceRequest) (AdapterOperation, error) {
	if resource.ResourceType != "table" && resource.ResourceType != "view" {
		return AdapterOperation{}, contracts.NewRuntimeError(contracts.CodeToolArgumentInvalid, "database service connection resource must be a table or view", map[string]any{"resource_id": resource.ResourceID, "resource_type": resource.ResourceType})
	}
	baseID := firstCatalogString(request.OperationID, metadataString(resource.Metadata, "operation_id"), resource.ResourceID)
	baseSlug := catalogSlug(baseID)
	if baseSlug == "" {
		return AdapterOperation{}, contracts.NewRuntimeError(contracts.CodeToolArgumentInvalid, "cannot derive operation_id from resource", map[string]any{"resource_id": resource.ResourceID})
	}
	inputSchema := map[string]any{"type": "object"}
	if request.InputSchema != nil {
		inputSchema = request.InputSchema
	}
	parameterSchema := map[string]any{"type": "object"}
	if request.ParameterSchema != nil {
		parameterSchema = request.ParameterSchema
	}
	outputSchema := databaseResourceOutputSchema(resource)
	if request.OutputSchema != nil {
		outputSchema = request.OutputSchema
	}
	queryTemplate := firstCatalogString(request.QueryTemplate, databaseResourceQueryTemplate(resource))
	if queryTemplate == "" {
		return AdapterOperation{}, contracts.NewRuntimeError(contracts.CodeToolArgumentInvalid, "cannot derive query_template from database resource", map[string]any{"resource_id": resource.ResourceID})
	}
	description := firstCatalogString(request.Description, metadataString(resource.Metadata, "description"), "Read rows from "+resource.ResourceID+".")
	name := firstCatalogString(request.Name, resource.Name, baseSlug)
	operation := AdapterOperation{
		TenantID:            tenantID,
		ProviderID:          providerID,
		OperationID:         firstCatalogString(request.OperationID, baseSlug),
		ToolID:              firstCatalogString(request.ToolID, baseSlug),
		GroupID:             strings.TrimSpace(request.GroupID),
		Name:                name,
		Description:         description,
		WhenToUse:           request.WhenToUse,
		ServiceConnectionID: strings.TrimSpace(request.ServiceConnectionID),
		InputSchema:         inputSchema,
		OutputSchema:        outputSchema,
		ResourceID:          strings.TrimSpace(resource.ResourceID),
		QueryTemplate:       queryTemplate,
		ParameterSchema:     parameterSchema,
		MaxRows:             request.MaxRows,
		RedactColumns:       request.RedactColumns,
		ReadOnly:            true,
		RiskLevel:           request.RiskLevel,
		Visibility:          request.Visibility,
		Status:              firstCatalogString(request.Status, StatusDraft),
		Version:             request.Version,
	}
	return operation, nil
}

func serviceConnectionRequiresBaseURL(connectionType string) bool {
	switch connectionType {
	case serviceconnection.TypeHTTPAPI, serviceconnection.TypeWebhook, serviceconnection.TypeOAuth, serviceconnection.TypeCleanCore, serviceconnection.TypeDatabase:
		return true
	default:
		return false
	}
}

func (s *Service) CheckToolAvailability(ctx context.Context, tenantID contracts.TenantID, tool contracts.ToolDefinition) error {
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
		if !executorIsManagedAdapter(manifest.Executor.Type) && provider.HealthStatus == HealthUnhealthy {
			return contracts.NewRuntimeError(contracts.CodeToolPolicyDenied, "tool provider is unhealthy", map[string]any{"tool_id": tool.ToolID, "provider_id": manifest.Executor.ProviderID, "health_status": provider.HealthStatus})
		}
		switch manifest.Executor.Type {
		case ExecutorTypeStaticToolHost, ExecutorTypeAgentPlugin, ExecutorTypeMCP:
			if _, err := s.providerConnection(ctx, provider); err != nil {
				return err
			}
		case ExecutorTypeHTTPAPIAdapter, ExecutorTypeDatabaseAdapter:
			operation, ok := s.adapterOperation(manifest.TenantID, manifest.Executor.ProviderID, manifest.Executor.Operation)
			if !ok {
				return contracts.NewRuntimeError(contracts.CodeToolNotFound, "adapter operation not found", map[string]any{"tool_id": tool.ToolID, "provider_id": manifest.Executor.ProviderID, "operation": manifest.Executor.Operation})
			}
			if operation.Status != StatusEnabled {
				return contracts.NewRuntimeError(contracts.CodeToolPolicyDenied, "adapter operation is not enabled", map[string]any{"tool_id": tool.ToolID, "provider_id": manifest.Executor.ProviderID, "operation": manifest.Executor.Operation, "status": operation.Status})
			}
			if err := s.validateAdapterOperation(ctx, operation, true); err != nil {
				return err
			}
		}
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

func (s *Service) executorFor(ctx context.Context, manifest ToolManifest) (registry.Executor, error) {
	switch manifest.Executor.Type {
	case ExecutorTypeStaticToolHost, ExecutorTypeAgentPlugin:
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
		connection, err := s.providerConnection(ctx, provider)
		if err != nil {
			return nil, err
		}
		return ToolHostExecutor{
			Endpoint:     connection.BaseURL,
			ProviderID:   provider.ProviderID,
			ConnectionID: connection.ConnectionID,
			Operation:    manifest.Executor.Operation,
			Headers:      connectionHeaders(connection),
			TimeoutMS:    connection.TimeoutMS,
			RetryMax:     connection.RetryMax,
			Client:       s.client,
			TenantID:     manifest.TenantID,
			Trace:        s.trace,
			Now:          s.now,
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
		connection, err := s.providerConnection(ctx, provider)
		if err != nil {
			return nil, err
		}
		return MCPExecutor{
			Endpoint:     connection.BaseURL,
			ProviderID:   provider.ProviderID,
			ConnectionID: connection.ConnectionID,
			Operation:    manifest.Executor.Operation,
			Headers:      connectionHeaders(connection),
			TimeoutMS:    connection.TimeoutMS,
			RetryMax:     connection.RetryMax,
			Client:       s.client,
			TenantID:     manifest.TenantID,
			Trace:        s.trace,
			Now:          s.now,
		}, nil
	case ExecutorTypeHTTPAPIAdapter:
		provider, ok := s.provider(manifest.TenantID, manifest.Executor.ProviderID)
		if !ok {
			return nil, contracts.NewRuntimeError(contracts.CodeToolNotFound, "tool provider not found", map[string]any{"provider_id": manifest.Executor.ProviderID})
		}
		if provider.Status != StatusEnabled {
			return nil, contracts.NewRuntimeError(contracts.CodeToolPolicyDenied, "tool provider is not enabled", map[string]any{"provider_id": manifest.Executor.ProviderID})
		}
		operation, ok := s.adapterOperation(manifest.TenantID, manifest.Executor.ProviderID, manifest.Executor.Operation)
		if !ok {
			return nil, contracts.NewRuntimeError(contracts.CodeToolNotFound, "adapter operation not found", map[string]any{"provider_id": manifest.Executor.ProviderID, "operation": manifest.Executor.Operation})
		}
		if operation.Status != StatusEnabled {
			return nil, contracts.NewRuntimeError(contracts.CodeToolPolicyDenied, "adapter operation is not enabled", map[string]any{"provider_id": manifest.Executor.ProviderID, "operation": manifest.Executor.Operation, "status": operation.Status})
		}
		if err := s.validateAdapterOperation(ctx, operation, true); err != nil {
			return nil, err
		}
		connection, err := s.operationConnection(ctx, operation, true)
		if err != nil {
			return nil, err
		}
		return HTTPAPIAdapterExecutor{
			Endpoint:   connection.BaseURL,
			ProviderID: provider.ProviderID,
			Operation:  operation,
			Headers:    mergeHeaders(connectionHeaders(connection), operation.Headers),
			TimeoutMS:  connection.TimeoutMS,
			RetryMax:   connection.RetryMax,
			Client:     s.client,
			TenantID:   manifest.TenantID,
			Trace:      s.trace,
			Now:        s.now,
		}, nil
	case ExecutorTypeDatabaseAdapter:
		provider, ok := s.provider(manifest.TenantID, manifest.Executor.ProviderID)
		if !ok {
			return nil, contracts.NewRuntimeError(contracts.CodeToolNotFound, "tool provider not found", map[string]any{"provider_id": manifest.Executor.ProviderID})
		}
		if provider.Status != StatusEnabled {
			return nil, contracts.NewRuntimeError(contracts.CodeToolPolicyDenied, "tool provider is not enabled", map[string]any{"provider_id": manifest.Executor.ProviderID})
		}
		operation, ok := s.adapterOperation(manifest.TenantID, manifest.Executor.ProviderID, manifest.Executor.Operation)
		if !ok {
			return nil, contracts.NewRuntimeError(contracts.CodeToolNotFound, "adapter operation not found", map[string]any{"provider_id": manifest.Executor.ProviderID, "operation": manifest.Executor.Operation})
		}
		if operation.Status != StatusEnabled {
			return nil, contracts.NewRuntimeError(contracts.CodeToolPolicyDenied, "adapter operation is not enabled", map[string]any{"provider_id": manifest.Executor.ProviderID, "operation": manifest.Executor.Operation, "status": operation.Status})
		}
		if err := s.validateAdapterOperation(ctx, operation, true); err != nil {
			return nil, err
		}
		connection, err := s.operationConnection(ctx, operation, true)
		if err != nil {
			return nil, err
		}
		return DatabaseAdapterExecutor{
			Connection: connection,
			ProviderID: provider.ProviderID,
			Operation:  operation,
			TenantID:   manifest.TenantID,
			Trace:      s.trace,
			Now:        s.now,
			OpenDB:     s.openDB,
		}, nil
	default:
		return nil, contracts.NewRuntimeError(contracts.CodeToolArgumentInvalid, "unsupported tool executor type", map[string]any{"executor_type": manifest.Executor.Type})
	}
}

func normalizeProvider(provider ToolProvider) ToolProvider {
	provider.ProviderID = strings.TrimSpace(provider.ProviderID)
	provider.ProviderType = strings.TrimSpace(provider.ProviderType)
	provider.ServiceConnectionID = strings.TrimSpace(provider.ServiceConnectionID)
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

func normalizeAdapterOperation(operation AdapterOperation) AdapterOperation {
	operation.ProviderID = strings.TrimSpace(operation.ProviderID)
	operation.OperationID = strings.TrimSpace(operation.OperationID)
	operation.ToolID = strings.TrimSpace(operation.ToolID)
	operation.GroupID = strings.TrimSpace(operation.GroupID)
	operation.Name = strings.TrimSpace(operation.Name)
	operation.Description = strings.TrimSpace(operation.Description)
	operation.ServiceConnectionID = strings.TrimSpace(operation.ServiceConnectionID)
	operation.Method = strings.ToUpper(strings.TrimSpace(operation.Method))
	operation.Path = strings.TrimSpace(operation.Path)
	operation.ResourceID = strings.TrimSpace(operation.ResourceID)
	operation.QueryTemplate = strings.TrimSpace(operation.QueryTemplate)
	operation.RedactColumns = normalizeStringList(operation.RedactColumns)
	if operation.MaxRows < 0 {
		operation.MaxRows = 0
	}
	if operation.RiskLevel == "" {
		operation.RiskLevel = contracts.RiskLow
	}
	if operation.Visibility == "" {
		operation.Visibility = contracts.ToolProtected
	}
	if operation.Status == "" {
		operation.Status = StatusEnabled
	}
	if operation.Version == "" {
		operation.Version = "v1"
	}
	return operation
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
	if manifest.ExecutionProfile == "" {
		switch manifest.Executor.Type {
		case ExecutorTypeDatabaseAdapter:
			manifest.ExecutionProfile = `{"id":"database-adapter","domain_id":"database"}`
		case ExecutorTypeAgentTool:
			manifest.ExecutionProfile = `{"id":"agent-tool","domain_id":"agent_tool"}`
		case ExecutorTypeStaticToolHost, ExecutorTypeAgentPlugin, ExecutorTypeMCP, ExecutorTypeHTTPAPIAdapter:
			manifest.ExecutionProfile = ""
		default:
			manifest.ExecutionProfile = "local"
		}
	}
	return manifest
}

func normalizeManifestWithConnection(manifest ToolManifest, connection serviceconnection.ServiceConnection) ToolManifest {
	manifest = normalizeManifest(manifest)
	if manifest.ExecutionProfile != "" {
		return manifest
	}
	switch manifest.Executor.Type {
	case ExecutorTypeStaticToolHost, ExecutorTypeAgentPlugin, ExecutorTypeMCP, ExecutorTypeHTTPAPIAdapter:
		manifest.ExecutionProfile = httpExecutionProfileForConnection(connection)
	case ExecutorTypeDatabaseAdapter:
		manifest.ExecutionProfile = `{"id":"database-adapter","domain_id":"database"}`
	}
	return manifest
}

func httpExecutionProfileForConnection(connection serviceconnection.ServiceConnection) string {
	hosts := connectionAllowedHosts(connection)
	profile := map[string]any{
		"id":        "provider-http",
		"domain_id": "http",
		"network_policy": map[string]any{
			"allow_network": true,
		},
	}
	if len(hosts) > 0 {
		profile["network_policy"].(map[string]any)["allowed_hosts"] = hosts
	}
	data, err := json.Marshal(profile)
	if err != nil {
		return `{"id":"provider-http","domain_id":"http","network_policy":{"allow_network":true}}`
	}
	return string(data)
}

func connectionAllowedHosts(connection serviceconnection.ServiceConnection) []string {
	values := splitNetworkScope(connection.NetworkScope)
	if len(values) == 0 {
		if host := urlHost(connection.BaseURL); host != "" {
			values = append(values, host)
		}
	}
	out := make([]string, 0, len(values))
	seen := map[string]struct{}{}
	for _, value := range values {
		host := normalizeNetworkScopeHost(value)
		if host == "" {
			continue
		}
		key := strings.ToLower(host)
		if _, ok := seen[key]; ok {
			continue
		}
		seen[key] = struct{}{}
		out = append(out, host)
	}
	if len(out) == 0 {
		return nil
	}
	return out
}

func splitNetworkScope(scope string) []string {
	scope = strings.TrimSpace(scope)
	if scope == "" {
		return nil
	}
	var jsonValues []string
	if strings.HasPrefix(scope, "[") && json.Unmarshal([]byte(scope), &jsonValues) == nil {
		return jsonValues
	}
	return strings.FieldsFunc(scope, func(r rune) bool {
		return r == ',' || r == '\n' || r == '\r' || r == '\t' || r == ' '
	})
}

func normalizeNetworkScopeHost(value string) string {
	value = strings.TrimSpace(strings.Trim(value, `"'`))
	if value == "" {
		return ""
	}
	if strings.Contains(value, "://") {
		return urlHost(value)
	}
	if strings.HasPrefix(value, "*.") {
		if strings.TrimPrefix(value, "*.") == "" {
			return ""
		}
		return strings.ToLower(value)
	}
	if host := urlHost("https://" + value); host != "" {
		return host
	}
	return strings.ToLower(value)
}

func validateProvider(provider ToolProvider) error {
	if strings.TrimSpace(provider.ProviderID) == "" {
		return contracts.NewRuntimeError(contracts.CodeToolArgumentInvalid, "provider_id is required", nil)
	}
	if strings.TrimSpace(provider.ProviderType) == "" {
		return contracts.NewRuntimeError(contracts.CodeToolArgumentInvalid, "provider_type is required", nil)
	}
	if providerUsesProviderConnection(provider.ProviderType) && strings.TrimSpace(provider.ServiceConnectionID) == "" {
		return contracts.NewRuntimeError(contracts.CodeToolArgumentInvalid, "service_connection_id is required", nil)
	}
	if providerIsManagedAdapter(provider.ProviderType) && strings.TrimSpace(provider.ServiceConnectionID) != "" {
		return contracts.NewRuntimeError(contracts.CodeToolArgumentInvalid, "managed adapter provider must not set service_connection_id; set it on adapter operations", map[string]any{"provider_type": provider.ProviderType})
	}
	if provider.ProviderType != ProviderTypeStaticToolHost && provider.ProviderType != ProviderTypeAgentPlugin && provider.ProviderType != ProviderTypeMCP && provider.ProviderType != ProviderTypeHTTPAPIAdapter && provider.ProviderType != ProviderTypeDatabaseAdapter {
		return contracts.NewRuntimeError(contracts.CodeToolArgumentInvalid, "unsupported provider_type", map[string]any{"provider_type": provider.ProviderType})
	}
	if providerIsManagedAdapter(provider.ProviderType) && provider.ProviderID != managedAdapterProviderIDForType(provider.ProviderType) {
		return contracts.NewRuntimeError(contracts.CodeToolArgumentInvalid, "managed adapter provider must use the canonical provider_id", map[string]any{"provider_type": provider.ProviderType, "provider_id": provider.ProviderID, "expected_provider_id": managedAdapterProviderIDForType(provider.ProviderType)})
	}
	if provider.Status != StatusDraft && provider.Status != StatusEnabled && provider.Status != StatusDisabled {
		return contracts.NewRuntimeError(contracts.CodeToolArgumentInvalid, "unsupported provider status", map[string]any{"status": provider.Status})
	}
	if !validHealthStatus(provider.HealthStatus) {
		return contracts.NewRuntimeError(contracts.CodeToolArgumentInvalid, "unsupported provider health_status", map[string]any{"health_status": provider.HealthStatus})
	}
	return nil
}

func (s *Service) validateAdapterOperation(ctx context.Context, operation AdapterOperation, requireEnabledConnection bool) error {
	if strings.TrimSpace(operation.ProviderID) == "" || strings.TrimSpace(operation.OperationID) == "" {
		return contracts.NewRuntimeError(contracts.CodeToolArgumentInvalid, "provider_id and operation_id are required", nil)
	}
	if strings.TrimSpace(operation.ToolID) == "" || strings.TrimSpace(operation.Name) == "" {
		return contracts.NewRuntimeError(contracts.CodeToolArgumentInvalid, "tool_id and name are required", nil)
	}
	if strings.TrimSpace(operation.Description) == "" {
		return contracts.NewRuntimeError(contracts.CodeToolArgumentInvalid, "description is required", nil)
	}
	if strings.TrimSpace(operation.ServiceConnectionID) == "" {
		return contracts.NewRuntimeError(contracts.CodeToolArgumentInvalid, "service_connection_id is required", nil)
	}
	if operation.InputSchema == nil {
		return contracts.NewRuntimeError(contracts.CodeToolArgumentInvalid, "input_schema is required", nil)
	}
	if err := validateJSONSchema("input_schema", operation.InputSchema); err != nil {
		return err
	}
	if operation.Status == StatusEnabled && operation.OutputSchema == nil {
		return contracts.NewRuntimeError(contracts.CodeToolArgumentInvalid, "output_schema is required for enabled adapter operation", nil)
	}
	if operation.OutputSchema != nil {
		if err := validateJSONSchema("output_schema", operation.OutputSchema); err != nil {
			return err
		}
	}
	if err := operation.RiskLevel.Validate(); err != nil {
		return contracts.NewRuntimeError(contracts.CodeToolArgumentInvalid, err.Error(), nil)
	}
	if !validVisibility(operation.Visibility) {
		return contracts.NewRuntimeError(contracts.CodeToolArgumentInvalid, "unknown tool visibility", map[string]any{"visibility": operation.Visibility})
	}
	if operation.Status != StatusDraft && operation.Status != StatusEnabled && operation.Status != StatusDisabled {
		return contracts.NewRuntimeError(contracts.CodeToolArgumentInvalid, "unsupported operation status", map[string]any{"status": operation.Status})
	}
	provider, ok := s.provider(operation.TenantID, operation.ProviderID)
	if !ok {
		return contracts.NewRuntimeError(contracts.CodeToolNotFound, "tool provider not found", map[string]any{"provider_id": operation.ProviderID})
	}
	if !providerIsManagedAdapter(provider.ProviderType) {
		return contracts.NewRuntimeError(contracts.CodeToolArgumentInvalid, "adapter operation requires managed adapter provider", map[string]any{"provider_type": provider.ProviderType})
	}
	connection, err := s.operationConnection(ctx, operation, requireEnabledConnection)
	if err != nil {
		return err
	}
	switch provider.ProviderType {
	case ProviderTypeHTTPAPIAdapter:
		if connection.ConnectionType != serviceconnection.TypeHTTPAPI {
			return contracts.NewRuntimeError(contracts.CodeToolArgumentInvalid, "http_api_adapter operation requires http_api service connection", map[string]any{"connection_type": connection.ConnectionType})
		}
		if err := validateHTTPAdapterOperationFields(operation); err != nil {
			return err
		}
		if err := validateHTTPAdapterMappings(operation); err != nil {
			return err
		}
		if strings.TrimSpace(operation.Path) == "" {
			return contracts.NewRuntimeError(contracts.CodeToolArgumentInvalid, "path is required for http_api_adapter operation", nil)
		}
		if !validHTTPMethod(operation.Method) {
			return contracts.NewRuntimeError(contracts.CodeToolArgumentInvalid, "unsupported operation method", map[string]any{"method": operation.Method})
		}
	case ProviderTypeDatabaseAdapter:
		if connection.ConnectionType != serviceconnection.TypeDatabase {
			return contracts.NewRuntimeError(contracts.CodeToolArgumentInvalid, "database_adapter operation requires database service connection", map[string]any{"connection_type": connection.ConnectionType})
		}
		if err := validateDatabaseAdapterOperationFields(operation); err != nil {
			return err
		}
		if strings.TrimSpace(operation.QueryTemplate) == "" {
			return contracts.NewRuntimeError(contracts.CodeToolArgumentInvalid, "query_template is required for database_adapter operation", nil)
		}
		if !isReadOnlySQL(operation.QueryTemplate) {
			return contracts.NewRuntimeError(contracts.CodeToolPolicyDenied, "database_adapter only allows read-only SQL", map[string]any{"operation": operation.OperationID})
		}
		if !operation.ReadOnly {
			return contracts.NewRuntimeError(contracts.CodeToolPolicyDenied, "database_adapter operation must be read_only", nil)
		}
		if operation.ParameterSchema == nil {
			return contracts.NewRuntimeError(contracts.CodeToolArgumentInvalid, "parameter_schema is required for database_adapter operation", nil)
		}
		if err := validateJSONSchema("parameter_schema", operation.ParameterSchema); err != nil {
			return err
		}
		if err := s.validateDatabaseOperationResource(ctx, operation, connection, requireEnabledConnection || operation.Status == StatusEnabled); err != nil {
			return err
		}
	}
	return nil
}

func validateHTTPAdapterOperationFields(operation AdapterOperation) error {
	if err := validateHTTPHeaders("headers", operation.Headers); err != nil {
		return err
	}
	if strings.TrimSpace(operation.ResourceID) != "" {
		return unsupportedAdapterOperationField(ProviderTypeHTTPAPIAdapter, "resource_id")
	}
	if strings.TrimSpace(operation.QueryTemplate) != "" {
		return unsupportedAdapterOperationField(ProviderTypeHTTPAPIAdapter, "query_template")
	}
	if operation.ParameterSchema != nil {
		return unsupportedAdapterOperationField(ProviderTypeHTTPAPIAdapter, "parameter_schema")
	}
	if operation.MaxRows > 0 {
		return unsupportedAdapterOperationField(ProviderTypeHTTPAPIAdapter, "max_rows")
	}
	if len(operation.RedactColumns) > 0 {
		return unsupportedAdapterOperationField(ProviderTypeHTTPAPIAdapter, "redact_columns")
	}
	if operation.ReadOnly {
		return unsupportedAdapterOperationField(ProviderTypeHTTPAPIAdapter, "read_only")
	}
	return nil
}

func validateDatabaseAdapterOperationFields(operation AdapterOperation) error {
	if strings.TrimSpace(operation.Method) != "" {
		return unsupportedAdapterOperationField(ProviderTypeDatabaseAdapter, "method")
	}
	if strings.TrimSpace(operation.Path) != "" {
		return unsupportedAdapterOperationField(ProviderTypeDatabaseAdapter, "path")
	}
	if len(operation.Headers) > 0 {
		return unsupportedAdapterOperationField(ProviderTypeDatabaseAdapter, "headers")
	}
	if len(operation.RequestMapping) > 0 {
		return unsupportedAdapterOperationField(ProviderTypeDatabaseAdapter, "request_mapping")
	}
	if len(operation.ResponseMapping) > 0 {
		return unsupportedAdapterOperationField(ProviderTypeDatabaseAdapter, "response_mapping")
	}
	return nil
}

func unsupportedAdapterOperationField(providerType string, field string) error {
	return contracts.NewRuntimeError(contracts.CodeToolArgumentInvalid, "adapter operation field is not supported for provider_type", map[string]any{"provider_type": providerType, "field": field})
}

func (s *Service) validateDatabaseOperationResource(ctx context.Context, operation AdapterOperation, connection serviceconnection.ServiceConnection, requireCatalogMatch bool) error {
	resourceID := strings.TrimSpace(operation.ResourceID)
	if resourceID == "" {
		return contracts.NewRuntimeError(contracts.CodeToolArgumentInvalid, "resource_id is required for database_adapter operation", map[string]any{"provider_id": operation.ProviderID, "operation_id": operation.OperationID})
	}
	resources, err := s.connections.ListResources(ctx, operation.TenantID, connection.ConnectionID)
	if err != nil {
		return err
	}
	if len(resources) == 0 {
		if requireCatalogMatch {
			return contracts.NewRuntimeError(contracts.CodeToolArgumentInvalid, "database_adapter resource_id has not been discovered for service connection", map[string]any{"connection_id": connection.ConnectionID, "resource_id": resourceID})
		}
		return nil
	}
	for _, resource := range resources {
		if resource.ResourceID != resourceID {
			continue
		}
		if resource.ResourceType != "table" && resource.ResourceType != "view" {
			return contracts.NewRuntimeError(contracts.CodeToolArgumentInvalid, "database_adapter resource must be a table or view", map[string]any{"connection_id": connection.ConnectionID, "resource_id": resourceID, "resource_type": resource.ResourceType})
		}
		if err := validateDatabaseQueryTargetsResource(operation, resource); err != nil {
			return err
		}
		return validateDatabaseRedactColumns(operation, resource)
	}
	return contracts.NewRuntimeError(contracts.CodeToolArgumentInvalid, "database_adapter resource_id is not in service connection resource catalog", map[string]any{"connection_id": connection.ConnectionID, "resource_id": resourceID})
}

func validateDatabaseQueryTargetsResource(operation AdapterOperation, resource serviceconnection.ServiceConnectionResource) error {
	expected := databaseResourceSQLIdentifier(resource)
	if expected == "" {
		expected = resource.ResourceID
	}
	allowed := map[string]struct{}{}
	for _, value := range []string{expected, resource.ResourceID} {
		normalized := normalizeSQLRelationIdentifier(value)
		if normalized != "" {
			allowed[normalized] = struct{}{}
		}
	}
	if len(allowed) == 0 {
		return nil
	}
	relations := databaseQueryRelationIdentifiers(operation.QueryTemplate)
	if len(relations) == 0 {
		return contracts.NewRuntimeError(contracts.CodeToolPolicyDenied, "database_adapter query_template must reference bound resource_id", map[string]any{
			"operation":     operation.OperationID,
			"resource_id":   resource.ResourceID,
			"query_target":  expected,
			"query_related": relations,
		})
	}
	for _, relation := range relations {
		if _, ok := allowed[relation]; ok {
			continue
		}
		return contracts.NewRuntimeError(contracts.CodeToolPolicyDenied, "database_adapter query_template can only reference bound resource_id", map[string]any{
			"operation":     operation.OperationID,
			"resource_id":   resource.ResourceID,
			"query_target":  expected,
			"query_related": relations,
		})
	}
	return nil
}

func validateDatabaseRedactColumns(operation AdapterOperation, resource serviceconnection.ServiceConnectionResource) error {
	if len(operation.RedactColumns) == 0 {
		return nil
	}
	columnNames := databaseResourceColumnNames(resource)
	if len(columnNames) == 0 {
		return contracts.NewRuntimeError(contracts.CodeToolArgumentInvalid, "redact_columns require discovered column schema", map[string]any{"resource_id": resource.ResourceID})
	}
	for _, column := range operation.RedactColumns {
		if _, ok := columnNames[strings.ToLower(strings.TrimSpace(column))]; !ok {
			return contracts.NewRuntimeError(contracts.CodeToolArgumentInvalid, "redact_columns contains column not present in resource schema", map[string]any{"resource_id": resource.ResourceID, "column": column})
		}
	}
	return nil
}

func databaseResourceColumnNames(resource serviceconnection.ServiceConnectionResource) map[string]struct{} {
	out := map[string]struct{}{}
	switch columns := resource.Schema["columns"].(type) {
	case []map[string]any:
		for _, column := range columns {
			addDatabaseResourceColumnName(out, column["name"])
		}
	case []any:
		for _, raw := range columns {
			if column, ok := raw.(map[string]any); ok {
				addDatabaseResourceColumnName(out, column["name"])
			}
		}
	}
	return out
}

func addDatabaseResourceColumnName(out map[string]struct{}, value any) {
	name, ok := value.(string)
	if !ok {
		return
	}
	name = strings.ToLower(strings.TrimSpace(name))
	if name == "" {
		return
	}
	out[name] = struct{}{}
}

func databaseResourceQueryTemplate(resource serviceconnection.ServiceConnectionResource) string {
	identifier := databaseResourceSQLIdentifier(resource)
	if identifier == "" {
		return ""
	}
	return "select * from " + identifier + " limit 100"
}

func databaseResourceSQLIdentifier(resource serviceconnection.ServiceConnectionResource) string {
	schemaName := metadataString(resource.Metadata, "schema")
	tableName := metadataString(resource.Metadata, "table_name")
	if schemaName != "" && tableName != "" {
		schemaIdent := quoteSQLIdentifier(schemaName)
		tableIdent := quoteSQLIdentifier(tableName)
		if schemaIdent == "" || tableIdent == "" {
			return ""
		}
		return schemaIdent + "." + tableIdent
	}
	parts := strings.Split(strings.TrimSpace(resource.ResourceID), ".")
	if len(parts) == 2 {
		schemaIdent := quoteSQLIdentifier(parts[0])
		tableIdent := quoteSQLIdentifier(parts[1])
		if schemaIdent == "" || tableIdent == "" {
			return ""
		}
		return schemaIdent + "." + tableIdent
	}
	return quoteSQLIdentifier(resource.ResourceID)
}

func quoteSQLIdentifier(value string) string {
	value = strings.TrimSpace(value)
	if value == "" {
		return ""
	}
	if strings.Contains(value, "\x00") {
		return ""
	}
	return `"` + strings.ReplaceAll(value, `"`, `""`) + `"`
}

func databaseResourceOutputSchema(resource serviceconnection.ServiceConnectionResource) map[string]any {
	rowProperties := map[string]any{}
	switch columns := resource.Schema["columns"].(type) {
	case []map[string]any:
		for _, column := range columns {
			addDatabaseResourceOutputColumn(rowProperties, column)
		}
	case []any:
		for _, raw := range columns {
			if column, ok := raw.(map[string]any); ok {
				addDatabaseResourceOutputColumn(rowProperties, column)
			}
		}
	}
	rowSchema := map[string]any{"type": "object"}
	if len(rowProperties) > 0 {
		rowSchema["properties"] = rowProperties
	}
	return map[string]any{
		"type": "object",
		"properties": map[string]any{
			"rows":      map[string]any{"type": "array", "items": rowSchema},
			"row_count": map[string]any{"type": "integer"},
		},
	}
}

func addDatabaseResourceOutputColumn(properties map[string]any, column map[string]any) {
	name := metadataString(column, "name")
	if name == "" {
		return
	}
	if schema, ok := column["json_schema"].(map[string]any); ok && len(schema) > 0 {
		properties[name] = normalizeOpenAPISchema(schema, "object")
		return
	}
	properties[name] = map[string]any{"type": "string"}
}

func (s *Service) manifestForAdapterOperation(operation AdapterOperation) (ToolManifest, bool) {
	operation = normalizeAdapterOperation(operation)
	if operation.Status != StatusEnabled {
		return ToolManifest{}, false
	}
	executorType := executorTypeForProvider(s.providerTypeForOperation(operation))
	if executorType == "" {
		return ToolManifest{}, false
	}
	return normalizeManifest(ToolManifest{
		TenantID:     operation.TenantID,
		ToolID:       operation.ToolID,
		GroupID:      operation.GroupID,
		Name:         operation.Name,
		Description:  operation.Description,
		WhenToUse:    operation.WhenToUse,
		InputSchema:  operation.InputSchema,
		OutputSchema: operation.OutputSchema,
		RiskLevel:    operation.RiskLevel,
		Visibility:   operation.Visibility,
		Executor: ExecutorSpec{
			Type:       executorType,
			ProviderID: operation.ProviderID,
			Operation:  operation.OperationID,
		},
		Status:  StatusEnabled,
		Version: operation.Version,
	}), true
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
	if providerIsManagedAdapter(provider.ProviderType) {
		operations := s.ListAdapterOperations(provider.TenantID, provider.ProviderID)
		for _, operation := range operations {
			if operation.Status != StatusEnabled {
				continue
			}
			if err := s.validateAdapterOperation(ctx, operation, true); err != nil {
				return HealthUnhealthy, err.Error()
			}
		}
		return HealthHealthy, ""
	}
	connection, err := s.providerConnection(ctx, provider)
	if err != nil {
		return HealthUnhealthy, err.Error()
	}
	healthURL := strings.TrimRight(connection.BaseURL, "/") + "/healthz"
	if err := requestJSON(ctx, s.client, http.MethodGet, healthURL, nil, nil, nil, requestOptions{
		Headers:   connectionHeaders(connection),
		TimeoutMS: connection.TimeoutMS,
		RetryMax:  connection.RetryMax,
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

func validateManifest(manifest ToolManifest, allowManagedInternal bool) error {
	if strings.TrimSpace(manifest.ToolID) == "" || strings.TrimSpace(manifest.Name) == "" {
		return contracts.NewRuntimeError(contracts.CodeToolArgumentInvalid, "tool_id and name are required", nil)
	}
	if strings.TrimSpace(manifest.Description) == "" {
		return contracts.NewRuntimeError(contracts.CodeToolArgumentInvalid, "description is required", nil)
	}
	if manifest.InputSchema == nil {
		return contracts.NewRuntimeError(contracts.CodeToolArgumentInvalid, "input_schema is required", nil)
	}
	if err := validateJSONSchema("input_schema", manifest.InputSchema); err != nil {
		return err
	}
	if manifest.Status == StatusEnabled && manifest.OutputSchema == nil {
		return contracts.NewRuntimeError(contracts.CodeToolArgumentInvalid, "output_schema is required for enabled tool", nil)
	}
	if manifest.OutputSchema != nil {
		if err := validateJSONSchema("output_schema", manifest.OutputSchema); err != nil {
			return err
		}
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
	if manifest.Executor.Type == ExecutorTypeStaticToolHost && (strings.TrimSpace(manifest.Executor.ProviderID) == "" || strings.TrimSpace(manifest.Executor.Operation) == "") {
		return contracts.NewRuntimeError(contracts.CodeToolArgumentInvalid, "executor.provider_id and executor.operation are required for static_tool_host", nil)
	}
	if manifest.Executor.Type == ExecutorTypeAgentPlugin && (strings.TrimSpace(manifest.Executor.ProviderID) == "" || strings.TrimSpace(manifest.Executor.Operation) == "") {
		return contracts.NewRuntimeError(contracts.CodeToolArgumentInvalid, "executor.provider_id and executor.operation are required for agent_plugin_service", nil)
	}
	if manifest.Executor.Type == ExecutorTypeMCP && (strings.TrimSpace(manifest.Executor.ProviderID) == "" || strings.TrimSpace(manifest.Executor.Operation) == "") {
		return contracts.NewRuntimeError(contracts.CodeToolArgumentInvalid, "executor.provider_id and executor.operation are required for mcp", nil)
	}
	if manifest.Executor.Type == ExecutorTypeAgentTool && strings.TrimSpace(manifest.Executor.ProviderID) == "" {
		return contracts.NewRuntimeError(contracts.CodeToolArgumentInvalid, "executor.provider_id is required for agent_tool", nil)
	}
	if allowManagedInternal && executorIsManagedAdapter(manifest.Executor.Type) && (strings.TrimSpace(manifest.Executor.ProviderID) == "" || strings.TrimSpace(manifest.Executor.Operation) == "") {
		return contracts.NewRuntimeError(contracts.CodeToolArgumentInvalid, "executor.provider_id and executor.operation are required for managed adapter executors", map[string]any{"executor_type": manifest.Executor.Type})
	}
	if manifest.Executor.Type != ExecutorTypeStaticToolHost && manifest.Executor.Type != ExecutorTypeAgentPlugin && manifest.Executor.Type != ExecutorTypeMCP && manifest.Executor.Type != ExecutorTypeAgentTool && !(allowManagedInternal && executorIsManagedAdapter(manifest.Executor.Type)) {
		return contracts.NewRuntimeError(contracts.CodeToolArgumentInvalid, "unsupported executor.type", map[string]any{"executor_type": manifest.Executor.Type})
	}
	return nil
}

func validateJSONSchema(field string, schema map[string]any) error {
	if len(schema) == 0 {
		return contracts.NewRuntimeError(contracts.CodeToolArgumentInvalid, field+".type is required", map[string]any{"field": field})
	}
	schemaType, ok := schema["type"]
	if !ok {
		return contracts.NewRuntimeError(contracts.CodeToolArgumentInvalid, field+".type is required", map[string]any{"field": field})
	}
	types, err := jsonSchemaTypes(field+".type", schemaType)
	if err != nil {
		return err
	}
	if raw, ok := schema["properties"]; ok {
		if !containsString(types, "object") {
			return contracts.NewRuntimeError(contracts.CodeToolArgumentInvalid, field+".properties requires type object", map[string]any{"field": field})
		}
		properties, ok := raw.(map[string]any)
		if !ok {
			return contracts.NewRuntimeError(contracts.CodeToolArgumentInvalid, field+".properties must be an object", map[string]any{"field": field})
		}
		for name, value := range properties {
			child, ok := value.(map[string]any)
			if !ok {
				return contracts.NewRuntimeError(contracts.CodeToolArgumentInvalid, field+".properties."+name+" must be a schema object", map[string]any{"field": field})
			}
			if err := validateJSONSchema(field+".properties."+name, child); err != nil {
				return err
			}
		}
	}
	if raw, ok := schema["required"]; ok {
		if !containsString(types, "object") {
			return contracts.NewRuntimeError(contracts.CodeToolArgumentInvalid, field+".required requires type object", map[string]any{"field": field})
		}
		if !jsonSchemaStringArray(raw) {
			return contracts.NewRuntimeError(contracts.CodeToolArgumentInvalid, field+".required must be an array of strings", map[string]any{"field": field})
		}
	}
	if raw, ok := schema["items"]; ok {
		if !containsString(types, "array") {
			return contracts.NewRuntimeError(contracts.CodeToolArgumentInvalid, field+".items requires type array", map[string]any{"field": field})
		}
		switch items := raw.(type) {
		case map[string]any:
			if err := validateJSONSchema(field+".items", items); err != nil {
				return err
			}
		case []any:
			for i, value := range items {
				child, ok := value.(map[string]any)
				if !ok {
					return contracts.NewRuntimeError(contracts.CodeToolArgumentInvalid, field+".items must contain schema objects", map[string]any{"field": field, "index": i})
				}
				if err := validateJSONSchema(fmt.Sprintf("%s.items[%d]", field, i), child); err != nil {
					return err
				}
			}
		default:
			return contracts.NewRuntimeError(contracts.CodeToolArgumentInvalid, field+".items must be a schema object or schema array", map[string]any{"field": field})
		}
	}
	if raw, ok := schema["additionalProperties"]; ok {
		switch value := raw.(type) {
		case bool:
		case map[string]any:
			if err := validateJSONSchema(field+".additionalProperties", value); err != nil {
				return err
			}
		default:
			return contracts.NewRuntimeError(contracts.CodeToolArgumentInvalid, field+".additionalProperties must be a boolean or schema object", map[string]any{"field": field})
		}
	}
	if raw, ok := schema["enum"]; ok {
		if !jsonSchemaNonEmptyArray(raw) {
			return contracts.NewRuntimeError(contracts.CodeToolArgumentInvalid, field+".enum must be a non-empty array", map[string]any{"field": field})
		}
	}
	return nil
}

func validateHTTPAdapterMappings(operation AdapterOperation) error {
	if err := validateMappingKeys("request_mapping", operation.RequestMapping, []string{"body", "query", "path_params", "headers"}); err != nil {
		return err
	}
	if err := validateMappingKeys("response_mapping", operation.ResponseMapping, []string{"body_path", "output"}); err != nil {
		return err
	}
	if raw, ok := operation.RequestMapping["body"]; ok {
		if err := validateMappingSection("request_mapping.body", raw); err != nil {
			return err
		}
	}
	if raw, ok := operation.RequestMapping["query"]; ok {
		if err := validateMappingSection("request_mapping.query", raw); err != nil {
			return err
		}
	}
	if raw, ok := operation.RequestMapping["path_params"]; ok {
		if err := validateScalarMappingSection("request_mapping.path_params", raw); err != nil {
			return err
		}
	}
	if raw, ok := operation.RequestMapping["headers"]; ok {
		if err := validateScalarMappingSection("request_mapping.headers", raw); err != nil {
			return err
		}
		if err := validateHeaderMappingKeys(raw); err != nil {
			return err
		}
	}
	if err := validateHTTPPathParameterMapping(operation.Path, operation.RequestMapping["path_params"]); err != nil {
		return err
	}
	if raw, ok := operation.ResponseMapping["body_path"]; ok {
		if _, ok := raw.(string); !ok {
			return contracts.NewRuntimeError(contracts.CodeToolArgumentInvalid, "response_mapping.body_path must be a string path", map[string]any{"field": "response_mapping.body_path"})
		}
	}
	if raw, ok := operation.ResponseMapping["output"]; ok {
		if err := validateMappingSection("response_mapping.output", raw); err != nil {
			return err
		}
	}
	return nil
}

func validateHTTPPathParameterMapping(path string, raw any) error {
	params, err := httpPathPlaceholders(path)
	if err != nil {
		return err
	}
	if len(params) == 0 {
		if raw != nil {
			if typed, ok := raw.(map[string]any); ok {
				for key := range typed {
					return contracts.NewRuntimeError(contracts.CodeToolArgumentInvalid, "path parameter placeholder not found", map[string]any{"param": key})
				}
			}
		}
		return nil
	}
	typed, ok := raw.(map[string]any)
	if !ok || len(typed) == 0 {
		return contracts.NewRuntimeError(contracts.CodeToolArgumentInvalid, "request_mapping.path_params is required for parameterized operation path", map[string]any{"field": "request_mapping.path_params"})
	}
	expected := map[string]struct{}{}
	for _, param := range params {
		expected[param] = struct{}{}
		if _, ok := typed[param]; !ok {
			return contracts.NewRuntimeError(contracts.CodeToolArgumentInvalid, "request_mapping.path_params missing path parameter", map[string]any{"param": param})
		}
	}
	for key := range typed {
		if _, ok := expected[key]; !ok {
			return contracts.NewRuntimeError(contracts.CodeToolArgumentInvalid, "path parameter placeholder not found", map[string]any{"param": key})
		}
	}
	return nil
}

func httpPathPlaceholders(path string) ([]string, error) {
	remaining := path
	out := []string{}
	for {
		start := strings.Index(remaining, "{")
		closeBeforeStart := strings.Index(remaining, "}")
		if closeBeforeStart >= 0 && (start < 0 || closeBeforeStart < start) {
			return nil, contracts.NewRuntimeError(contracts.CodeToolArgumentInvalid, "operation path contains invalid path parameter syntax", map[string]any{"path": path})
		}
		if start < 0 {
			break
		}
		endRel := strings.Index(remaining[start+1:], "}")
		if endRel < 0 {
			return nil, contracts.NewRuntimeError(contracts.CodeToolArgumentInvalid, "operation path contains invalid path parameter syntax", map[string]any{"path": path})
		}
		name := strings.TrimSpace(remaining[start+1 : start+1+endRel])
		if name == "" {
			return nil, contracts.NewRuntimeError(contracts.CodeToolArgumentInvalid, "operation path contains an empty path parameter", map[string]any{"path": path})
		}
		out = append(out, name)
		remaining = remaining[start+1+endRel+1:]
	}
	if strings.Contains(remaining, "}") {
		return nil, contracts.NewRuntimeError(contracts.CodeToolArgumentInvalid, "operation path contains invalid path parameter syntax", map[string]any{"path": path})
	}
	return out, nil
}

func validateScalarMappingSection(field string, value any) error {
	typed, ok := value.(map[string]any)
	if !ok {
		return contracts.NewRuntimeError(contracts.CodeToolArgumentInvalid, field+" must be an object mapping", map[string]any{"field": field})
	}
	for key, child := range typed {
		if strings.TrimSpace(key) == "" {
			return contracts.NewRuntimeError(contracts.CodeToolArgumentInvalid, field+" contains an empty target field", map[string]any{"field": field})
		}
		mapped, ok := child.(string)
		if !ok || strings.TrimSpace(mapped) == "" {
			return contracts.NewRuntimeError(contracts.CodeToolArgumentInvalid, field+"."+key+" must be a path string", map[string]any{"field": field + "." + key})
		}
	}
	return nil
}

func validateHeaderMappingKeys(value any) error {
	typed, ok := value.(map[string]any)
	if !ok {
		return nil
	}
	for key := range typed {
		if err := validateHTTPHeaderName("request_mapping.headers."+key, key); err != nil {
			return err
		}
	}
	return nil
}

func validateHTTPHeaders(field string, headers map[string]string) error {
	for key, value := range headers {
		if err := validateHTTPHeaderName(field+"."+key, key); err != nil {
			return err
		}
		if strings.ContainsAny(value, "\r\n") {
			return contracts.NewRuntimeError(contracts.CodeToolArgumentInvalid, "headers contains an invalid header value", map[string]any{"field": field + "." + key})
		}
	}
	return nil
}

func validateHTTPHeaderName(field string, name string) error {
	name = strings.TrimSpace(name)
	if name == "" || strings.ContainsAny(name, ":\r\n") {
		if strings.HasPrefix(field, "request_mapping.headers.") {
			return contracts.NewRuntimeError(contracts.CodeToolArgumentInvalid, "request_mapping.headers contains an invalid header name", map[string]any{"field": field})
		}
		return contracts.NewRuntimeError(contracts.CodeToolArgumentInvalid, "headers contains an invalid header name", map[string]any{"field": field})
	}
	if strings.EqualFold(name, providerAuthRefHeader) {
		return contracts.NewRuntimeError(contracts.CodeToolArgumentInvalid, "headers must not override platform auth header", map[string]any{"field": field})
	}
	return nil
}

func validateMappingKeys(field string, mapping map[string]any, allowed []string) error {
	if len(mapping) == 0 {
		return nil
	}
	allowedSet := map[string]struct{}{}
	for _, key := range allowed {
		allowedSet[key] = struct{}{}
	}
	for key := range mapping {
		if _, ok := allowedSet[key]; !ok {
			return contracts.NewRuntimeError(contracts.CodeToolArgumentInvalid, "unsupported mapping field", map[string]any{"field": field + "." + key})
		}
	}
	return nil
}

func validateMappingSection(field string, value any) error {
	switch typed := value.(type) {
	case string:
		if strings.TrimSpace(typed) == "" {
			return contracts.NewRuntimeError(contracts.CodeToolArgumentInvalid, field+" must not be empty", map[string]any{"field": field})
		}
	case map[string]any:
		for key, child := range typed {
			if strings.TrimSpace(key) == "" {
				return contracts.NewRuntimeError(contracts.CodeToolArgumentInvalid, field+" contains an empty target field", map[string]any{"field": field})
			}
			switch mapped := child.(type) {
			case string:
				if strings.TrimSpace(mapped) == "" {
					return contracts.NewRuntimeError(contracts.CodeToolArgumentInvalid, field+"."+key+" must not be empty", map[string]any{"field": field + "." + key})
				}
			case map[string]any:
				if err := validateMappingSection(field+"."+key, mapped); err != nil {
					return err
				}
			default:
				return contracts.NewRuntimeError(contracts.CodeToolArgumentInvalid, field+"."+key+" must be a path string or nested object", map[string]any{"field": field + "." + key})
			}
		}
	default:
		return contracts.NewRuntimeError(contracts.CodeToolArgumentInvalid, field+" must be a path string or object mapping", map[string]any{"field": field})
	}
	return nil
}

func jsonSchemaTypes(field string, value any) ([]string, error) {
	switch typed := value.(type) {
	case string:
		if !validJSONSchemaType(typed) {
			return nil, contracts.NewRuntimeError(contracts.CodeToolArgumentInvalid, "unsupported JSON schema type", map[string]any{"field": field, "type": typed})
		}
		return []string{typed}, nil
	case []any:
		if len(typed) == 0 {
			return nil, contracts.NewRuntimeError(contracts.CodeToolArgumentInvalid, field+" must not be empty", map[string]any{"field": field})
		}
		out := make([]string, 0, len(typed))
		seen := map[string]struct{}{}
		for _, item := range typed {
			itemType, ok := item.(string)
			if !ok || !validJSONSchemaType(itemType) {
				return nil, contracts.NewRuntimeError(contracts.CodeToolArgumentInvalid, "unsupported JSON schema type", map[string]any{"field": field, "type": item})
			}
			if _, exists := seen[itemType]; exists {
				return nil, contracts.NewRuntimeError(contracts.CodeToolArgumentInvalid, field+" must not contain duplicate types", map[string]any{"field": field, "type": itemType})
			}
			seen[itemType] = struct{}{}
			out = append(out, itemType)
		}
		return out, nil
	case []string:
		if len(typed) == 0 {
			return nil, contracts.NewRuntimeError(contracts.CodeToolArgumentInvalid, field+" must not be empty", map[string]any{"field": field})
		}
		out := make([]string, 0, len(typed))
		seen := map[string]struct{}{}
		for _, itemType := range typed {
			if !validJSONSchemaType(itemType) {
				return nil, contracts.NewRuntimeError(contracts.CodeToolArgumentInvalid, "unsupported JSON schema type", map[string]any{"field": field, "type": itemType})
			}
			if _, exists := seen[itemType]; exists {
				return nil, contracts.NewRuntimeError(contracts.CodeToolArgumentInvalid, field+" must not contain duplicate types", map[string]any{"field": field, "type": itemType})
			}
			seen[itemType] = struct{}{}
			out = append(out, itemType)
		}
		return out, nil
	default:
		return nil, contracts.NewRuntimeError(contracts.CodeToolArgumentInvalid, field+" must be a string or array of strings", map[string]any{"field": field})
	}
}

func validJSONSchemaType(value string) bool {
	switch value {
	case "object", "array", "string", "number", "integer", "boolean", "null":
		return true
	default:
		return false
	}
}

func jsonSchemaStringArray(value any) bool {
	switch typed := value.(type) {
	case []string:
		for _, item := range typed {
			if strings.TrimSpace(item) == "" {
				return false
			}
		}
		return true
	case []any:
		for _, item := range typed {
			text, ok := item.(string)
			if !ok || strings.TrimSpace(text) == "" {
				return false
			}
		}
		return true
	default:
		return false
	}
}

func jsonSchemaNonEmptyArray(value any) bool {
	switch typed := value.(type) {
	case []string:
		return len(typed) > 0
	case []any:
		return len(typed) > 0
	default:
		return false
	}
}

func containsString(items []string, want string) bool {
	for _, item := range items {
		if item == want {
			return true
		}
	}
	return false
}

func validVisibility(visibility contracts.ToolVisibility) bool {
	switch visibility {
	case contracts.ToolPrivate, contracts.ToolProtected, contracts.ToolExposed:
		return true
	default:
		return false
	}
}

func validHTTPMethod(method string) bool {
	switch strings.ToUpper(strings.TrimSpace(method)) {
	case http.MethodGet, http.MethodPost, http.MethodPut, http.MethodPatch, http.MethodDelete:
		return true
	default:
		return false
	}
}

func providerUsesProviderConnection(providerType string) bool {
	return providerType == ProviderTypeStaticToolHost || providerType == ProviderTypeAgentPlugin || providerType == ProviderTypeMCP
}

func providerIsManagedAdapter(providerType string) bool {
	return providerType == ProviderTypeHTTPAPIAdapter || providerType == ProviderTypeDatabaseAdapter
}

func executorIsManagedAdapter(executorType string) bool {
	return executorType == ExecutorTypeHTTPAPIAdapter || executorType == ExecutorTypeDatabaseAdapter
}

func managedAdapterProviderIDForType(providerType string) string {
	switch providerType {
	case ProviderTypeHTTPAPIAdapter:
		return ManagedHTTPAPIAdapterID
	case ProviderTypeDatabaseAdapter:
		return ManagedDatabaseAdapterID
	default:
		return ""
	}
}

func managedAdapterProviderTypeForID(providerID string) string {
	switch strings.TrimSpace(providerID) {
	case ManagedHTTPAPIAdapterID:
		return ProviderTypeHTTPAPIAdapter
	case ManagedDatabaseAdapterID:
		return ProviderTypeDatabaseAdapter
	default:
		return ""
	}
}

func managedAdapterProviderName(providerType string) string {
	switch providerType {
	case ProviderTypeHTTPAPIAdapter:
		return "Managed HTTP API Adapter"
	case ProviderTypeDatabaseAdapter:
		return "Managed Database Adapter"
	default:
		return "Managed Adapter"
	}
}

func (s *Service) ensureManagedAdapterProvider(ctx context.Context, tenantID contracts.TenantID, providerID string, actorID string) (ToolProvider, error) {
	providerID = strings.TrimSpace(providerID)
	providerType := managedAdapterProviderTypeForID(providerID)
	if providerType == "" {
		return ToolProvider{}, contracts.NewRuntimeError(contracts.CodeToolArgumentInvalid, "adapter operation provider_id must be a managed adapter provider", map[string]any{
			"provider_id":                  providerID,
			"http_api_adapter_provider_id": ManagedHTTPAPIAdapterID,
			"database_adapter_provider_id": ManagedDatabaseAdapterID,
		})
	}
	if provider, ok := s.provider(tenantID, providerID); ok {
		provider = normalizeProvider(provider)
		if provider.ProviderType != providerType {
			return ToolProvider{}, contracts.NewRuntimeError(contracts.CodeToolArgumentInvalid, "managed adapter provider has unexpected provider_type", map[string]any{"provider_id": providerID, "provider_type": provider.ProviderType, "expected_provider_type": providerType})
		}
		if strings.TrimSpace(provider.ServiceConnectionID) != "" {
			return ToolProvider{}, contracts.NewRuntimeError(contracts.CodeToolArgumentInvalid, "managed adapter provider must not set service_connection_id; set it on adapter operations", map[string]any{"provider_type": provider.ProviderType})
		}
		if provider.Status == StatusDisabled {
			return ToolProvider{}, contracts.NewRuntimeError(contracts.CodeToolPolicyDenied, "managed adapter provider is disabled", map[string]any{"provider_id": providerID, "status": provider.Status})
		}
		return provider, nil
	}
	provider := normalizeProvider(ToolProvider{
		TenantID:     tenantID,
		ProviderID:   providerID,
		ProviderType: providerType,
		Name:         managedAdapterProviderName(providerType),
		Status:       StatusEnabled,
		HealthStatus: HealthHealthy,
		Version:      "v1",
	})
	if s.store != nil {
		if err := s.store.UpsertProvider(ctx, provider); err != nil {
			return ToolProvider{}, err
		}
	}
	s.storeProviderLocked(provider)
	s.auditEvent(ctx, tenantID, actorID, contracts.AuditToolProviderUpserted, auditResourceToolProvider, provider.ProviderID, "allowed", "managed_adapter_auto_ensure")
	return provider, nil
}

func (s *Service) providerTypeForOperation(operation AdapterOperation) string {
	provider, ok := s.provider(operation.TenantID, operation.ProviderID)
	if !ok {
		return ""
	}
	return provider.ProviderType
}

type ToolHostExecutor struct {
	Endpoint     string
	ProviderID   string
	ConnectionID string
	Operation    string
	Headers      map[string]string
	TimeoutMS    int
	RetryMax     int
	Client       *http.Client
	TenantID     contracts.TenantID
	Trace        trace.Recorder
	Now          func() time.Time
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
		"provider_id":   e.ProviderID,
		"tool_id":       call.ToolID,
		"tool_call_id":  call.ToolCallID,
		"operation":     e.Operation,
		"connection_id": e.ConnectionID,
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
			"provider_id":   e.ProviderID,
			"tool_id":       call.ToolID,
			"tool_call_id":  call.ToolCallID,
			"operation":     e.Operation,
			"connection_id": e.ConnectionID,
			"latency_ms":    int(executorNow(e.Now).Sub(started).Milliseconds()),
			"error_code":    errorCode(err),
		})
		return nil, nil, err
	}
	e.recordTrace(ctx, call, contracts.TraceToolProviderCompleted, map[string]any{
		"provider_id":   e.ProviderID,
		"tool_id":       call.ToolID,
		"tool_call_id":  call.ToolCallID,
		"operation":     e.Operation,
		"connection_id": e.ConnectionID,
		"latency_ms":    int(executorNow(e.Now).Sub(started).Milliseconds()),
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

type HTTPAPIAdapterExecutor struct {
	Endpoint   string
	ProviderID string
	Operation  AdapterOperation
	Headers    map[string]string
	TimeoutMS  int
	RetryMax   int
	Client     *http.Client
	TenantID   contracts.TenantID
	Trace      trace.Recorder
	Now        func() time.Time
}

func (e HTTPAPIAdapterExecutor) NetworkTargetHost() string {
	return urlHost(e.Endpoint)
}

func (e HTTPAPIAdapterExecutor) Execute(ctx context.Context, call contracts.ToolCall) (map[string]any, []contracts.ArtifactRef, error) {
	if e.TenantID != "" && call.TenantID != e.TenantID {
		return nil, nil, contracts.NewRuntimeError(contracts.CodeToolPolicyDenied, "tool tenant does not match call tenant", nil)
	}
	started := executorNow(e.Now)
	e.recordTrace(ctx, call, contracts.TraceToolProviderInvoked, map[string]any{
		"provider_id":   e.ProviderID,
		"tool_id":       call.ToolID,
		"tool_call_id":  call.ToolCallID,
		"operation":     e.Operation.OperationID,
		"connection_id": e.Operation.ServiceConnectionID,
	})
	method := strings.ToUpper(strings.TrimSpace(e.Operation.Method))
	_, hasPathParamMapping := e.Operation.RequestMapping["path_params"]
	pathParams, err := applyHTTPRequestScalarMapping(e.Operation.RequestMapping, "path_params", call.Arguments)
	if err != nil {
		e.recordProviderFailure(ctx, call, started, err)
		return nil, nil, err
	}
	operationPath, err := applyHTTPPathParams(e.Operation.Path, pathParams)
	if err != nil {
		e.recordProviderFailure(ctx, call, started, err)
		return nil, nil, err
	}
	targetURL := joinURL(e.Endpoint, operationPath)
	if _, hasQueryMapping := e.Operation.RequestMapping["query"]; hasQueryMapping || (!hasPathParamMapping && (method == http.MethodGet || method == http.MethodDelete)) {
		query, err := applyHTTPRequestMapping(e.Operation.RequestMapping, "query", call.Arguments)
		if err != nil {
			e.recordProviderFailure(ctx, call, started, err)
			return nil, nil, err
		}
		targetURL = appendQuery(targetURL, query)
	}
	mappedHeaders, err := applyHTTPRequestScalarMapping(e.Operation.RequestMapping, "headers", call.Arguments)
	if err != nil {
		e.recordProviderFailure(ctx, call, started, err)
		return nil, nil, err
	}
	headers := mergeHeaders(e.Headers, mappedHeaders)
	var payload any
	if method != http.MethodGet && method != http.MethodDelete {
		mappedBody, err := applyHTTPRequestMapping(e.Operation.RequestMapping, "body", call.Arguments)
		if err != nil {
			e.recordProviderFailure(ctx, call, started, err)
			return nil, nil, err
		}
		payload = mappedBody
	}
	var raw json.RawMessage
	if err := requestJSON(ctx, e.Client, method, targetURL, headers, payload, &raw, requestOptions{
		TimeoutMS: e.TimeoutMS,
		RetryMax:  e.RetryMax,
	}); err != nil {
		e.recordProviderFailure(ctx, call, started, err)
		return nil, nil, err
	}
	value, err := decodeAdapterValue(raw)
	if err != nil {
		e.recordProviderFailure(ctx, call, started, err)
		return nil, nil, err
	}
	output, err := applyHTTPResponseMapping(e.Operation.ResponseMapping, value)
	if err != nil {
		e.recordProviderFailure(ctx, call, started, err)
		return nil, nil, err
	}
	e.recordTrace(ctx, call, contracts.TraceToolProviderCompleted, map[string]any{
		"provider_id":   e.ProviderID,
		"tool_id":       call.ToolID,
		"tool_call_id":  call.ToolCallID,
		"operation":     e.Operation.OperationID,
		"connection_id": e.Operation.ServiceConnectionID,
		"latency_ms":    int(executorNow(e.Now).Sub(started).Milliseconds()),
	})
	return output, nil, nil
}

func (e HTTPAPIAdapterExecutor) recordProviderFailure(ctx context.Context, call contracts.ToolCall, started time.Time, err error) {
	e.recordTrace(ctx, call, contracts.TraceToolProviderFailed, map[string]any{
		"provider_id":   e.ProviderID,
		"tool_id":       call.ToolID,
		"tool_call_id":  call.ToolCallID,
		"operation":     e.Operation.OperationID,
		"connection_id": e.Operation.ServiceConnectionID,
		"latency_ms":    int(executorNow(e.Now).Sub(started).Milliseconds()),
		"error_code":    errorCode(err),
	})
}

func (e HTTPAPIAdapterExecutor) recordTrace(ctx context.Context, call contracts.ToolCall, eventType string, payload map[string]any) {
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

type DatabaseAdapterExecutor struct {
	Connection serviceconnection.ServiceConnection
	ProviderID string
	Operation  AdapterOperation
	TenantID   contracts.TenantID
	Trace      trace.Recorder
	Now        func() time.Time
	OpenDB     func(driverName string, dataSourceName string) (*sql.DB, error)
}

func (e DatabaseAdapterExecutor) Execute(ctx context.Context, call contracts.ToolCall) (map[string]any, []contracts.ArtifactRef, error) {
	if e.TenantID != "" && call.TenantID != e.TenantID {
		return nil, nil, contracts.NewRuntimeError(contracts.CodeToolPolicyDenied, "tool tenant does not match call tenant", nil)
	}
	started := executorNow(e.Now)
	e.recordTrace(ctx, call, contracts.TraceToolProviderInvoked, map[string]any{
		"provider_id":   e.ProviderID,
		"tool_id":       call.ToolID,
		"tool_call_id":  call.ToolCallID,
		"operation":     e.Operation.OperationID,
		"connection_id": e.Connection.ConnectionID,
	})
	output, err := e.executeQuery(ctx, call.Arguments)
	if err != nil {
		e.recordTrace(ctx, call, contracts.TraceToolProviderFailed, map[string]any{
			"provider_id":   e.ProviderID,
			"tool_id":       call.ToolID,
			"tool_call_id":  call.ToolCallID,
			"operation":     e.Operation.OperationID,
			"connection_id": e.Connection.ConnectionID,
			"latency_ms":    int(executorNow(e.Now).Sub(started).Milliseconds()),
			"error_code":    errorCode(err),
		})
		return nil, nil, err
	}
	e.recordTrace(ctx, call, contracts.TraceToolProviderCompleted, map[string]any{
		"provider_id":   e.ProviderID,
		"tool_id":       call.ToolID,
		"tool_call_id":  call.ToolCallID,
		"operation":     e.Operation.OperationID,
		"connection_id": e.Connection.ConnectionID,
		"latency_ms":    int(executorNow(e.Now).Sub(started).Milliseconds()),
		"row_count":     output["row_count"],
	})
	return output, nil, nil
}

func (e DatabaseAdapterExecutor) executeQuery(ctx context.Context, arguments map[string]any) (map[string]any, error) {
	if !e.Operation.ReadOnly {
		return nil, contracts.NewRuntimeError(contracts.CodeToolPolicyDenied, "database_adapter operation must be read_only", nil)
	}
	query, args, err := buildReadOnlySQL(e.Operation, arguments)
	if err != nil {
		return nil, err
	}
	openDB := e.OpenDB
	if openDB == nil {
		openDB = sql.Open
	}
	driverName, err := serviceconnection.DatabaseDriverName(e.Connection)
	if err != nil {
		return nil, err
	}
	db, err := openDB(driverName, strings.TrimSpace(e.Connection.BaseURL))
	if err != nil {
		return nil, err
	}
	defer db.Close()
	db.SetMaxOpenConns(1)
	db.SetMaxIdleConns(0)
	reqCtx, cancel := contextWithTimeout(ctx, e.Connection.TimeoutMS)
	defer cancel()
	rows, err := db.QueryContext(reqCtx, query, args...)
	if err != nil {
		return nil, contracts.NewRuntimeError(contracts.CodeToolExecutionFailed, "database query failed", map[string]any{"provider_id": e.ProviderID, "operation": e.Operation.OperationID, "error": err.Error()})
	}
	defer rows.Close()
	resultRows, err := scanSQLRows(rows, e.Operation.MaxRows, e.Operation.RedactColumns)
	if err != nil {
		return nil, err
	}
	return map[string]any{"rows": resultRows, "row_count": len(resultRows)}, nil
}

func (e DatabaseAdapterExecutor) recordTrace(ctx context.Context, call contracts.ToolCall, eventType string, payload map[string]any) {
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
	connection, err := s.providerConnection(ctx, provider)
	if err != nil {
		return providerCatalog{}, err
	}
	paths := []string{"/tools/catalog", "/tools", "/.well-known/agent-plugin.json"}
	var lastErr error
	for _, path := range paths {
		catalog, err := s.fetchCatalogPath(ctx, provider, connection, path)
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

func (s *Service) fetchCatalogPath(ctx context.Context, provider ToolProvider, connection serviceconnection.ServiceConnection, path string) (providerCatalog, error) {
	var raw json.RawMessage
	if err := requestJSON(ctx, s.client, http.MethodGet, joinURL(connection.BaseURL, path), connectionHeaders(connection), nil, &raw, requestOptions{
		TimeoutMS: connection.TimeoutMS,
		RetryMax:  connection.RetryMax,
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
	if item.OutputSchema == nil {
		item.OutputSchema = item.OutputCamel
	}
	item.RiskLevel = contracts.RiskLevel(firstCatalogString(string(item.RiskLevel), string(item.Risk), string(item.RiskCamel), string(contracts.RiskLow)))
	if item.Visibility == "" {
		item.Visibility = contracts.ToolProtected
	}
	item.Version = firstCatalogString(item.Version, "v1")
	return item
}

func buildReadOnlySQL(operation AdapterOperation, arguments map[string]any) (string, []any, error) {
	template := strings.TrimSpace(operation.QueryTemplate)
	if template == "" {
		return "", nil, contracts.NewRuntimeError(contracts.CodeToolArgumentInvalid, "query_template is required", nil)
	}
	if !isReadOnlySQL(template) {
		return "", nil, contracts.NewRuntimeError(contracts.CodeToolPolicyDenied, "database_adapter only allows read-only SQL", map[string]any{"operation": operation.OperationID})
	}
	return bindSQLParameters(template, arguments)
}

func isReadOnlySQL(query string) bool {
	query = strings.TrimSpace(stripSQLComments(query))
	if query == "" || strings.Contains(query, ";") {
		return false
	}
	first := firstSQLToken(query)
	switch strings.ToLower(first) {
	case "select", "with", "explain", "show":
		return !containsWriteSQLToken(query)
	default:
		return false
	}
}

func stripSQLComments(query string) string {
	lines := strings.Split(query, "\n")
	for i, line := range lines {
		if idx := strings.Index(line, "--"); idx >= 0 {
			lines[i] = line[:idx]
		}
	}
	return strings.Join(lines, "\n")
}

func firstSQLToken(query string) string {
	fields := strings.Fields(query)
	if len(fields) == 0 {
		return ""
	}
	return strings.Trim(fields[0], " \t\r\n(")
}

func containsWriteSQLToken(query string) bool {
	blocked := []string{"insert", "update", "delete", "drop", "alter", "truncate", "create", "grant", "revoke", "copy", "call", "execute", "merge"}
	lower := strings.ToLower(query)
	for _, token := range blocked {
		if containsSQLWord(lower, token) {
			return true
		}
	}
	return false
}

func containsSQLWord(query string, word string) bool {
	for _, field := range strings.FieldsFunc(query, func(r rune) bool {
		return !(r >= 'a' && r <= 'z') && !(r >= '0' && r <= '9') && r != '_'
	}) {
		if field == word {
			return true
		}
	}
	return false
}

func databaseQueryRelationIdentifiers(query string) []string {
	tokens := sqlLexTokens(stripSQLComments(query))
	cteNames := sqlCTENames(tokens)
	relations := make([]string, 0)
	seen := map[string]struct{}{}
	for i := 0; i < len(tokens); i++ {
		token := strings.ToLower(tokens[i])
		if token != "from" && token != "join" {
			continue
		}
		if i+1 >= len(tokens) {
			continue
		}
		next := strings.ToLower(tokens[i+1])
		if next == "(" || isSQLRelationSkipToken(next) {
			continue
		}
		relation, consumed := sqlRelationIdentifierAt(tokens, i+1)
		if relation == "" {
			continue
		}
		if _, ok := cteNames[relation]; ok {
			continue
		}
		if _, ok := seen[relation]; ok {
			continue
		}
		seen[relation] = struct{}{}
		relations = append(relations, relation)
		if token == "from" {
			nextIndex := i + 1 + consumed
			for nextIndex < len(tokens) {
				lower := strings.ToLower(tokens[nextIndex])
				if lower == "join" || isSQLRelationBoundaryToken(lower) {
					break
				}
				if tokens[nextIndex] != "," {
					nextIndex++
					continue
				}
				nextRelation, nextConsumed := sqlRelationIdentifierAt(tokens, nextIndex+1)
				if nextRelation == "" {
					break
				}
				if _, ok := cteNames[nextRelation]; !ok {
					if _, exists := seen[nextRelation]; !exists {
						seen[nextRelation] = struct{}{}
						relations = append(relations, nextRelation)
					}
				}
				nextIndex += 1 + nextConsumed
			}
			i = nextIndex - 1
			continue
		}
		i += consumed
	}
	return relations
}

func sqlRelationIdentifierAt(tokens []string, start int) (string, int) {
	parts := make([]string, 0, 2)
	i := start
	for i < len(tokens) {
		token := tokens[i]
		lower := strings.ToLower(token)
		if token == "." {
			i++
			continue
		}
		if lower == "," || lower == ")" || lower == "(" || isSQLRelationBoundaryToken(lower) {
			break
		}
		part := normalizeSQLRelationIdentifier(token)
		if part == "" {
			break
		}
		parts = append(parts, part)
		i++
		if i >= len(tokens) || tokens[i] != "." {
			break
		}
		i++
	}
	if len(parts) == 0 {
		return "", 0
	}
	return strings.Join(parts, "."), i - start
}

func sqlCTENames(tokens []string) map[string]struct{} {
	names := map[string]struct{}{}
	if len(tokens) == 0 || strings.ToLower(tokens[0]) != "with" {
		return names
	}
	i := 1
	if i < len(tokens) && strings.ToLower(tokens[i]) == "recursive" {
		i++
	}
	for i < len(tokens) {
		name := normalizeSQLRelationIdentifier(tokens[i])
		if name == "" || isSQLRelationBoundaryToken(strings.ToLower(tokens[i])) {
			return names
		}
		names[name] = struct{}{}
		i++
		if i < len(tokens) && tokens[i] == "(" {
			depth := 1
			i++
			for i < len(tokens) && depth > 0 {
				if tokens[i] == "(" {
					depth++
				}
				if tokens[i] == ")" {
					depth--
				}
				i++
			}
		}
		if i >= len(tokens) || strings.ToLower(tokens[i]) != "as" {
			return names
		}
		i++
		if i >= len(tokens) || tokens[i] != "(" {
			return names
		}
		depth := 1
		i++
		for i < len(tokens) && depth > 0 {
			if tokens[i] == "(" {
				depth++
			}
			if tokens[i] == ")" {
				depth--
			}
			i++
		}
		if i >= len(tokens) || tokens[i] != "," {
			return names
		}
		i++
	}
	return names
}

func sqlLexTokens(query string) []string {
	tokens := make([]string, 0)
	for i := 0; i < len(query); {
		ch := query[i]
		if ch == '\'' {
			i++
			for i < len(query) {
				if query[i] == '\'' {
					i++
					if i < len(query) && query[i] == '\'' {
						i++
						continue
					}
					break
				}
				i++
			}
			continue
		}
		if ch == '"' {
			start := i
			i++
			for i < len(query) {
				if query[i] == '"' {
					i++
					if i < len(query) && query[i] == '"' {
						i++
						continue
					}
					break
				}
				i++
			}
			tokens = append(tokens, query[start:i])
			continue
		}
		if isSQLIdentifierByte(ch) || ch == '.' {
			start := i
			for i < len(query) && (isSQLIdentifierByte(query[i]) || query[i] == '.') {
				i++
			}
			tokens = append(tokens, query[start:i])
			continue
		}
		if ch == '(' || ch == ')' || ch == ',' {
			tokens = append(tokens, string(ch))
		}
		i++
	}
	return tokens
}

func isSQLIdentifierByte(ch byte) bool {
	return (ch >= 'a' && ch <= 'z') || (ch >= 'A' && ch <= 'Z') || (ch >= '0' && ch <= '9') || ch == '_'
}

func isSQLRelationSkipToken(token string) bool {
	switch token {
	case "select", "values", "lateral", "unnest", "json_each":
		return true
	default:
		return false
	}
}

func isSQLRelationBoundaryToken(token string) bool {
	switch token {
	case "where", "on", "group", "order", "limit", "offset", "union", "inner", "left", "right", "full", "cross", "join", "from", "having":
		return true
	default:
		return false
	}
}

func normalizeSQLRelationIdentifier(value string) string {
	value = strings.TrimSpace(strings.ToLower(value))
	if value == "" {
		return ""
	}
	value = strings.Trim(value, ",()")
	replacer := strings.NewReplacer(`"`, "", "`", "", "[", "", "]", "")
	value = replacer.Replace(value)
	value = strings.ReplaceAll(value, " ", "")
	value = strings.Trim(value, ".")
	return value
}

func bindSQLParameters(template string, arguments map[string]any) (string, []any, error) {
	var out strings.Builder
	args := make([]any, 0)
	for i := 0; i < len(template); i++ {
		if template[i] == ':' && i+1 < len(template) && template[i+1] == ':' {
			out.WriteString("::")
			i++
			continue
		}
		if template[i] != ':' || i+1 >= len(template) || !isSQLParamStart(template[i+1]) {
			out.WriteByte(template[i])
			continue
		}
		j := i + 2
		for j < len(template) && isSQLParamPart(template[j]) {
			j++
		}
		name := template[i+1 : j]
		value, ok := arguments[name]
		if !ok {
			return "", nil, contracts.NewRuntimeError(contracts.CodeToolArgumentInvalid, "missing database query parameter", map[string]any{"parameter": name})
		}
		args = append(args, value)
		out.WriteString(fmt.Sprintf("$%d", len(args)))
		i = j - 1
	}
	return out.String(), args, nil
}

func isSQLParamStart(ch byte) bool {
	return (ch >= 'a' && ch <= 'z') || (ch >= 'A' && ch <= 'Z') || ch == '_'
}

func isSQLParamPart(ch byte) bool {
	return isSQLParamStart(ch) || (ch >= '0' && ch <= '9')
}

func scanSQLRows(rows *sql.Rows, maxRows int, redactColumns []string) ([]map[string]any, error) {
	columns, err := rows.Columns()
	if err != nil {
		return nil, err
	}
	redacted := redactColumnSet(redactColumns)
	out := []map[string]any{}
	limit := maxRows
	if limit <= 0 {
		limit = 100
	}
	for rows.Next() {
		values := make([]any, len(columns))
		dest := make([]any, len(columns))
		for i := range values {
			dest[i] = &values[i]
		}
		if err := rows.Scan(dest...); err != nil {
			return nil, err
		}
		row := map[string]any{}
		for i, column := range columns {
			if _, ok := redacted[strings.ToLower(strings.TrimSpace(column))]; ok {
				row[column] = "[REDACTED]"
				continue
			}
			row[column] = normalizeSQLValue(values[i])
		}
		out = append(out, row)
		if len(out) >= limit {
			break
		}
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	return out, nil
}

func redactColumnSet(columns []string) map[string]struct{} {
	out := map[string]struct{}{}
	for _, column := range columns {
		if normalized := strings.ToLower(strings.TrimSpace(column)); normalized != "" {
			out[normalized] = struct{}{}
		}
	}
	return out
}

func normalizeSQLValue(value any) any {
	switch v := value.(type) {
	case []byte:
		return string(v)
	case time.Time:
		return v.UTC().Format(time.RFC3339Nano)
	default:
		return v
	}
}

func normalizeStringList(values []string) []string {
	out := make([]string, 0, len(values))
	seen := map[string]struct{}{}
	for _, value := range values {
		trimmed := strings.TrimSpace(value)
		if trimmed == "" {
			continue
		}
		key := strings.ToLower(trimmed)
		if _, ok := seen[key]; ok {
			continue
		}
		seen[key] = struct{}{}
		out = append(out, trimmed)
	}
	if len(out) == 0 {
		return nil
	}
	return out
}

func firstCatalogString(values ...string) string {
	for _, value := range values {
		if trimmed := strings.TrimSpace(value); trimmed != "" {
			return trimmed
		}
	}
	return ""
}

func metadataString(values map[string]any, key string) string {
	if len(values) == 0 {
		return ""
	}
	value, ok := values[key]
	if !ok || value == nil {
		return ""
	}
	text, ok := value.(string)
	if !ok {
		return ""
	}
	return strings.TrimSpace(text)
}

func parseHTTPResourceID(resourceID string) (string, string) {
	parts := strings.Fields(strings.TrimSpace(resourceID))
	if len(parts) < 2 {
		return "", ""
	}
	return strings.ToUpper(parts[0]), strings.Join(parts[1:], " ")
}

func openAPIResourceInputSchemaAndMapping(resource serviceconnection.ServiceConnectionResource) (map[string]any, map[string]any) {
	properties := map[string]any{}
	required := []any{}
	mapping := map[string]any{}
	pathParams := map[string]any{}
	query := map[string]any{}
	headers := map[string]any{}
	for _, rawParam := range openAPIResourceParameters(resource) {
		param, ok := rawParam.(map[string]any)
		if !ok {
			continue
		}
		name := metadataString(param, "name")
		location := strings.ToLower(metadataString(param, "in"))
		if name == "" || location == "" {
			continue
		}
		properties[name] = openAPIParameterSchema(param)
		requiredValue, _ := param["required"].(bool)
		if requiredValue || location == "path" {
			required = append(required, name)
		}
		switch location {
		case "path":
			pathParams[name] = name
		case "query":
			query[name] = name
		case "header":
			if !strings.EqualFold(name, providerAuthRefHeader) {
				headers[name] = name
			}
		}
	}
	if bodySchema := openAPIRequestBodySchema(resource); bodySchema != nil {
		properties["body"] = bodySchema
		if openAPIRequestBodyRequired(resource) {
			required = append(required, "body")
		}
		mapping["body"] = "body"
	}
	if len(pathParams) > 0 {
		mapping["path_params"] = pathParams
	}
	if len(query) > 0 {
		mapping["query"] = query
	}
	if len(headers) > 0 {
		mapping["headers"] = headers
	}
	schema := map[string]any{
		"type":       "object",
		"properties": properties,
	}
	if len(required) > 0 {
		schema["required"] = required
	}
	return schema, mapping
}

func openAPIResourceParameters(resource serviceconnection.ServiceConnectionResource) []any {
	raw, ok := resource.Schema["parameters"].([]any)
	if ok {
		return raw
	}
	return nil
}

func openAPIParameterSchema(param map[string]any) map[string]any {
	if schema, ok := param["schema"].(map[string]any); ok && len(schema) > 0 {
		return normalizeOpenAPISchema(schema, "string")
	}
	return map[string]any{"type": "string"}
}

func openAPIRequestBodySchema(resource serviceconnection.ServiceConnectionResource) map[string]any {
	body, ok := resource.Schema["request_body"].(map[string]any)
	if !ok {
		return nil
	}
	return normalizeOpenAPISchema(openAPIContentSchema(body["content"]), "object")
}

func openAPIRequestBodyRequired(resource serviceconnection.ServiceConnectionResource) bool {
	body, ok := resource.Schema["request_body"].(map[string]any)
	if !ok {
		return false
	}
	required, _ := body["required"].(bool)
	return required
}

func openAPIResourceOutputSchema(resource serviceconnection.ServiceConnectionResource) map[string]any {
	responses, ok := resource.Schema["responses"].(map[string]any)
	if !ok {
		return map[string]any{"type": "object"}
	}
	for _, status := range []string{"200", "201", "202", "204", "default"} {
		if schema := openAPIResponseSchema(responses[status]); schema != nil {
			return normalizeOpenAPISchema(schema, "object")
		}
	}
	statuses := make([]string, 0, len(responses))
	for status := range responses {
		if strings.HasPrefix(status, "2") {
			statuses = append(statuses, status)
		}
	}
	sort.Strings(statuses)
	for _, status := range statuses {
		if schema := openAPIResponseSchema(responses[status]); schema != nil {
			return normalizeOpenAPISchema(schema, "object")
		}
	}
	return map[string]any{"type": "object"}
}

func openAPIResponseSchema(raw any) map[string]any {
	response, ok := raw.(map[string]any)
	if !ok {
		return nil
	}
	if schema := openAPIContentSchema(response["content"]); schema != nil {
		return schema
	}
	return nil
}

func openAPIContentSchema(raw any) map[string]any {
	content, ok := raw.(map[string]any)
	if !ok {
		return nil
	}
	for _, mediaType := range []string{"application/json", "application/*+json", "*/*"} {
		if schema := openAPIMediaTypeSchema(content[mediaType]); schema != nil {
			return schema
		}
	}
	mediaTypes := make([]string, 0, len(content))
	for mediaType := range content {
		mediaTypes = append(mediaTypes, mediaType)
	}
	sort.Strings(mediaTypes)
	for _, mediaType := range mediaTypes {
		if schema := openAPIMediaTypeSchema(content[mediaType]); schema != nil {
			return schema
		}
	}
	return nil
}

func openAPIMediaTypeSchema(raw any) map[string]any {
	media, ok := raw.(map[string]any)
	if !ok {
		return nil
	}
	schema, ok := media["schema"].(map[string]any)
	if !ok || len(schema) == 0 {
		return nil
	}
	return schema
}

func normalizeOpenAPISchema(schema map[string]any, defaultType string) map[string]any {
	if len(schema) == 0 {
		return map[string]any{"type": defaultType}
	}
	out := map[string]any{}
	if rawType, ok := schema["type"]; ok {
		out["type"] = rawType
	} else if _, ok := schema["properties"]; ok {
		out["type"] = "object"
	} else if _, ok := schema["items"]; ok {
		out["type"] = "array"
	} else {
		out["type"] = defaultType
	}
	if raw, ok := schema["properties"].(map[string]any); ok {
		properties := map[string]any{}
		for name, value := range raw {
			child, _ := value.(map[string]any)
			properties[name] = normalizeOpenAPISchema(child, "object")
		}
		out["properties"] = properties
	}
	if raw, ok := schema["required"]; ok {
		out["required"] = raw
	}
	if raw, ok := schema["items"].(map[string]any); ok {
		out["items"] = normalizeOpenAPISchema(raw, "object")
	}
	if raw, ok := schema["additionalProperties"]; ok {
		switch value := raw.(type) {
		case bool:
			out["additionalProperties"] = value
		case map[string]any:
			out["additionalProperties"] = normalizeOpenAPISchema(value, "object")
		}
	}
	if raw, ok := schema["enum"]; ok {
		out["enum"] = raw
	}
	return out
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
		if err != nil {
			cancel()
			lastErr = err
			if attempt < attempts {
				continue
			}
			return err
		}
		retry, err := decodeJSONResponse(resp, target)
		cancel()
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
	if response, ok := target.(*toolInvokeResponse); ok {
		decoder := json.NewDecoder(io.LimitReader(resp.Body, 1<<20))
		decoder.DisallowUnknownFields()
		if err := decoder.Decode(response); err != nil {
			return false, contracts.NewRuntimeError(contracts.CodeToolExecutionFailed, "tool provider returned invalid invoke response: "+err.Error(), nil)
		}
		if response.Error != nil {
			return false, contracts.NewRuntimeError(response.Error.Code, response.Error.Message, response.Error.Details)
		}
		return false, nil
	}
	if err := json.NewDecoder(resp.Body).Decode(target); err != nil {
		return false, err
	}
	return false, nil
}

func connectionHeaders(connection serviceconnection.ServiceConnection) map[string]string {
	if connection.AuthRef == "" {
		return nil
	}
	return map[string]string{providerAuthRefHeader: connection.AuthRef}
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

func appendQuery(rawURL string, args map[string]any) string {
	if len(args) == 0 {
		return rawURL
	}
	parsed, err := url.Parse(rawURL)
	if err != nil {
		return rawURL
	}
	values := parsed.Query()
	for key, value := range args {
		switch typed := value.(type) {
		case nil:
			continue
		case []any:
			for _, item := range typed {
				values.Add(key, fmt.Sprint(item))
			}
		default:
			values.Set(key, fmt.Sprint(typed))
		}
	}
	parsed.RawQuery = values.Encode()
	return parsed.String()
}

func applyHTTPRequestMapping(mapping map[string]any, section string, arguments map[string]any) (map[string]any, error) {
	raw, ok := mapping[section]
	if !ok {
		return arguments, nil
	}
	mapped, err := applyObjectMapping(raw, arguments)
	if err != nil {
		return nil, err
	}
	return mapped, nil
}

func applyHTTPRequestScalarMapping(mapping map[string]any, section string, arguments map[string]any) (map[string]string, error) {
	raw, ok := mapping[section]
	if !ok {
		return nil, nil
	}
	typed, ok := raw.(map[string]any)
	if !ok {
		return nil, contracts.NewRuntimeError(contracts.CodeToolArgumentInvalid, "mapping section must be an object", map[string]any{"field": "request_mapping." + section})
	}
	out := map[string]string{}
	for key, value := range typed {
		target := strings.TrimSpace(key)
		if target == "" {
			return nil, contracts.NewRuntimeError(contracts.CodeToolArgumentInvalid, "mapping contains an empty target field", map[string]any{"field": "request_mapping." + section})
		}
		path, ok := value.(string)
		if !ok || strings.TrimSpace(path) == "" {
			return nil, contracts.NewRuntimeError(contracts.CodeToolArgumentInvalid, "mapping field must be a path string", map[string]any{"field": "request_mapping." + section + "." + target})
		}
		resolved, ok := lookupPath(arguments, path)
		if !ok {
			return nil, contracts.NewRuntimeError(contracts.CodeToolArgumentInvalid, "mapping path did not match source", map[string]any{"field": target, "path": path})
		}
		if resolved == nil {
			continue
		}
		out[target] = fmt.Sprint(resolved)
	}
	if len(out) == 0 {
		return nil, nil
	}
	return out, nil
}

func applyHTTPPathParams(path string, params map[string]string) (string, error) {
	if len(params) == 0 {
		placeholders, err := httpPathPlaceholders(path)
		if err != nil {
			return "", err
		}
		if len(placeholders) > 0 {
			return "", contracts.NewRuntimeError(contracts.CodeToolArgumentInvalid, "operation path contains unresolved path parameter", map[string]any{"param": placeholders[0]})
		}
		return path, nil
	}
	out := path
	for key, value := range params {
		name := strings.TrimSpace(key)
		if name == "" {
			return "", contracts.NewRuntimeError(contracts.CodeToolArgumentInvalid, "path parameter name is required", nil)
		}
		placeholder := "{" + name + "}"
		if !strings.Contains(out, placeholder) {
			return "", contracts.NewRuntimeError(contracts.CodeToolArgumentInvalid, "path parameter placeholder not found", map[string]any{"param": name})
		}
		out = strings.ReplaceAll(out, placeholder, url.PathEscape(value))
	}
	if strings.Contains(out, "{") || strings.Contains(out, "}") {
		return "", contracts.NewRuntimeError(contracts.CodeToolArgumentInvalid, "operation path contains unresolved path parameter", map[string]any{"path": out})
	}
	return out, nil
}

func applyHTTPResponseMapping(mapping map[string]any, value any) (map[string]any, error) {
	selected := value
	if rawPath, ok := mapping["body_path"]; ok {
		path := strings.TrimSpace(fmt.Sprint(rawPath))
		if path != "" {
			resolved, ok := lookupPath(value, path)
			if !ok {
				return nil, contracts.NewRuntimeError(contracts.CodeToolExecutionFailed, "response_mapping.body_path did not match response", map[string]any{"path": path})
			}
			selected = resolved
		}
	}
	if rawMapping, ok := mapping["output"]; ok {
		return applyObjectMappingWithCode(rawMapping, map[string]any{"body": value, "selected": selected}, contracts.CodeToolExecutionFailed)
	}
	return adapterValueToOutput(selected), nil
}

func applyObjectMapping(mapping any, source any) (map[string]any, error) {
	return applyObjectMappingWithCode(mapping, source, contracts.CodeToolArgumentInvalid)
}

func applyObjectMappingWithCode(mapping any, source any, code contracts.ErrorCode) (map[string]any, error) {
	switch typed := mapping.(type) {
	case string:
		value, ok := lookupPath(source, typed)
		if !ok {
			return nil, contracts.NewRuntimeError(code, "mapping path did not match source", map[string]any{"path": typed})
		}
		out, ok := value.(map[string]any)
		if !ok {
			return nil, contracts.NewRuntimeError(code, "mapping path must resolve to an object", map[string]any{"path": typed})
		}
		return out, nil
	case map[string]any:
		out := make(map[string]any, len(typed))
		for key, value := range typed {
			switch mapped := value.(type) {
			case string:
				resolved, ok := lookupPath(source, mapped)
				if !ok {
					return nil, contracts.NewRuntimeError(code, "mapping path did not match source", map[string]any{"field": key, "path": mapped})
				}
				out[key] = resolved
			case map[string]any:
				nested, err := applyObjectMappingWithCode(mapped, source, code)
				if err != nil {
					return nil, err
				}
				out[key] = nested
			default:
				return nil, contracts.NewRuntimeError(code, "mapping field must be a path string or nested object", map[string]any{"field": key})
			}
		}
		return out, nil
	default:
		return nil, contracts.NewRuntimeError(code, "mapping must be a path string or object mapping", nil)
	}
}

func lookupPath(source any, path string) (any, bool) {
	path = strings.TrimSpace(path)
	if path == "" || path == "." {
		return source, true
	}
	current := source
	for _, part := range strings.Split(path, ".") {
		part = strings.TrimSpace(part)
		if part == "" {
			return nil, false
		}
		switch typed := current.(type) {
		case map[string]any:
			value, ok := typed[part]
			if !ok {
				return nil, false
			}
			current = value
		default:
			return nil, false
		}
	}
	return current, true
}

func decodeAdapterValue(raw json.RawMessage) (any, error) {
	if len(bytes.TrimSpace(raw)) == 0 {
		return map[string]any{}, nil
	}
	var object map[string]any
	if err := json.Unmarshal(raw, &object); err == nil {
		if output, ok := object["output"].(map[string]any); ok && len(object) <= 2 {
			return output, nil
		}
		return object, nil
	}
	var value any
	if err := json.Unmarshal(raw, &value); err != nil {
		return nil, err
	}
	return value, nil
}

func decodeAdapterOutput(raw json.RawMessage) (map[string]any, error) {
	value, err := decodeAdapterValue(raw)
	if err != nil {
		return nil, err
	}
	return adapterValueToOutput(value), nil
}

func adapterValueToOutput(value any) map[string]any {
	if object, ok := value.(map[string]any); ok {
		return object
	}
	return map[string]any{"result": value}
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

func operationKey(tenantID contracts.TenantID, providerID string, operationID string) string {
	return string(tenantID) + "\x00" + providerID + "\x00" + operationID
}

func groupKey(tenantID contracts.TenantID, groupID string) string {
	return string(tenantID) + "\x00" + groupID
}

func manifestKey(tenantID contracts.TenantID, toolID string) string {
	return string(tenantID) + "\x00" + toolID
}

func (s *Service) auditEvent(ctx context.Context, tenantID contracts.TenantID, actorID string, action string, resourceType string, resourceID string, decision string, reason string) {
	if s.audit == nil {
		return
	}
	_ = s.audit.Log(ctx, contracts.AuditEvent{
		AuditID:      idgen.New("audit"),
		TenantID:     tenantID,
		ActorID:      actorID,
		ActorType:    "optimizer",
		Action:       action,
		ResourceType: resourceType,
		ResourceID:   resourceID,
		Decision:     decision,
		Reason:       reason,
		CreatedAt:    s.now(),
	})
}
