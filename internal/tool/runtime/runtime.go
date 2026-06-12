package runtime

import (
	"context"
	"strings"
	"time"

	"znt/internal/contracts"
	"znt/internal/execution/domain"
	"znt/internal/governance/audit"
	"znt/internal/governance/trace"
	"znt/internal/policy/toolpolicy"
	"znt/internal/tool/registry"
	"znt/pkg/idgen"
	"znt/pkg/jsonschema"
)

type Runtime struct {
	Registry     registry.Registry
	Policy       toolpolicy.Evaluator
	Trace        trace.Recorder
	Audit        audit.Logger
	Domains      domain.Resolver
	Availability AvailabilityChecker
	Credentials  domain.CredentialResolver
	Now          func() time.Time
}

type Invoker interface {
	Invoke(ctx context.Context, req InvokeRequest) (contracts.ToolResult, error)
}

type AvailabilityChecker interface {
	CheckToolAvailability(ctx context.Context, tenantID contracts.TenantID, tool contracts.ToolDefinition) error
}

func New(registry registry.Registry, policy toolpolicy.Evaluator, traceRecorder trace.Recorder) Runtime {
	return Runtime{
		Registry: registry,
		Policy:   policy,
		Trace:    traceRecorder,
		Domains:  domain.NewResolver(domain.LocalExecutionDomain{}, domain.HTTPDomain(), domain.AgentToolDomain(), domain.DatabaseDomain(), domain.WorkerDomain(), domain.SandboxDomain(), domain.ManagedDomain()),
		Now:      func() time.Time { return time.Now().UTC() },
	}
}

func (r Runtime) Invoke(ctx context.Context, req InvokeRequest) (contracts.ToolResult, error) {
	if req.Call.TenantID == "" {
		req.Call.TenantID = req.TenantID
	}
	if req.Call.TraceID == "" {
		req.Call.TraceID = req.TraceID
	}
	tool, ok := r.Registry.GetForTenant(req.TenantID, req.Call.ToolID)
	if !ok {
		return contracts.ToolResult{}, contracts.NewRuntimeError(contracts.CodeToolNotFound, "tool not found", map[string]any{"tool_id": req.Call.ToolID})
	}
	if r.Availability != nil {
		if err := r.Availability.CheckToolAvailability(ctx, req.TenantID, tool.Definition); err != nil {
			result := r.result(req.Call, contracts.ToolResultDenied, nil, &contracts.ToolExecutionError{Code: contracts.CodeToolPolicyDenied, Message: err.Error()}, nil)
			_ = r.record(ctx, req, contracts.TraceToolDenied, result)
			return result, nil
		}
	}
	if err := jsonschema.ValidateObject(req.Call.Arguments, tool.Definition.InputSchema); err != nil {
		result := r.result(req.Call, contracts.ToolResultFailed, nil, &contracts.ToolExecutionError{Code: contracts.CodeToolArgumentInvalid, Message: err.Error()}, nil)
		_ = r.record(ctx, req, contracts.TraceToolFailed, result)
		return result, nil
	}
	policyDecision, err := r.Policy.Evaluate(ctx, toolpolicy.EvaluateRequest{
		TenantID:  req.TenantID,
		TraceID:   req.TraceID,
		ActorID:   req.ActorID,
		ActorType: req.ActorType,
		Agent:     req.Agent,
		Policy:    req.PolicySet,
		Tool:      tool.Definition,
		Call:      req.Call,
		Approved:  req.ApprovalGranted,
	})
	_ = r.recordEvent(ctx, req, contracts.TraceToolPolicyChecked, map[string]any{
		"tool_id":  tool.Definition.ToolID,
		"decision": policyDecision.Decision,
		"reason":   policyDecision.Reason,
	})
	if err != nil {
		result := r.result(req.Call, contracts.ToolResultDenied, nil, &contracts.ToolExecutionError{Code: contracts.CodeToolPolicyDenied, Message: err.Error()}, nil)
		_ = r.record(ctx, req, contracts.TraceToolDenied, result)
		return result, nil
	}
	if policyDecision.Decision == contracts.PolicyDecisionApprovalRequired {
		result := r.result(req.Call, contracts.ToolResultPendingApproval, nil, nil, nil)
		_ = r.record(ctx, req, contracts.TraceToolPendingApproval, result)
		return result, nil
	}
	started := r.Now()
	execDomain, execProfile, err := r.Domains.ResolveProfile(tool.Definition.ExecutionProfile)
	if err != nil {
		return contracts.ToolResult{}, err
	}
	execPayload := map[string]any{
		"tool_call_id": req.Call.ToolCallID,
		"tool_id":      tool.Definition.ToolID,
		"domain_id":    execDomain.ID(),
		"profile_id":   execProfile.ID,
	}
	if execProfile.WorkerRef != "" {
		execPayload["worker_ref"] = execProfile.WorkerRef
	}
	if execProfile.Sandbox != "" {
		execPayload["sandbox"] = execProfile.Sandbox
	}
	if execProfile.ManagedRuntime != "" {
		execPayload["managed_runtime"] = execProfile.ManagedRuntime
	}
	if len(execProfile.CredentialScope.AllowedCredentialRefs) > 0 || execProfile.CredentialScope.AllowRuntimeSecrets {
		execPayload["credential_scope"] = execProfile.CredentialScope
	}
	if execProfile.DataBoundary.AllowExternalData || len(execProfile.DataBoundary.AllowedTenantIDs) > 0 || len(execProfile.DataBoundary.AllowedDataScopes) > 0 {
		execPayload["data_boundary"] = execProfile.DataBoundary
	}
	_ = r.recordEvent(ctx, req, contracts.TraceToolInvoked, execPayload)
	if err := validateDataBoundary(req.TenantID, execProfile.DataBoundary); err != nil {
		result := r.executionFailedResult(req, started, err, nil)
		_ = r.recordExecutionDeniedAudit(ctx, req, tool.Definition, result)
		_ = r.record(ctx, req, contracts.TraceToolFailed, result)
		return result, nil
	}
	credentials, err := r.resolveCredentials(ctx, req, tool.Definition, execDomain.ID(), execProfile)
	if err != nil {
		result := r.executionFailedResult(req, started, err, nil)
		_ = r.recordExecutionDeniedAudit(ctx, req, tool.Definition, result)
		_ = r.record(ctx, req, contracts.TraceToolFailed, result)
		return result, nil
	}
	for _, credential := range credentials {
		_ = r.recordEvent(ctx, req, contracts.TraceCredentialUsed, map[string]any{
			"tool_call_id":   req.Call.ToolCallID,
			"tool_id":        tool.Definition.ToolID,
			"credential_ref": credential.Ref,
			"domain_id":      execDomain.ID(),
		})
		_ = r.recordCredentialAudit(ctx, req, tool.Definition, execDomain.ID(), credential.Ref)
	}
	execResult, execErr := execDomain.Execute(ctx, domain.ExecutionRequest{
		Profile:     execProfile,
		Tool:        tool.Definition,
		Call:        req.Call,
		Executor:    tool.Executor,
		Credentials: credentials,
	})
	if execErr != nil {
		result := r.executionFailedResult(req, started, execErr, execResult.Metadata)
		if result.Error != nil && (result.Error.Code == contracts.CodeToolPolicyDenied || result.Error.Code == contracts.CodeHandoffDenied) {
			_ = r.recordExecutionDeniedAudit(ctx, req, tool.Definition, result)
		}
		_ = r.record(ctx, req, contracts.TraceToolFailed, result)
		return result, nil
	}
	result := contracts.ToolResult{
		ToolResultID: contracts.ToolResultID(idgen.New("toolres")),
		ToolCallID:   req.Call.ToolCallID,
		Status:       contracts.ToolResultSucceeded,
		Output:       execResult.Output,
		ArtifactRefs: execResult.ArtifactRefs,
		StartedAt:    started,
		CompletedAt:  r.Now(),
	}
	if err := jsonschema.ValidateObject(result.Output, tool.Definition.OutputSchema); err != nil {
		result = contracts.ToolResult{
			ToolResultID: contracts.ToolResultID(idgen.New("toolres")),
			ToolCallID:   req.Call.ToolCallID,
			Status:       contracts.ToolResultFailed,
			Error:        &contracts.ToolExecutionError{Code: contracts.CodeToolExecutionFailed, Message: "tool result schema validation failed", Details: map[string]any{"error": err.Error()}},
			StartedAt:    started,
			CompletedAt:  r.Now(),
		}
		_ = r.record(ctx, req, contracts.TraceToolFailed, result)
		return result, nil
	}
	_ = r.recordEvent(ctx, req, contracts.TraceToolCompleted, map[string]any{
		"tool_call_id":       req.Call.ToolCallID,
		"status":             result.Status,
		"execution_metadata": execResult.Metadata,
	})
	return result, nil
}

func (r Runtime) Definition(tenantID contracts.TenantID, toolID string) (contracts.ToolDefinition, bool) {
	tool, ok := r.Registry.GetForTenant(tenantID, toolID)
	if !ok {
		return contracts.ToolDefinition{}, false
	}
	return tool.Definition, true
}

type InvokeRequest struct {
	TenantID        contracts.TenantID
	TraceID         contracts.TraceID
	ActorID         string
	ActorType       string
	Agent           contracts.AgentDefinition
	PolicySet       contracts.PolicySet
	Call            contracts.ToolCall
	ApprovalGranted bool
}

func (r Runtime) result(call contracts.ToolCall, status contracts.ToolResultStatus, output map[string]any, execErr *contracts.ToolExecutionError, artifacts []contracts.ArtifactRef) contracts.ToolResult {
	now := r.Now()
	return contracts.ToolResult{
		ToolResultID: contracts.ToolResultID(idgen.New("toolres")),
		ToolCallID:   call.ToolCallID,
		Status:       status,
		Output:       output,
		Error:        execErr,
		ArtifactRefs: artifacts,
		StartedAt:    now,
		CompletedAt:  now,
	}
}

func (r Runtime) executionFailedResult(req InvokeRequest, started time.Time, execErr error, metadata map[string]any) contracts.ToolResult {
	code := contracts.CodeToolExecutionFailed
	details := map[string]any{}
	if runtimeErr, ok := execErr.(*contracts.RuntimeError); ok {
		code = runtimeErr.Code
		for k, v := range runtimeErr.Details {
			details[k] = v
		}
	}
	for k, v := range metadata {
		details[k] = v
	}
	return contracts.ToolResult{
		ToolResultID: contracts.ToolResultID(idgen.New("toolres")),
		ToolCallID:   req.Call.ToolCallID,
		Status:       contracts.ToolResultFailed,
		Error:        &contracts.ToolExecutionError{Code: code, Message: execErr.Error(), Details: details},
		StartedAt:    started,
		CompletedAt:  r.Now(),
	}
}

func (r Runtime) resolveCredentials(ctx context.Context, req InvokeRequest, tool contracts.ToolDefinition, domainID string, profile domain.ExecutionProfile) ([]domain.ResolvedCredential, error) {
	refs, err := cleanCredentialRefs(profile.CredentialScope.AllowedCredentialRefs)
	if err != nil {
		return nil, err
	}
	if len(refs) == 0 && !profile.CredentialScope.AllowRuntimeSecrets {
		return nil, nil
	}
	if len(refs) > 0 && r.Credentials == nil {
		return nil, contracts.NewRuntimeError(contracts.CodeToolPolicyDenied, "credential resolver is not configured", map[string]any{
			"tool_id": tool.ToolID,
		})
	}
	out := make([]domain.ResolvedCredential, 0, len(refs))
	for _, ref := range refs {
		credential, err := r.Credentials.ResolveCredential(ctx, domain.CredentialResolveRequest{
			TenantID:      req.TenantID,
			ActorID:       req.ActorID,
			ActorType:     req.ActorType,
			AgentID:       req.Agent.AgentID,
			ToolID:        tool.ToolID,
			DomainID:      domainID,
			CredentialRef: ref,
			DataBoundary:  profile.DataBoundary,
		})
		if err != nil {
			return nil, err
		}
		if credential.Ref == "" {
			credential.Ref = ref
		}
		if err := validateResolvedCredential(req.TenantID, ref, credential, profile.DataBoundary); err != nil {
			return nil, err
		}
		out = append(out, credential)
	}
	if profile.CredentialScope.AllowRuntimeSecrets {
		runtimeSecretResolver, ok := r.Credentials.(domain.RuntimeSecretResolver)
		if !ok {
			return nil, contracts.NewRuntimeError(contracts.CodeToolPolicyDenied, "runtime secret resolver is not configured", map[string]any{
				"tool_id": tool.ToolID,
			})
		}
		secrets, err := runtimeSecretResolver.ResolveRuntimeSecrets(ctx, domain.RuntimeSecretResolveRequest{
			TenantID:        req.TenantID,
			ActorID:         req.ActorID,
			ActorType:       req.ActorType,
			AgentID:         req.Agent.AgentID,
			ToolID:          tool.ToolID,
			DomainID:        domainID,
			CredentialScope: profile.CredentialScope,
			DataBoundary:    profile.DataBoundary,
		})
		if err != nil {
			return nil, err
		}
		for _, credential := range secrets {
			if strings.TrimSpace(credential.Ref) == "" {
				return nil, contracts.NewRuntimeError(contracts.CodeToolPolicyDenied, "runtime secret ref is empty", nil)
			}
			if err := validateResolvedCredential(req.TenantID, credential.Ref, credential, profile.DataBoundary); err != nil {
				return nil, err
			}
			out = append(out, credential)
		}
	}
	return out, nil
}

func cleanCredentialRefs(values []string) ([]string, error) {
	out := make([]string, 0, len(values))
	seen := map[string]struct{}{}
	for _, value := range values {
		ref := strings.TrimSpace(value)
		if ref == "" {
			return nil, contracts.NewRuntimeError(contracts.CodeToolPolicyDenied, "credential ref is empty", nil)
		}
		if _, ok := seen[ref]; ok {
			continue
		}
		seen[ref] = struct{}{}
		out = append(out, ref)
	}
	return out, nil
}

func validateDataBoundary(tenantID contracts.TenantID, boundary domain.DataBoundary) error {
	if len(boundary.AllowedTenantIDs) == 0 {
		return nil
	}
	currentAllowed := false
	for _, allowed := range boundary.AllowedTenantIDs {
		if allowed == tenantID {
			currentAllowed = true
			continue
		}
		if allowed != "" && !boundary.AllowExternalData {
			return contracts.NewRuntimeError(contracts.CodeToolPolicyDenied, "data boundary includes external tenant without allow_external_data", map[string]any{
				"tenant_id": tenantID,
			})
		}
	}
	if !currentAllowed {
		return contracts.NewRuntimeError(contracts.CodeToolPolicyDenied, "data boundary does not allow caller tenant", map[string]any{
			"tenant_id": tenantID,
		})
	}
	return nil
}

func validateResolvedCredential(tenantID contracts.TenantID, expectedRef string, credential domain.ResolvedCredential, boundary domain.DataBoundary) error {
	if expectedRef != "" && credential.Ref != expectedRef {
		return contracts.NewRuntimeError(contracts.CodeToolPolicyDenied, "credential resolver returned unexpected credential ref", map[string]any{
			"credential_ref": credential.Ref,
		})
	}
	if credential.TenantID == "" {
		return contracts.NewRuntimeError(contracts.CodeToolPolicyDenied, "credential tenant is required", map[string]any{
			"credential_ref": credential.Ref,
		})
	}
	if credential.TenantID != tenantID && !boundary.AllowExternalData {
		return contracts.NewRuntimeError(contracts.CodeToolPolicyDenied, "credential tenant does not match caller tenant", map[string]any{
			"credential_ref": credential.Ref,
		})
	}
	if len(boundary.AllowedTenantIDs) > 0 && !tenantAllowed(credential.TenantID, boundary.AllowedTenantIDs) {
		return contracts.NewRuntimeError(contracts.CodeToolPolicyDenied, "credential tenant is outside data boundary", map[string]any{
			"credential_ref": credential.Ref,
		})
	}
	if len(boundary.AllowedDataScopes) > 0 {
		for _, scope := range credential.Scopes {
			if strings.TrimSpace(scope) == "" {
				continue
			}
			if !dataScopeAllowed(scope, boundary.AllowedDataScopes) {
				return contracts.NewRuntimeError(contracts.CodeToolPolicyDenied, "credential scope is outside data boundary", map[string]any{
					"credential_ref": credential.Ref,
					"scope":          scope,
				})
			}
		}
	}
	return nil
}

func tenantAllowed(tenantID contracts.TenantID, allowed []contracts.TenantID) bool {
	for _, value := range allowed {
		if value == tenantID {
			return true
		}
	}
	return false
}

func dataScopeAllowed(scope string, allowed []string) bool {
	scope = strings.TrimSpace(scope)
	for _, value := range allowed {
		if strings.TrimSpace(value) == scope {
			return true
		}
	}
	return false
}

func (r Runtime) record(ctx context.Context, req InvokeRequest, eventType string, result contracts.ToolResult) error {
	return r.recordEvent(ctx, req, eventType, map[string]any{"tool_call_id": req.Call.ToolCallID, "status": result.Status})
}

func (r Runtime) recordEvent(ctx context.Context, req InvokeRequest, eventType string, payload map[string]any) error {
	if r.Trace == nil {
		return nil
	}
	return r.Trace.Record(ctx, contracts.TraceEvent{
		TraceID:   req.TraceID,
		TenantID:  req.TenantID,
		SpanID:    contracts.SpanID(idgen.New("span")),
		RunID:     req.Call.RunID,
		TaskID:    req.Call.TaskID,
		Type:      eventType,
		Payload:   payload,
		CreatedAt: r.Now(),
	})
}

func (r Runtime) recordCredentialAudit(ctx context.Context, req InvokeRequest, tool contracts.ToolDefinition, domainID string, credentialRef string) error {
	if r.Audit == nil {
		return nil
	}
	return r.Audit.Log(ctx, contracts.AuditEvent{
		AuditID:      idgen.New("audit"),
		TenantID:     req.TenantID,
		ActorID:      req.ActorID,
		ActorType:    req.ActorType,
		Action:       contracts.AuditCredentialUsed,
		ResourceType: "credential",
		ResourceID:   credentialRef,
		Decision:     "allowed",
		Reason:       "tool_id=" + tool.ToolID + "; domain_id=" + domainID,
		TraceID:      req.TraceID,
		TaskID:       req.Call.TaskID,
		RunID:        req.Call.RunID,
		CreatedAt:    r.Now(),
	})
}

func (r Runtime) recordExecutionDeniedAudit(ctx context.Context, req InvokeRequest, tool contracts.ToolDefinition, result contracts.ToolResult) error {
	if r.Audit == nil || result.Error == nil {
		return nil
	}
	return r.Audit.Log(ctx, contracts.AuditEvent{
		AuditID:      idgen.New("audit"),
		TenantID:     req.TenantID,
		ActorID:      req.ActorID,
		ActorType:    req.ActorType,
		Action:       contracts.AuditToolPolicyDenied,
		ResourceType: "tool",
		ResourceID:   tool.ToolID,
		Decision:     "denied",
		Reason:       result.Error.Message,
		TraceID:      req.TraceID,
		TaskID:       req.Call.TaskID,
		RunID:        req.Call.RunID,
		CreatedAt:    r.Now(),
	})
}
