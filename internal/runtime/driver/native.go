package driver

import (
	"context"

	"znt/internal/contracts"
	"znt/internal/runtime/kernel"
)

type NativeDriver struct {
	Coordinator *kernel.Coordinator
	CarrierKind contracts.AgentCarrierKind
}

func NewNative(coordinator kernel.Coordinator) NativeDriver {
	return NativeDriver{Coordinator: &coordinator, CarrierKind: contracts.AgentCarrierKindNativeAgent}
}

func NewNativeRef(coordinator *kernel.Coordinator) NativeDriver {
	return NativeDriver{Coordinator: coordinator, CarrierKind: contracts.AgentCarrierKindNativeAgent}
}

func NewManagedCoordinator(kind contracts.AgentCarrierKind, coordinator kernel.Coordinator) NativeDriver {
	if kind == "" {
		kind = contracts.AgentCarrierKindNativeAgent
	}
	return NativeDriver{Coordinator: &coordinator, CarrierKind: kind}
}

func NewManagedCoordinatorRef(kind contracts.AgentCarrierKind, coordinator *kernel.Coordinator) NativeDriver {
	if kind == "" {
		kind = contracts.AgentCarrierKindNativeAgent
	}
	return NativeDriver{Coordinator: coordinator, CarrierKind: kind}
}

func (d NativeDriver) Kind() contracts.AgentCarrierKind {
	if d.CarrierKind == "" {
		return contracts.AgentCarrierKindNativeAgent
	}
	return d.CarrierKind
}

func (d NativeDriver) Contract() contracts.RuntimeContractKind {
	return contracts.RuntimeContractManaged
}

func (d NativeDriver) StartRun(ctx context.Context, req StartRunRequest) (RunResult, error) {
	if d.Coordinator == nil {
		return RunResult{}, contracts.NewRuntimeError(contracts.CodeAgentRuntimeDriverUnavailable, "native coordinator is unavailable", map[string]any{"carrier_kind": d.Kind()})
	}
	return d.Coordinator.HandleEnvelope(ctx, req.Envelope)
}

func (d NativeDriver) PrepareRun(ctx context.Context, req StartRunRequest) (PreparedRun, error) {
	if d.Coordinator == nil {
		return PreparedRun{}, contracts.NewRuntimeError(contracts.CodeAgentRuntimeDriverUnavailable, "native coordinator is unavailable", map[string]any{"carrier_kind": d.Kind()})
	}
	return d.Coordinator.PrepareEnvelopeRun(ctx, req.Envelope)
}

func (d NativeDriver) ExecutePreparedRun(ctx context.Context, prepared PreparedRun) (RunResult, error) {
	if d.Coordinator == nil {
		return RunResult{}, contracts.NewRuntimeError(contracts.CodeAgentRuntimeDriverUnavailable, "native coordinator is unavailable", map[string]any{"carrier_kind": d.Kind()})
	}
	return d.Coordinator.ExecutePreparedRun(ctx, prepared)
}

func (d NativeDriver) ResumeRun(ctx context.Context, req ResumeRunRequest) (RunResult, error) {
	if d.Coordinator == nil {
		return RunResult{}, contracts.NewRuntimeError(contracts.CodeAgentRuntimeDriverUnavailable, "native coordinator is unavailable", map[string]any{"carrier_kind": d.Kind()})
	}
	return d.Coordinator.ResumeRun(ctx, req.Envelope, req.RunID, req.TaskID)
}

func (d NativeDriver) CancelRun(ctx context.Context, req CancelRunRequest) (RunResult, error) {
	if req.RunID == "" {
		return RunResult{}, contracts.NewRuntimeError(contracts.CodeDecisionSchemaError, "cancel run requires run_id", nil)
	}
	if d.Coordinator == nil || d.Coordinator.Runs == nil {
		return RunResult{}, contracts.NewRuntimeError(contracts.CodeAgentRuntimeDriverUnavailable, "run repository is unavailable", map[string]any{"carrier_kind": d.Kind()})
	}
	run, err := d.Coordinator.Runs.MarkCancelled(ctx, req.RunID)
	if err != nil {
		return RunResult{}, err
	}
	return RunResult{RunID: run.RunID, TaskID: run.TaskID, Status: run.Status}, nil
}

func (d NativeDriver) Preview(ctx context.Context, req PreviewRequest) (PreviewResult, error) {
	if d.Coordinator == nil {
		return PreviewResult{}, contracts.NewRuntimeError(contracts.CodeAgentRuntimeDriverUnavailable, "native coordinator is unavailable", map[string]any{"carrier_kind": d.Kind()})
	}
	return d.Coordinator.PreviewPromptBundle(ctx, req.PromptPreviewRequest)
}

func (d NativeDriver) ValidateSource(ctx context.Context, req ValidateSourceRequest) (ValidateSourceResult, error) {
	return ValidateSourceResult{Valid: true}, nil
}
