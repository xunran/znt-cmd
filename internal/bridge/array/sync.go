package array

import (
	"context"
	"fmt"
	"time"

	"znt/internal/contracts"
	"znt/internal/governance/audit"
	"znt/internal/governance/trace"
	"znt/pkg/idgen"
)

type Syncer struct {
	Bridge *Bridge
	Trace  trace.Recorder
	Audit  audit.Logger
	Now    func() time.Time
}

type SyncContext struct {
	TenantID  contracts.TenantID
	TraceID   contracts.TraceID
	RunID     contracts.AgentRunID
	TaskID    contracts.TaskID
	ActorID   string
	ActorType string
}

func NewSyncer(bridge *Bridge) Syncer {
	return Syncer{Bridge: bridge, Now: func() time.Time { return time.Now().UTC() }}
}

func NewGovernedSyncer(bridge *Bridge, traceRecorder trace.Recorder, auditLogger audit.Logger) Syncer {
	syncer := NewSyncer(bridge)
	syncer.Trace = traceRecorder
	syncer.Audit = auditLogger
	return syncer
}

func (s Syncer) Reply(ctx context.Context, binding *contracts.ExternalTaskBinding, message string) {
	s.ReplyWithContext(ctx, binding, SyncContext{}, message)
}

func (s Syncer) ReplyWithContext(ctx context.Context, binding *contracts.ExternalTaskBinding, syncCtx SyncContext, message string) {
	s.send(ctx, binding, syncCtx, "reply", message)
}

func (s Syncer) WaitingInput(ctx context.Context, binding *contracts.ExternalTaskBinding, question string) {
	s.WaitingInputWithContext(ctx, binding, SyncContext{}, question)
}

func (s Syncer) WaitingInputWithContext(ctx context.Context, binding *contracts.ExternalTaskBinding, syncCtx SyncContext, question string) {
	s.send(ctx, binding, syncCtx, "waiting_input", question)
}

func (s Syncer) WaitingApproval(ctx context.Context, binding *contracts.ExternalTaskBinding, reason string) {
	s.WaitingApprovalWithContext(ctx, binding, SyncContext{}, reason)
}

func (s Syncer) WaitingApprovalWithContext(ctx context.Context, binding *contracts.ExternalTaskBinding, syncCtx SyncContext, reason string) {
	s.send(ctx, binding, syncCtx, "waiting_approval", reason)
}

func (s Syncer) RunFailed(ctx context.Context, binding *contracts.ExternalTaskBinding, reason string) {
	s.RunFailedWithContext(ctx, binding, SyncContext{}, reason)
}

func (s Syncer) RunFailedWithContext(ctx context.Context, binding *contracts.ExternalTaskBinding, syncCtx SyncContext, reason string) {
	s.send(ctx, binding, syncCtx, "run_failed", reason)
}

func (s Syncer) HandoffCreated(ctx context.Context, binding *contracts.ExternalTaskBinding, handoff contracts.AgentHandoff) {
	s.HandoffCreatedWithContext(ctx, binding, SyncContext{}, handoff)
}

func (s Syncer) HandoffCreatedWithContext(ctx context.Context, binding *contracts.ExternalTaskBinding, syncCtx SyncContext, handoff contracts.AgentHandoff) {
	s.send(ctx, binding, syncCtx, "handoff_created", fmt.Sprintf("handoff %s created for %s", handoff.HandoffID, handoff.ToAgentID))
}

func (s Syncer) ArtifactCreated(ctx context.Context, binding *contracts.ExternalTaskBinding, ref contracts.ArtifactRef) {
	s.ArtifactCreatedWithContext(ctx, binding, SyncContext{}, ref)
}

func (s Syncer) ArtifactCreatedWithContext(ctx context.Context, binding *contracts.ExternalTaskBinding, syncCtx SyncContext, ref contracts.ArtifactRef) {
	if s.Bridge == nil || binding == nil || binding.Provider == "" || binding.ExternalTaskID == "" {
		return
	}
	err := s.Bridge.AttachArtifact(ctx, contracts.AttachArtifactRequest{
		Ref:            externalRef(binding),
		ArtifactRef:    ref,
		IdempotencyKey: s.idempotencyKey(binding, syncCtx, "artifact_created", string(ref.ArtifactID)),
	})
	s.observe(ctx, binding, syncCtx, "artifact_created", "artifact", err)
}

func (s Syncer) send(ctx context.Context, binding *contracts.ExternalTaskBinding, syncCtx SyncContext, eventType string, message string) {
	if s.Bridge == nil || binding == nil || binding.Provider == "" || binding.ExternalTaskID == "" {
		return
	}
	if message == "" {
		message = eventType
	}
	err := s.Bridge.SendMessage(ctx, contracts.SendExternalMessageRequest{
		Ref:            externalRef(binding),
		Message:        "[" + eventType + "] " + message,
		IdempotencyKey: s.idempotencyKey(binding, syncCtx, eventType, message),
	})
	s.observe(ctx, binding, syncCtx, eventType, "message", err)
}

func (s Syncer) idempotencyKey(binding *contracts.ExternalTaskBinding, syncCtx SyncContext, eventType string, suffix string) string {
	if binding == nil {
		return ""
	}
	base := binding.Provider + ":" + string(binding.ExternalTaskID) + ":" + eventType
	if syncCtx.RunID != "" {
		base += ":" + string(syncCtx.RunID)
	}
	if syncCtx.TaskID != "" {
		base += ":" + string(syncCtx.TaskID)
	} else if binding.CoreTaskID != "" {
		base += ":" + string(binding.CoreTaskID)
	}
	if suffix != "" {
		base += ":" + suffix
	}
	return base
}

func externalRef(binding *contracts.ExternalTaskBinding) contracts.ExternalTaskRef {
	return contracts.ExternalTaskRef{Provider: binding.Provider, ExternalTaskID: binding.ExternalTaskID}
}

func (s Syncer) observe(ctx context.Context, binding *contracts.ExternalTaskBinding, syncCtx SyncContext, eventType string, channel string, err error) {
	if binding == nil {
		return
	}
	if syncCtx.TenantID == "" {
		syncCtx.TenantID = binding.TenantID
	}
	if syncCtx.TaskID == "" {
		syncCtx.TaskID = binding.CoreTaskID
	}
	if syncCtx.ActorID == "" {
		syncCtx.ActorID = "clean-core"
	}
	if syncCtx.ActorType == "" {
		syncCtx.ActorType = "system"
	}
	traceType := contracts.TraceExternalWritebackOK
	payload := map[string]any{
		"provider":         binding.Provider,
		"external_task_id": binding.ExternalTaskID,
		"core_task_id":     binding.CoreTaskID,
		"event_type":       eventType,
		"channel":          channel,
		"binding_status":   binding.Status,
	}
	if err != nil {
		traceType = contracts.TraceExternalWritebackFailed
		payload["error"] = err.Error()
	}
	if s.Trace != nil && syncCtx.TraceID != "" {
		_ = s.Trace.Record(ctx, contracts.TraceEvent{
			TraceID:   syncCtx.TraceID,
			TenantID:  syncCtx.TenantID,
			SpanID:    contracts.SpanID(idgen.New("span")),
			RunID:     syncCtx.RunID,
			TaskID:    syncCtx.TaskID,
			Type:      traceType,
			Payload:   payload,
			CreatedAt: s.now(),
		})
	}
	if err != nil && s.Audit != nil {
		_ = s.Audit.Log(ctx, contracts.AuditEvent{
			TenantID:     syncCtx.TenantID,
			ActorID:      syncCtx.ActorID,
			ActorType:    syncCtx.ActorType,
			Action:       contracts.AuditExternalWritebackFailed,
			ResourceType: "external_task",
			ResourceID:   binding.Provider + ":" + string(binding.ExternalTaskID),
			Decision:     "failed",
			Reason:       err.Error(),
			TraceID:      syncCtx.TraceID,
			TaskID:       syncCtx.TaskID,
			RunID:        syncCtx.RunID,
			CreatedAt:    s.now(),
		})
	}
}

func (s Syncer) now() time.Time {
	if s.Now != nil {
		return s.Now()
	}
	return time.Now().UTC()
}
