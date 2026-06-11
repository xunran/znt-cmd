package hook

import (
	"context"

	"znt/internal/contracts"
	tooldiscovery "znt/internal/discovery/tool"
)

type Event string

const (
	OnRunStarted    Event = "on_run_started"
	OnContextBuilt  Event = "on_context_built"
	OnModelDecision Event = "on_model_decision"
	OnToolResult    Event = "on_tool_result"
	OnRunFinished   Event = "on_run_finished"
)

type HookPoint string

const (
	BeforeContextBuild      HookPoint = "before_context_build"
	AfterCandidateRetrieval HookPoint = "after_candidate_retrieval"
	BeforeModelCall         HookPoint = "before_model_call"
	BeforeMemoryWrite       HookPoint = "before_memory_write"
)

type Observer interface {
	Observe(ctx context.Context, event Observation) error
}

type Transformer interface {
	Apply(ctx context.Context, request TransformRequest) (Patch, error)
}

type Observation struct {
	Event    Event                     `json:"event"`
	TenantID contracts.TenantID        `json:"tenant_id,omitempty"`
	TraceID  contracts.TraceID         `json:"trace_id,omitempty"`
	RunID    contracts.AgentRunID      `json:"run_id,omitempty"`
	TaskID   contracts.TaskID          `json:"task_id,omitempty"`
	Agent    contracts.AgentDefinition `json:"agent,omitempty"`
	Payload  map[string]any            `json:"payload,omitempty"`
}

type TransformRequest struct {
	HookPoint    HookPoint                  `json:"hook_point"`
	TenantID     contracts.TenantID         `json:"tenant_id,omitempty"`
	TraceID      contracts.TraceID          `json:"trace_id,omitempty"`
	RunID        contracts.AgentRunID       `json:"run_id,omitempty"`
	TaskID       contracts.TaskID           `json:"task_id,omitempty"`
	Agent        contracts.AgentDefinition  `json:"agent,omitempty"`
	Policy       contracts.PolicySet        `json:"policy,omitempty"`
	Objective    string                     `json:"objective,omitempty"`
	Candidates   tooldiscovery.CandidateSet `json:"candidates,omitempty"`
	WorkView     contracts.WorkView         `json:"work_view,omitempty"`
	PromptBundle contracts.PromptBundle     `json:"prompt_bundle,omitempty"`
}

type Patch struct {
	AddContextBlocks    []ContextBlock       `json:"add_context_blocks,omitempty"`
	DropContextRefs     []string             `json:"drop_context_refs,omitempty"`
	ToolRankAdjustments []ToolRankAdjustment `json:"tool_rank_adjustments,omitempty"`
	MemoryWriteIntents  []MemoryWriteIntent  `json:"memory_write_intents,omitempty"`
	PlannerHints        []PlannerHint        `json:"planner_hints,omitempty"`
}

type ContextBlock struct {
	ID       string         `json:"id,omitempty"`
	Title    string         `json:"title,omitempty"`
	Content  string         `json:"content"`
	Metadata map[string]any `json:"metadata,omitempty"`
}

type ToolRankAdjustment struct {
	ToolID string  `json:"tool_id"`
	Delta  float64 `json:"delta,omitempty"`
	Boost  bool    `json:"boost,omitempty"`
	Drop   bool    `json:"drop,omitempty"`
}

type MemoryWriteIntent struct {
	Scope    string         `json:"scope,omitempty"`
	Summary  string         `json:"summary,omitempty"`
	Content  string         `json:"content,omitempty"`
	Metadata map[string]any `json:"metadata,omitempty"`
}

type PlannerHint struct {
	Key     string `json:"key,omitempty"`
	Content string `json:"content"`
}

type Chain struct {
	Observers    []Observer
	Transformers []Transformer
}

func (c Chain) Observe(ctx context.Context, observation Observation) {
	for _, observer := range c.Observers {
		if observer != nil {
			_ = observer.Observe(ctx, observation)
		}
	}
}

func (c Chain) Apply(ctx context.Context, request TransformRequest) Patch {
	merged := Patch{}
	for _, transformer := range c.Transformers {
		if transformer == nil {
			continue
		}
		patch, err := transformer.Apply(ctx, request)
		if err != nil {
			continue
		}
		merged = Merge(merged, patch)
	}
	return merged
}

var _ interface {
	Observe(ctx context.Context, observation Observation)
	Apply(ctx context.Context, request TransformRequest) Patch
} = Chain{}

func Merge(base Patch, next Patch) Patch {
	base.AddContextBlocks = append(base.AddContextBlocks, next.AddContextBlocks...)
	base.DropContextRefs = append(base.DropContextRefs, next.DropContextRefs...)
	base.ToolRankAdjustments = append(base.ToolRankAdjustments, next.ToolRankAdjustments...)
	base.MemoryWriteIntents = append(base.MemoryWriteIntents, next.MemoryWriteIntents...)
	base.PlannerHints = append(base.PlannerHints, next.PlannerHints...)
	return base
}
