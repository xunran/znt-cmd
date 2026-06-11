package domain

import (
	"context"
	"encoding/json"
	"fmt"
	"net"
	"strings"
	"time"

	"znt/internal/contracts"
	"znt/internal/tool/registry"
)

type ExecutionProfile struct {
	ID              string          `json:"id"`
	DomainID        string          `json:"domain_id"`
	Sandbox         string          `json:"sandbox,omitempty"`
	WorkerRef       string          `json:"worker_ref,omitempty"`
	ManagedRuntime  string          `json:"managed_runtime,omitempty"`
	ResourceLimits  ResourceLimits  `json:"resource_limits,omitempty"`
	NetworkPolicy   NetworkPolicy   `json:"network_policy,omitempty"`
	CredentialScope CredentialScope `json:"credential_scope,omitempty"`
	DataBoundary    DataBoundary    `json:"data_boundary,omitempty"`
	Metadata        map[string]any  `json:"metadata,omitempty"`
	Timeout         time.Duration   `json:"-"`
	TimeoutMS       int64           `json:"timeout_ms,omitempty"`
	Raw             json.RawMessage `json:"-"`
}

type ResourceLimits struct {
	CPUMillis int64 `json:"cpu_millis,omitempty"`
	MemoryMB  int64 `json:"memory_mb,omitempty"`
	TimeoutMS int64 `json:"timeout_ms,omitempty"`
}

type NetworkPolicy struct {
	AllowNetwork bool     `json:"allow_network"`
	AllowedHosts []string `json:"allowed_hosts,omitempty"`
}

type CredentialScope struct {
	AllowedCredentialRefs []string `json:"allowed_credential_refs,omitempty"`
	AllowRuntimeSecrets   bool     `json:"allow_runtime_secrets,omitempty"`
}

type DataBoundary struct {
	AllowedTenantIDs  []contracts.TenantID `json:"allowed_tenant_ids,omitempty"`
	AllowedDataScopes []string             `json:"allowed_data_scopes,omitempty"`
	AllowExternalData bool                 `json:"allow_external_data,omitempty"`
}

type ExecutionRequest struct {
	Profile     ExecutionProfile
	Tool        contracts.ToolDefinition
	Call        contracts.ToolCall
	Executor    registry.Executor
	Credentials []ResolvedCredential
}

type ExecutionResult struct {
	Output       map[string]any
	ArtifactRefs []contracts.ArtifactRef
	Metadata     map[string]any
}

type ResolvedCredential struct {
	Ref       string             `json:"ref"`
	TenantID  contracts.TenantID `json:"tenant_id,omitempty"`
	SecretRef string             `json:"secret_ref,omitempty"`
	Scopes    []string           `json:"scopes,omitempty"`
	Metadata  map[string]any     `json:"metadata,omitempty"`
}

type CredentialResolveRequest struct {
	TenantID      contracts.TenantID
	ActorID       string
	ActorType     string
	AgentID       contracts.AgentID
	ToolID        string
	DomainID      string
	CredentialRef string
	DataBoundary  DataBoundary
}

type RuntimeSecretResolveRequest struct {
	TenantID        contracts.TenantID
	ActorID         string
	ActorType       string
	AgentID         contracts.AgentID
	ToolID          string
	DomainID        string
	CredentialScope CredentialScope
	DataBoundary    DataBoundary
}

type CredentialResolver interface {
	ResolveCredential(ctx context.Context, req CredentialResolveRequest) (ResolvedCredential, error)
}

type RuntimeSecretResolver interface {
	ResolveRuntimeSecrets(ctx context.Context, req RuntimeSecretResolveRequest) ([]ResolvedCredential, error)
}

type ExecutionDomain interface {
	ID() string
	Execute(ctx context.Context, req ExecutionRequest) (ExecutionResult, error)
}

type ProductionStatus struct {
	DomainID string `json:"domain_id"`
	Enabled  bool   `json:"enabled"`
	Status   string `json:"status"`
	Details  string `json:"details,omitempty"`
}

const (
	ProductionReadyStatus = "production_ready"
	DisabledStatus        = "disabled"
)

func SingleNodeProductionStatuses() []ProductionStatus {
	return []ProductionStatus{
		{DomainID: "local", Enabled: true, Status: ProductionReadyStatus, Details: "in-process execution with policy and trace coverage"},
		{DomainID: "http", Enabled: true, Status: ProductionReadyStatus, Details: "HTTP execution requires explicit network policy and registered executor"},
		{DomainID: "agent_tool", Enabled: true, Status: ProductionReadyStatus, Details: "internal agent tool execution has server/runtime coverage"},
		{DomainID: "worker", Enabled: false, Status: DisabledStatus, Details: FutureDomainDisabledReason("worker")},
		{DomainID: "sandbox", Enabled: false, Status: DisabledStatus, Details: FutureDomainDisabledReason("sandbox")},
		{DomainID: "managed", Enabled: false, Status: DisabledStatus, Details: FutureDomainDisabledReason("managed")},
	}
}

func FutureDomainDisabledReason(domainID string) string {
	return fmt.Sprintf("%s execution is not production-enabled in single-node mode; configure and inject an explicit adapter before use", domainID)
}

type NetworkTarget interface {
	NetworkTargetHost() string
}

type LocalExecutionDomain struct{}

func (LocalExecutionDomain) ID() string {
	return "local"
}

func (LocalExecutionDomain) Execute(ctx context.Context, req ExecutionRequest) (ExecutionResult, error) {
	if err := validateLocalRequest(req); err != nil {
		return ExecutionResult{}, err
	}
	output, artifacts, err := req.Executor.Execute(ctx, req.Call)
	if err != nil {
		return ExecutionResult{}, err
	}
	return ExecutionResult{
		Output:       output,
		ArtifactRefs: artifacts,
		Metadata:     metadata(req.Profile),
	}, nil
}

type HTTPExecutionDomain struct{}

func (HTTPExecutionDomain) ID() string {
	return "http"
}

func (HTTPExecutionDomain) Execute(ctx context.Context, req ExecutionRequest) (ExecutionResult, error) {
	if req.Executor == nil {
		return ExecutionResult{}, contracts.NewRuntimeError(contracts.CodeExecutionDomainUnavailable, "http execution requires a tool executor", metadata(req.Profile))
	}
	if err := validateHTTPNetworkRequest(req); err != nil {
		return ExecutionResult{}, err
	}
	if len(req.Profile.CredentialScope.AllowedCredentialRefs) > 0 || req.Profile.CredentialScope.AllowRuntimeSecrets {
		return ExecutionResult{}, contracts.NewRuntimeError(contracts.CodeExecutionDomainUnavailable, "http execution cannot receive credential scope", metadata(req.Profile))
	}
	output, artifacts, err := req.Executor.Execute(ctx, req.Call)
	if err != nil {
		return ExecutionResult{}, err
	}
	return ExecutionResult{
		Output:       output,
		ArtifactRefs: artifacts,
		Metadata:     metadata(req.Profile),
	}, nil
}

type AgentToolExecutionDomain struct{}

func (AgentToolExecutionDomain) ID() string {
	return "agent_tool"
}

func (AgentToolExecutionDomain) Execute(ctx context.Context, req ExecutionRequest) (ExecutionResult, error) {
	if req.Executor == nil {
		return ExecutionResult{}, contracts.NewRuntimeError(contracts.CodeExecutionDomainUnavailable, "agent_tool execution requires a tool executor", metadata(req.Profile))
	}
	output, artifacts, err := req.Executor.Execute(ctx, req.Call)
	if err != nil {
		return ExecutionResult{}, err
	}
	return ExecutionResult{
		Output:       output,
		ArtifactRefs: artifacts,
		Metadata:     metadata(req.Profile),
	}, nil
}

type UnavailableDomain struct {
	DomainID string
}

func (d UnavailableDomain) ID() string {
	return d.DomainID
}

func (d UnavailableDomain) Execute(context.Context, ExecutionRequest) (ExecutionResult, error) {
	return ExecutionResult{}, contracts.NewRuntimeError(contracts.CodeExecutionDomainUnavailable, fmt.Sprintf("execution domain %q is configured but has no adapter", d.DomainID), nil)
}

type DisabledExecutionDomain struct {
	DomainID string
	Reason   string
}

func (d DisabledExecutionDomain) ID() string {
	return d.DomainID
}

func (d DisabledExecutionDomain) Execute(_ context.Context, req ExecutionRequest) (ExecutionResult, error) {
	details := metadata(req.Profile)
	details["production_status"] = DisabledStatus
	details["enabled"] = false
	if d.Reason != "" {
		details["reason"] = d.Reason
	}
	return ExecutionResult{}, contracts.NewRuntimeError(contracts.CodeExecutionDomainUnavailable, fmt.Sprintf("execution domain %q is disabled", d.DomainID), details)
}

type WorkerAdapter interface {
	DispatchTool(ctx context.Context, req ExecutionRequest) (ExecutionResult, error)
}

type WorkerExecutionDomain struct {
	Adapter WorkerAdapter
}

func (WorkerExecutionDomain) ID() string {
	return "worker"
}

func (d WorkerExecutionDomain) Execute(ctx context.Context, req ExecutionRequest) (ExecutionResult, error) {
	if d.Adapter == nil {
		return ExecutionResult{}, contracts.NewRuntimeError(contracts.CodeExecutionDomainUnavailable, "worker execution adapter is not configured", metadata(req.Profile))
	}
	result, err := d.Adapter.DispatchTool(ctx, req)
	result.Metadata = mergeMetadata(metadata(req.Profile), result.Metadata)
	return result, err
}

type SandboxAdapter interface {
	RunTool(ctx context.Context, req ExecutionRequest) (ExecutionResult, error)
}

type SandboxExecutionDomain struct {
	Adapter SandboxAdapter
}

func (SandboxExecutionDomain) ID() string {
	return "sandbox"
}

func (d SandboxExecutionDomain) Execute(ctx context.Context, req ExecutionRequest) (ExecutionResult, error) {
	if d.Adapter == nil {
		return ExecutionResult{}, contracts.NewRuntimeError(contracts.CodeExecutionDomainUnavailable, "sandbox execution adapter is not configured", metadata(req.Profile))
	}
	result, err := d.Adapter.RunTool(ctx, req)
	result.Metadata = mergeMetadata(metadata(req.Profile), result.Metadata)
	return result, err
}

type ManagedAdapter interface {
	InvokeManagedTool(ctx context.Context, req ExecutionRequest) (ExecutionResult, error)
}

type ManagedExecutionDomain struct {
	Adapter ManagedAdapter
}

func (ManagedExecutionDomain) ID() string {
	return "managed"
}

func (d ManagedExecutionDomain) Execute(ctx context.Context, req ExecutionRequest) (ExecutionResult, error) {
	if d.Adapter == nil {
		return ExecutionResult{}, contracts.NewRuntimeError(contracts.CodeExecutionDomainUnavailable, "managed execution adapter is not configured", metadata(req.Profile))
	}
	result, err := d.Adapter.InvokeManagedTool(ctx, req)
	result.Metadata = mergeMetadata(metadata(req.Profile), result.Metadata)
	return result, err
}

func WorkerDomain() ExecutionDomain {
	return DisabledExecutionDomain{DomainID: "worker", Reason: FutureDomainDisabledReason("worker")}
}

func HTTPDomain() ExecutionDomain {
	return HTTPExecutionDomain{}
}

func AgentToolDomain() ExecutionDomain {
	return AgentToolExecutionDomain{}
}

func SandboxDomain() ExecutionDomain {
	return DisabledExecutionDomain{DomainID: "sandbox", Reason: FutureDomainDisabledReason("sandbox")}
}

func ManagedDomain() ExecutionDomain {
	return DisabledExecutionDomain{DomainID: "managed", Reason: FutureDomainDisabledReason("managed")}
}

type Resolver struct {
	domains map[string]ExecutionDomain
}

func NewResolver(domains ...ExecutionDomain) Resolver {
	out := Resolver{domains: map[string]ExecutionDomain{}}
	for _, domain := range domains {
		out.domains[domain.ID()] = domain
	}
	return out
}

func (r Resolver) Resolve(profile string) (ExecutionDomain, error) {
	domain, _, err := r.ResolveProfile(profile)
	return domain, err
}

func (r Resolver) ResolveProfile(profile string) (ExecutionDomain, ExecutionProfile, error) {
	parsed, err := ParseProfile(profile)
	if err != nil {
		return nil, ExecutionProfile{}, err
	}
	domain, ok := r.domains[parsed.DomainID]
	if !ok {
		return nil, ExecutionProfile{}, contracts.NewRuntimeError(contracts.CodeExecutionDomainUnavailable, fmt.Sprintf("execution domain %q unavailable", parsed.DomainID), metadata(parsed))
	}
	return domain, parsed, nil
}

func ParseProfile(raw string) (ExecutionProfile, error) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		raw = "local"
	}
	if strings.HasPrefix(raw, "{") {
		var profile ExecutionProfile
		if err := json.Unmarshal([]byte(raw), &profile); err != nil {
			return ExecutionProfile{}, contracts.NewRuntimeError(contracts.CodeToolArgumentInvalid, "invalid execution profile json", map[string]any{"error": err.Error()})
		}
		profile.Raw = append(profile.Raw[:0], []byte(raw)...)
		normalize(&profile)
		return profile, nil
	}
	profile := ExecutionProfile{ID: raw}
	if before, after, ok := strings.Cut(raw, ":"); ok {
		profile.ID = raw
		profile.DomainID = before
		switch before {
		case "worker":
			profile.WorkerRef = after
		case "sandbox":
			profile.Sandbox = after
		case "managed":
			profile.ManagedRuntime = after
		default:
			profile.Metadata = map[string]any{"profile_ref": after}
		}
	} else {
		profile.DomainID = raw
	}
	normalize(&profile)
	return profile, nil
}

func validateLocalRequest(req ExecutionRequest) error {
	if req.Executor == nil {
		return contracts.NewRuntimeError(contracts.CodeExecutionDomainUnavailable, "local execution requires a tool executor", metadata(req.Profile))
	}
	if req.Profile.NetworkPolicy.AllowNetwork || len(req.Profile.NetworkPolicy.AllowedHosts) > 0 {
		return contracts.NewRuntimeError(contracts.CodeExecutionDomainUnavailable, "local execution cannot grant network access", metadata(req.Profile))
	}
	if len(req.Profile.CredentialScope.AllowedCredentialRefs) > 0 || req.Profile.CredentialScope.AllowRuntimeSecrets {
		return contracts.NewRuntimeError(contracts.CodeExecutionDomainUnavailable, "local execution cannot receive credential scope", metadata(req.Profile))
	}
	if req.Profile.DataBoundary.AllowExternalData || len(req.Profile.DataBoundary.AllowedTenantIDs) > 0 || len(req.Profile.DataBoundary.AllowedDataScopes) > 0 {
		return contracts.NewRuntimeError(contracts.CodeExecutionDomainUnavailable, "local execution cannot widen data boundary", metadata(req.Profile))
	}
	return nil
}

func validateHTTPNetworkRequest(req ExecutionRequest) error {
	if !req.Profile.NetworkPolicy.AllowNetwork {
		return contracts.NewRuntimeError(contracts.CodeExecutionDomainUnavailable, "http execution requires explicit network access", metadata(req.Profile))
	}
	if len(req.Profile.NetworkPolicy.AllowedHosts) == 0 {
		return nil
	}
	target, ok := req.Executor.(NetworkTarget)
	if !ok {
		return contracts.NewRuntimeError(contracts.CodeToolPolicyDenied, "http execution cannot enforce network allowlist for executor", metadata(req.Profile))
	}
	host := normalizeHost(target.NetworkTargetHost())
	if host == "" {
		return contracts.NewRuntimeError(contracts.CodeToolPolicyDenied, "http execution target host is unavailable", metadata(req.Profile))
	}
	for _, allowed := range req.Profile.NetworkPolicy.AllowedHosts {
		if hostAllowed(host, allowed) {
			return nil
		}
	}
	details := metadata(req.Profile)
	details["target_host"] = host
	return contracts.NewRuntimeError(contracts.CodeToolPolicyDenied, "http execution target host is not allowed", details)
}

func hostAllowed(host string, allowed string) bool {
	allowed = normalizeHost(allowed)
	if allowed == "" {
		return false
	}
	if strings.HasPrefix(allowed, "*.") {
		suffix := strings.TrimPrefix(allowed, "*")
		return strings.HasSuffix(host, suffix) && host != strings.TrimPrefix(suffix, ".")
	}
	return host == allowed
}

func normalizeHost(host string) string {
	host = strings.TrimSpace(strings.ToLower(host))
	if host == "" {
		return ""
	}
	if parsedHost, _, err := net.SplitHostPort(host); err == nil {
		host = parsedHost
	}
	return strings.Trim(host, "[]")
}

func normalize(profile *ExecutionProfile) {
	if profile.DomainID == "" {
		profile.DomainID = "local"
	}
	if profile.ID == "" {
		profile.ID = profile.DomainID
	}
	if profile.TimeoutMS > 0 {
		profile.Timeout = time.Duration(profile.TimeoutMS) * time.Millisecond
	}
	if profile.ResourceLimits.TimeoutMS > 0 && profile.Timeout == 0 {
		profile.Timeout = time.Duration(profile.ResourceLimits.TimeoutMS) * time.Millisecond
	}
}

func metadata(profile ExecutionProfile) map[string]any {
	out := map[string]any{
		"profile_id": profile.ID,
		"domain_id":  profile.DomainID,
	}
	if profile.WorkerRef != "" {
		out["worker_ref"] = profile.WorkerRef
	}
	if profile.Sandbox != "" {
		out["sandbox"] = profile.Sandbox
	}
	if profile.ManagedRuntime != "" {
		out["managed_runtime"] = profile.ManagedRuntime
	}
	if profile.ResourceLimits != (ResourceLimits{}) {
		out["resource_limits"] = profile.ResourceLimits
	}
	if profile.NetworkPolicy.AllowNetwork || len(profile.NetworkPolicy.AllowedHosts) > 0 {
		out["network_policy"] = profile.NetworkPolicy
	}
	if len(profile.CredentialScope.AllowedCredentialRefs) > 0 || profile.CredentialScope.AllowRuntimeSecrets {
		out["credential_scope"] = profile.CredentialScope
	}
	if profile.DataBoundary.AllowExternalData || len(profile.DataBoundary.AllowedTenantIDs) > 0 || len(profile.DataBoundary.AllowedDataScopes) > 0 {
		out["data_boundary"] = profile.DataBoundary
	}
	for k, v := range profile.Metadata {
		out[k] = v
	}
	return out
}

func mergeMetadata(base map[string]any, extra map[string]any) map[string]any {
	if len(extra) == 0 {
		return base
	}
	if base == nil {
		base = map[string]any{}
	}
	for k, v := range extra {
		base[k] = v
	}
	return base
}
