package kernel

import (
	"context"
	"errors"
	"fmt"
	"sort"
	"strings"
	"time"

	"znt/internal/agentdef/loader"
	agentstrategy "znt/internal/agentdef/strategy"
	"znt/internal/asset/artifact"
	"znt/internal/bridge/array"
	contextcollector "znt/internal/context/collector"
	contextcompressor "znt/internal/context/compressor"
	contextconversation "znt/internal/context/conversation"
	promptbuilder "znt/internal/context/promptbundle"
	workviewbuilder "znt/internal/context/workview"
	"znt/internal/contracts"
	conversationstore "znt/internal/conversation"
	decisionvalidator "znt/internal/decision/validator"
	tooldiscovery "znt/internal/discovery/tool"
	"znt/internal/governance/trace"
	modelclient "znt/internal/model/client"
	policyengine "znt/internal/policy/engine"
	outputpolicy "znt/internal/policy/outputpolicy"
	promptpolicy "znt/internal/policy/promptpolicy"
	runtimehook "znt/internal/runtime/hook"
	runrepo "znt/internal/runtime/run"
	taskplan "znt/internal/task/plan"
	taskrepo "znt/internal/task/repository"
	taskruntime "znt/internal/task/runtime"
	toolrepo "znt/internal/tool/repository"
	toolruntime "znt/internal/tool/runtime"
	"znt/pkg/hash"
	"znt/pkg/idgen"
)

type Coordinator struct {
	Agents                       loader.Loader
	Runs                         runrepo.Repository
	Tasks                        *taskruntime.Service
	TaskRepo                     taskrepo.TaskRepository
	Plans                        taskplan.Reader
	Memory                       artifact.MemoryStore
	Trace                        trace.Recorder
	Tools                        tooldiscovery.CandidateProvider
	WorkView                     workviewbuilder.Builder
	Prompts                      promptbuilder.Builder
	Model                        modelclient.ModelClient
	ModelProvider                string
	ModelName                    string
	Validator                    decisionvalidator.Validator
	ToolRuntime                  toolruntime.Invoker
	ToolRepo                     toolrepo.Repository
	ConversationStore            conversationstore.Store
	AddressingJudge              contextconversation.AddressingJudge
	SufficiencyJudge             contextconversation.SufficiencyJudge
	ContextRetriever             contextconversation.Retriever
	ContextDefaults              contracts.ContextStrategy
	EnableDirectConversation     bool
	DisableConversationRetrieval bool
	ExternalSync                 array.Syncer
	ExternalBinding              func(context.Context, contracts.TenantID, contracts.TaskID) (*contracts.ExternalTaskBinding, bool)
	Policies                     policyengine.Store
	PolicyEngine                 *policyengine.Engine
	DisabledToolIDs              map[string]struct{}
	RuntimeHooks                 RuntimeHookEngine
	Now                          func() time.Time
}

type RuntimeHookEngine interface {
	Observe(ctx context.Context, observation runtimehook.Observation)
	Apply(ctx context.Context, request runtimehook.TransformRequest) runtimehook.Patch
}

type RunResult struct {
	RunID        contracts.AgentRunID            `json:"run_id"`
	TaskID       contracts.TaskID                `json:"task_id"`
	Status       contracts.RunStatus             `json:"status"`
	Reply        *contracts.DecisionReply        `json:"reply,omitempty"`
	Ask          *contracts.ClarificationRequest `json:"ask,omitempty"`
	ArtifactRefs []contracts.ArtifactRef         `json:"artifact_refs,omitempty"`
	Error        *contracts.RuntimeError         `json:"error,omitempty"`
}

type PreparedRun struct {
	Envelope   contracts.AgentEnvelope
	Definition contracts.AgentDefinition
	Task       contracts.Task
	Run        contracts.AgentRun
	UserInput  string
	Source     string
}

func (p PreparedRun) Result() RunResult {
	return RunResult{RunID: p.Run.RunID, TaskID: p.Task.TaskID, Status: p.Run.Status}
}

type PromptPreviewRequest struct {
	Target    contracts.AgentTarget      `json:"target"`
	Input     string                     `json:"input"`
	Context   contracts.RuntimeContext   `json:"context"`
	Draft     *contracts.AgentDefinition `json:"draft,omitempty"`
	CreatedAt time.Time                  `json:"created_at,omitempty"`
}

type PromptPreviewResult struct {
	Agent                 contracts.AgentDefinition           `json:"agent"`
	PolicySet             contracts.PolicySet                 `json:"policy_set"`
	EffectiveStrategies   contracts.AgentStrategies           `json:"effective_strategies"`
	StrategyHash          string                              `json:"strategy_hash,omitempty"`
	GuardrailAdjustments  []agentstrategy.GuardrailAdjustment `json:"guardrail_adjustments,omitempty"`
	HookEffects           []PromptPreviewHookEffect           `json:"hook_effects,omitempty"`
	WorkView              contracts.WorkView                  `json:"work_view"`
	PromptBundle          contracts.PromptBundle              `json:"prompt_bundle"`
	ContextAssemblyReport *contracts.ContextAssemblyReport    `json:"context_assembly_report,omitempty"`
	CompressionReport     *contracts.ContextCompressionReport `json:"compression_report,omitempty"`
	TokenEstimate         int                                 `json:"token_estimate"`
	ModelProvider         string                              `json:"model_provider,omitempty"`
	ModelName             string                              `json:"model_name,omitempty"`
}

type PromptPreviewHookEffect struct {
	Phase               runtimehook.HookPoint `json:"phase"`
	ContextBlocksAdded  int                   `json:"context_blocks_added,omitempty"`
	ContextRefsDropped  int                   `json:"context_refs_dropped,omitempty"`
	ToolRankAdjustments int                   `json:"tool_rank_adjustments,omitempty"`
	PlannerHints        int                   `json:"planner_hints,omitempty"`
	MemoryWriteIntents  int                   `json:"memory_write_intents,omitempty"`
}

func NewCoordinator(agents loader.Loader, runs runrepo.Repository, tasks *taskruntime.Service, taskRepo taskrepo.TaskRepository, traceRecorder trace.Recorder, model modelclient.ModelClient) Coordinator {
	return Coordinator{
		Agents:           agents,
		Runs:             runs,
		Tasks:            tasks,
		TaskRepo:         taskRepo,
		Trace:            traceRecorder,
		Tools:            tooldiscovery.StaticCandidateProvider{Capabilities: tooldiscovery.DefaultCapabilities(), Skills: tooldiscovery.DefaultSkills(), Cards: tooldiscovery.DefaultCards()},
		WorkView:         workviewbuilder.NewBuilder(),
		Prompts:          promptbuilder.NewBuilder(),
		Model:            model,
		Validator:        decisionvalidator.New(),
		AddressingJudge:  contextconversation.HeuristicAddressingJudge{},
		SufficiencyJudge: contextconversation.HeuristicSufficiencyJudge{},
		ContextRetriever: contextconversation.BasicRetriever{},
		DisabledToolIDs:  map[string]struct{}{},
		Now:              func() time.Time { return time.Now().UTC() },
	}
}

func (c Coordinator) strategyDefaults() agentstrategy.Defaults {
	defaults := agentstrategy.DefaultValues()
	if contextStrategyConfigured(c.ContextDefaults) {
		defaults.Context = c.ContextDefaults
	}
	return defaults
}

func contextStrategyConfigured(strategy contracts.ContextStrategy) bool {
	return strategy.Mode != "" ||
		strategy.RecentMessageLimit != nil ||
		strategy.RetrievalMaxResults != nil ||
		strategy.TaskHistoryMaxItems != nil ||
		strategy.MemoryMaxItems != nil ||
		strategy.ArtifactRefMaxItems != nil ||
		strategy.ToolResultMaxItems != nil ||
		strategy.ContextTokenBudget != nil ||
		len(strategy.EnabledSources) > 0 ||
		len(strategy.SourceBudgets) > 0 ||
		strategy.Compression.Mode != "" ||
		strategy.Compression.Enabled ||
		strategy.Compression.TriggerRatio > 0 ||
		strategy.Compression.TargetTokens > 0 ||
		strategy.Compression.PromptProfileID != "" ||
		strategy.Compression.InlinePrompt != nil
}

func appendPromptPreviewHookEffect(effects []PromptPreviewHookEffect, phase runtimehook.HookPoint, patch runtimehook.Patch) []PromptPreviewHookEffect {
	effect := PromptPreviewHookEffect{
		Phase:               phase,
		ContextBlocksAdded:  len(patch.AddContextBlocks),
		ContextRefsDropped:  len(patch.DropContextRefs),
		ToolRankAdjustments: len(patch.ToolRankAdjustments),
		PlannerHints:        len(patch.PlannerHints),
		MemoryWriteIntents:  len(patch.MemoryWriteIntents),
	}
	if effect.ContextBlocksAdded == 0 &&
		effect.ContextRefsDropped == 0 &&
		effect.ToolRankAdjustments == 0 &&
		effect.PlannerHints == 0 &&
		effect.MemoryWriteIntents == 0 {
		return effects
	}
	return append(effects, effect)
}

func (c Coordinator) PreviewPromptBundle(ctx context.Context, request PromptPreviewRequest) (PromptPreviewResult, error) {
	now := c.Now()
	if !request.CreatedAt.IsZero() {
		now = request.CreatedAt
	}
	tenantID := request.Context.TenantID
	definition := contracts.AgentDefinition{}
	if request.Draft != nil {
		definition = *request.Draft
	} else {
		loaded, err := c.Agents.Load(ctx, tenantID, request.Target.AgentID, request.Target.Version)
		if err != nil {
			return PromptPreviewResult{}, err
		}
		definition = loaded
	}
	if definition.TenantID == "" {
		definition.TenantID = tenantID
	}
	if request.Input == "" {
		request.Input = "preview"
	}
	policySet := c.policySet(ctx, tenantID, definition.PolicyRefs.PolicySetID)
	if definition.PolicyRefs.PolicySetID == "" {
		policySet = c.policySet(ctx, tenantID, "policy_default")
	}
	effective, strategyReport, err := agentstrategy.Resolve(definition, policySet, c.strategyDefaults())
	if err != nil {
		return PromptPreviewResult{}, err
	}
	activeDefinition := applyEffectiveStrategiesToRuntimeDefinition(definition, effective)
	activePolicySet := effective.Policy
	runID := contracts.AgentRunID(idgen.New("previewrun"))
	taskID := contracts.TaskID(idgen.New("previewtask"))
	run := contracts.AgentRun{
		RunID:        runID,
		TenantID:     tenantID,
		AgentID:      definition.AgentID,
		AgentVersion: definition.Version,
		TaskID:       taskID,
		Status:       contracts.RunCreated,
		PolicySetID:  definition.PolicyRefs.PolicySetID,
		StartedAt:    now,
	}
	task := contracts.Task{
		TaskID:       taskID,
		TenantID:     tenantID,
		Title:        "prompt preview",
		Objective:    request.Input,
		Status:       contracts.TaskCreated,
		AgentID:      definition.AgentID,
		AgentVersion: definition.Version,
		PolicySetID:  definition.PolicyRefs.PolicySetID,
		CreatedAt:    now,
		UpdatedAt:    now,
	}
	candidates := tooldiscovery.CandidateSet{}
	if c.Tools != nil {
		found, err := c.Tools.Candidates(ctx, activeDefinition, activePolicySet, request.Input)
		if err != nil {
			return PromptPreviewResult{}, err
		}
		candidates = found
	}
	envelope := contracts.AgentEnvelope{
		TraceID: "preview",
		Target: contracts.AgentTarget{
			AgentID: definition.AgentID,
			Version: definition.Version,
		},
		Context: request.Context,
	}
	hookEffects := make([]PromptPreviewHookEffect, 0, 3)
	candidatePatch := c.applyRuntimeHook(ctx, runtimehook.AfterCandidateRetrieval, runtimehook.TransformRequest{
		TenantID:   envelope.Context.TenantID,
		TraceID:    envelope.TraceID,
		RunID:      runID,
		TaskID:     taskID,
		Agent:      activeDefinition,
		Policy:     activePolicySet,
		Objective:  request.Input,
		Candidates: candidates,
	})
	hookEffects = appendPromptPreviewHookEffect(hookEffects, runtimehook.AfterCandidateRetrieval, candidatePatch)
	candidates = applyCandidatePatch(candidates, candidatePatch)
	candidates = tooldiscovery.ApplyToolUseStrategy(candidates, effective.Tools)
	candidates = tooldiscovery.ApplySkillUseStrategy(candidates, effective.Skills)
	candidates = tooldiscovery.ApplyKnowledgeUseStrategy(candidates, effective.Knowledge)
	contextPatch := c.applyRuntimeHook(ctx, runtimehook.BeforeContextBuild, runtimehook.TransformRequest{
		TenantID:   tenantID,
		TraceID:    envelope.TraceID,
		RunID:      runID,
		TaskID:     taskID,
		Agent:      activeDefinition,
		Policy:     activePolicySet,
		Objective:  request.Input,
		Candidates: candidates,
	})
	hookEffects = appendPromptPreviewHookEffect(hookEffects, runtimehook.BeforeContextBuild, contextPatch)
	view, err := c.WorkView.Build(ctx, workviewbuilder.BuildInput{
		Run:               run,
		Task:              task,
		Agent:             activeDefinition,
		UserInput:         request.Input,
		Capabilities:      candidates.Capabilities,
		Skills:            candidates.Skills,
		SkillInstructions: candidates.SkillInstructions,
		Tools:             candidates.Tools,
		Collaborators:     candidates.Collaborators,
	})
	if err != nil {
		return PromptPreviewResult{}, err
	}
	workviewbuilder.ApplyRuntimeHookPatch(&view, contextPatch)
	sourceReports := contextcollector.ApplyContextSourcePolicy(&view, effective.Context, effective.Memory)
	view.ContextAssemblyReport = contextcollector.ContextAssemblyReport(strategyReport.StrategyHash, effective.Context, sourceReports)
	bundle, err := c.Prompts.Build(ctx, activeDefinition, view)
	if err != nil {
		return PromptPreviewResult{}, err
	}
	promptPatch := c.applyRuntimeHook(ctx, runtimehook.BeforeModelCall, runtimehook.TransformRequest{
		TenantID:     envelope.Context.TenantID,
		TraceID:      envelope.TraceID,
		RunID:        runID,
		TaskID:       taskID,
		Agent:        activeDefinition,
		Policy:       activePolicySet,
		Objective:    request.Input,
		Candidates:   candidates,
		WorkView:     view,
		PromptBundle: bundle,
	})
	var promptSourceReports []contracts.ContextSourceReport
	promptPatch.AddContextBlocks, promptSourceReports = contextcollector.FilterRuntimeHookContextBlocks(promptPatch.AddContextBlocks, effective.Context)
	if bundle.ContextAssemblyReport != nil {
		for _, sourceReport := range promptSourceReports {
			contextcollector.MergeContextSourceReportRow(bundle.ContextAssemblyReport, sourceReport)
		}
	}
	hookEffects = appendPromptPreviewHookEffect(hookEffects, runtimehook.BeforeModelCall, promptPatch)
	bundle, err = promptbuilder.ApplyRuntimeHookPatch(bundle, promptPatch)
	if err != nil {
		return PromptPreviewResult{}, err
	}
	bundle = outputpolicy.ApplyPromptBundle(bundle, activeDefinition.Strategies.Output)
	if err := promptbuilder.RefreshHash(&bundle); err != nil {
		return PromptPreviewResult{}, err
	}
	var compressionReport *contracts.ContextCompressionReport
	bundle, compressionReport, err = c.applyPromptPolicy(ctx, activePolicySet, activeDefinition, effective.Context, bundle)
	if err != nil {
		return PromptPreviewResult{}, err
	}
	return PromptPreviewResult{
		Agent:                 activeDefinition,
		PolicySet:             activePolicySet,
		EffectiveStrategies:   effectiveAgentStrategies(effective),
		StrategyHash:          strategyReport.StrategyHash,
		GuardrailAdjustments:  strategyReport.Adjustments,
		HookEffects:           hookEffects,
		WorkView:              view,
		PromptBundle:          bundle,
		ContextAssemblyReport: bundle.ContextAssemblyReport,
		CompressionReport:     compressionReport,
		TokenEstimate:         estimatePromptTokens(bundle),
		ModelProvider:         c.modelProviderForDefinition(activeDefinition),
		ModelName:             c.modelNameForDefinition(activeDefinition),
	}, nil
}

func (c Coordinator) HandleEnvelope(ctx context.Context, envelope contracts.AgentEnvelope) (RunResult, error) {
	prepared, err := c.PrepareEnvelopeRun(ctx, envelope)
	if err != nil {
		var runtimeErr *contracts.RuntimeError
		if errors.As(err, &runtimeErr) {
			return RunResult{Error: runtimeErr}, err
		}
		return RunResult{}, err
	}
	return c.ExecutePreparedRun(ctx, prepared)
}

func (c Coordinator) PrepareEnvelopeRun(ctx context.Context, envelope contracts.AgentEnvelope) (PreparedRun, error) {
	if envelope.Command != "agent.run" {
		runtimeErr := contracts.NewRuntimeError(contracts.CodeDecisionSchemaError, fmt.Sprintf("unsupported command %q", envelope.Command), nil)
		return PreparedRun{}, runtimeErr
	}
	normalized, userInput, err := c.normalizeRunEnvelope(envelope)
	if err != nil {
		return PreparedRun{}, err
	}
	envelope = normalized
	c.recordTrace(ctx, envelope.TraceID, envelope.Context.TenantID, "", envelope.Context.TaskID, contracts.TraceInputReceived, map[string]any{
		"command": envelope.Command,
		"target":  envelope.Target,
	})
	if envelope.Context.TaskID != "" {
		task, err := c.TaskRepo.Get(ctx, envelope.Context.TaskID)
		if err != nil {
			return PreparedRun{}, err
		}
		if task.TenantID != envelope.Context.TenantID {
			return PreparedRun{}, contracts.NewRuntimeError(contracts.CodeToolPolicyDenied, "task tenant does not match envelope tenant", nil)
		}
		return c.prepareTaskRun(ctx, envelope, task, userInput)
	}
	return c.prepareNewTaskRun(ctx, envelope, userInput)
}

func (c Coordinator) normalizeRunEnvelope(envelope contracts.AgentEnvelope) (contracts.AgentEnvelope, string, error) {
	if envelope.Payload == nil {
		envelope.Payload = map[string]any{}
	}
	userInput, _ := envelope.Payload["input"].(string)
	userInput = strings.TrimSpace(userInput)
	if userInput == "" {
		return contracts.AgentEnvelope{}, "", contracts.NewRuntimeError(contracts.CodeDecisionSchemaError, "agent.run requires non-empty payload.input", nil)
	}
	if envelope.Context.ExternalTask != nil {
		if strings.TrimSpace(envelope.Context.ExternalTask.Provider) == "" || strings.TrimSpace(string(envelope.Context.ExternalTask.ExternalTaskID)) == "" {
			return contracts.AgentEnvelope{}, "", contracts.NewRuntimeError(contracts.CodeDecisionSchemaError, "context.external_task requires provider and external_task_id", nil)
		}
	}
	if conversation := envelope.Context.Conversation; conversation != nil {
		conversation.ConversationID = strings.TrimSpace(conversation.ConversationID)
		if conversation.ConversationID == "" {
			return contracts.AgentEnvelope{}, "", contracts.NewRuntimeError(contracts.CodeDecisionSchemaError, "context.conversation requires conversation_id", nil)
		}
		conversation.ThreadID = strings.TrimSpace(conversation.ThreadID)
		if conversation.ThreadID == "" {
			conversation.ThreadID = conversation.ConversationID
		}
		if conversation.CurrentMessage == nil {
			conversation.CurrentMessage = &contracts.RuntimeMessage{}
		}
		current := conversation.CurrentMessage
		if strings.TrimSpace(current.Text) != "" && strings.TrimSpace(current.Text) != userInput {
			return contracts.AgentEnvelope{}, "", contracts.NewRuntimeError(contracts.CodeDecisionSchemaError, "context.conversation.current_message.text must match payload.input", nil)
		}
		current.Text = userInput
		current.MessageID = strings.TrimSpace(current.MessageID)
		if current.MessageID == "" {
			current.MessageID = string(envelope.EnvelopeID)
		}
		if current.MessageID == "" {
			current.MessageID = idgen.New("msg")
		}
		if current.ThreadID == "" {
			current.ThreadID = conversation.ThreadID
		}
		if current.SpeakerID == "" {
			current.SpeakerID = envelope.Caller.CallerID
		}
		if current.SpeakerID == "" {
			current.SpeakerID = string(envelope.Context.UserID)
		}
		if current.SpeakerType == "" {
			current.SpeakerType = envelope.Caller.CallerType
		}
		if current.SpeakerType == "" {
			current.SpeakerType = "user"
		}
		if current.CreatedAt.IsZero() {
			current.CreatedAt = envelope.CreatedAt
		}
		if current.CreatedAt.IsZero() {
			current.CreatedAt = c.Now()
		}
		if conversation.Kind == "" {
			conversation.Kind = contextconversation.KindDirect
		}
	}
	return envelope, userInput, nil
}

func (c Coordinator) prepareNewTaskRun(ctx context.Context, envelope contracts.AgentEnvelope, userInput string) (PreparedRun, error) {
	now := c.Now()
	definition, err := c.Agents.Load(ctx, envelope.Context.TenantID, envelope.Target.AgentID, envelope.Target.Version)
	if err != nil {
		return PreparedRun{}, err
	}
	if definition.TenantID == "" {
		definition.TenantID = envelope.Context.TenantID
	}
	c.recordTrace(ctx, envelope.TraceID, envelope.Context.TenantID, "", "", contracts.TraceAgentLoaded, map[string]any{
		"agent_id":      definition.AgentID,
		"agent_version": definition.Version,
		"policy_set_id": definition.PolicyRefs.PolicySetID,
	})
	task := taskrepo.NewTask(
		contracts.TaskID(idgen.New("task")),
		envelope.Context.TenantID,
		definition.AgentID,
		definition.Version,
		definition.PolicyRefs.PolicySetID,
		"agent.run",
		userInput,
		now,
	)
	task, err = c.Tasks.CreateTask(ctx, task, envelope.Caller.CallerID, envelope.Caller.CallerType)
	if err != nil {
		return PreparedRun{}, err
	}
	c.recordTrace(ctx, envelope.TraceID, envelope.Context.TenantID, "", task.TaskID, contracts.TraceTaskCreated, map[string]any{
		"task_id":       task.TaskID,
		"agent_id":      task.AgentID,
		"agent_version": task.AgentVersion,
		"policy_set_id": task.PolicySetID,
	})
	snapshot := c.versionSnapshot(ctx, envelope.Context.TenantID, definition, userInput)
	run := contracts.AgentRun{
		RunID:            contracts.AgentRunID(idgen.New("run")),
		TraceID:          envelope.TraceID,
		TenantID:         envelope.Context.TenantID,
		AgentID:          definition.AgentID,
		AgentVersion:     definition.Version,
		CarrierKind:      snapshot.CarrierKind,
		RuntimeContract:  snapshot.RuntimeContract,
		SourceKind:       snapshot.SourceKind,
		SourceProviderID: snapshot.SourceProviderID,
		CarrierVersion:   snapshot.CarrierVersion,
		ManifestHash:     snapshot.ManifestHash,
		TaskID:           task.TaskID,
		Input:            userInput,
		ConversationID:   runtimeConversationID(envelope),
		ThreadID:         runtimeConversationThreadID(envelope),
		MessageID:        runtimeConversationMessageID(envelope),
		Status:           contracts.RunCreated,
		PolicySetID:      definition.PolicyRefs.PolicySetID,
		VersionSnapshot:  snapshot,
		StartedAt:        now,
	}
	if err := c.Runs.Create(ctx, run); err != nil {
		return PreparedRun{}, err
	}
	if err := c.recordPreparedInputFacts(ctx, envelope, run, task, userInput); err != nil {
		return PreparedRun{}, err
	}
	c.observeHook(ctx, runtimehook.OnRunStarted, envelope, definition, run.RunID, task.TaskID, map[string]any{"source": "new_task"})
	_ = c.Trace.Record(ctx, contracts.TraceEvent{
		TraceID:  envelope.TraceID,
		TenantID: envelope.Context.TenantID,
		SpanID:   contracts.SpanID(idgen.New("span")),
		RunID:    run.RunID,
		TaskID:   task.TaskID,
		Type:     contracts.TraceRunCreated,
		Payload: map[string]any{
			"agent_id":             definition.AgentID,
			"agent_version":        definition.Version,
			"carrier_kind":         run.VersionSnapshot.CarrierKind,
			"runtime_contract":     run.VersionSnapshot.RuntimeContract,
			"carrier_version":      run.VersionSnapshot.CarrierVersion,
			"source_kind":          run.VersionSnapshot.SourceKind,
			"source_provider_id":   run.VersionSnapshot.SourceProviderID,
			"manifest_version":     run.VersionSnapshot.ManifestVersion,
			"manifest_hash":        run.VersionSnapshot.ManifestHash,
			"policy_set_id":        run.VersionSnapshot.PolicySet,
			"policy_version_id":    run.VersionSnapshot.PolicyVersionID,
			"policy_set_version":   run.VersionSnapshot.PolicySetVersion,
			"agent_package":        run.VersionSnapshot.AgentPackage,
			"model_provider":       run.VersionSnapshot.ModelProvider,
			"model_name":           run.VersionSnapshot.ModelName,
			"contract_version":     run.VersionSnapshot.ContractVersion,
			"tool_definitions":     run.VersionSnapshot.ToolDefinitions,
			"skill_definitions":    run.VersionSnapshot.SkillDefinitions,
			"agent_definition":     run.VersionSnapshot.AgentDefinition,
			"prompt_bundle_hash":   run.VersionSnapshot.PromptBundleHash,
			"snapshot_recorded_at": now,
		},
		CreatedAt: now,
	})
	return PreparedRun{Envelope: envelope, Definition: definition, Task: task, Run: run, UserInput: userInput, Source: "new_task"}, nil
}

func (c Coordinator) ResumeRun(ctx context.Context, envelope contracts.AgentEnvelope, runID contracts.AgentRunID, taskID contracts.TaskID) (RunResult, error) {
	run, err := c.Runs.Get(ctx, runID)
	if err != nil {
		return RunResult{}, err
	}
	if run.TenantID != envelope.Context.TenantID {
		return RunResult{}, contracts.NewRuntimeError(contracts.CodeToolPolicyDenied, "run tenant does not match envelope tenant", nil)
	}
	definition, err := c.Agents.Load(ctx, envelope.Context.TenantID, run.AgentID, run.AgentVersion)
	if err != nil {
		return RunResult{}, err
	}
	if definition.TenantID == "" {
		definition.TenantID = envelope.Context.TenantID
	}
	c.recordTrace(ctx, envelope.TraceID, envelope.Context.TenantID, runID, taskID, contracts.TraceAgentLoaded, map[string]any{
		"agent_id":      definition.AgentID,
		"agent_version": definition.Version,
		"policy_set_id": definition.PolicyRefs.PolicySetID,
		"source":        "resume",
	})
	task, err := c.TaskRepo.Get(ctx, taskID)
	if err != nil {
		return RunResult{}, err
	}
	if task.TenantID != envelope.Context.TenantID {
		return RunResult{}, contracts.NewRuntimeError(contracts.CodeToolPolicyDenied, "task tenant does not match envelope tenant", nil)
	}
	c.recordTrace(ctx, envelope.TraceID, envelope.Context.TenantID, runID, taskID, contracts.TraceTaskLoaded, map[string]any{
		"task_id":       task.TaskID,
		"status":        task.Status,
		"agent_id":      task.AgentID,
		"agent_version": task.AgentVersion,
	})
	if _, err := c.Runs.MarkRunning(ctx, runID); err != nil {
		return RunResult{}, err
	}
	resumedInput, _ := envelope.Payload["input"].(string)
	resumedInput = strings.TrimSpace(resumedInput)
	currentInput := strings.TrimSpace(run.Input)
	if resumedInput != "" {
		normalized, input, err := c.normalizeRunEnvelope(envelope)
		if err != nil {
			return RunResult{}, err
		}
		envelope = normalized
		currentInput = input
		if err := c.recordResumeInputFacts(ctx, envelope, run, task, input); err != nil {
			return RunResult{}, err
		}
	}
	if currentInput == "" {
		return RunResult{}, contracts.NewRuntimeError(contracts.CodeDecisionSchemaError, "resume requires run.input or non-empty payload.input", nil)
	}
	result, err := c.loop(ctx, envelope, definition, runID, taskID, currentInput)
	if err != nil {
		runtimeErr := normalizeRuntimeError(err)
		_, _ = c.Runs.MarkFailed(ctx, runID, runtimeErr)
		c.syncRunFailed(ctx, envelope, runID, taskID, runtimeErr.Message)
		_, _, _, _ = c.Tasks.ApplyCommand(ctx, taskruntime.CommandInput{
			TaskID:    taskID,
			Command:   contracts.CmdFail,
			ActorID:   string(definition.AgentID),
			ActorType: "agent",
			RunID:     runID,
			Payload:   runtimeErr.ToTracePayload(),
		})
		result.Error = runtimeErr
		return result, err
	}
	return result, nil
}

func (c Coordinator) StartTaskRun(ctx context.Context, envelope contracts.AgentEnvelope, task contracts.Task) (RunResult, error) {
	normalized, userInput, err := c.normalizeRunEnvelope(envelope)
	if err != nil {
		return RunResult{}, err
	}
	prepared, err := c.prepareTaskRun(ctx, normalized, task, userInput)
	if err != nil {
		return RunResult{}, err
	}
	return c.ExecutePreparedRun(ctx, prepared)
}

func (c Coordinator) prepareTaskRun(ctx context.Context, envelope contracts.AgentEnvelope, task contracts.Task, userInput string) (PreparedRun, error) {
	definition, err := c.Agents.Load(ctx, task.TenantID, task.AgentID, task.AgentVersion)
	if err != nil {
		return PreparedRun{}, err
	}
	if definition.TenantID == "" {
		definition.TenantID = task.TenantID
	}
	c.recordTrace(ctx, envelope.TraceID, task.TenantID, "", task.TaskID, contracts.TraceAgentLoaded, map[string]any{
		"agent_id":      definition.AgentID,
		"agent_version": definition.Version,
		"policy_set_id": definition.PolicyRefs.PolicySetID,
		"source":        "task",
	})
	c.recordTrace(ctx, envelope.TraceID, task.TenantID, "", task.TaskID, contracts.TraceTaskLoaded, map[string]any{
		"task_id":       task.TaskID,
		"status":        task.Status,
		"agent_id":      task.AgentID,
		"agent_version": task.AgentVersion,
	})
	now := c.Now()
	snapshot := c.versionSnapshot(ctx, task.TenantID, definition, task.Objective)
	run := contracts.AgentRun{
		RunID:            contracts.AgentRunID(idgen.New("run")),
		TraceID:          envelope.TraceID,
		TenantID:         task.TenantID,
		AgentID:          definition.AgentID,
		AgentVersion:     definition.Version,
		CarrierKind:      snapshot.CarrierKind,
		RuntimeContract:  snapshot.RuntimeContract,
		SourceKind:       snapshot.SourceKind,
		SourceProviderID: snapshot.SourceProviderID,
		CarrierVersion:   snapshot.CarrierVersion,
		ManifestHash:     snapshot.ManifestHash,
		TaskID:           task.TaskID,
		Input:            userInput,
		ConversationID:   runtimeConversationID(envelope),
		ThreadID:         runtimeConversationThreadID(envelope),
		MessageID:        runtimeConversationMessageID(envelope),
		Status:           contracts.RunCreated,
		PolicySetID:      definition.PolicyRefs.PolicySetID,
		VersionSnapshot:  snapshot,
		StartedAt:        now,
	}
	if err := c.Runs.Create(ctx, run); err != nil {
		return PreparedRun{}, err
	}
	if err := c.recordPreparedInputFacts(ctx, envelope, run, task, userInput); err != nil {
		return PreparedRun{}, err
	}
	c.observeHook(ctx, runtimehook.OnRunStarted, envelope, definition, run.RunID, task.TaskID, map[string]any{"source": "existing_task"})
	_ = c.Trace.Record(ctx, contracts.TraceEvent{
		TraceID:  envelope.TraceID,
		TenantID: task.TenantID,
		SpanID:   contracts.SpanID(idgen.New("span")),
		RunID:    run.RunID,
		TaskID:   task.TaskID,
		Type:     contracts.TraceRunCreated,
		Payload: map[string]any{
			"agent_id":           definition.AgentID,
			"agent_version":      definition.Version,
			"carrier_kind":       run.VersionSnapshot.CarrierKind,
			"runtime_contract":   run.VersionSnapshot.RuntimeContract,
			"carrier_version":    run.VersionSnapshot.CarrierVersion,
			"source_kind":        run.VersionSnapshot.SourceKind,
			"source_provider_id": run.VersionSnapshot.SourceProviderID,
			"manifest_version":   run.VersionSnapshot.ManifestVersion,
			"manifest_hash":      run.VersionSnapshot.ManifestHash,
			"source":             "existing_task",
			"policy_set_id":      run.VersionSnapshot.PolicySet,
			"policy_version_id":  run.VersionSnapshot.PolicyVersionID,
			"policy_set_version": run.VersionSnapshot.PolicySetVersion,
			"agent_package":      run.VersionSnapshot.AgentPackage,
			"model_provider":     run.VersionSnapshot.ModelProvider,
			"model_name":         run.VersionSnapshot.ModelName,
			"contract_version":   run.VersionSnapshot.ContractVersion,
			"tool_definitions":   run.VersionSnapshot.ToolDefinitions,
			"skill_definitions":  run.VersionSnapshot.SkillDefinitions,
			"agent_definition":   run.VersionSnapshot.AgentDefinition,
		},
		CreatedAt: now,
	})
	return PreparedRun{Envelope: envelope, Definition: definition, Task: task, Run: run, UserInput: userInput, Source: "existing_task"}, nil
}

func (c Coordinator) ExecutePreparedRun(ctx context.Context, prepared PreparedRun) (RunResult, error) {
	envelope := prepared.Envelope
	definition := prepared.Definition
	task := prepared.Task
	run := prepared.Run
	for _, command := range []contracts.TaskCommand{contracts.CmdAccept, contracts.CmdPlanStarted, contracts.CmdRunStarted} {
		if _, _, _, err := c.Tasks.ApplyCommand(ctx, taskruntime.CommandInput{
			TaskID:    task.TaskID,
			Command:   command,
			ActorID:   string(definition.AgentID),
			ActorType: "agent",
			RunID:     run.RunID,
		}); err != nil {
			return RunResult{}, err
		}
	}
	if _, err := c.Runs.MarkRunning(ctx, run.RunID); err != nil {
		return RunResult{}, err
	}
	result, err := c.loop(ctx, envelope, definition, run.RunID, task.TaskID, prepared.UserInput)
	if err != nil {
		runtimeErr := normalizeRuntimeError(err)
		_, _ = c.Runs.MarkFailed(ctx, run.RunID, runtimeErr)
		c.syncRunFailed(ctx, envelope, run.RunID, task.TaskID, runtimeErr.Message)
		_, _, _, _ = c.Tasks.ApplyCommand(ctx, taskruntime.CommandInput{
			TaskID:    task.TaskID,
			Command:   contracts.CmdFail,
			ActorID:   string(definition.AgentID),
			ActorType: "agent",
			RunID:     run.RunID,
			Payload:   runtimeErr.ToTracePayload(),
		})
		result.Error = runtimeErr
		return result, err
	}
	return result, nil
}

func (c Coordinator) loop(ctx context.Context, envelope contracts.AgentEnvelope, definition contracts.AgentDefinition, runID contracts.AgentRunID, taskID contracts.TaskID, userInput string) (RunResult, error) {
	for {
		run, err := c.Runs.Get(ctx, runID)
		if err != nil {
			return RunResult{}, err
		}
		if definition.Runtime.MaxSteps > 0 && run.StepCount >= definition.Runtime.MaxSteps {
			return RunResult{}, contracts.NewRuntimeError(contracts.CodeModelError, "max steps exceeded", nil)
		}
		if definition.Runtime.MaxDuration > 0 && !run.StartedAt.IsZero() && c.Now().Sub(run.StartedAt) > definition.Runtime.MaxDuration {
			return RunResult{}, contracts.NewRuntimeError(contracts.CodeModelTimeout, "max duration exceeded", nil)
		}
		result, terminal, err := c.step(ctx, envelope, &definition, runID, taskID, userInput)
		if err != nil || terminal {
			return result, err
		}
	}
}

func (c Coordinator) step(ctx context.Context, envelope contracts.AgentEnvelope, definition *contracts.AgentDefinition, runID contracts.AgentRunID, taskID contracts.TaskID, userInput string) (RunResult, bool, error) {
	run, stepID, err := c.Runs.IncrementStep(ctx, runID)
	if err != nil {
		return RunResult{}, true, err
	}
	task, err := c.TaskRepo.Get(ctx, taskID)
	if err != nil {
		return RunResult{}, true, err
	}
	policySet := c.policySetForRun(ctx, run, envelope.Context.TenantID, definition.PolicyRefs.PolicySetID)
	if upgradedDefinition, upgraded, err := c.maybeUpgradeByPolicy(ctx, envelope.TraceID, envelope.Context.TenantID, *definition, task, policySet); err != nil {
		return RunResult{}, true, err
	} else if upgraded {
		*definition = upgradedDefinition
		snapshot, err := c.refreshRunSnapshot(ctx, runID, *definition, task.Objective)
		if err != nil {
			return RunResult{}, true, err
		}
		run.VersionSnapshot = snapshot
		policySet = c.policySetForRun(ctx, run, envelope.Context.TenantID, definition.PolicyRefs.PolicySetID)
	}
	activeDefinition := *definition
	effective, strategyReport, err := agentstrategy.Resolve(activeDefinition, policySet, c.strategyDefaults())
	if err != nil {
		return RunResult{}, true, err
	}
	activeDefinition = applyEffectiveStrategiesToRuntimeDefinition(activeDefinition, effective)
	policySet = effective.Policy
	if activeDefinition.Runtime.MaxSteps > 0 && run.StepCount > activeDefinition.Runtime.MaxSteps {
		return RunResult{}, true, contracts.NewRuntimeError(contracts.CodeModelError, "max steps exceeded", nil)
	}
	if activeDefinition.Runtime.MaxDuration > 0 && !run.StartedAt.IsZero() && c.Now().Sub(run.StartedAt) > activeDefinition.Runtime.MaxDuration {
		return RunResult{}, true, contracts.NewRuntimeError(contracts.CodeModelTimeout, "max duration exceeded", nil)
	}
	c.recordTrace(ctx, envelope.TraceID, envelope.Context.TenantID, runID, taskID, contracts.TraceStrategyResolved, map[string]any{
		"strategy_hash":         strategyReport.StrategyHash,
		"source_kind":           activeDefinition.SourceKind,
		"source_provider_id":    activeDefinition.SourceProviderID,
		"manifest_version":      activeDefinition.ManifestVersion,
		"manifest_hash":         activeDefinition.ManifestHash,
		"agent_package":         activeDefinition.PackageVersionID,
		"guardrail_adjustments": strategyReport.Adjustments,
		"context_mode":          effective.Context.Mode,
		"context_sources":       effective.Context.EnabledSources,
		"context_token_budget":  contracts.IntValue(effective.Context.ContextTokenBudget),
		"model":                 effective.Model,
		"runtime":               activeDefinition.Runtime,
		"tools":                 effective.Tools,
		"skills":                effective.Skills,
		"collaboration":         effective.Collaboration,
		"memory":                effective.Memory,
		"knowledge":             effective.Knowledge,
		"repair":                effective.Repair,
		"output":                effective.Output,
	})
	if len(strategyReport.Adjustments) > 0 {
		c.recordTrace(ctx, envelope.TraceID, envelope.Context.TenantID, runID, taskID, contracts.TraceStrategyGuardrailApplied, map[string]any{
			"strategy_hash":    strategyReport.StrategyHash,
			"adjustment_count": len(strategyReport.Adjustments),
			"adjustments":      strategyReport.Adjustments,
		})
	}
	c.recordModelStrategySelected(ctx, envelope, activeDefinition, runID, taskID, strategyReport.StrategyHash)
	c.recordRuntimeStrategyApplied(ctx, envelope, runID, taskID, strategyReport.StrategyHash, effective.Runtime, activeDefinition.Runtime)
	c.recordRepairStrategyApplied(ctx, envelope, runID, taskID, strategyReport.StrategyHash, effective.Repair, activeDefinition.Runtime)
	candidates, err := c.Tools.Candidates(ctx, activeDefinition, policySet, task.Objective)
	if err != nil {
		return RunResult{}, true, err
	}
	candidates = c.applyCandidateHook(ctx, envelope, activeDefinition, policySet, runID, taskID, task.Objective, candidates)
	candidatesBeforeStrategy := candidates
	candidates = tooldiscovery.ApplyToolUseStrategy(candidates, effective.Tools)
	candidates = tooldiscovery.ApplySkillUseStrategy(candidates, effective.Skills)
	candidates = tooldiscovery.ApplyKnowledgeUseStrategy(candidates, effective.Knowledge)
	c.recordToolStrategyApplied(ctx, envelope, runID, taskID, strategyReport.StrategyHash, effective.Tools, effective.Knowledge, candidatesBeforeStrategy, candidates)
	c.recordCollaborationStrategyApplied(ctx, envelope, runID, taskID, strategyReport.StrategyHash, effective.Collaboration, activeDefinition, candidates)
	c.recordTrace(ctx, envelope.TraceID, envelope.Context.TenantID, runID, taskID, contracts.TraceCapabilityRetrieved, map[string]any{
		"capability_count":  len(candidates.Capabilities),
		"skill_count":       len(candidates.Skills),
		"tool_count":        len(candidates.Tools),
		"policy_set_id":     policySet.PolicySetID,
		"policy_version_id": run.VersionSnapshot.PolicyVersionID,
		"policy_version":    policySet.Version,
	})
	plan, currentStep, err := c.planContext(ctx, taskID)
	if err != nil {
		return RunResult{}, true, err
	}
	contextSources, err := c.contextCollector().Collect(ctx, contextcollector.Input{
		TenantID:        envelope.Context.TenantID,
		TaskID:          taskID,
		RunID:           runID,
		AgentID:         activeDefinition.AgentID,
		UserID:          envelope.Context.UserID,
		ContextStrategy: effective.Context,
		MemoryStrategy:  effective.Memory,
	})
	if err != nil {
		return RunResult{}, true, err
	}
	events := contextSources.TaskEvents
	toolResults := contextSources.ToolResults
	taskHistory := contextSources.TaskHistory
	memorySummaries := contextSources.Memory
	c.recordMemoryStrategyApplied(ctx, envelope, runID, taskID, strategyReport.StrategyHash, effective.Memory, memorySummaries)
	artifactRefs := contextSources.ArtifactRefs
	conversation := c.conversationContext(ctx, envelope, activeDefinition, effective.Context, runID, taskID, events, memorySummaries, artifactRefs, toolResults, userInput)
	contextPatch := c.applyRuntimeHook(ctx, runtimehook.BeforeContextBuild, runtimehook.TransformRequest{
		TenantID:   envelope.Context.TenantID,
		TraceID:    envelope.TraceID,
		RunID:      runID,
		TaskID:     taskID,
		Agent:      activeDefinition,
		Policy:     policySet,
		Objective:  task.Objective,
		Candidates: candidates,
	})
	view, err := c.WorkView.Build(ctx, workviewbuilder.BuildInput{
		Run:               run,
		Task:              task,
		TaskEvents:        events,
		Agent:             activeDefinition,
		UserInput:         userInput,
		TaskHistory:       taskHistory,
		Plan:              plan,
		CurrentStep:       currentStep,
		Capabilities:      candidates.Capabilities,
		Skills:            candidates.Skills,
		SkillInstructions: candidates.SkillInstructions,
		Tools:             candidates.Tools,
		Collaborators:     candidates.Collaborators,
		ToolResults:       toolResults,
		Memory:            memorySummaries,
		Artifacts:         artifactRefs,
		Conversation:      conversation,
	})
	if err != nil {
		return RunResult{}, true, err
	}
	workviewbuilder.ApplyRuntimeHookPatch(&view, contextPatch)
	sourceReports := contextcollector.ApplyContextSourcePolicy(&view, effective.Context, effective.Memory)
	view.ContextAssemblyReport = contextcollector.ContextAssemblyReport(strategyReport.StrategyHash, effective.Context, sourceReports)
	c.observeHook(ctx, runtimehook.OnContextBuilt, envelope, activeDefinition, runID, taskID, map[string]any{"work_view": view})
	_ = c.Trace.Record(ctx, contracts.TraceEvent{
		TraceID:  envelope.TraceID,
		TenantID: envelope.Context.TenantID,
		SpanID:   contracts.SpanID(stepID),
		RunID:    runID,
		TaskID:   taskID,
		Type:     contracts.TraceWorkViewBuilt,
		Payload: map[string]any{
			"candidate_capabilities":  len(candidates.Capabilities),
			"candidate_skills":        len(candidates.Skills),
			"candidate_tools":         len(candidates.Tools),
			"candidate_collaborators": len(candidates.Collaborators),
			"has_conversation":        conversation != nil,
			"retrieved_context":       retrievedContextCount(conversation),
			"task_history_count":      len(taskHistory),
		},
		CreatedAt: c.Now(),
	})
	if shouldNoOpByConversationGuard(conversation) {
		c.recordContextCollectionCompleted(ctx, envelope.TraceID, envelope.Context.TenantID, runID, taskID, view.ContextAssemblyReport, 0)
		decision := contracts.Decision{
			DecisionID: contracts.DecisionID(idgen.New("decision")),
			Type:       contracts.DecisionTypeNoOp,
			Reason:     "not_addressed_to_agent",
			Confidence: conversation.Addressing.Confidence,
		}
		c.recordTrace(ctx, envelope.TraceID, envelope.Context.TenantID, runID, taskID, contracts.TraceConversationRouteGuardApplied, map[string]any{
			"action":             "no_op",
			"reason":             conversation.Addressing.Reason,
			"confidence":         conversation.Addressing.Confidence,
			"addressed_to_agent": conversation.Addressing.AddressedToAgent,
		})
		return c.dispatch(ctx, envelope, activeDefinition, policySet, runID, taskID, stepID, decision, candidates)
	}
	bundle, err := c.Prompts.Build(ctx, activeDefinition, view)
	if err != nil {
		return RunResult{}, true, err
	}
	bundle, err = c.applyPromptHook(ctx, envelope, activeDefinition, policySet, runID, taskID, task.Objective, candidates, view, effective.Context, bundle)
	if err != nil {
		return RunResult{}, true, err
	}
	bundle = outputpolicy.ApplyPromptBundle(bundle, activeDefinition.Strategies.Output)
	if err := promptbuilder.RefreshHash(&bundle); err != nil {
		return RunResult{}, true, err
	}
	c.recordContextCollectionCompleted(ctx, envelope.TraceID, envelope.Context.TenantID, runID, taskID, bundle.ContextAssemblyReport, estimatePromptTokens(bundle))
	var compressionReport *contracts.ContextCompressionReport
	if contextCompressionRequested(effective.Context.Compression) {
		c.recordTrace(ctx, envelope.TraceID, envelope.Context.TenantID, runID, taskID, contracts.TraceContextCompressionRequested, contextCompressionRequestedTracePayload(effective.Context))
	}
	bundle, compressionReport, err = c.applyPromptPolicy(ctx, policySet, activeDefinition, effective.Context, bundle)
	if compressionReport != nil && (compressionReport.Applied || compressionReport.FailureReason != "") {
		payload := contextCompressionTracePayload(*compressionReport)
		c.recordTrace(ctx, envelope.TraceID, envelope.Context.TenantID, runID, taskID, contracts.TraceContextCompressionCompleted, payload)
		if compressionReport.FailureReason != "" {
			c.recordTrace(ctx, envelope.TraceID, envelope.Context.TenantID, runID, taskID, contracts.TraceContextCompressionFailed, payload)
			if compressionReport.Applied {
				c.recordTrace(ctx, envelope.TraceID, envelope.Context.TenantID, runID, taskID, contracts.TraceContextCompressionFallbackApplied, payload)
			}
		}
	}
	if err != nil {
		return RunResult{}, true, err
	}
	if err := c.pinPromptBundleHash(ctx, runID, bundle.Hash); err != nil {
		return RunResult{}, true, err
	}
	_ = c.Trace.Record(ctx, contracts.TraceEvent{
		TraceID:  envelope.TraceID,
		TenantID: envelope.Context.TenantID,
		SpanID:   contracts.SpanID(stepID),
		RunID:    runID,
		TaskID:   taskID,
		Type:     contracts.TracePromptBundleBuilt,
		Payload: map[string]any{
			"hash":                    bundle.Hash,
			"context_assembly_report": bundle.ContextAssemblyReport,
			"compression_report":      compressionReport,
		},
		CreatedAt: c.Now(),
	})
	decision, err := c.modelDecision(ctx, envelope, activeDefinition, runID, taskID, stepID, bundle, policySet, candidates.Tools)
	if err != nil {
		return RunResult{}, true, err
	}
	if err := c.applyMemoryWriteHook(ctx, envelope, activeDefinition, policySet, effective.Memory, runID, taskID, task.Objective, candidates, view, bundle, decision); err != nil {
		return RunResult{}, true, err
	}
	return c.dispatch(ctx, envelope, activeDefinition, policySet, runID, taskID, stepID, decision, candidates)
}

func (c Coordinator) modelDecision(ctx context.Context, envelope contracts.AgentEnvelope, definition contracts.AgentDefinition, runID contracts.AgentRunID, taskID contracts.TaskID, stepID string, bundle contracts.PromptBundle, policySet contracts.PolicySet, candidateTools []contracts.ToolCard) (contracts.Decision, error) {
	maxRepairAttempts := repairAttemptLimit(policySet, definition)
	for repairAttempt := 0; ; repairAttempt++ {
		c.recordModelCalled(ctx, envelope, definition, runID, taskID, stepID, bundle, repairAttempt)
		modelResp, err := c.completeModel(ctx, envelope, definition, runID, taskID, stepID, bundle)
		if err != nil {
			c.recordModelCompleted(ctx, envelope, runID, taskID, stepID, modelclient.ModelResponse{}, err)
			return contracts.Decision{}, err
		}
		c.recordModelCompleted(ctx, envelope, runID, taskID, stepID, modelResp, nil)
		decision, err := outputpolicy.ParseDecision(modelResp.RawDecisionJSON, definition.Strategies.Output)
		if err != nil {
			if repairAttempt >= maxRepairAttempts || !isRepairable(err) {
				return contracts.Decision{}, err
			}
			bundle = c.repairPrompt(ctx, envelope, runID, taskID, stepID, bundle, repairAttempt+1, err)
			continue
		}
		if decision.DecisionID == "" {
			decision.DecisionID = contracts.DecisionID(idgen.New("decision"))
		}
		_ = c.Trace.Record(ctx, contracts.TraceEvent{
			TraceID:   envelope.TraceID,
			TenantID:  envelope.Context.TenantID,
			SpanID:    contracts.SpanID(stepID),
			RunID:     runID,
			TaskID:    taskID,
			Type:      contracts.TraceDecisionCreated,
			Payload:   map[string]any{"decision_id": decision.DecisionID, "type": decision.Type, "repair_attempt": repairAttempt},
			CreatedAt: c.Now(),
		})
		validation, err := c.Validator.Normalize(decision, candidateTools)
		if err != nil {
			_ = c.Trace.Record(ctx, contracts.TraceEvent{
				TraceID:   envelope.TraceID,
				TenantID:  envelope.Context.TenantID,
				SpanID:    contracts.SpanID(stepID),
				RunID:     runID,
				TaskID:    taskID,
				Type:      contracts.TraceDecisionValidated,
				Payload:   map[string]any{"decision_id": decision.DecisionID, "type": decision.Type, "error": err.Error(), "repair_attempt": repairAttempt},
				CreatedAt: c.Now(),
			})
			if repairAttempt >= maxRepairAttempts || !isRepairable(err) {
				return contracts.Decision{}, err
			}
			bundle = c.repairPrompt(ctx, envelope, runID, taskID, stepID, bundle, repairAttempt+1, err)
			continue
		}
		if err := outputpolicy.ValidateDecision(definition.Strategies.Output, validation); err != nil {
			_ = c.Trace.Record(ctx, contracts.TraceEvent{
				TraceID:   envelope.TraceID,
				TenantID:  envelope.Context.TenantID,
				SpanID:    contracts.SpanID(stepID),
				RunID:     runID,
				TaskID:    taskID,
				Type:      contracts.TraceDecisionValidated,
				Payload:   map[string]any{"decision_id": decision.DecisionID, "type": decision.Type, "error": err.Error(), "repair_attempt": repairAttempt, "output_strategy": definition.Strategies.Output},
				CreatedAt: c.Now(),
			})
			if repairAttempt >= maxRepairAttempts || !isRepairable(err) {
				return contracts.Decision{}, err
			}
			bundle = c.repairPrompt(ctx, envelope, runID, taskID, stepID, bundle, repairAttempt+1, err)
			continue
		}
		decision = validation.Decision
		c.observeHook(ctx, runtimehook.OnModelDecision, envelope, definition, runID, taskID, map[string]any{"decision": decision, "repair_attempt": repairAttempt})
		_ = c.Trace.Record(ctx, contracts.TraceEvent{
			TraceID:   envelope.TraceID,
			TenantID:  envelope.Context.TenantID,
			SpanID:    contracts.SpanID(stepID),
			RunID:     runID,
			TaskID:    taskID,
			Type:      contracts.TraceDecisionValidated,
			Payload:   map[string]any{"decision_id": decision.DecisionID, "type": decision.Type, "warnings": validation.Warnings, "repair_attempt": repairAttempt},
			CreatedAt: c.Now(),
		})
		_ = c.Trace.Record(ctx, contracts.TraceEvent{
			TraceID:   envelope.TraceID,
			TenantID:  envelope.Context.TenantID,
			SpanID:    contracts.SpanID(stepID),
			RunID:     runID,
			TaskID:    taskID,
			Type:      contracts.TraceDecisionCompleted,
			Payload:   map[string]any{"decision_id": decision.DecisionID, "type": decision.Type, "repair_attempt": repairAttempt},
			CreatedAt: c.Now(),
		})
		return decision, nil
	}
}

func (c Coordinator) recordModelCalled(ctx context.Context, envelope contracts.AgentEnvelope, definition contracts.AgentDefinition, runID contracts.AgentRunID, taskID contracts.TaskID, stepID string, bundle contracts.PromptBundle, repairAttempt int) {
	payload := map[string]any{"prompt_bundle_hash": bundle.Hash, "repair_attempt": repairAttempt}
	if provider := c.modelProviderForDefinition(definition); provider != "" {
		payload["model_provider"] = provider
	}
	if modelName := c.modelNameForDefinition(definition); modelName != "" {
		payload["model_name"] = modelName
	}
	if definition.Strategies.Model.MaxOutputTokens > 0 {
		payload["max_output_tokens"] = definition.Strategies.Model.MaxOutputTokens
	}
	if capabilities, ok := c.modelCapabilities(); ok {
		payload["model_capabilities"] = capabilities
		if capabilitiesHash, err := hash.StableJSON(capabilities); err == nil {
			payload["model_capabilities_hash"] = capabilitiesHash
		}
	}
	_ = c.Trace.Record(ctx, contracts.TraceEvent{
		TraceID:   envelope.TraceID,
		TenantID:  envelope.Context.TenantID,
		SpanID:    contracts.SpanID(stepID),
		RunID:     runID,
		TaskID:    taskID,
		Type:      contracts.TraceModelCalled,
		Payload:   payload,
		CreatedAt: c.Now(),
	})
}

func (c Coordinator) recordModelCompleted(ctx context.Context, envelope contracts.AgentEnvelope, runID contracts.AgentRunID, taskID contracts.TaskID, stepID string, modelResp modelclient.ModelResponse, err error) {
	payload := map[string]any{}
	if err != nil {
		payload["error"] = err.Error()
	} else {
		payload["model_provider"] = modelResp.ModelProvider
		payload["model_name"] = modelResp.ModelName
		payload["prompt_tokens"] = modelResp.Usage.PromptTokens
		payload["completion_tokens"] = modelResp.Usage.CompletionTokens
	}
	_ = c.Trace.Record(ctx, contracts.TraceEvent{
		TraceID:   envelope.TraceID,
		TenantID:  envelope.Context.TenantID,
		SpanID:    contracts.SpanID(stepID),
		RunID:     runID,
		TaskID:    taskID,
		Type:      contracts.TraceModelCompleted,
		Payload:   payload,
		CreatedAt: c.Now(),
	})
}

func (c Coordinator) recordModelDelta(ctx context.Context, envelope contracts.AgentEnvelope, runID contracts.AgentRunID, taskID contracts.TaskID, stepID string, event modelclient.ModelStreamEvent) {
	payload := map[string]any{
		"delta_length": len(event.Delta),
	}
	if event.ModelProvider != "" {
		payload["model_provider"] = event.ModelProvider
	}
	if event.ModelName != "" {
		payload["model_name"] = event.ModelName
	}
	_ = c.Trace.Record(ctx, contracts.TraceEvent{
		TraceID:   envelope.TraceID,
		TenantID:  envelope.Context.TenantID,
		SpanID:    contracts.SpanID(stepID),
		RunID:     runID,
		TaskID:    taskID,
		Type:      contracts.TraceModelDelta,
		Payload:   payload,
		CreatedAt: c.Now(),
	})
}

func (c Coordinator) recordModelStrategySelected(ctx context.Context, envelope contracts.AgentEnvelope, definition contracts.AgentDefinition, runID contracts.AgentRunID, taskID contracts.TaskID, strategyHash string) {
	strategy := definition.Strategies.Model
	payload := map[string]any{
		"strategy_hash":  strategyHash,
		"model_provider": c.modelProviderForDefinition(definition),
		"model_name":     c.modelNameForDefinition(definition),
	}
	if strategy.MaxOutputTokens > 0 {
		payload["max_output_tokens"] = strategy.MaxOutputTokens
	}
	if strategy.Temperature != nil {
		payload["temperature"] = *strategy.Temperature
	}
	if strings.TrimSpace(strategy.Thinking) != "" {
		payload["thinking"] = strings.TrimSpace(strategy.Thinking)
	}
	if strings.TrimSpace(strategy.ReasoningEffort) != "" {
		payload["reasoning_effort"] = strings.TrimSpace(strategy.ReasoningEffort)
	}
	if strategy.TimeoutMS > 0 {
		payload["timeout_ms"] = strategy.TimeoutMS
	}
	payload["streaming"] = modelStreamingEnabled(strategy)
	c.recordTrace(ctx, envelope.TraceID, envelope.Context.TenantID, runID, taskID, contracts.TraceModelStrategySelected, payload)
}

func (c Coordinator) recordRuntimeStrategyApplied(ctx context.Context, envelope contracts.AgentEnvelope, runID contracts.AgentRunID, taskID contracts.TaskID, strategyHash string, strategy contracts.RuntimeStrategy, runtime contracts.RuntimeLimits) {
	payload := map[string]any{
		"strategy_hash":                      strategyHash,
		"execution_mode":                     strategy.ExecutionMode,
		"effective_max_steps":                runtime.MaxSteps,
		"effective_max_tool_calls":           runtime.MaxToolCalls,
		"effective_max_model_retries":        runtime.MaxModelRetries,
		"effective_max_consecutive_failures": runtime.MaxConsecutiveToolFailures,
	}
	if strategy.MaxSteps != nil {
		payload["max_steps"] = *strategy.MaxSteps
	}
	if strategy.MaxDurationSeconds != nil {
		payload["max_duration_seconds"] = *strategy.MaxDurationSeconds
	}
	if runtime.MaxDuration > 0 {
		payload["effective_max_duration_seconds"] = int(runtime.MaxDuration.Seconds())
	}
	if strategy.MaxModelRetries != nil {
		payload["max_model_retries"] = *strategy.MaxModelRetries
	}
	if strategy.MaxConsecutiveToolFailures != nil {
		payload["max_consecutive_tool_failures"] = *strategy.MaxConsecutiveToolFailures
	}
	c.recordTrace(ctx, envelope.TraceID, envelope.Context.TenantID, runID, taskID, contracts.TraceRuntimeStrategyApplied, payload)
}

func (c Coordinator) recordRepairStrategyApplied(ctx context.Context, envelope contracts.AgentEnvelope, runID contracts.AgentRunID, taskID contracts.TaskID, strategyHash string, strategy contracts.RepairStrategy, runtime contracts.RuntimeLimits) {
	payload := map[string]any{
		"strategy_hash":                 strategyHash,
		"effective_max_repair_attempts": runtime.MaxRepairAttempts,
		"failure_mode":                  strategy.FailureMode,
		"repairable_error_codes":        strategy.RepairableErrorCodes,
	}
	if strategy.Enabled != nil {
		payload["enabled"] = *strategy.Enabled
	}
	if strategy.MaxRepairAttempts != nil {
		payload["max_repair_attempts"] = *strategy.MaxRepairAttempts
	}
	if strategy.RequestModelRepairOnFail != nil {
		payload["request_model_repair_on_fail"] = *strategy.RequestModelRepairOnFail
	}
	if strategy.StopOnDenied != nil {
		payload["stop_on_denied"] = *strategy.StopOnDenied
	}
	c.recordTrace(ctx, envelope.TraceID, envelope.Context.TenantID, runID, taskID, contracts.TraceRepairStrategyApplied, payload)
}

func (c Coordinator) recordToolStrategyApplied(ctx context.Context, envelope contracts.AgentEnvelope, runID contracts.AgentRunID, taskID contracts.TaskID, strategyHash string, toolStrategy contracts.ToolUseStrategy, knowledgeStrategy contracts.KnowledgeUseStrategy, before tooldiscovery.CandidateSet, after tooldiscovery.CandidateSet) {
	payload := map[string]any{
		"strategy_hash":              strategyHash,
		"tool_choice_mode":           toolStrategy.ToolChoiceMode,
		"candidate_tool_count":       len(before.Tools),
		"selected_tool_count":        len(after.Tools),
		"candidate_capability_count": len(before.Capabilities),
		"selected_capability_count":  len(after.Capabilities),
		"allowed_tool_count":         len(toolStrategy.AllowedToolIDs),
		"denied_tool_count":          len(toolStrategy.DeniedToolIDs),
		"preferred_tool_count":       len(toolStrategy.PreferredToolIDs),
		"selected_tool_ids":          toolCardIDs(after.Tools),
	}
	if toolStrategy.MaxToolCalls != nil {
		payload["max_tool_calls"] = *toolStrategy.MaxToolCalls
	}
	if toolStrategy.RequireApprovalAtRiskLevel != "" {
		payload["require_approval_at_risk_level"] = toolStrategy.RequireApprovalAtRiskLevel
	}
	if knowledgeStrategy.Enabled != nil {
		payload["knowledge_enabled"] = *knowledgeStrategy.Enabled
	}
	if strings.TrimSpace(knowledgeStrategy.InjectMode) != "" {
		payload["knowledge_inject_mode"] = strings.TrimSpace(knowledgeStrategy.InjectMode)
	}
	if len(knowledgeStrategy.KnowledgeBaseIDs) > 0 {
		payload["knowledge_base_count"] = len(knowledgeStrategy.KnowledgeBaseIDs)
	}
	if strings.TrimSpace(knowledgeStrategy.SearchMode) != "" {
		payload["knowledge_search_mode"] = strings.TrimSpace(knowledgeStrategy.SearchMode)
	}
	if knowledgeStrategy.MaxResults > 0 {
		payload["knowledge_max_results"] = knowledgeStrategy.MaxResults
	}
	payload["knowledge_allow_cross_group"] = knowledgeStrategy.AllowCrossGroup
	c.recordTrace(ctx, envelope.TraceID, envelope.Context.TenantID, runID, taskID, contracts.TraceToolStrategyApplied, payload)
}

func (c Coordinator) recordCollaborationStrategyApplied(ctx context.Context, envelope contracts.AgentEnvelope, runID contracts.AgentRunID, taskID contracts.TaskID, strategyHash string, strategy contracts.CollaborationStrategy, definition contracts.AgentDefinition, candidates tooldiscovery.CandidateSet) {
	payload := map[string]any{
		"strategy_hash":        strategyHash,
		"delegation_mode":      strategy.DelegationMode,
		"collaborator_count":   len(candidates.Collaborators),
		"collaborator_ids":     retrievedCollaboratorIDs(candidates.Collaborators),
		"delegate_tool_denied": stringAllowed(definition.Tools.DeniedToolIDs, "origin.agent.delegate"),
	}
	if strategy.MaxHandoffDepth != nil {
		payload["max_handoff_depth"] = *strategy.MaxHandoffDepth
	}
	if strategy.MaxChildTasks != nil {
		payload["max_child_tasks"] = *strategy.MaxChildTasks
	}
	if strategy.MaxContextTokens != nil {
		payload["max_context_tokens"] = *strategy.MaxContextTokens
	}
	if strategy.DefaultHandoffMode != "" {
		payload["default_handoff_mode"] = strategy.DefaultHandoffMode
	}
	c.recordTrace(ctx, envelope.TraceID, envelope.Context.TenantID, runID, taskID, contracts.TraceCollaborationStrategyApplied, payload)
}

func (c Coordinator) recordMemoryStrategyApplied(ctx context.Context, envelope contracts.AgentEnvelope, runID contracts.AgentRunID, taskID contracts.TaskID, strategyHash string, strategy contracts.MemoryUseStrategy, summaries []contracts.MemorySummary) {
	payload := map[string]any{
		"strategy_hash":           strategyHash,
		"read_enabled":            memoryStrategyEnabled(strategy.ReadEnabled),
		"write_enabled":           memoryStrategyEnabled(strategy.WriteEnabled),
		"auto_write_mode":         strategy.AutoWriteMode,
		"selected_memory_count":   len(summaries),
		"read_scope_count":        len(strategy.ReadScopes),
		"write_scope_count":       len(strategy.WriteScopes),
		"write_prompt_profile_id": strategy.WritePromptProfileID,
	}
	if strategy.MaxMemoryItems != nil {
		payload["max_memory_items"] = *strategy.MaxMemoryItems
	}
	c.recordTrace(ctx, envelope.TraceID, envelope.Context.TenantID, runID, taskID, contracts.TraceMemoryStrategyApplied, payload)
}

func (c Coordinator) repairPrompt(ctx context.Context, envelope contracts.AgentEnvelope, runID contracts.AgentRunID, taskID contracts.TaskID, stepID string, bundle contracts.PromptBundle, repairAttempt int, cause error) contracts.PromptBundle {
	bundle.Constraints = append(bundle.Constraints, fmt.Sprintf("repair attempt %d: previous decision was invalid (%s); return one valid Decision JSON object that matches the contract", repairAttempt, cause.Error()))
	_ = promptbuilder.RefreshHash(&bundle)
	_ = c.Trace.Record(ctx, contracts.TraceEvent{
		TraceID:   envelope.TraceID,
		TenantID:  envelope.Context.TenantID,
		SpanID:    contracts.SpanID(stepID),
		RunID:     runID,
		TaskID:    taskID,
		Type:      contracts.TraceDecisionRepairRequested,
		Payload:   map[string]any{"repair_attempt": repairAttempt, "reason": cause.Error(), "prompt_bundle_hash": bundle.Hash},
		CreatedAt: c.Now(),
	})
	return bundle
}

func (c Coordinator) observeHook(ctx context.Context, event runtimehook.Event, envelope contracts.AgentEnvelope, agent contracts.AgentDefinition, runID contracts.AgentRunID, taskID contracts.TaskID, payload map[string]any) {
	if c.RuntimeHooks == nil {
		return
	}
	c.RuntimeHooks.Observe(ctx, runtimehook.Observation{
		Event:    event,
		TenantID: envelope.Context.TenantID,
		TraceID:  envelope.TraceID,
		RunID:    runID,
		TaskID:   taskID,
		Agent:    agent,
		Payload:  payload,
	})
}

func (c Coordinator) applyRuntimeHook(ctx context.Context, point runtimehook.HookPoint, request runtimehook.TransformRequest) runtimehook.Patch {
	if c.RuntimeHooks == nil {
		return runtimehook.Patch{}
	}
	request.HookPoint = point
	return c.RuntimeHooks.Apply(ctx, request)
}

func (c Coordinator) applyCandidateHook(ctx context.Context, envelope contracts.AgentEnvelope, agent contracts.AgentDefinition, policy contracts.PolicySet, runID contracts.AgentRunID, taskID contracts.TaskID, objective string, candidates tooldiscovery.CandidateSet) tooldiscovery.CandidateSet {
	patch := c.applyRuntimeHook(ctx, runtimehook.AfterCandidateRetrieval, runtimehook.TransformRequest{
		TenantID:   envelope.Context.TenantID,
		TraceID:    envelope.TraceID,
		RunID:      runID,
		TaskID:     taskID,
		Agent:      agent,
		Policy:     policy,
		Objective:  objective,
		Candidates: candidates,
	})
	return applyCandidatePatch(candidates, patch)
}

func (c Coordinator) applyPromptHook(ctx context.Context, envelope contracts.AgentEnvelope, agent contracts.AgentDefinition, policy contracts.PolicySet, runID contracts.AgentRunID, taskID contracts.TaskID, objective string, candidates tooldiscovery.CandidateSet, view contracts.WorkView, strategy contracts.ContextStrategy, bundle contracts.PromptBundle) (contracts.PromptBundle, error) {
	patch := c.applyRuntimeHook(ctx, runtimehook.BeforeModelCall, runtimehook.TransformRequest{
		TenantID:     envelope.Context.TenantID,
		TraceID:      envelope.TraceID,
		RunID:        runID,
		TaskID:       taskID,
		Agent:        agent,
		Policy:       policy,
		Objective:    objective,
		Candidates:   candidates,
		WorkView:     view,
		PromptBundle: bundle,
	})
	var sourceReports []contracts.ContextSourceReport
	patch.AddContextBlocks, sourceReports = contextcollector.FilterRuntimeHookContextBlocks(patch.AddContextBlocks, strategy)
	if bundle.ContextAssemblyReport != nil {
		for _, sourceReport := range sourceReports {
			contextcollector.MergeContextSourceReportRow(bundle.ContextAssemblyReport, sourceReport)
		}
	}
	return promptbuilder.ApplyRuntimeHookPatch(bundle, patch)
}

func (c Coordinator) applyMemoryWriteHook(ctx context.Context, envelope contracts.AgentEnvelope, agent contracts.AgentDefinition, policy contracts.PolicySet, strategy contracts.MemoryUseStrategy, runID contracts.AgentRunID, taskID contracts.TaskID, objective string, candidates tooldiscovery.CandidateSet, view contracts.WorkView, bundle contracts.PromptBundle, decision contracts.Decision) error {
	if c.Memory == nil || !memoryStrategyEnabled(strategy.WriteEnabled) || strategy.AutoWriteMode == "disabled" {
		return nil
	}
	patch := c.applyRuntimeHook(ctx, runtimehook.BeforeMemoryWrite, runtimehook.TransformRequest{
		TenantID:     envelope.Context.TenantID,
		TraceID:      envelope.TraceID,
		RunID:        runID,
		TaskID:       taskID,
		Agent:        agent,
		Policy:       policy,
		Objective:    objective,
		Candidates:   candidates,
		WorkView:     view,
		PromptBundle: bundle,
	})
	if len(patch.MemoryWriteIntents) == 0 {
		return nil
	}
	for _, intent := range patch.MemoryWriteIntents {
		if strings.TrimSpace(intent.Content) == "" {
			continue
		}
		scope := memoryIntentScope(intent)
		if len(strategy.WriteScopes) > 0 && !stringAllowed(strategy.WriteScopes, scope) {
			continue
		}
		event := contracts.MemoryEvent{
			TenantID:      envelope.Context.TenantID,
			AgentID:       agent.AgentID,
			UserID:        envelope.Context.UserID,
			Scope:         scope,
			Content:       intent.Content,
			Summary:       memoryIntentSummary(intent),
			SourceEventID: string(decision.DecisionID),
			Visibility:    memoryIntentString(intent.Metadata, "visibility", "private"),
			Confidence:    memoryIntentFloat(intent.Metadata, "confidence", 0.5),
			CreatedAt:     c.Now(),
		}
		if source := memoryIntentString(intent.Metadata, "source_event_id", ""); source != "" {
			event.SourceEventID = source
		}
		if _, err := c.Memory.WriteMemoryWithPolicy(ctx, event, policy.MemoryPolicy, string(agent.AgentID), "agent", envelope.TraceID); err != nil {
			var runtimeErr *contracts.RuntimeError
			if errors.As(err, &runtimeErr) && runtimeErr.Code == contracts.CodeToolPolicyDenied {
				continue
			}
			return err
		}
	}
	return nil
}

func (c Coordinator) dispatch(ctx context.Context, envelope contracts.AgentEnvelope, definition contracts.AgentDefinition, policySet contracts.PolicySet, runID contracts.AgentRunID, taskID contracts.TaskID, stepID string, decision contracts.Decision, candidates tooldiscovery.CandidateSet) (RunResult, bool, error) {
	switch decision.Type {
	case contracts.DecisionTypeReply:
		if _, _, _, err := c.Tasks.ApplyCommand(ctx, taskruntime.CommandInput{
			TaskID:    taskID,
			Command:   contracts.CmdComplete,
			ActorID:   string(definition.AgentID),
			ActorType: "agent",
			RunID:     runID,
			StepID:    stepID,
		}); err != nil {
			return RunResult{}, true, err
		}
		run, err := c.Runs.MarkCompleted(ctx, runID)
		if err != nil {
			return RunResult{}, true, err
		}
		c.recordResponse(ctx, envelope, runID, taskID, stepID, "reply", run.Status)
		c.syncReply(ctx, envelope, runID, taskID, decision.Reply)
		c.observeHook(ctx, runtimehook.OnRunFinished, envelope, definition, runID, taskID, map[string]any{"status": run.Status, "decision_type": decision.Type})
		return RunResult{RunID: runID, TaskID: taskID, Status: run.Status, Reply: decision.Reply}, true, nil
	case contracts.DecisionTypeAskClarification:
		if _, _, _, err := c.Tasks.ApplyCommand(ctx, taskruntime.CommandInput{
			TaskID:    taskID,
			Command:   contracts.CmdAskClarification,
			ActorID:   string(definition.AgentID),
			ActorType: "agent",
			RunID:     runID,
			StepID:    stepID,
			Payload:   map[string]any{"question": decision.Ask.Question},
		}); err != nil {
			return RunResult{}, true, err
		}
		run, err := c.Runs.MarkWaitingInput(ctx, runID)
		if err != nil {
			return RunResult{}, true, err
		}
		c.recordResponse(ctx, envelope, runID, taskID, stepID, "ask_clarification", run.Status)
		c.syncWaitingInput(ctx, envelope, runID, taskID, decision.Ask.Question)
		c.observeHook(ctx, runtimehook.OnRunFinished, envelope, definition, runID, taskID, map[string]any{"status": run.Status, "decision_type": decision.Type})
		return RunResult{RunID: runID, TaskID: taskID, Status: run.Status, Ask: decision.Ask}, true, nil
	case contracts.DecisionTypeNoOp:
		if _, _, _, err := c.Tasks.ApplyCommand(ctx, taskruntime.CommandInput{
			TaskID:    taskID,
			Command:   contracts.CmdComplete,
			ActorID:   string(definition.AgentID),
			ActorType: "agent",
			RunID:     runID,
			StepID:    stepID,
			Payload:   map[string]any{"decision_id": decision.DecisionID, "decision_type": decision.Type},
		}); err != nil {
			return RunResult{}, true, err
		}
		run, err := c.Runs.MarkCompleted(ctx, runID)
		if err != nil {
			return RunResult{}, true, err
		}
		reply := &contracts.DecisionReply{Kind: contracts.ReplyStatusUpdate, Text: "no operation required"}
		c.recordResponse(ctx, envelope, runID, taskID, stepID, "no_op", run.Status)
		c.observeHook(ctx, runtimehook.OnRunFinished, envelope, definition, runID, taskID, map[string]any{"status": run.Status, "decision_type": decision.Type})
		return RunResult{RunID: runID, TaskID: taskID, Status: run.Status, Reply: reply}, true, nil
	case contracts.DecisionTypeUnsupported:
		if _, _, _, err := c.Tasks.ApplyCommand(ctx, taskruntime.CommandInput{
			TaskID:    taskID,
			Command:   contracts.CmdComplete,
			ActorID:   string(definition.AgentID),
			ActorType: "agent",
			RunID:     runID,
			StepID:    stepID,
			Payload:   map[string]any{"decision_id": decision.DecisionID, "decision_type": decision.Type},
		}); err != nil {
			return RunResult{}, true, err
		}
		run, err := c.Runs.MarkCompleted(ctx, runID)
		if err != nil {
			return RunResult{}, true, err
		}
		reply := &contracts.DecisionReply{Kind: contracts.ReplyRefusal, Text: decision.Reason}
		c.recordResponse(ctx, envelope, runID, taskID, stepID, "unsupported", run.Status)
		c.observeHook(ctx, runtimehook.OnRunFinished, envelope, definition, runID, taskID, map[string]any{"status": run.Status, "decision_type": decision.Type})
		return RunResult{RunID: runID, TaskID: taskID, Status: run.Status, Reply: reply}, true, nil
	case contracts.DecisionTypeError:
		runtimeErr := contracts.NewRuntimeError(contracts.CodeDecisionSchemaError, "decision error", map[string]any{"message": decision.Error.Message})
		_, _, _, _ = c.Tasks.ApplyCommand(ctx, taskruntime.CommandInput{
			TaskID:    taskID,
			Command:   contracts.CmdFail,
			ActorID:   string(definition.AgentID),
			ActorType: "agent",
			RunID:     runID,
			StepID:    stepID,
			Payload:   runtimeErr.ToTracePayload(),
		})
		run, err := c.Runs.MarkFailed(ctx, runID, runtimeErr)
		if err != nil {
			return RunResult{}, true, err
		}
		c.recordResponse(ctx, envelope, runID, taskID, stepID, "error", run.Status)
		c.syncRunFailed(ctx, envelope, runID, taskID, runtimeErr.Message)
		c.observeHook(ctx, runtimehook.OnRunFinished, envelope, definition, runID, taskID, map[string]any{"status": run.Status, "decision_type": decision.Type, "error": runtimeErr.ToTracePayload()})
		return RunResult{RunID: runID, TaskID: taskID, Status: run.Status, Error: runtimeErr}, true, runtimeErr
	case contracts.DecisionTypeToolCall:
		return c.dispatchToolCalls(ctx, envelope, definition, policySet, runID, taskID, stepID, decision.ToolCalls, candidates)
	default:
		run, err := c.Runs.MarkCompleted(ctx, runID)
		if err != nil {
			return RunResult{}, true, err
		}
		c.observeHook(ctx, runtimehook.OnRunFinished, envelope, definition, runID, taskID, map[string]any{"status": run.Status, "decision_type": decision.Type})
		return RunResult{RunID: runID, TaskID: taskID, Status: run.Status}, true, nil
	}
}

func (c Coordinator) dispatchToolCalls(ctx context.Context, envelope contracts.AgentEnvelope, definition contracts.AgentDefinition, policySet contracts.PolicySet, runID contracts.AgentRunID, taskID contracts.TaskID, stepID string, calls []contracts.ToolCall, candidates tooldiscovery.CandidateSet) (RunResult, bool, error) {
	if c.ToolRuntime == nil || c.ToolRepo == nil {
		runtimeErr := contracts.NewRuntimeError(contracts.CodeToolNotFound, "tool runtime is not configured", nil)
		run, err := c.Runs.MarkFailed(ctx, runID, runtimeErr)
		if err != nil {
			return RunResult{}, true, err
		}
		c.syncRunFailed(ctx, envelope, runID, taskID, runtimeErr.Message)
		c.observeHook(ctx, runtimehook.OnRunFinished, envelope, definition, runID, taskID, map[string]any{"status": run.Status, "error": runtimeErr.ToTracePayload()})
		return RunResult{RunID: runID, TaskID: taskID, Status: run.Status, Error: runtimeErr}, true, runtimeErr
	}
	for _, call := range calls {
		if call.ToolCallID == "" {
			call.ToolCallID = contracts.ToolCallID(idgen.New("toolcall"))
		}
		if call.ToolID == "" {
			call.ToolID = call.Name
		}
		if call.Arguments == nil {
			call.Arguments = map[string]any{}
		}
		if _, ok := call.Arguments["trace_id"]; !ok {
			call.Arguments["trace_id"] = string(envelope.TraceID)
		}
		call = applyKnowledgeUseStrategyToToolCall(call, definition.Strategies.Knowledge)
		if call.ToolID == "origin.agent.delegate" {
			if _, ok := call.Arguments["parent_task_id"]; !ok {
				call.Arguments["parent_task_id"] = string(taskID)
			}
			call.Arguments["_retrieved_collaborators"] = retrievedCollaboratorIDs(candidates.Collaborators)
		}
		if _, disabled := c.DisabledToolIDs[call.ToolID]; disabled {
			runtimeErr := contracts.NewRuntimeError(contracts.CodeToolPolicyDenied, "tool is disabled by release switch", map[string]any{"tool_id": call.ToolID})
			failed, failErr := c.Runs.MarkFailed(ctx, runID, runtimeErr)
			if failErr != nil {
				return RunResult{}, true, failErr
			}
			c.syncRunFailed(ctx, envelope, runID, taskID, runtimeErr.Message)
			c.observeHook(ctx, runtimehook.OnRunFinished, envelope, definition, runID, taskID, map[string]any{"status": failed.Status, "error": runtimeErr.ToTracePayload()})
			return RunResult{RunID: runID, TaskID: taskID, Status: failed.Status, Error: runtimeErr}, true, runtimeErr
		}
		currentPlanStep, ok, err := c.currentPlanStep(ctx, taskID)
		if err != nil {
			return RunResult{}, true, err
		}
		if ok && currentPlanStep.Status == contracts.PlanStepPending {
			starter, supportsStart := c.Plans.(interface {
				StartStep(context.Context, contracts.TaskID, string, string, string) (contracts.PlanStep, contracts.PlanEvent, error)
			})
			if supportsStart {
				currentPlanStep, _, err = starter.StartStep(ctx, taskID, currentPlanStep.StepID, string(definition.AgentID), "agent")
				if err != nil {
					return RunResult{}, true, err
				}
			}
		}
		call.TenantID = envelope.Context.TenantID
		call.RunID = runID
		call.TaskID = taskID
		call.PlanStepID = ""
		if ok {
			call.PlanStepID = currentPlanStep.StepID
		}
		call = c.snapshotToolCall(envelope.Context.TenantID, call)
		if call.IdempotencyKey == "" {
			key, err := toolCallIdempotencyKey(runID, stepID, call)
			if err != nil {
				return RunResult{}, true, err
			}
			call.IdempotencyKey = key
		}
		call.CreatedAt = c.Now()
		run, err := c.Runs.IncrementToolCall(ctx, runID)
		if err != nil {
			return RunResult{}, true, err
		}
		if definition.Runtime.MaxToolCalls > 0 && run.ToolCallCount > definition.Runtime.MaxToolCalls {
			runtimeErr := contracts.NewRuntimeError(contracts.CodeToolExecutionFailed, "max tool calls exceeded", nil)
			failed, failErr := c.Runs.MarkFailed(ctx, runID, runtimeErr)
			if failErr != nil {
				return RunResult{}, true, failErr
			}
			c.syncRunFailed(ctx, envelope, runID, taskID, runtimeErr.Message)
			c.observeHook(ctx, runtimehook.OnRunFinished, envelope, definition, runID, taskID, map[string]any{"status": failed.Status, "error": runtimeErr.ToTracePayload()})
			return RunResult{RunID: runID, TaskID: taskID, Status: failed.Status, Error: runtimeErr}, true, runtimeErr
		}
		saved, duplicate, err := c.ToolRepo.SaveCall(ctx, call)
		if err != nil {
			return RunResult{}, true, err
		}
		call = saved
		if duplicate {
			existing, ok, err := c.ToolRepo.GetResultByCall(ctx, call.ToolCallID)
			if err != nil {
				return RunResult{}, true, err
			}
			if ok {
				if existing.Status == contracts.ToolResultPendingApproval {
					run, err := c.Runs.MarkWaitingApproval(ctx, runID)
					if err != nil {
						return RunResult{}, true, err
					}
					c.syncWaitingApproval(ctx, envelope, runID, taskID, "tool approval is still pending")
					c.observeHook(ctx, runtimehook.OnRunFinished, envelope, definition, runID, taskID, map[string]any{"status": run.Status, "reason": "tool approval is still pending"})
					return RunResult{RunID: runID, TaskID: taskID, Status: run.Status}, true, nil
				}
				continue
			}
		}
		result, err := c.ToolRuntime.Invoke(ctx, toolruntime.InvokeRequest{
			TenantID:  envelope.Context.TenantID,
			TraceID:   envelope.TraceID,
			ActorID:   string(definition.AgentID),
			ActorType: "agent",
			Agent:     definition,
			PolicySet: policySet,
			Call:      call,
		})
		if err != nil {
			return RunResult{}, true, err
		}
		if err := c.ToolRepo.SaveResult(ctx, result); err != nil {
			return RunResult{}, true, err
		}
		c.observeHook(ctx, runtimehook.OnToolResult, envelope, definition, runID, taskID, map[string]any{"tool_id": call.ToolID, "result": result})
		if ok {
			if err := c.recordPlanStepResult(ctx, taskID, currentPlanStep, result, definition); err != nil {
				return RunResult{}, true, err
			}
		}
		if (result.Status == contracts.ToolResultFailed || result.Status == contracts.ToolResultDenied) &&
			!c.shouldContinueAfterToolFailure(ctx, definition, policySet, runID, call, result) {
			runtimeErr := toolFailureRuntimeError(result)
			failed, failErr := c.Runs.MarkFailed(ctx, runID, runtimeErr)
			if failErr != nil {
				return RunResult{}, true, failErr
			}
			_, _, _, _ = c.Tasks.ApplyCommand(ctx, taskruntime.CommandInput{
				TaskID:    taskID,
				Command:   contracts.CmdFail,
				ActorID:   string(definition.AgentID),
				ActorType: "agent",
				RunID:     runID,
				StepID:    stepID,
				Payload:   runtimeErr.ToTracePayload(),
			})
			c.syncRunFailed(ctx, envelope, runID, taskID, runtimeErr.Message)
			c.observeHook(ctx, runtimehook.OnRunFinished, envelope, definition, runID, taskID, map[string]any{"status": failed.Status, "error": runtimeErr.ToTracePayload()})
			return RunResult{RunID: runID, TaskID: taskID, Status: failed.Status, Error: runtimeErr}, true, runtimeErr
		}
		if result.Status == contracts.ToolResultPendingApproval {
			if _, _, _, err := c.Tasks.ApplyCommand(ctx, taskruntime.CommandInput{
				TaskID:    taskID,
				Command:   contracts.CmdApprovalRequired,
				ActorID:   string(definition.AgentID),
				ActorType: "agent",
				RunID:     runID,
				StepID:    stepID,
			}); err != nil {
				return RunResult{}, true, err
			}
			run, err := c.Runs.MarkWaitingApproval(ctx, runID)
			if err != nil {
				return RunResult{}, true, err
			}
			c.syncWaitingApproval(ctx, envelope, runID, taskID, "tool approval required")
			c.observeHook(ctx, runtimehook.OnRunFinished, envelope, definition, runID, taskID, map[string]any{"status": run.Status, "reason": "tool approval required"})
			return RunResult{RunID: runID, TaskID: taskID, Status: run.Status}, true, nil
		}
		if _, _, _, err := c.Tasks.ApplyCommand(ctx, taskruntime.CommandInput{
			TaskID:    taskID,
			Command:   contracts.CmdToolWaiting,
			ActorID:   string(definition.AgentID),
			ActorType: "agent",
			RunID:     runID,
			StepID:    stepID,
		}); err != nil {
			return RunResult{}, true, err
		}
		if _, _, _, err := c.Tasks.ApplyCommand(ctx, taskruntime.CommandInput{
			TaskID:    taskID,
			Command:   contracts.CmdToolCompleted,
			ActorID:   string(definition.AgentID),
			ActorType: "agent",
			RunID:     runID,
			StepID:    stepID,
			Payload:   map[string]any{"tool_result_id": result.ToolResultID, "status": result.Status},
		}); err != nil {
			return RunResult{}, true, err
		}
		for _, ref := range result.ArtifactRefs {
			c.syncArtifact(ctx, envelope, runID, taskID, ref)
		}
	}
	return RunResult{RunID: runID, TaskID: taskID, Status: contracts.RunRunning}, false, nil
}

func (c Coordinator) shouldContinueAfterToolFailure(ctx context.Context, definition contracts.AgentDefinition, policySet contracts.PolicySet, runID contracts.AgentRunID, call contracts.ToolCall, result contracts.ToolResult) bool {
	failureScanLimit := consecutiveToolFailureScanLimit(definition.Runtime.MaxConsecutiveToolFailures, policySet.ToolRepairPolicy.MaxRepairAttempts)
	failureSeen := c.consecutiveToolFailures(ctx, runID, failureScanLimit)
	if definition.Runtime.MaxConsecutiveToolFailures > 0 && failureSeen > definition.Runtime.MaxConsecutiveToolFailures {
		return false
	}
	toolDef := contracts.ToolDefinition{ToolID: call.ToolID}
	if runtimeWithDefinition, ok := c.ToolRuntime.(interface {
		Definition(contracts.TenantID, string) (contracts.ToolDefinition, bool)
	}); ok {
		if found, foundOK := runtimeWithDefinition.Definition(policySet.TenantID, call.ToolID); foundOK {
			toolDef = found
		}
	}
	decision := policyengine.EvaluateRepair(policySet.ToolRepairPolicy, policyengine.RepairRequest{
		Policy:      policySet,
		Tool:        toolDef,
		Result:      result,
		FailureSeen: failureSeen,
	})
	return decision.Action == string(policyengine.RepairActionContinue)
}

func (c Coordinator) maybeUpgradeByPolicy(ctx context.Context, traceID contracts.TraceID, tenantID contracts.TenantID, definition contracts.AgentDefinition, task contracts.Task, policySet contracts.PolicySet) (contracts.AgentDefinition, bool, error) {
	policy := policySet.TaskUpgradePolicy
	if !policy.Enabled || policy.TargetVersion == "" || policy.TargetVersion == definition.Version {
		return definition, false, nil
	}
	if policy.MinTaskAgeSeconds > 0 && c.Now().Sub(task.CreatedAt) < time.Duration(policy.MinTaskAgeSeconds)*time.Second {
		return definition, false, nil
	}
	upgraded, err := c.Agents.Load(ctx, tenantID, definition.AgentID, policy.TargetVersion)
	if err != nil {
		return definition, false, err
	}
	if upgraded.TenantID == "" {
		upgraded.TenantID = tenantID
	}
	c.recordTrace(ctx, traceID, tenantID, "", task.TaskID, contracts.TraceAgentLoaded, map[string]any{
		"agent_id":          upgraded.AgentID,
		"agent_version":     upgraded.Version,
		"previous_version":  definition.Version,
		"policy_set_id":     upgraded.PolicyRefs.PolicySetID,
		"upgrade_policy":    true,
		"min_task_age_secs": policy.MinTaskAgeSeconds,
	})
	return upgraded, true, nil
}

func toolFailureRuntimeError(result contracts.ToolResult) *contracts.RuntimeError {
	if result.Error != nil {
		return contracts.NewRuntimeError(result.Error.Code, result.Error.Message, result.Error.Details)
	}
	return contracts.NewRuntimeError(contracts.CodeToolExecutionFailed, string(result.Status), nil)
}

func (c Coordinator) completeModel(ctx context.Context, envelope contracts.AgentEnvelope, definition contracts.AgentDefinition, runID contracts.AgentRunID, taskID contracts.TaskID, stepID string, bundle contracts.PromptBundle) (modelclient.ModelResponse, error) {
	attempts := definition.Runtime.MaxModelRetries + 1
	if attempts < 1 {
		attempts = 1
	}
	var lastErr error
	for attempt := 0; attempt < attempts; attempt++ {
		resp, err := c.invokeModel(ctx, envelope, definition, runID, taskID, stepID, bundle)
		if err == nil {
			return resp, nil
		}
		lastErr = err
		if !isRetryableModelError(err) {
			break
		}
	}
	return modelclient.ModelResponse{}, lastErr
}

func (c Coordinator) invokeModel(ctx context.Context, envelope contracts.AgentEnvelope, definition contracts.AgentDefinition, runID contracts.AgentRunID, taskID contracts.TaskID, stepID string, bundle contracts.PromptBundle) (modelclient.ModelResponse, error) {
	if modelStreamingEnabled(definition.Strategies.Model) {
		return c.streamModel(ctx, envelope, definition, runID, taskID, stepID, bundle)
	}
	return c.callModel(ctx, definition, runID, bundle)
}

func (c Coordinator) callModel(ctx context.Context, definition contracts.AgentDefinition, runID contracts.AgentRunID, bundle contracts.PromptBundle) (modelclient.ModelResponse, error) {
	if err := c.validateModelProviderForDefinition(definition); err != nil {
		return modelclient.ModelResponse{}, err
	}
	return c.Model.Complete(ctx, c.modelRequestForDefinition(definition, runID, bundle))
}

func (c Coordinator) streamModel(ctx context.Context, envelope contracts.AgentEnvelope, definition contracts.AgentDefinition, runID contracts.AgentRunID, taskID contracts.TaskID, stepID string, bundle contracts.PromptBundle) (modelclient.ModelResponse, error) {
	if err := c.validateModelProviderForDefinition(definition); err != nil {
		return modelclient.ModelResponse{}, err
	}
	events, err := c.Model.Stream(ctx, c.modelRequestForDefinition(definition, runID, bundle))
	if err != nil {
		return modelclient.ModelResponse{}, err
	}
	var raw strings.Builder
	var provider string
	var modelName string
	var usage modelclient.ModelUsage
	for event := range events {
		switch event.Type {
		case modelclient.ModelStreamDelta:
			if event.Delta != "" {
				raw.WriteString(event.Delta)
			}
			if event.ModelProvider != "" {
				provider = event.ModelProvider
			}
			if event.ModelName != "" {
				modelName = event.ModelName
			}
			c.recordModelDelta(ctx, envelope, runID, taskID, stepID, event)
		case modelclient.ModelStreamCompleted:
			if len(event.RawDecision) > 0 {
				raw.Reset()
				raw.Write(event.RawDecision)
			}
			if event.ModelProvider != "" {
				provider = event.ModelProvider
			}
			if event.ModelName != "" {
				modelName = event.ModelName
			}
			usage = event.Usage
		case modelclient.ModelStreamError:
			if event.Err != nil {
				return modelclient.ModelResponse{}, event.Err
			}
			return modelclient.ModelResponse{}, contracts.NewRuntimeError(contracts.CodeModelError, "model stream failed", nil)
		default:
			return modelclient.ModelResponse{}, contracts.NewRuntimeError(contracts.CodeModelError, fmt.Sprintf("unknown model stream event %q", event.Type), nil)
		}
	}
	if raw.Len() == 0 {
		return modelclient.ModelResponse{}, contracts.NewRuntimeError(contracts.CodeModelError, "model stream completed without a decision", nil)
	}
	return modelclient.ModelResponse{
		RawDecisionJSON: []byte(raw.String()),
		ModelProvider:   provider,
		ModelName:       modelName,
		Usage:           usage,
	}, nil
}

func isRetryableModelError(err error) bool {
	var runtimeErr *contracts.RuntimeError
	if errors.As(err, &runtimeErr) {
		return runtimeErr.Retryable
	}
	return true
}

func (c Coordinator) policySet(ctx context.Context, tenantID contracts.TenantID, policySetID contracts.PolicySetID) contracts.PolicySet {
	if c.Policies == nil {
		return policyengine.FallbackPolicySet(tenantID, policySetID)
	}
	policy, ok, err := c.Policies.Get(ctx, tenantID, policySetID)
	if err != nil || !ok {
		return policyengine.FallbackPolicySet(tenantID, policySetID)
	}
	return policy
}

func (c Coordinator) policySetForRun(ctx context.Context, run contracts.AgentRun, tenantID contracts.TenantID, policySetID contracts.PolicySetID) contracts.PolicySet {
	if c.Policies != nil && run.VersionSnapshot.PolicyVersionID != "" {
		version, policy, ok, err := c.Policies.GetVersion(ctx, run.VersionSnapshot.PolicyVersionID)
		if err == nil && ok && version.TenantID == tenantID && version.PolicySetID == policySetID {
			return policy
		}
	}
	return c.policySet(ctx, tenantID, policySetID)
}

func (c Coordinator) versionSnapshot(ctx context.Context, tenantID contracts.TenantID, definition contracts.AgentDefinition, objective string) contracts.VersionSnapshot {
	policySetID := definition.PolicyRefs.PolicySetID
	policy := c.policySet(ctx, tenantID, policySetID)
	policyVersionID := c.currentPolicyVersionID(ctx, tenantID, policySetID, policy.Version)
	if definition.TenantID == "" {
		definition.TenantID = tenantID
	}
	candidates := tooldiscovery.CandidateSet{}
	if c.Tools != nil {
		if found, err := c.Tools.Candidates(ctx, definition, policy, objective); err == nil {
			candidates = found
		}
	}
	skills := map[string]string{}
	for _, ref := range definition.Skills {
		if ref.SkillID != "" {
			skills[ref.SkillID] = ref.Version
		}
	}
	for _, skill := range candidates.Skills {
		if skill.SkillID != "" {
			skills[skill.SkillID] = skill.Version
		}
	}
	tools := map[string]string{}
	for _, tool := range candidates.Tools {
		if tool.ToolID != "" {
			tools[tool.ToolID] = tool.Version
		}
	}
	additional := map[string]string{}
	if definition.RuntimeHooks.Mode != "" || len(definition.RuntimeHooks.Hooks) > 0 {
		if hooksHash, err := hash.StableJSON(definition.RuntimeHooks); err == nil {
			additional["runtime_hooks_hash"] = hooksHash
		}
	}
	if capabilities, ok := c.modelCapabilities(); ok {
		if capabilitiesHash, err := hash.StableJSON(capabilities); err == nil {
			additional["model_capabilities_hash"] = capabilitiesHash
		}
	}
	strategyHash := ""
	if _, report, err := agentstrategy.Resolve(definition, policy, c.strategyDefaults()); err == nil {
		strategyHash = report.StrategyHash
		if strategyHash != "" {
			additional["strategy_hash"] = strategyHash
		}
	}
	sourceKind := contracts.NormalizeSourceKind(definition.SourceKind)
	carrierKind := contracts.NormalizeCarrierKind(sourceKind, definition.CarrierKind)
	runtimeContract := contracts.NormalizeRuntimeContract(carrierKind, definition.RuntimeContract)
	return contracts.VersionSnapshot{
		ContractVersion:      contractVersion(definition),
		AgentDefinition:      definition.Version,
		AgentPackage:         definition.PackageVersionID,
		CarrierKind:          carrierKind,
		RuntimeContract:      runtimeContract,
		CarrierVersion:       definition.Version,
		SourceKind:           sourceKind,
		SourceProviderID:     definition.SourceProviderID,
		ManifestVersion:      definition.ManifestVersion,
		ManifestHash:         definition.ManifestHash,
		StrategyHash:         strategyHash,
		PolicySet:            policySetID,
		PolicyVersionID:      policyVersionID,
		PolicySetVersion:     policy.Version,
		SkillDefinitions:     skills,
		ToolDefinitions:      tools,
		ModelProvider:        c.modelProviderForDefinition(definition),
		ModelName:            c.modelNameForDefinition(definition),
		AdditionalAttributes: additional,
	}
}

func (c Coordinator) currentPolicyVersionID(ctx context.Context, tenantID contracts.TenantID, policySetID contracts.PolicySetID, version string) contracts.PolicyVersionID {
	if c.Policies == nil {
		return ""
	}
	versions, err := c.Policies.ListVersions(ctx, tenantID, policySetID)
	if err != nil {
		return ""
	}
	var selected contracts.PolicyVersion
	for _, candidate := range versions {
		if candidate.Status != contracts.ReleaseStable {
			continue
		}
		if version != "" && candidate.Version != version {
			continue
		}
		if selected.PolicyVersionID == "" || candidate.CreatedAt.After(selected.CreatedAt) {
			selected = candidate
		}
	}
	return selected.PolicyVersionID
}

func (c Coordinator) refreshRunSnapshot(ctx context.Context, runID contracts.AgentRunID, definition contracts.AgentDefinition, objective string) (contracts.VersionSnapshot, error) {
	if c.Runs == nil {
		return contracts.VersionSnapshot{}, nil
	}
	run, err := c.Runs.Get(ctx, runID)
	if err != nil {
		return contracts.VersionSnapshot{}, err
	}
	snapshot := c.versionSnapshot(ctx, run.TenantID, definition, objective)
	snapshot.PromptBundleHash = run.VersionSnapshot.PromptBundleHash
	updated, err := c.Runs.UpdateVersionSnapshot(ctx, runID, snapshot)
	if err != nil {
		return contracts.VersionSnapshot{}, err
	}
	return updated.VersionSnapshot, nil
}

func (c Coordinator) pinPromptBundleHash(ctx context.Context, runID contracts.AgentRunID, promptBundleHash string) error {
	if promptBundleHash == "" || c.Runs == nil {
		return nil
	}
	run, err := c.Runs.Get(ctx, runID)
	if err != nil {
		return err
	}
	if run.VersionSnapshot.PromptBundleHash == promptBundleHash {
		return nil
	}
	snapshot := run.VersionSnapshot
	snapshot.PromptBundleHash = promptBundleHash
	_, err = c.Runs.UpdateVersionSnapshot(ctx, runID, snapshot)
	return err
}

func (c Coordinator) snapshotToolCall(tenantID contracts.TenantID, call contracts.ToolCall) contracts.ToolCall {
	runtimeWithDefinition, ok := c.ToolRuntime.(interface {
		Definition(contracts.TenantID, string) (contracts.ToolDefinition, bool)
	})
	if !ok {
		return call
	}
	definition, found := runtimeWithDefinition.Definition(tenantID, call.ToolID)
	if !found {
		return call
	}
	if call.ToolVersion == "" {
		call.ToolVersion = definition.Version
	}
	if call.ExecutionProfile == "" {
		call.ExecutionProfile = definition.ExecutionProfile
	}
	return call
}

func applyKnowledgeUseStrategyToToolCall(call contracts.ToolCall, strategy contracts.KnowledgeUseStrategy) contracts.ToolCall {
	if !isKnowledgeSearchCall(call) {
		return call
	}
	arguments := cloneArgumentMap(call.Arguments)
	if len(strategy.KnowledgeBaseIDs) > 0 {
		values := make([]string, 0, len(strategy.KnowledgeBaseIDs))
		for _, id := range strategy.KnowledgeBaseIDs {
			id = contracts.KnowledgeBaseID(strings.TrimSpace(string(id)))
			if id != "" {
				values = append(values, string(id))
			}
		}
		arguments["knowledge_base_ids"] = values
	}
	if searchMode := strings.TrimSpace(strategy.SearchMode); searchMode != "" {
		arguments["search_mode"] = searchMode
	}
	if strategy.MaxResults > 0 {
		current := intArgumentValue(arguments["limit"])
		if current <= 0 || current > strategy.MaxResults {
			arguments["limit"] = strategy.MaxResults
		}
	}
	arguments["allow_cross_group"] = strategy.AllowCrossGroup
	call.Arguments = arguments
	return call
}

func isKnowledgeSearchCall(call contracts.ToolCall) bool {
	return strings.TrimSpace(call.ToolID) == "origin.knowledge.search" || strings.TrimSpace(call.Name) == "origin.knowledge.search"
}

func cloneArgumentMap(arguments map[string]any) map[string]any {
	out := map[string]any{}
	for key, value := range arguments {
		out[key] = value
	}
	return out
}

func intArgumentValue(value any) int {
	switch typed := value.(type) {
	case int:
		return typed
	case int64:
		return int(typed)
	case float64:
		return int(typed)
	case float32:
		return int(typed)
	default:
		return 0
	}
}

func toolCallIdempotencyKey(runID contracts.AgentRunID, stepID string, call contracts.ToolCall) (string, error) {
	toolID := call.ToolID
	if toolID == "" {
		toolID = call.Name
	}
	value := map[string]any{
		"run_id":    runID,
		"step_id":   stepID,
		"tool_name": toolID,
		"arguments": call.Arguments,
	}
	sum, err := hash.StableJSON(value)
	if err != nil {
		return "", err
	}
	return "toolcall:" + sum, nil
}

func contractVersion(definition contracts.AgentDefinition) string {
	if definition.ContractVersion != "" {
		return definition.ContractVersion
	}
	return "v1.0-alpha"
}

func (c Coordinator) snapshotModelProvider() string {
	if c.ModelProvider != "" {
		return c.ModelProvider
	}
	if capabilities, ok := c.modelCapabilities(); ok && capabilities.Provider != "" {
		return capabilities.Provider
	}
	return "stub"
}

func (c Coordinator) snapshotModelName() string {
	if c.ModelName != "" {
		return c.ModelName
	}
	if capabilities, ok := c.modelCapabilities(); ok && capabilities.Model != "" {
		return capabilities.Model
	}
	if c.snapshotModelProvider() == "stub" {
		return "stub-decision"
	}
	return ""
}

func (c Coordinator) modelProviderForDefinition(definition contracts.AgentDefinition) string {
	if strings.TrimSpace(definition.Strategies.Model.Provider) != "" {
		return strings.TrimSpace(definition.Strategies.Model.Provider)
	}
	return c.snapshotModelProvider()
}

func (c Coordinator) modelNameForDefinition(definition contracts.AgentDefinition) string {
	if strings.TrimSpace(definition.Strategies.Model.Model) != "" {
		return strings.TrimSpace(definition.Strategies.Model.Model)
	}
	return c.snapshotModelName()
}

func (c Coordinator) modelRequestForDefinition(definition contracts.AgentDefinition, runID contracts.AgentRunID, bundle contracts.PromptBundle) modelclient.ModelRequest {
	model := definition.Strategies.Model
	return modelclient.ModelRequest{
		RunID:           runID,
		PromptBundle:    bundle,
		Timeout:         modelCallTimeout(definition.Runtime.MaxDuration, model.TimeoutMS),
		ModelProvider:   c.modelProviderForDefinition(definition),
		ModelName:       c.modelNameForDefinition(definition),
		MaxOutputTokens: model.MaxOutputTokens,
		Temperature:     model.Temperature,
		Thinking:        model.Thinking,
		ReasoningEffort: model.ReasoningEffort,
	}
}

func (c Coordinator) validateModelProviderForDefinition(definition contracts.AgentDefinition) error {
	requested := strings.TrimSpace(definition.Strategies.Model.Provider)
	if requested == "" {
		return nil
	}
	available := c.snapshotModelProvider()
	if available == "" || requested == available {
		return nil
	}
	return contracts.NewRuntimeError(contracts.CodeModelError, "model provider is not available for this runtime", map[string]any{
		"requested_provider": requested,
		"available_provider": available,
	})
}

func modelCallTimeout(runtimeLimit time.Duration, strategyTimeoutMS int) time.Duration {
	if strategyTimeoutMS <= 0 {
		return runtimeLimit
	}
	strategyLimit := time.Duration(strategyTimeoutMS) * time.Millisecond
	if runtimeLimit <= 0 || strategyLimit < runtimeLimit {
		return strategyLimit
	}
	return runtimeLimit
}

func modelStreamingEnabled(strategy contracts.ModelStrategy) bool {
	if strategy.Streaming == nil {
		return true
	}
	return *strategy.Streaming
}

func (c Coordinator) modelCapabilities() (modelclient.ModelCapabilityDescriptor, bool) {
	provider, ok := c.Model.(modelclient.ModelCapabilityProvider)
	if !ok {
		return modelclient.ModelCapabilityDescriptor{}, false
	}
	capabilities := provider.Capabilities()
	if capabilities.Provider == "" && c.ModelProvider != "" {
		capabilities.Provider = c.ModelProvider
	}
	if capabilities.Model == "" && c.ModelName != "" {
		capabilities.Model = c.ModelName
	}
	return capabilities, true
}

func (c Coordinator) recordResponse(ctx context.Context, envelope contracts.AgentEnvelope, runID contracts.AgentRunID, taskID contracts.TaskID, stepID string, responseType string, status contracts.RunStatus) {
	if c.Trace == nil {
		return
	}
	_ = c.Trace.Record(ctx, contracts.TraceEvent{
		TraceID:  envelope.TraceID,
		TenantID: envelope.Context.TenantID,
		SpanID:   contracts.SpanID(stepID),
		RunID:    runID,
		TaskID:   taskID,
		Type:     contracts.TraceResponseSent,
		Payload: map[string]any{
			"response_type": responseType,
			"run_status":    status,
		},
		CreatedAt: c.Now(),
	})
}

func (c Coordinator) recordTrace(ctx context.Context, traceID contracts.TraceID, tenantID contracts.TenantID, runID contracts.AgentRunID, taskID contracts.TaskID, eventType string, payload map[string]any) {
	if c.Trace == nil || traceID == "" {
		return
	}
	if payload == nil {
		payload = map[string]any{}
	}
	_ = c.Trace.Record(ctx, contracts.TraceEvent{
		TraceID:   traceID,
		TenantID:  tenantID,
		SpanID:    contracts.SpanID(idgen.New("span")),
		RunID:     runID,
		TaskID:    taskID,
		Type:      eventType,
		Payload:   payload,
		CreatedAt: c.Now(),
	})
}

func (c Coordinator) syncReply(ctx context.Context, envelope contracts.AgentEnvelope, runID contracts.AgentRunID, taskID contracts.TaskID, reply *contracts.DecisionReply) {
	if reply == nil {
		return
	}
	tenantID := envelope.Context.TenantID
	if binding, ok := c.externalBinding(ctx, tenantID, taskID); ok {
		c.ExternalSync.ReplyWithContext(ctx, binding, c.externalSyncContext(envelope, runID, taskID), reply.Text)
	}
}

func (c Coordinator) syncWaitingInput(ctx context.Context, envelope contracts.AgentEnvelope, runID contracts.AgentRunID, taskID contracts.TaskID, question string) {
	tenantID := envelope.Context.TenantID
	if binding, ok := c.externalBinding(ctx, tenantID, taskID); ok {
		c.ExternalSync.WaitingInputWithContext(ctx, binding, c.externalSyncContext(envelope, runID, taskID), question)
	}
}

func (c Coordinator) syncWaitingApproval(ctx context.Context, envelope contracts.AgentEnvelope, runID contracts.AgentRunID, taskID contracts.TaskID, reason string) {
	tenantID := envelope.Context.TenantID
	if binding, ok := c.externalBinding(ctx, tenantID, taskID); ok {
		c.ExternalSync.WaitingApprovalWithContext(ctx, binding, c.externalSyncContext(envelope, runID, taskID), reason)
	}
}

func (c Coordinator) syncRunFailed(ctx context.Context, envelope contracts.AgentEnvelope, runID contracts.AgentRunID, taskID contracts.TaskID, reason string) {
	tenantID := envelope.Context.TenantID
	if binding, ok := c.externalBinding(ctx, tenantID, taskID); ok {
		c.ExternalSync.RunFailedWithContext(ctx, binding, c.externalSyncContext(envelope, runID, taskID), reason)
	}
}

func (c Coordinator) syncArtifact(ctx context.Context, envelope contracts.AgentEnvelope, runID contracts.AgentRunID, taskID contracts.TaskID, ref contracts.ArtifactRef) {
	tenantID := envelope.Context.TenantID
	if binding, ok := c.externalBinding(ctx, tenantID, taskID); ok {
		c.ExternalSync.ArtifactCreatedWithContext(ctx, binding, c.externalSyncContext(envelope, runID, taskID), ref)
	}
}

func (c Coordinator) externalSyncContext(envelope contracts.AgentEnvelope, runID contracts.AgentRunID, taskID contracts.TaskID) array.SyncContext {
	return array.SyncContext{
		TenantID:  envelope.Context.TenantID,
		TraceID:   envelope.TraceID,
		RunID:     runID,
		TaskID:    taskID,
		ActorID:   envelope.Caller.CallerID,
		ActorType: envelope.Caller.CallerType,
	}
}

func (c Coordinator) externalBinding(ctx context.Context, tenantID contracts.TenantID, taskID contracts.TaskID) (*contracts.ExternalTaskBinding, bool) {
	if c.ExternalBinding == nil || taskID == "" {
		return nil, false
	}
	return c.ExternalBinding(ctx, tenantID, taskID)
}

func estimatePromptTokens(bundle contracts.PromptBundle) int {
	return contextcompressor.EstimatePromptTokens(bundle)
}

func effectiveAgentStrategies(effective agentstrategy.EffectiveRunConfig) contracts.AgentStrategies {
	return contracts.AgentStrategies{
		Prompt:        effective.Prompt,
		Model:         effective.Model,
		Context:       effective.Context,
		Tools:         effective.Tools,
		Skills:        effective.Skills,
		Collaboration: effective.Collaboration,
		Memory:        effective.Memory,
		Knowledge:     effective.Knowledge,
		Runtime:       effective.Runtime,
		Repair:        effective.Repair,
		Output:        effective.Output,
	}
}

func applyEffectiveStrategiesToRuntimeDefinition(definition contracts.AgentDefinition, effective agentstrategy.EffectiveRunConfig) contracts.AgentDefinition {
	strategies := effectiveAgentStrategies(effective)
	definition.Strategies = strategies
	if strings.TrimSpace(strategies.Prompt.IdentityPrompt) != "" {
		definition.IdentityPrompt = strings.TrimSpace(strategies.Prompt.IdentityPrompt)
	}
	if strings.TrimSpace(strategies.Prompt.SystemPrompt) != "" {
		definition.SystemPrompt = strings.TrimSpace(strategies.Prompt.SystemPrompt)
	}
	if strings.TrimSpace(strategies.Prompt.DeveloperPrompt) != "" {
		definition.DeveloperPrompt = strings.TrimSpace(strategies.Prompt.DeveloperPrompt)
	}
	definition.Tools.AllowedToolIDs = cloneStrings(strategies.Tools.AllowedToolIDs)
	definition.Tools.AllowedToolGroupIDs = cloneStrings(strategies.Tools.AllowedToolGroupIDs)
	definition.Tools.DeniedToolIDs = cloneStrings(strategies.Tools.DeniedToolIDs)
	definition.Tools.DeniedToolGroupIDs = cloneStrings(strategies.Tools.DeniedToolGroupIDs)
	if strategies.Tools.MaxToolCalls != nil {
		definition.Runtime.MaxToolCalls = *strategies.Tools.MaxToolCalls
	}
	if strategies.Runtime.MaxSteps != nil {
		definition.Runtime.MaxSteps = *strategies.Runtime.MaxSteps
	}
	if strategies.Runtime.MaxDurationSeconds != nil {
		definition.Runtime.MaxDuration = time.Duration(*strategies.Runtime.MaxDurationSeconds) * time.Second
	}
	if strategies.Runtime.MaxModelRetries != nil {
		definition.Runtime.MaxModelRetries = *strategies.Runtime.MaxModelRetries
	}
	if strategies.Runtime.MaxConsecutiveToolFailures != nil {
		definition.Runtime.MaxConsecutiveToolFailures = *strategies.Runtime.MaxConsecutiveToolFailures
	}
	if strategies.Repair.MaxRepairAttempts != nil {
		definition.Runtime.MaxRepairAttempts = *strategies.Repair.MaxRepairAttempts
	}
	if strategies.Collaboration.MaxHandoffDepth != nil {
		definition.Runtime.MaxHandoffDepth = *strategies.Collaboration.MaxHandoffDepth
	}
	if strategies.Collaboration.MaxChildTasks != nil {
		definition.Runtime.MaxChildTasks = *strategies.Collaboration.MaxChildTasks
	}
	if strings.TrimSpace(strategies.Collaboration.DelegationMode) == "disabled" {
		definition.Tools.DeniedToolIDs = appendUniqueRuntimeString(definition.Tools.DeniedToolIDs, "origin.agent.delegate")
	}
	return definition
}

func cloneStrings(values []string) []string {
	if len(values) == 0 {
		return nil
	}
	return append([]string(nil), values...)
}

func appendUniqueRuntimeString(values []string, value string) []string {
	value = strings.TrimSpace(value)
	if value == "" {
		return values
	}
	for _, existing := range values {
		if existing == value {
			return values
		}
	}
	return append(values, value)
}

func memoryStrategyEnabled(value *bool) bool {
	return contextcollector.MemoryStrategyEnabled(value)
}

func memoryReadLimit(contextStrategy contracts.ContextStrategy, memoryStrategy contracts.MemoryUseStrategy) int {
	return contextcollector.MemoryReadLimit(contextStrategy, memoryStrategy)
}

func stringAllowed(values []string, expected string) bool {
	for _, value := range values {
		if strings.TrimSpace(value) == expected {
			return true
		}
	}
	return false
}

func contextSourceReport(source string, candidates int, selected int, limit int, reason string) contracts.ContextSourceReport {
	return contextcollector.NewContextSourceReport(source, candidates, selected, limit, reason)
}

func (c Coordinator) recordContextCollectionCompleted(ctx context.Context, traceID contracts.TraceID, tenantID contracts.TenantID, runID contracts.AgentRunID, taskID contracts.TaskID, report *contracts.ContextAssemblyReport, estimatedTokensIn int) {
	if report == nil {
		return
	}
	traceReport := contextCollectionReportForTrace(*report, estimatedTokensIn)
	c.recordTrace(ctx, traceID, tenantID, runID, taskID, contracts.TraceContextCollectionCompleted, contextCollectionTracePayload(traceReport))
	for _, source := range report.Sources {
		if !contextcollector.IsExternalContextSource(source.SourceType) {
			continue
		}
		payload := contextSourceTracePayload(source)
		if source.SelectedCount > 0 {
			c.recordTrace(ctx, traceID, tenantID, runID, taskID, contracts.TraceContextExternalSourceSelected, payload)
		}
		if source.DroppedCount > 0 {
			c.recordTrace(ctx, traceID, tenantID, runID, taskID, contracts.TraceContextExternalSourceDropped, payload)
		}
	}
}

func contextCollectionReportForTrace(report contracts.ContextAssemblyReport, estimatedTokensIn int) contracts.ContextAssemblyReport {
	if report.EstimatedTokensIn <= 0 && estimatedTokensIn > 0 {
		report.EstimatedTokensIn = estimatedTokensIn
	}
	return report
}

func contextCollectionTracePayload(report contracts.ContextAssemblyReport) map[string]any {
	candidateCount := 0
	selectedCount := 0
	droppedCount := 0
	sourceCounts := map[string]map[string]int{}
	for _, source := range report.Sources {
		candidateCount += source.CandidateCount
		selectedCount += source.SelectedCount
		droppedCount += source.DroppedCount
		counts := sourceCounts[source.SourceType]
		if counts == nil {
			counts = map[string]int{}
			sourceCounts[source.SourceType] = counts
		}
		counts["candidate_count"] += source.CandidateCount
		counts["selected_count"] += source.SelectedCount
		counts["dropped_count"] += source.DroppedCount
	}
	return map[string]any{
		"strategy_hash":           report.StrategyHash,
		"mode":                    report.Mode,
		"token_budget":            report.TokenBudget,
		"estimated_tokens_in":     report.EstimatedTokensIn,
		"estimated_tokens_out":    report.EstimatedTokensOut,
		"candidate_count":         candidateCount,
		"selected_count":          selectedCount,
		"dropped_count":           droppedCount,
		"source_counts":           sourceCounts,
		"context_assembly_report": report,
		"external_sources":        contextcollector.ExternalContextSourceReports(report.Sources),
	}
}

func contextSourceTracePayload(source contracts.ContextSourceReport) map[string]any {
	return map[string]any{
		"source_type":     source.SourceType,
		"source_ref":      source.SourceRef,
		"provider_id":     source.ProviderID,
		"hook_id":         source.HookID,
		"tool_call_id":    source.ToolCallID,
		"trust_level":     source.TrustLevel,
		"candidate_count": source.CandidateCount,
		"selected_count":  source.SelectedCount,
		"dropped_count":   source.DroppedCount,
		"limit":           source.Limit,
		"reason":          source.Reason,
	}
}

func (c Coordinator) applyPromptPolicy(ctx context.Context, policy contracts.PolicySet, definition contracts.AgentDefinition, strategy contracts.ContextStrategy, bundle contracts.PromptBundle) (contracts.PromptBundle, *contracts.ContextCompressionReport, error) {
	if err := promptpolicy.ApplySafetyPolicy(policy.PromptPolicy, bundle); err != nil {
		return contracts.PromptBundle{}, nil, err
	}
	limit := promptpolicy.TokenLimit(policy, definition)
	result, err := (contextcompressor.LocalCompressor{Model: c.Model}).Compress(ctx, contextcompressor.Request{
		Strategy:       strategy,
		PromptBundle:   bundle,
		HardTokenLimit: limit,
	})
	if err != nil {
		report := result.Report
		return result.PromptBundle, &report, err
	}
	bundle = result.PromptBundle
	report := result.Report
	if bundle.ContextAssemblyReport != nil {
		bundle.ContextAssemblyReport.EstimatedTokensIn = report.InputTokens
		bundle.ContextAssemblyReport.EstimatedTokensOut = estimatePromptTokens(bundle)
		bundle.ContextAssemblyReport.Compression = &report
	}
	if err := promptpolicy.ApplyLimitPolicy(policy, definition, bundle); err != nil {
		return bundle, &report, err
	}
	if err := promptbuilder.RefreshHash(&bundle); err != nil {
		return contracts.PromptBundle{}, nil, err
	}
	return bundle, &report, nil
}

func contextCompressionRequested(compression contracts.ContextCompressionStrategy) bool {
	mode := strings.TrimSpace(compression.Mode)
	return compression.Enabled && mode != "" && mode != "none"
}

func contextCompressionRequestedTracePayload(strategy contracts.ContextStrategy) map[string]any {
	compression := strategy.Compression
	return map[string]any{
		"mode":                 compression.Mode,
		"model_provider":       compression.ModelProvider,
		"model_name":           compression.ModelName,
		"prompt_profile_id":    contextcompressor.PromptProfileID(compression),
		"trigger_ratio":        compression.TriggerRatio,
		"target_tokens":        compression.TargetTokens,
		"context_token_budget": contracts.IntValue(strategy.ContextTokenBudget),
	}
}

func contextCompressionTracePayload(report contracts.ContextCompressionReport) map[string]any {
	return map[string]any{
		"applied":           report.Applied,
		"mode":              report.Mode,
		"model_provider":    report.ModelProvider,
		"model_name":        report.ModelName,
		"prompt_profile_id": report.PromptProfileID,
		"input_tokens":      report.InputTokens,
		"output_tokens":     report.OutputTokens,
		"summary_hash":      report.SummaryHash,
		"failure_reason":    report.FailureReason,
		"source_refs":       report.SourceRefs,
	}
}

func repairAttemptLimit(policy contracts.PolicySet, definition contracts.AgentDefinition) int {
	limit := definition.Runtime.MaxRepairAttempts
	if policy.RuntimePolicy.MaxRepairAttempts > 0 && policy.RuntimePolicy.MaxRepairAttempts < limit {
		limit = policy.RuntimePolicy.MaxRepairAttempts
	}
	return limit
}

func isRepairable(err error) bool {
	var runtimeErr *contracts.RuntimeError
	if errors.As(err, &runtimeErr) {
		return runtimeErr.IsRepairable()
	}
	return false
}

func applyCandidatePatch(candidates tooldiscovery.CandidateSet, patch runtimehook.Patch) tooldiscovery.CandidateSet {
	if len(patch.ToolRankAdjustments) == 0 {
		return candidates
	}
	drop := map[string]struct{}{}
	boost := map[string]float64{}
	for _, adjustment := range patch.ToolRankAdjustments {
		if adjustment.ToolID == "" {
			continue
		}
		if adjustment.Drop {
			drop[adjustment.ToolID] = struct{}{}
			continue
		}
		if adjustment.Boost {
			boost[adjustment.ToolID] += 1000
		}
		boost[adjustment.ToolID] += adjustment.Delta
	}
	tools := make([]contracts.ToolCard, 0, len(candidates.Tools))
	for _, tool := range candidates.Tools {
		if _, ok := drop[tool.ToolID]; ok {
			continue
		}
		tools = append(tools, tool)
	}
	sort.SliceStable(tools, func(i, j int) bool {
		left := boost[tools[i].ToolID]
		right := boost[tools[j].ToolID]
		if left == right {
			return false
		}
		return left > right
	})
	candidates.Tools = tools
	return candidates
}

func retrievedCollaboratorIDs(collaborators []contracts.CollaboratorCard) []string {
	out := make([]string, 0, len(collaborators))
	for _, collaborator := range collaborators {
		if collaborator.AgentID != "" {
			out = append(out, string(collaborator.AgentID))
		}
	}
	return out
}

func toolCardIDs(tools []contracts.ToolCard) []string {
	out := make([]string, 0, len(tools))
	for _, tool := range tools {
		if tool.ToolID != "" {
			out = append(out, tool.ToolID)
		}
	}
	return out
}

func memoryIntentScope(intent runtimehook.MemoryWriteIntent) string {
	if strings.TrimSpace(intent.Scope) != "" {
		return strings.TrimSpace(intent.Scope)
	}
	return "agent"
}

func memoryIntentSummary(intent runtimehook.MemoryWriteIntent) string {
	if strings.TrimSpace(intent.Summary) != "" {
		return strings.TrimSpace(intent.Summary)
	}
	words := strings.Fields(intent.Content)
	if len(words) == 0 {
		return ""
	}
	if len(words) > 18 {
		words = words[:18]
	}
	return strings.Join(words, " ")
}

func memoryIntentString(metadata map[string]any, key string, fallback string) string {
	if metadata == nil {
		return fallback
	}
	if value, ok := metadata[key].(string); ok && strings.TrimSpace(value) != "" {
		return strings.TrimSpace(value)
	}
	return fallback
}

func memoryIntentFloat(metadata map[string]any, key string, fallback float64) float64 {
	if metadata == nil {
		return fallback
	}
	switch value := metadata[key].(type) {
	case float64:
		return value
	case float32:
		return float64(value)
	case int:
		return float64(value)
	case int64:
		return float64(value)
	default:
		return fallback
	}
}

func (c Coordinator) planContext(ctx context.Context, taskID contracts.TaskID) (*contracts.TaskPlan, *contracts.PlanStep, error) {
	if c.Plans == nil {
		return nil, nil, nil
	}
	plan, ok, err := c.Plans.ActivePlan(ctx, taskID)
	if err != nil || !ok {
		return nil, nil, err
	}
	step, stepOK, err := c.Plans.CurrentStep(ctx, taskID)
	if err != nil {
		return nil, nil, err
	}
	if !stepOK {
		return &plan, nil, nil
	}
	return &plan, &step, nil
}

func (c Coordinator) currentPlanStep(ctx context.Context, taskID contracts.TaskID) (contracts.PlanStep, bool, error) {
	if c.Plans == nil {
		return contracts.PlanStep{}, false, nil
	}
	return c.Plans.CurrentStep(ctx, taskID)
}

func (c Coordinator) recordPlanStepResult(ctx context.Context, taskID contracts.TaskID, step contracts.PlanStep, result contracts.ToolResult, definition contracts.AgentDefinition) error {
	progressor, ok := c.Plans.(interface {
		CompleteStep(context.Context, contracts.TaskID, string, []contracts.ArtifactRef, contracts.ToolResultID, string, string) (contracts.PlanStep, contracts.PlanEvent, error)
		FailStep(context.Context, contracts.TaskID, string, string, string, string) (contracts.PlanStep, contracts.PlanEvent, error)
	})
	if !ok {
		return nil
	}
	if result.Status == contracts.ToolResultFailed || result.Status == contracts.ToolResultDenied {
		reason := string(result.Status)
		if result.Error != nil {
			reason = result.Error.Message
		}
		_, _, err := progressor.FailStep(ctx, taskID, step.StepID, reason, string(definition.AgentID), "agent")
		return err
	}
	if result.Status == contracts.ToolResultSucceeded {
		_, _, err := progressor.CompleteStep(ctx, taskID, step.StepID, result.ArtifactRefs, result.ToolResultID, string(definition.AgentID), "agent")
		return err
	}
	return nil
}

func (c Coordinator) contextCollector() contextcollector.Collector {
	return contextcollector.Collector{
		Runs:     c.Runs,
		Tasks:    c.Tasks,
		Memory:   c.Memory,
		ToolRepo: c.ToolRepo,
	}
}

func (c Coordinator) taskEventsForContext(ctx context.Context, taskID contracts.TaskID, strategy contracts.ContextStrategy) ([]contracts.TaskEvent, error) {
	return c.contextCollector().TaskEventsForContext(ctx, taskID, strategy)
}

func taskEventsReadLimit(strategy contracts.ContextStrategy) (int, bool) {
	return contextcollector.TaskEventsReadLimit(strategy)
}

func (c Coordinator) toolSummaries(ctx context.Context, runID contracts.AgentRunID, limit int) []contracts.ToolResultSummary {
	return c.contextCollector().ToolSummaries(ctx, runID, limit)
}

func (c Coordinator) taskHistory(ctx context.Context, tenantID contracts.TenantID, taskID contracts.TaskID, currentRunID contracts.AgentRunID, events []contracts.TaskEvent, limit int) []contracts.RetrievedContext {
	return c.contextCollector().TaskHistory(ctx, tenantID, taskID, currentRunID, events, limit)
}

func (c Coordinator) artifactRefs(ctx context.Context, runID contracts.AgentRunID, limit int) []contracts.ArtifactRef {
	return c.contextCollector().ArtifactRefs(ctx, runID, limit)
}

func (c Coordinator) memorySummaries(ctx context.Context, tenantID contracts.TenantID, agentID contracts.AgentID, userID contracts.UserID, limit int, strategy contracts.MemoryUseStrategy) []contracts.MemorySummary {
	return c.contextCollector().MemorySummaries(ctx, tenantID, agentID, userID, limit, strategy)
}

func consecutiveToolFailureScanLimit(maxConsecutiveFailures int, maxRepairAttempts int) int {
	limit := 0
	if maxConsecutiveFailures > 0 {
		limit = maxConsecutiveFailures + 1
	}
	if maxRepairAttempts >= 0 && maxRepairAttempts+1 > limit {
		limit = maxRepairAttempts + 1
	}
	return limit
}

func (c Coordinator) consecutiveToolFailures(ctx context.Context, runID contracts.AgentRunID, limit int) int {
	if c.ToolRepo == nil {
		return 0
	}
	results, err := c.ToolRepo.ListResultsByRunLimit(ctx, runID, limit)
	if err != nil {
		return 0
	}
	sort.SliceStable(results, func(i, j int) bool {
		return results[i].CompletedAt.Before(results[j].CompletedAt)
	})
	count := 0
	for i := len(results) - 1; i >= 0; i-- {
		if results[i].Status != contracts.ToolResultFailed && results[i].Status != contracts.ToolResultDenied {
			break
		}
		count++
	}
	return count
}

func normalizeRuntimeError(err error) *contracts.RuntimeError {
	if runtimeErr, ok := err.(*contracts.RuntimeError); ok {
		return runtimeErr
	}
	return contracts.NewRuntimeError(contracts.CodeModelError, err.Error(), nil)
}
