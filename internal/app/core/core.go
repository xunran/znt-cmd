package core

import (
	"context"
	"database/sql"
	"errors"
	"strings"
	"time"

	"znt/internal/agentcapability"
	"znt/internal/agentdef/loader"
	agentpackage "znt/internal/agentdef/package"
	"znt/internal/agentfactory"
	"znt/internal/app/config"
	"znt/internal/asset/artifact"
	"znt/internal/bridge/a2a"
	"znt/internal/bridge/array"
	contextconversation "znt/internal/context/conversation"
	"znt/internal/contracts"
	conversationstore "znt/internal/conversation"
	"znt/internal/crossgroup"
	tooldiscovery "znt/internal/discovery/tool"
	"znt/internal/eval"
	"znt/internal/governance/approval"
	"znt/internal/governance/audit"
	processgovernance "znt/internal/governance/process"
	"znt/internal/governance/trace"
	"znt/internal/identity"
	"znt/internal/intake"
	"znt/internal/knowledge"
	"znt/internal/memoryscope"
	modelclient "znt/internal/model/client"
	grouppermission "znt/internal/permission"
	policyengine "znt/internal/policy/engine"
	"znt/internal/policy/toolpolicy"
	"znt/internal/runtime/admission"
	runtimedriver "znt/internal/runtime/driver"
	runtimehook "znt/internal/runtime/hook"
	"znt/internal/runtime/kernel"
	runrepo "znt/internal/runtime/run"
	serviceconnection "znt/internal/serviceconnection"
	"znt/internal/skillupdate"
	"znt/internal/storage/postgres"
	taskhandoff "znt/internal/task/handoff"
	taskplan "znt/internal/task/plan"
	taskprogress "znt/internal/task/progress"
	taskrepo "znt/internal/task/repository"
	taskruntime "znt/internal/task/runtime"
	"znt/internal/tone"
	"znt/internal/tool/agenttool"
	toolcatalog "znt/internal/tool/catalog"
	toolhandoff "znt/internal/tool/handoff"
	toolinvoke "znt/internal/tool/invoke"
	"znt/internal/tool/originext"
	"znt/internal/tool/registry"
	toolrepo "znt/internal/tool/repository"
	toolruntime "znt/internal/tool/runtime"
)

type Core struct {
	Config      config.Config
	DB          *sql.DB
	ReadinessDB *sql.DB

	Agents        loader.Loader
	AgentRegistry *loader.StaticLoader
	Runs          runrepo.Repository

	TaskRepo    taskrepo.TaskRepository
	TaskEvents  taskrepo.EventRepository
	TaskRuntime *taskruntime.Service
	Handoffs    *taskhandoff.Service
	Plans       *taskplan.Service

	Trace               trace.Recorder
	Audit               audit.Logger
	Approvals           *approval.Service
	GovernanceProcesses *processgovernance.Service
	Policies            policyengine.Store
	PolicyEngine        policyengine.Engine
	PolicyManager       policyengine.PolicyManager

	Tools              *registry.InMemoryRegistry
	ToolCatalog        *toolcatalog.Service
	ServiceConnections *serviceconnection.Service
	RuntimeHooks       *runtimehook.Service
	ToolRepo           toolrepo.Repository
	ToolRuntime        toolruntime.Runtime
	ExternalTools      toolinvoke.Service
	Conversations      conversationstore.Store
	Artifacts          artifact.Store
	Memory             artifact.MemoryStore
	ContextPackages    artifact.ContextPackageStore

	Identity          identity.Service
	GroupPermissions  grouppermission.Service
	MemoryScopes      memoryscope.Service
	Knowledge         knowledge.Service
	CrossGroups       *crossgroup.Service
	SkillUpdates      *skillupdate.Service
	TaskProgress      *taskprogress.Service
	AgentCapabilities *agentcapability.Service
	AgentFactory      *agentfactory.Service
	Tones             *tone.Service
	Intake            *intake.Service

	Model          modelclient.ModelClient
	Coordinator    kernel.Coordinator
	RuntimeDrivers *runtimedriver.Registry
	Admission      *admission.Limiter
	EvalRunner     eval.Runner
	Evals          *eval.Store

	ArrayBridge *array.Bridge
	Packages    *agentpackage.Service
}

func New(cfg config.Config) (*Core, error) {
	var pg *postgres.Repositories
	var readinessDB *sql.DB
	if cfg.DatabaseURL != "" {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		db, err := postgres.Open(ctx, cfg.DatabaseURL, databasePoolConfig(cfg))
		if err != nil {
			return nil, err
		}
		readinessDB, err = postgres.Open(ctx, cfg.DatabaseURL, readinessPoolConfig(cfg))
		if err != nil {
			_ = db.Close()
			return nil, err
		}
		pg = postgres.NewRepositories(db)
	}
	agents := loader.NewStaticLoader()
	if strings.EqualFold(strings.TrimSpace(cfg.Env), "test") {
		agents.Put(loader.TestAgentDefinition())
	}
	var runs runrepo.Repository = runrepo.NewInMemoryRepository()
	taskStore := taskrepo.NewInMemoryStore()
	var taskRepo taskrepo.TaskRepository = taskStore
	var taskEvents taskrepo.EventRepository = taskStore
	taskRuntime := taskruntime.NewService(taskStore, taskStore)
	plans := taskplan.NewService(taskplan.NewInMemoryRepository())
	var traceRecorder trace.Recorder = trace.NewInMemoryRecorder()
	var auditLogger audit.Logger = audit.NewInMemoryLogger()
	var policies policyengine.Store = policyengine.NewInMemoryStore(policyengine.DefaultPolicySet())
	policyEngine := policyengine.New(policies, auditLogger)
	policyManager := policyengine.NewPolicyManager(policies, auditLogger)
	tools := registry.NewInMemoryRegistry()
	var artifacts artifact.Store = artifact.NewInMemoryStoreWithAudit(auditLogger)
	var memory artifact.MemoryStore = artifact.NewInMemoryMemoryStore(auditLogger)
	var contextPackages artifact.ContextPackageStore = artifact.NewInMemoryContextPackageStore()
	var toolRepo toolrepo.Repository = toolrepo.NewInMemoryRepository()
	var conversationStore conversationstore.Store = conversationstore.NewInMemoryStore()
	var packageStore agentpackage.Store
	var identityStore identity.Store
	var permissionStore grouppermission.Store
	var memoryScopeStore memoryscope.Store
	var knowledgeStore knowledge.Store
	var crossGroupStore crossgroup.Store
	var governanceProcessStore processgovernance.Store = processgovernance.NewInMemoryStore()
	var groupTaskBindingStore taskprogress.Store = taskprogress.NewInMemoryStore()
	var skillUpdateStore skillupdate.Store
	var agentCapabilityStore agentcapability.Store
	var agentDraftRequestStore agentfactory.Store
	var tonePolicyStore tone.Store
	var toolCatalogStore toolcatalog.Store
	var serviceConnectionStore serviceconnection.Store
	var runtimeHookStore runtimehook.Store = runtimehook.NewInMemoryStore()
	evalStore := eval.NewStore()
	if pg != nil {
		runs = pg.Runs
		taskRepo = pg.Tasks
		taskEvents = pg.Tasks
		taskRuntime = taskruntime.NewService(pg.Tasks, pg.Tasks)
		plans = taskplan.NewService(pg.Plans)
		traceRecorder = pg.Trace
		auditLogger = pg.Audit
		artifacts = pg.Artifacts
		memory = pg.Memory
		contextPackages = pg.ContextPackages
		toolRepo = pg.Tools
		conversationStore = pg.Conversations
		packageStore = pg.Packages
		identityStore = pg.GroupMembers
		permissionStore = pg.GroupPermissions
		memoryScopeStore = pg.MemoryScopes
		knowledgeStore = pg.Knowledge
		crossGroupStore = pg.CrossGroupShares
		governanceProcessStore = pg.GovernanceProcesses
		groupTaskBindingStore = pg.GroupTaskBindings
		skillUpdateStore = pg.SkillUpdates
		agentCapabilityStore = pg.AgentCapabilities
		agentDraftRequestStore = pg.AgentDraftRequests
		tonePolicyStore = pg.TonePolicies
		toolCatalogStore = pg.ToolCatalog
		serviceConnectionStore = pg.ServiceConnections
		runtimeHookStore = pg.RuntimeHooks
		policies = pg.Policies
		policyEngine = policyengine.New(policies, auditLogger)
		policyManager = policyengine.NewPolicyManager(policies, auditLogger)
		evalStore = eval.NewStoreWithRepository(pg.Evals)
	}
	packageService := agentpackage.NewServiceWithStore(auditLogger, packageStore)
	if err := restorePersistedAgentDefinitions(context.Background(), agents, packageStore); err != nil {
		return nil, err
	}
	agentLoader := loader.NewPromptProfileOverlayLoader(agents, packageService)
	identityService := identity.NewInMemoryServiceWithStore(identityStore)
	groupPermissions := grouppermission.NewInMemoryServiceWithStore(permissionStore, auditLogger, traceRecorder)
	memoryScopes := memoryscope.NewInMemoryServiceWithStore(memoryScopeStore, auditLogger, traceRecorder)
	knowledgeService := knowledge.NewInMemoryServiceWithStore(knowledgeStore, groupPermissions, auditLogger, traceRecorder)
	crossGroups := crossgroup.NewServiceWithStore(knowledgeService, groupPermissions, crossGroupStore, auditLogger, traceRecorder)
	skillUpdates := skillupdate.NewServiceWithStore(skillUpdateStore, groupPermissions, auditLogger, traceRecorder)
	agentCapabilities := agentcapability.NewServiceWithStore(agentCapabilityStore, traceRecorder)
	agentFactory := agentfactory.NewServiceWithStore(agentDraftRequestStore, packageService, groupPermissions, auditLogger, traceRecorder)
	tones := tone.NewServiceWithStore(tonePolicyStore, traceRecorder)
	intakeService := intake.NewService(nil, auditLogger, traceRecorder)
	taskRuntime.Audit = auditLogger
	approvals := approval.NewService(auditLogger, traceRecorder)
	governanceProcesses := processgovernance.NewService(governanceProcessStore, auditLogger, traceRecorder)
	if err := registry.RegisterBuiltinsWithArtifacts(tools, artifacts); err != nil {
		return nil, err
	}
	toolCatalog := toolcatalog.NewServiceWithStore(tools, auditLogger, toolCatalogStore)
	toolCatalog.SetTraceRecorder(traceRecorder)
	serviceConnections := serviceconnection.NewServiceWithStore(serviceConnectionStore)
	toolCatalog.SetServiceConnections(serviceConnections)
	runtimeHooks := runtimehook.NewService(runtimeHookStore, traceRecorder, auditLogger)
	runtimeHooks.SetServiceConnections(serviceConnections)
	toolRuntime := toolruntime.New(tools, toolpolicy.New(auditLogger), traceRecorder)
	toolRuntime.Audit = auditLogger
	toolRuntime.Availability = toolCatalog
	externalTools := toolinvoke.Service{
		Agents: agentLoader,
		AgentRunnable: func(ctx context.Context, tenantID contracts.TenantID, agentID contracts.AgentID) error {
			asset, ok, err := packageService.GetAgentAsset(ctx, tenantID, agentID)
			if err != nil {
				return err
			}
			if !ok || asset.Status == "" || asset.Status == agentpackage.AgentAssetActive {
				return nil
			}
			return contracts.NewRuntimeError(contracts.CodeAgentNotFound, "agent asset is not active", map[string]any{
				"agent_id": agentID,
				"status":   asset.Status,
			})
		},
		ToolRepo:      toolRepo,
		ToolRuntime:   toolRuntime,
		Policies:      policies,
		Audit:         auditLogger,
		Disabled:      cfg.DisableExternalToolsInvoke,
		DisabledTools: stringSet(cfg.DisabledToolIDs),
	}
	model := modelClientFromConfig(cfg)
	coordinator := kernel.NewCoordinator(agentLoader, runs, taskRuntime, taskRepo, traceRecorder, model)
	configureConversationJudge(&coordinator, cfg, model)
	coordinator.ModelProvider = modelProviderFromConfig(cfg)
	coordinator.ModelName = cfg.ModelName
	coordinator.ContextDefaults = cfg.ContextDefaultStrategy()
	coordinator.Plans = plans
	coordinator.Tools = tooldiscovery.StaticCandidateProvider{
		Capabilities: tooldiscovery.DefaultCapabilities(),
		Skills:       tooldiscovery.DefaultSkills(),
		Cards:        tools.Cards(),
		Registry:     tools,
	}
	coordinator.ToolRepo = toolRepo
	coordinator.ConversationStore = conversationStore
	coordinator.ToolRuntime = toolRuntime
	coordinator.Policies = policies
	coordinator.PolicyEngine = &policyEngine
	coordinator.Memory = memory
	coordinator.DisabledToolIDs = stringSet(cfg.DisabledToolIDs)
	coordinator.RuntimeHooks = runtimeHooks
	var runtimeDrivers *runtimedriver.Registry
	toolCatalog.SetAgentToolHandler(agenttool.Handler{
		Agents: agentLoader,
		AgentRunnable: func(ctx context.Context, tenantID contracts.TenantID, agentID contracts.AgentID) error {
			asset, ok, err := packageService.GetAgentAsset(ctx, tenantID, agentID)
			if err != nil {
				return err
			}
			if !ok || asset.Status == "" || asset.Status == agentpackage.AgentAssetActive {
				return nil
			}
			return contracts.NewRuntimeError(contracts.CodeAgentNotFound, "agent asset is not active", map[string]any{
				"agent_id": agentID,
				"status":   asset.Status,
			})
		},
		StartAgentRun: func(ctx context.Context, envelope contracts.AgentEnvelope) (agenttool.RunResult, error) {
			driver, err := runtimeDrivers.DefaultNative()
			if err != nil {
				return agenttool.RunResult{}, err
			}
			result, err := driver.StartRun(ctx, runtimedriver.StartRunRequest{Envelope: envelope})
			return agenttool.RunResult{
				RunID:        result.RunID,
				TaskID:       result.TaskID,
				Status:       result.Status,
				Reply:        result.Reply,
				Ask:          result.Ask,
				ArtifactRefs: result.ArtifactRefs,
				Error:        result.Error,
			}, err
		},
		Trace: traceRecorder,
	})
	handoffs := taskhandoff.NewService(taskRuntime, taskRepo, taskEvents)
	handoffs.ContextPackages = contextPackages
	handoffs.Audit = auditLogger
	handoffs.Trace = traceRecorder
	if pg != nil {
		handoffs.Repository = pg.Handoffs
	}
	if err := registry.RegisterInternal(tools, registry.Tool{
		Definition: contracts.ToolDefinition{
			ToolID:      "origin.agent.delegate",
			Name:        "origin.agent.delegate",
			Description: "Delegate a task to another Clean Core agent through AgentHandoff.",
			InputSchema: map[string]any{
				"type":     "object",
				"required": []any{"to_agent_id", "objective"},
				"properties": map[string]any{
					"parent_task_id":   map[string]any{"type": "string"},
					"to_agent_id":      map[string]any{"type": "string"},
					"to_agent_version": map[string]any{"type": "string"},
					"capability_query": map[string]any{"type": "string"},
					"objective":        map[string]any{"type": "string"},
					"reason":           map[string]any{"type": "string"},
					"handoff_mode":     map[string]any{"type": "string"},
					"trace_id":         map[string]any{"type": "string"},
					"artifact_refs":    map[string]any{"type": "array"},
					"memory_refs":      map[string]any{"type": "array"},
					"expected_output":  map[string]any{"type": "object"},
				},
			},
			OutputSchema:     map[string]any{"type": "object", "required": []any{"handoff"}},
			RiskLevel:        contracts.RiskMedium,
			Visibility:       contracts.ToolProtected,
			ExecutionProfile: "local",
			Version:          "v1",
		},
		Executor: toolhandoff.Executor{
			Agents:   agents,
			Tasks:    taskRepo,
			Handoffs: handoffs,
			Policies: policies,
			TargetReleaseLookup: func(ctx context.Context, tenantID contracts.TenantID, agentID contracts.AgentID, version contracts.AgentVersion) (contracts.AgentPackageVersion, bool, error) {
				if asset, ok, err := packageService.GetAgentAsset(ctx, tenantID, agentID); err != nil {
					return contracts.AgentPackageVersion{}, false, err
				} else if ok && asset.Status != "" && asset.Status != agentpackage.AgentAssetActive {
					return contracts.AgentPackageVersion{}, false, contracts.NewRuntimeError(contracts.CodeHandoffDenied, "target agent asset is not active", map[string]any{
						"agent_id": agentID,
						"status":   asset.Status,
					})
				}
				for _, release := range packageService.ListReleases() {
					if release.TenantID == tenantID && release.AgentID == agentID && release.Version == version {
						return release, true, nil
					}
				}
				return contracts.AgentPackageVersion{}, false, nil
			},
			StartTaskRun: func(ctx context.Context, envelope contracts.AgentEnvelope, task contracts.Task) (toolhandoff.RunResult, error) {
				result, err := coordinator.StartTaskRun(ctx, envelope, task)
				return toolhandoff.RunResult{
					RunID:  result.RunID,
					TaskID: result.TaskID,
					Status: result.Status,
					Reply:  result.Reply,
				}, err
			},
		},
		WhenToUse: []string{"delegate work to another agent", "handoff", "agent collaboration"},
	}); err != nil {
		return nil, err
	}
	taskProgress := taskprogress.NewService(groupTaskBindingStore, taskRepo, runs, handoffs, auditLogger, traceRecorder)
	if err := originext.Register(tools, originext.Services{
		Identity:          identityService,
		Permissions:       groupPermissions,
		MemoryScopes:      memoryScopes,
		SkillUpdates:      skillUpdates,
		Knowledge:         knowledgeService,
		CrossGroups:       crossGroups,
		TaskProgress:      taskProgress,
		AgentCapabilities: agentCapabilities,
		AgentFactory:      agentFactory,
		Tones:             tones,
	}); err != nil {
		return nil, err
	}
	if err := toolCatalog.Restore(context.Background()); err != nil {
		return nil, err
	}
	coordinator.Tools = tooldiscovery.StaticCandidateProvider{
		Capabilities: tooldiscovery.DefaultCapabilities(),
		Skills:       tooldiscovery.DefaultSkills(),
		Cards:        tools.Cards(),
		Registry:     tools,
	}
	var bridgeStore array.BindingStore
	if pg != nil {
		bridgeStore = pg.ExternalTasks
	}
	var bridgeAdapter contracts.CollaborationProvider
	if cfg.ExternalBridgeBaseURL != "" {
		switch strings.ToLower(strings.TrimSpace(cfg.ExternalBridgeProvider)) {
		case "a2a":
			bridgeAdapter = a2a.NewHTTPAdapter(cfg.ExternalBridgeBaseURL, cfg.ExternalBridgeToken)
		default:
			bridgeAdapter = array.NewHTTPAdapter(cfg.ExternalBridgeBaseURL, cfg.ExternalBridgeToken)
		}
	}
	arrayBridge := array.NewBridgeWithStoreAndAdapter(bridgeStore, bridgeAdapter)
	coordinator.ExternalSync = array.NewGovernedSyncer(arrayBridge, traceRecorder, auditLogger)
	externalBinding := func(ctx context.Context, tenantID contracts.TenantID, taskID contracts.TaskID) (*contracts.ExternalTaskBinding, bool) {
		binding, ok, err := arrayBridge.GetBindingByCoreTask(ctx, tenantID, taskID)
		if err != nil || !ok || binding.Status != "active" {
			return nil, false
		}
		return &binding, true
	}
	coordinator.ExternalBinding = externalBinding
	handoffs.ExternalSync = array.NewGovernedSyncer(arrayBridge, traceRecorder, auditLogger)
	handoffs.ExternalBinding = externalBinding
	admissionLimiter := admission.New(admission.Config{
		MaxRunningRuns:       cfg.RunMaxConcurrent,
		TenantMaxRunningRuns: cfg.TenantRunMaxConcurrent,
		AgentMaxRunningRuns:  cfg.AgentRunMaxConcurrent,
	})
	app := &Core{
		Config:              cfg,
		DB:                  dbFromRepositories(pg),
		ReadinessDB:         readinessDB,
		Agents:              agentLoader,
		AgentRegistry:       agents,
		Runs:                runs,
		TaskRepo:            taskRepo,
		TaskEvents:          taskEvents,
		TaskRuntime:         taskRuntime,
		Handoffs:            handoffs,
		Plans:               plans,
		Trace:               traceRecorder,
		Audit:               auditLogger,
		Approvals:           approvals,
		GovernanceProcesses: governanceProcesses,
		Policies:            policies,
		PolicyEngine:        policyEngine,
		PolicyManager:       policyManager,
		Tools:               tools,
		ToolCatalog:         toolCatalog,
		ServiceConnections:  serviceConnections,
		RuntimeHooks:        runtimeHooks,
		ToolRepo:            toolRepo,
		ToolRuntime:         toolRuntime,
		ExternalTools:       externalTools,
		Conversations:       conversationStore,
		Artifacts:           artifacts,
		Memory:              memory,
		ContextPackages:     contextPackages,
		Identity:            identityService,
		GroupPermissions:    groupPermissions,
		MemoryScopes:        memoryScopes,
		Knowledge:           knowledgeService,
		CrossGroups:         crossGroups,
		SkillUpdates:        skillUpdates,
		TaskProgress:        taskProgress,
		AgentCapabilities:   agentCapabilities,
		AgentFactory:        agentFactory,
		Tones:               tones,
		Intake:              intakeService,
		Model:               model,
		Coordinator:         coordinator,
		RuntimeDrivers:      runtimeDrivers,
		Admission:           admissionLimiter,
		EvalRunner:          eval.NewRunner(coordinator),
		Evals:               evalStore,
		ArrayBridge:         arrayBridge,
		Packages:            packageService,
	}
	app.RuntimeDrivers = runtimedriver.MustRegistry(
		runtimedriver.NewNativeRef(&app.Coordinator),
		runtimedriver.NewManagedCoordinatorRef(contracts.AgentCarrierKindAgentPluginSource, &app.Coordinator),
	)
	runtimeDrivers = app.RuntimeDrivers
	return app, nil
}

func restorePersistedAgentDefinitions(ctx context.Context, registry *loader.StaticLoader, store any) error {
	if registry == nil || store == nil {
		return nil
	}
	restoreStore, ok := store.(agentpackage.DefinitionRestoreStore)
	if !ok {
		return nil
	}
	definitions, err := restoreStore.ListAgentDefinitions(ctx)
	if err != nil {
		if isMigrationNotReadyError(err) {
			return nil
		}
		return err
	}
	for _, definition := range definitions {
		if definition.AgentID == "" || definition.Version == "" {
			continue
		}
		registry.PutVersion(definition)
	}
	assets, err := restoreStore.ListAgentAssets(ctx, "")
	if err != nil {
		if isMigrationNotReadyError(err) {
			return nil
		}
		return err
	}
	for _, asset := range assets {
		version := asset.ActiveVersion
		if version == "" {
			version = asset.DefaultVersion
		}
		if version == "" {
			continue
		}
		if _, err := registry.Load(ctx, asset.TenantID, asset.AgentID, version); err != nil {
			continue
		}
		if err := registry.SetDefaultForTenant(asset.TenantID, asset.AgentID, version); err != nil {
			continue
		}
	}
	return nil
}

func isMigrationNotReadyError(err error) bool {
	if err == nil || errors.Is(err, sql.ErrNoRows) {
		return false
	}
	message := strings.ToLower(err.Error())
	return strings.Contains(message, "sqlstate 42p01") ||
		strings.Contains(message, "undefined_table") ||
		strings.Contains(message, "relation ") && strings.Contains(message, " does not exist")
}

func dbFromRepositories(pg *postgres.Repositories) *sql.DB {
	if pg == nil {
		return nil
	}
	return pg.DB
}

func databasePoolConfig(cfg config.Config) postgres.PoolConfig {
	return postgres.PoolConfig{
		MaxOpenConns:    cfg.DBMaxOpenConns,
		MaxIdleConns:    cfg.DBMaxIdleConns,
		ConnMaxLifetime: time.Duration(cfg.DBConnMaxLifetimeSeconds) * time.Second,
		ConnMaxIdleTime: time.Duration(cfg.DBConnMaxIdleTimeSeconds) * time.Second,
	}
}

func readinessPoolConfig(cfg config.Config) postgres.PoolConfig {
	return postgres.PoolConfig{
		MaxOpenConns:    cfg.DBReadinessMaxOpenConns,
		MaxIdleConns:    cfg.DBReadinessMaxIdleConns,
		ConnMaxLifetime: time.Duration(cfg.DBConnMaxLifetimeSeconds) * time.Second,
		ConnMaxIdleTime: time.Duration(cfg.DBConnMaxIdleTimeSeconds) * time.Second,
	}
}

func (c *Core) LoadPolicySet(ctx context.Context, tenantID contracts.TenantID, policySetID contracts.PolicySetID) contracts.PolicySet {
	if c.Policies == nil {
		return policyengine.FallbackPolicySet(tenantID, policySetID)
	}
	policy, ok, err := c.Policies.Get(ctx, tenantID, policySetID)
	if err != nil || !ok {
		return policyengine.FallbackPolicySet(tenantID, policySetID)
	}
	return policy
}

func (c *Core) LoadPolicySetVersion(ctx context.Context, tenantID contracts.TenantID, policyVersionID contracts.PolicyVersionID) (contracts.PolicySet, bool) {
	if c.PolicyManager.Store == nil || policyVersionID == "" {
		return contracts.PolicySet{}, false
	}
	version, policy, ok, err := c.PolicyManager.GetVersion(ctx, policyVersionID)
	if err != nil || !ok || version.TenantID != tenantID {
		return contracts.PolicySet{}, false
	}
	return policy, true
}

func (c *Core) EnsureAgentRunnable(ctx context.Context, tenantID contracts.TenantID, agentID contracts.AgentID) error {
	if c.Packages == nil || agentID == "" {
		return nil
	}
	asset, ok, err := c.Packages.GetAgentAsset(ctx, tenantID, agentID)
	if err != nil {
		return err
	}
	if !ok {
		return nil
	}
	if asset.Status == "" || asset.Status == agentpackage.AgentAssetActive {
		return nil
	}
	return contracts.NewRuntimeError(contracts.CodeAgentNotFound, "agent asset is not active", map[string]any{
		"agent_id": agentID,
		"status":   asset.Status,
	})
}

func modelClientFromConfig(cfg config.Config) modelclient.ModelClient {
	if cfg.ModelBaseURL != "" || cfg.ModelName != "" || cfg.ModelProvider == "openai-compatible" {
		return modelclient.OpenAICompatibleClient{
			BaseURL:         cfg.ModelBaseURL,
			APIKey:          cfg.ModelAPIKey,
			Model:           cfg.ModelName,
			MaxTokens:       cfg.ModelMaxTokens,
			Temperature:     cfg.ModelTemperature,
			Thinking:        cfg.ModelThinking,
			ReasoningEffort: cfg.ModelReasoningEffort,
		}
	}
	return modelclient.StubModelClient{}
}

func modelProviderFromConfig(cfg config.Config) string {
	if cfg.ModelProvider != "" {
		return cfg.ModelProvider
	}
	if cfg.ModelBaseURL != "" {
		return "openai-compatible"
	}
	return "stub"
}

func configureConversationJudge(coordinator *kernel.Coordinator, cfg config.Config, model modelclient.ModelClient) {
	coordinator.EnableDirectConversation = cfg.ConversationDirectEnabled
	coordinator.DisableConversationRetrieval = !cfg.ConversationRetrievalIsEnabled()
	modelJudge := contextconversation.ModelJudge{
		Model:   model,
		Timeout: time.Duration(cfg.ConversationJudgeTimeoutMS) * time.Millisecond,
	}
	switch cfg.ConversationJudgeMode {
	case "model":
		coordinator.AddressingJudge = modelJudge
		coordinator.SufficiencyJudge = modelJudge
	case "hybrid":
		coordinator.AddressingJudge = contextconversation.HybridJudge{Model: modelJudge}
		coordinator.SufficiencyJudge = modelJudge
	default:
	}
}

func stringSet(values []string) map[string]struct{} {
	out := map[string]struct{}{}
	for _, value := range values {
		out[value] = struct{}{}
	}
	return out
}
