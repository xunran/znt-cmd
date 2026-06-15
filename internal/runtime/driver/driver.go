package driver

import (
	"context"

	"znt/internal/contracts"
	"znt/internal/runtime/kernel"
)

type Driver interface {
	Kind() contracts.AgentCarrierKind
	Contract() contracts.RuntimeContractKind
	StartRun(ctx context.Context, req StartRunRequest) (RunResult, error)
	ResumeRun(ctx context.Context, req ResumeRunRequest) (RunResult, error)
	CancelRun(ctx context.Context, req CancelRunRequest) (RunResult, error)
	Preview(ctx context.Context, req PreviewRequest) (PreviewResult, error)
	ValidateSource(ctx context.Context, req ValidateSourceRequest) (ValidateSourceResult, error)
}

type PreparedDriver interface {
	Driver
	PrepareRun(ctx context.Context, req StartRunRequest) (PreparedRun, error)
	ExecutePreparedRun(ctx context.Context, prepared PreparedRun) (RunResult, error)
}

type StartRunRequest struct {
	Envelope contracts.AgentEnvelope
}

type ResumeRunRequest struct {
	Envelope contracts.AgentEnvelope
	RunID    contracts.AgentRunID
	TaskID   contracts.TaskID
}

type CancelRunRequest struct {
	RunID  contracts.AgentRunID
	TaskID contracts.TaskID
	Reason string
}

type PreviewRequest struct {
	kernel.PromptPreviewRequest
}

type ValidateSourceRequest struct {
	SourceKind contracts.AgentSourceKind
	Payload    map[string]any
}

type RunResult = kernel.RunResult
type PreparedRun = kernel.PreparedRun
type PreviewResult = kernel.PromptPreviewResult

type ValidateSourceResult struct {
	Valid    bool     `json:"valid"`
	Warnings []string `json:"warnings,omitempty"`
}
