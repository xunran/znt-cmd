package kernel

import (
	"context"
	"errors"
	"fmt"
	"sort"
	"strings"
	"time"

	"znt/internal/agentdef/loader"
	"znt/internal/asset/artifact"
	"znt/internal/bridge/array"
	contextconversation "znt/internal/context/conversation"
	promptbuilder "znt/internal/context/promptbundle"
	workviewbuilder "znt/internal/context/workview"
	"znt/internal/contracts"
	decisionparser "znt/internal/decision/parser"
	decisionvalidator "znt/internal/decision/validator"
	tooldiscovery "znt/internal/discovery/tool"
	"znt/internal/governance/trace"
	modelclient "znt/internal/model/client"
	policyengine "znt/internal/policy/engine"
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
	AddressingJudge              contextconversation.AddressingJudge
	SufficiencyJudge             contextconversation.SufficiencyJudge
	ContextRetriever             contextconversation.Retriever
	EnableDirectConversation     bool
	DisableConversationRetrieval bool
	ConversationMaxRetrieved     int
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
	Agent         contracts.AgentDefinition `json:"agent"`
	PolicySet     contracts.PolicySet       `json:"policy_set"`
	WorkView      contracts.WorkView        `json:"work_view"`
	PromptBundle  contracts.PromptBundle    `json:"prompt_bundle"`
	TokenEstimate int                       `json:"token_estimate"`
	ModelProvider string                    `json:"model_provider,omitempty"`
	ModelName     string                    `json:"model_name,omitempty"`
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
		found, err := c.Tools.Candidates(ctx, definition, policySet, request.Input)
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
	candidates = c.applyCandidateHook(ctx, envelope, definition, policySet, runID, taskID, request.Input, candidates)
	contextPatch := c.applyRuntimeHook(ctx, runtimehook.BeforeContextBuild, runtimehook.TransformRequest{
		TenantID:   tenantID,
		TraceID:    envelope.TraceID,
		RunID:      runID,
		TaskID:     taskID,
		Agent:      definition,
		Policy:     policySet,
		Objective:  request.Input,
		Candidates: candidates,
	})
	view, err := c.WorkView.Build(ctx, workviewbuilder.BuildInput{
		Run:               run,
		Task:              task,
		Agent:             definition,
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
	applyContextPatch(&view, contextPatch)
	bundle, err := c.Prompts.Build(ctx, definition, view)
	if err != nil {
		return PromptPreviewResult{}, err
	}
	bundle = c.applyPromptHook(ctx, envelope, definition, policySet, runID, taskID, request.Input, candidates, view, bundle)
	bundle, err = c.applyPromptPolicy(policySet, definition, bundle)
	if err != nil {
		return PromptPreviewResult{}, err
	}
	return PromptPreviewResult{
		Agent:         definition,
		PolicySet:     policySet,
		WorkView:      view,
		PromptBundle:  bundle,
		TokenEstimate: estimatePromptTokens(bundle),
		ModelProvider: c.snapshotModelProvider(),
		ModelName:     c.snapshotModelName(),
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
		return c.prepareTaskRun(ctx, envelope, task)
	}
	return c.prepareNewTaskRun(ctx, envelope)
}

func (c Coordinator) prepareNewTaskRun(ctx context.Context, envelope contracts.AgentEnvelope) (PreparedRun, error) {
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
	userInput, _ := envelope.Payload["input"].(string)
	if userInput == "" {
		userInput = envelope.Command
	}
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
	run := contracts.AgentRun{
		RunID:           contracts.AgentRunID(idgen.New("run")),
		TraceID:         envelope.TraceID,
		TenantID:        envelope.Context.TenantID,
		AgentID:         definition.AgentID,
		AgentVersion:    definition.Version,
		TaskID:          task.TaskID,
		Status:          contracts.RunCreated,
		PolicySetID:     definition.PolicyRefs.PolicySetID,
		VersionSnapshot: c.versionSnapshot(ctx, envelope.Context.TenantID, definition, userInput),
		StartedAt:       now,
	}
	if err := c.Runs.Create(ctx, run); err != nil {
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
	if resumedInput, _ := envelope.Payload["input"].(string); resumedInput != "" {
		c.recordConversationInput(ctx, envelope, runID, taskID, resumedInput)
	}
	result, err := c.loop(ctx, envelope, definition, runID, taskID, task.Objective)
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
	prepared, err := c.prepareTaskRun(ctx, envelope, task)
	if err != nil {
		return RunResult{}, err
	}
	return c.ExecutePreparedRun(ctx, prepared)
}

func (c Coordinator) prepareTaskRun(ctx context.Context, envelope contracts.AgentEnvelope, task contracts.Task) (PreparedRun, error) {
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
	run := contracts.AgentRun{
		RunID:           contracts.AgentRunID(idgen.New("run")),
		TraceID:         envelope.TraceID,
		TenantID:        task.TenantID,
		AgentID:         definition.AgentID,
		AgentVersion:    definition.Version,
		TaskID:          task.TaskID,
		Status:          contracts.RunCreated,
		PolicySetID:     definition.PolicyRefs.PolicySetID,
		VersionSnapshot: c.versionSnapshot(ctx, task.TenantID, definition, task.Objective),
		StartedAt:       now,
	}
	if err := c.Runs.Create(ctx, run); err != nil {
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
	return PreparedRun{Envelope: envelope, Definition: definition, Task: task, Run: run, UserInput: task.Objective, Source: "existing_task"}, nil
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
	if prepared.Source == "new_task" {
		c.recordConversationInput(ctx, envelope, run.RunID, task.TaskID, prepared.UserInput)
	} else if input, _ := envelope.Payload["input"].(string); input != "" {
		c.recordConversationInput(ctx, envelope, run.RunID, task.TaskID, input)
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
	if definition.Runtime.MaxSteps > 0 && run.StepCount > definition.Runtime.MaxSteps {
		return RunResult{}, true, contracts.NewRuntimeError(contracts.CodeModelError, "max steps exceeded", nil)
	}
	task, err := c.TaskRepo.Get(ctx, taskID)
	if err != nil {
		return RunResult{}, true, err
	}
	events, err := c.Tasks.Events(ctx, taskID)
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
	candidates, err := c.Tools.Candidates(ctx, activeDefinition, policySet, task.Objective)
	if err != nil {
		return RunResult{}, true, err
	}
	candidates = c.applyCandidateHook(ctx, envelope, activeDefinition, policySet, runID, taskID, task.Objective, candidates)
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
	toolResults := c.toolSummaries(ctx, runID)
	memorySummaries := c.memorySummaries(ctx, envelope.Context.TenantID, activeDefinition.AgentID, envelope.Context.UserID)
	artifactRefs := c.artifactRefs(ctx, runID)
	conversation := c.conversationContext(ctx, envelope, activeDefinition, runID, taskID, events, memorySummaries, artifactRefs, toolResults, userInput)
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
	applyContextPatch(&view, contextPatch)
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
		},
		CreatedAt: c.Now(),
	})
	if shouldNoOpByConversationGuard(conversation) {
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
	bundle = c.applyPromptHook(ctx, envelope, activeDefinition, policySet, runID, taskID, task.Objective, candidates, view, bundle)
	bundle, err = c.applyPromptPolicy(policySet, activeDefinition, bundle)
	if err != nil {
		return RunResult{}, true, err
	}
	if err := c.pinPromptBundleHash(ctx, runID, bundle.Hash); err != nil {
		return RunResult{}, true, err
	}
	_ = c.Trace.Record(ctx, contracts.TraceEvent{
		TraceID:   envelope.TraceID,
		TenantID:  envelope.Context.TenantID,
		SpanID:    contracts.SpanID(stepID),
		RunID:     runID,
		TaskID:    taskID,
		Type:      contracts.TracePromptBundleBuilt,
		Payload:   map[string]any{"hash": bundle.Hash},
		CreatedAt: c.Now(),
	})
	if limit := promptTokenLimit(policySet, activeDefinition); limit > 0 && estimatePromptTokens(bundle) > limit {
		return RunResult{}, true, contracts.NewRuntimeError(contracts.CodeModelError, "max prompt tokens exceeded", map[string]any{"max_prompt_tokens": limit})
	}
	decision, err := c.modelDecision(ctx, envelope, activeDefinition, runID, taskID, stepID, bundle, policySet, candidates.Tools)
	if err != nil {
		return RunResult{}, true, err
	}
	if err := c.applyMemoryWriteHook(ctx, envelope, activeDefinition, policySet, runID, taskID, task.Objective, candidates, view, bundle, decision); err != nil {
		return RunResult{}, true, err
	}
	return c.dispatch(ctx, envelope, activeDefinition, policySet, runID, taskID, stepID, decision, candidates)
}

func (c Coordinator) modelDecision(ctx context.Context, envelope contracts.AgentEnvelope, definition contracts.AgentDefinition, runID contracts.AgentRunID, taskID contracts.TaskID, stepID string, bundle contracts.PromptBundle, policySet contracts.PolicySet, candidateTools []contracts.ToolCard) (contracts.Decision, error) {
	maxRepairAttempts := repairAttemptLimit(policySet, definition)
	for repairAttempt := 0; ; repairAttempt++ {
		c.recordModelCalled(ctx, envelope, runID, taskID, stepID, bundle, repairAttempt)
		modelResp, err := c.completeModel(ctx, envelope, definition, runID, taskID, stepID, bundle)
		if err != nil {
			c.recordModelCompleted(ctx, envelope, runID, taskID, stepID, modelclient.ModelResponse{}, err)
			return contracts.Decision{}, err
		}
		c.recordModelCompleted(ctx, envelope, runID, taskID, stepID, modelResp, nil)
		decision, err := decisionparser.Parse(modelResp.RawDecisionJSON)
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

func (c Coordinator) recordModelCalled(ctx context.Context, envelope contracts.AgentEnvelope, runID contracts.AgentRunID, taskID contracts.TaskID, stepID string, bundle contracts.PromptBundle, repairAttempt int) {
	payload := map[string]any{"prompt_bundle_hash": bundle.Hash, "repair_attempt": repairAttempt}
	if capabilities, ok := c.modelCapabilities(); ok {
		payload["model_provider"] = capabilities.Provider
		payload["model_name"] = capabilities.Model
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

func (c Coordinator) repairPrompt(ctx context.Context, envelope contracts.AgentEnvelope, runID contracts.AgentRunID, taskID contracts.TaskID, stepID string, bundle contracts.PromptBundle, repairAttempt int, cause error) contracts.PromptBundle {
	bundle.Constraints = append(bundle.Constraints, fmt.Sprintf("repair attempt %d: previous decision was invalid (%s); return one valid Decision JSON object that matches the contract", repairAttempt, cause.Error()))
	_ = rehashPromptBundle(&bundle)
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

func (c Coordinator) applyPromptHook(ctx context.Context, envelope contracts.AgentEnvelope, agent contracts.AgentDefinition, policy contracts.PolicySet, runID contracts.AgentRunID, taskID contracts.TaskID, objective string, candidates tooldiscovery.CandidateSet, view contracts.WorkView, bundle contracts.PromptBundle) contracts.PromptBundle {
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
	return applyPromptPatch(bundle, patch)
}

func (c Coordinator) applyMemoryWriteHook(ctx context.Context, envelope contracts.AgentEnvelope, agent contracts.AgentDefinition, policy contracts.PolicySet, runID contracts.AgentRunID, taskID contracts.TaskID, objective string, candidates tooldiscovery.CandidateSet, view contracts.WorkView, bundle contracts.PromptBundle, decision contracts.Decision) error {
	if c.Memory == nil {
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
		event := contracts.MemoryEvent{
			TenantID:      envelope.Context.TenantID,
			AgentID:       agent.AgentID,
			UserID:        envelope.Context.UserID,
			Scope:         memoryIntentScope(intent),
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
	if definition.Runtime.MaxConsecutiveToolFailures > 0 && c.consecutiveToolFailures(ctx, runID) > definition.Runtime.MaxConsecutiveToolFailures {
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
		FailureSeen: c.consecutiveToolFailures(ctx, runID),
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
		resp, err := c.streamModel(ctx, envelope, definition, runID, taskID, stepID, bundle)
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

func (c Coordinator) streamModel(ctx context.Context, envelope contracts.AgentEnvelope, definition contracts.AgentDefinition, runID contracts.AgentRunID, taskID contracts.TaskID, stepID string, bundle contracts.PromptBundle) (modelclient.ModelResponse, error) {
	events, err := c.Model.Stream(ctx, modelclient.ModelRequest{
		RunID:        runID,
		PromptBundle: bundle,
		Timeout:      definition.Runtime.MaxDuration,
	})
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
	return contracts.VersionSnapshot{
		ContractVersion:      contractVersion(definition),
		AgentDefinition:      definition.Version,
		AgentPackage:         definition.PackageVersionID,
		PolicySet:            policySetID,
		PolicyVersionID:      policyVersionID,
		PolicySetVersion:     policy.Version,
		SkillDefinitions:     skills,
		ToolDefinitions:      tools,
		ModelProvider:        c.snapshotModelProvider(),
		ModelName:            c.snapshotModelName(),
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
	text := strings.Join([]string{bundle.System, bundle.Developer, bundle.Task, bundle.Context}, " ")
	for _, instruction := range bundle.SkillInstructions {
		text += " " + instruction
	}
	return len(strings.Fields(text))
}

func (c Coordinator) applyPromptPolicy(policy contracts.PolicySet, definition contracts.AgentDefinition, bundle contracts.PromptBundle) (contracts.PromptBundle, error) {
	for _, phrase := range policy.PromptPolicy.BlockedPhrases {
		phrase = strings.TrimSpace(phrase)
		if phrase == "" {
			continue
		}
		if strings.Contains(strings.ToLower(promptText(bundle)), strings.ToLower(phrase)) {
			return contracts.PromptBundle{}, contracts.NewRuntimeError(contracts.CodeToolPolicyDenied, "prompt policy blocked phrase", map[string]any{"phrase": phrase})
		}
	}
	limit := promptTokenLimit(policy, definition)
	if limit <= 0 || estimatePromptTokens(bundle) <= limit {
		return bundle, nil
	}
	if !policy.CompressionPolicy.Enabled {
		return bundle, nil
	}
	reserve := estimatePromptTokens(contracts.PromptBundle{
		System:            bundle.System,
		Developer:         bundle.Developer,
		Task:              bundle.Task,
		SkillInstructions: bundle.SkillInstructions,
	})
	maxContextTokens := limit - reserve - 16
	if policy.CompressionPolicy.MaxContextItems > 0 && maxContextTokens > policy.CompressionPolicy.MaxContextItems*40 {
		maxContextTokens = policy.CompressionPolicy.MaxContextItems * 40
	}
	if maxContextTokens < 0 {
		maxContextTokens = 0
	}
	bundle.Context = truncateWords(bundle.Context, maxContextTokens)
	bundle.Constraints = append(bundle.Constraints, "context compressed by policy before model call")
	if err := rehashPromptBundle(&bundle); err != nil {
		return contracts.PromptBundle{}, err
	}
	return bundle, nil
}

func promptTokenLimit(policy contracts.PolicySet, definition contracts.AgentDefinition) int {
	limit := definition.Runtime.MaxPromptTokens
	if policy.PromptPolicy.MaxPromptTokens > 0 && (limit == 0 || policy.PromptPolicy.MaxPromptTokens < limit) {
		limit = policy.PromptPolicy.MaxPromptTokens
	}
	return limit
}

func repairAttemptLimit(policy contracts.PolicySet, definition contracts.AgentDefinition) int {
	limit := definition.Runtime.MaxRepairAttempts
	if policy.RuntimePolicy.MaxRepairAttempts > 0 && (limit == 0 || policy.RuntimePolicy.MaxRepairAttempts < limit) {
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

func promptText(bundle contracts.PromptBundle) string {
	return strings.Join([]string{bundle.System, bundle.Developer, bundle.Task, bundle.Context}, "\n")
}

func truncateWords(value string, limit int) string {
	if limit <= 0 {
		return "[context omitted by compression policy]"
	}
	words := strings.Fields(value)
	if len(words) <= limit {
		return value
	}
	return strings.Join(words[:limit], " ") + "\n[context truncated by compression policy]"
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

func applyContextPatch(view *contracts.WorkView, patch runtimehook.Patch) {
	for _, ref := range patch.DropContextRefs {
		switch ref {
		case "conversation":
			view.ConversationContext = nil
		case "memory":
			view.MemorySummaries = nil
		case "artifacts":
			view.ArtifactRefs = nil
		case "tool_results":
			view.ToolResultSummaries = nil
		case "capabilities":
			view.CandidateCapabilities = nil
		case "skills":
			view.CandidateSkills = nil
			view.CandidateSkillInstructions = nil
		case "tools":
			view.CandidateTools = nil
		case "collaborators":
			view.CandidateCollaborators = nil
		}
	}
	for _, block := range patch.AddContextBlocks {
		if strings.TrimSpace(block.Content) == "" {
			continue
		}
		id := contracts.ArtifactID(block.ID)
		if id == "" {
			id = contracts.ArtifactID(idgen.New("hookctx"))
		}
		summary := block.Content
		if block.Title != "" {
			summary = block.Title + ": " + block.Content
		}
		view.ArtifactRefs = append(view.ArtifactRefs, contracts.ArtifactRef{
			ArtifactID: id,
			Type:       "runtime_hook_context",
			Summary:    summary,
		})
	}
	for _, hint := range patch.PlannerHints {
		if strings.TrimSpace(hint.Content) != "" {
			view.Constraints = append(view.Constraints, "planner hint: "+hint.Content)
		}
	}
}

func applyPromptPatch(bundle contracts.PromptBundle, patch runtimehook.Patch) contracts.PromptBundle {
	for _, block := range patch.AddContextBlocks {
		if strings.TrimSpace(block.Content) == "" {
			continue
		}
		title := block.Title
		if title == "" {
			title = block.ID
		}
		if title == "" {
			title = "runtime hook context"
		}
		bundle.Context = strings.TrimSpace(bundle.Context + "\n<runtime hook context>\n" + title + "\n" + block.Content + "\n</runtime hook context>")
	}
	for _, hint := range patch.PlannerHints {
		if strings.TrimSpace(hint.Content) != "" {
			bundle.Constraints = append(bundle.Constraints, "planner hint: "+hint.Content)
		}
	}
	_ = rehashPromptBundle(&bundle)
	return bundle
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

func rehashPromptBundle(bundle *contracts.PromptBundle) error {
	stable, err := hash.StableJSON(map[string]any{
		"system":      bundle.System,
		"developer":   bundle.Developer,
		"task":        bundle.Task,
		"context":     bundle.Context,
		"constraints": bundle.Constraints,
		"skills":      bundle.SkillInstructions,
		"tools":       bundle.ToolCards,
	})
	if err != nil {
		return err
	}
	bundle.Hash = stable
	return nil
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

func (c Coordinator) toolSummaries(ctx context.Context, runID contracts.AgentRunID) []contracts.ToolResultSummary {
	if c.ToolRepo == nil {
		return nil
	}
	results, err := c.ToolRepo.ListResultsByRun(ctx, runID)
	if err != nil {
		return nil
	}
	out := make([]contracts.ToolResultSummary, 0, len(results))
	for _, result := range results {
		summary := ""
		if result.Error != nil {
			summary = result.Error.Message
		} else if len(result.Output) > 0 {
			summary = "tool output available"
		}
		out = append(out, contracts.ToolResultSummary{
			ToolCallID: result.ToolCallID,
			Status:     result.Status,
			Summary:    summary,
		})
	}
	return out
}

func (c Coordinator) artifactRefs(ctx context.Context, runID contracts.AgentRunID) []contracts.ArtifactRef {
	if c.ToolRepo == nil {
		return nil
	}
	results, err := c.ToolRepo.ListResultsByRun(ctx, runID)
	if err != nil {
		return nil
	}
	seen := map[contracts.ArtifactID]struct{}{}
	out := make([]contracts.ArtifactRef, 0)
	for _, result := range results {
		for _, ref := range result.ArtifactRefs {
			if ref.ArtifactID == "" {
				continue
			}
			if _, ok := seen[ref.ArtifactID]; ok {
				continue
			}
			seen[ref.ArtifactID] = struct{}{}
			out = append(out, ref)
		}
	}
	return out
}

func (c Coordinator) memorySummaries(ctx context.Context, tenantID contracts.TenantID, agentID contracts.AgentID, userID contracts.UserID) []contracts.MemorySummary {
	if c.Memory == nil || tenantID == "" {
		return nil
	}
	memories, err := c.Memory.ListMemory(ctx, tenantID, agentID, userID)
	if err != nil {
		return nil
	}
	return memories
}

func (c Coordinator) consecutiveToolFailures(ctx context.Context, runID contracts.AgentRunID) int {
	if c.ToolRepo == nil {
		return 0
	}
	results, err := c.ToolRepo.ListResultsByRun(ctx, runID)
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
