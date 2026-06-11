package invoke

import (
	"context"
	"time"

	"znt/internal/agentdef/loader"
	"znt/internal/contracts"
	"znt/internal/governance/audit"
	policyengine "znt/internal/policy/engine"
	toolrepo "znt/internal/tool/repository"
	toolruntime "znt/internal/tool/runtime"
	"znt/pkg/idgen"
)

type Service struct {
	Agents        loader.Loader
	AgentRunnable func(ctx context.Context, tenantID contracts.TenantID, agentID contracts.AgentID) error
	ToolRepo      toolrepo.Repository
	ToolRuntime   toolruntime.Runtime
	Policies      policyengine.Store
	Audit         audit.Logger
	Disabled      bool
	DisabledTools map[string]struct{}
	Now           func() time.Time
}

type Request struct {
	Envelope        contracts.AgentEnvelope
	Caller          contracts.AgentCaller
	IdempotencyKey  string
	ApprovalGranted bool
}

func (s Service) Invoke(ctx context.Context, req Request) (contracts.ToolResult, error) {
	if s.Disabled {
		return contracts.ToolResult{}, contracts.NewRuntimeError(contracts.CodeToolPolicyDenied, "external tools.invoke is disabled by release switch", nil)
	}
	if s.AgentRunnable != nil {
		if err := s.AgentRunnable(ctx, req.Caller.TenantID, req.Envelope.Target.AgentID); err != nil {
			return contracts.ToolResult{}, err
		}
	}
	agent, err := s.Agents.Load(ctx, req.Caller.TenantID, req.Envelope.Target.AgentID, req.Envelope.Target.Version)
	if err != nil {
		return contracts.ToolResult{}, err
	}
	if agent.TenantID == "" {
		agent.TenantID = req.Caller.TenantID
	}
	toolID, _ := req.Envelope.Payload["tool_id"].(string)
	toolName, _ := req.Envelope.Payload["tool_name"].(string)
	if toolID == "" {
		toolID = toolName
	}
	if toolID == "" {
		return contracts.ToolResult{}, contracts.NewRuntimeError(contracts.CodeToolNotFound, "tools.invoke requires tool_id or tool_name", nil)
	}
	if _, disabled := s.DisabledTools[toolID]; disabled {
		return contracts.ToolResult{}, contracts.NewRuntimeError(contracts.CodeToolPolicyDenied, "tool is disabled by release switch", map[string]any{"tool_id": toolID})
	}
	if !contains(agent.Tools.ExposedToolIDs, toolID) {
		return contracts.ToolResult{}, contracts.NewRuntimeError(contracts.CodeToolPolicyDenied, "tool is not exposed by agent", map[string]any{"tool_id": toolID})
	}
	args, _ := req.Envelope.Payload["arguments"].(map[string]any)
	if args == nil {
		args = map[string]any{}
	}
	call := contracts.ToolCall{
		ToolCallID:     contracts.ToolCallID(idgen.New("toolcall")),
		TenantID:       req.Caller.TenantID,
		ToolID:         toolID,
		Name:           toolName,
		Arguments:      args,
		TraceID:        req.Envelope.TraceID,
		RunID:          contracts.AgentRunID(idgen.New("externalrun")),
		TaskID:         req.Envelope.Context.TaskID,
		IdempotencyKey: req.IdempotencyKey,
		CreatedAt:      s.now(),
	}
	if definition, ok := s.ToolRuntime.Definition(req.Caller.TenantID, toolID); ok {
		call.ToolVersion = definition.Version
		call.ExecutionProfile = definition.ExecutionProfile
	}
	saved, duplicate, err := s.ToolRepo.SaveCall(ctx, call)
	if err != nil {
		return contracts.ToolResult{}, err
	}
	if duplicate {
		if existing, ok, err := s.ToolRepo.GetResultByCall(ctx, saved.ToolCallID); err != nil {
			return contracts.ToolResult{}, err
		} else if ok {
			if existing.Status != contracts.ToolResultPendingApproval || !req.ApprovalGranted {
				return existing, nil
			}
		}
	}
	result, err := s.ToolRuntime.Invoke(ctx, toolruntime.InvokeRequest{
		TenantID:        req.Caller.TenantID,
		TraceID:         req.Envelope.TraceID,
		ActorID:         req.Caller.CallerID,
		ActorType:       req.Caller.CallerType,
		Agent:           agent,
		PolicySet:       s.policySet(ctx, req.Caller.TenantID, agent.PolicyRefs.PolicySetID),
		Call:            saved,
		ApprovalGranted: req.ApprovalGranted,
	})
	if err != nil {
		return contracts.ToolResult{}, err
	}
	if err := s.ToolRepo.SaveResult(ctx, result); err != nil {
		return contracts.ToolResult{}, err
	}
	if s.Audit != nil {
		_ = s.Audit.Log(ctx, contracts.AuditEvent{
			AuditID:      idgen.New("audit"),
			TenantID:     req.Caller.TenantID,
			ActorID:      req.Caller.CallerID,
			ActorType:    req.Caller.CallerType,
			Action:       contracts.AuditExternalToolCall,
			ResourceType: "tool",
			ResourceID:   toolID,
			Decision:     string(result.Status),
			TraceID:      req.Envelope.TraceID,
			TaskID:       req.Envelope.Context.TaskID,
			RunID:        saved.RunID,
			CreatedAt:    s.now(),
		})
	}
	return result, nil
}

func (s Service) policySet(ctx context.Context, tenantID contracts.TenantID, policySetID contracts.PolicySetID) contracts.PolicySet {
	if s.Policies == nil {
		return policyengine.FallbackPolicySet(tenantID, policySetID)
	}
	policy, ok, err := s.Policies.Get(ctx, tenantID, policySetID)
	if err != nil || !ok {
		return policyengine.FallbackPolicySet(tenantID, policySetID)
	}
	return policy
}

func (s Service) now() time.Time {
	if s.Now != nil {
		return s.Now().UTC()
	}
	return time.Now().UTC()
}

func contains(values []string, target string) bool {
	for _, value := range values {
		if value == target {
			return true
		}
	}
	return false
}
