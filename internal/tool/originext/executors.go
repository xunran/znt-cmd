package originext

import (
	"context"
	"fmt"

	"znt/internal/agentcapability"
	"znt/internal/agentfactory"
	"znt/internal/contracts"
	"znt/internal/crossgroup"
	"znt/internal/identity"
	"znt/internal/knowledge"
	"znt/internal/memoryscope"
	"znt/internal/permission"
	"znt/internal/skillupdate"
	taskprogress "znt/internal/task/progress"
	"znt/internal/tone"
	"znt/internal/tool/registry"
)

type Services struct {
	Identity          identity.Service
	Permissions       permission.Service
	MemoryScopes      memoryscope.Service
	SkillUpdates      *skillupdate.Service
	Knowledge         knowledge.Service
	CrossGroups       *crossgroup.Service
	TaskProgress      *taskprogress.Service
	AgentCapabilities *agentcapability.Service
	AgentFactory      *agentfactory.Service
	Tones             *tone.Service
}

func Register(reg registry.Registry, services Services) error {
	tools := []registry.Tool{
		permissionCheckTool(services),
		identityResolveTool(services),
		skillProposeTool(services),
		knowledgeCreateTool(services),
		knowledgeIngestTool(services),
		knowledgeSearchTool(services),
		crossGroupSearchTool(services),
		memoryShareTool(services),
		agentProgressTool(services),
		agentCapabilityTool(services),
		agentCreateDraftTool(services),
		toneDecideTool(services),
	}
	for _, tool := range tools {
		if err := reg.Register(tool); err != nil {
			return err
		}
	}
	return nil
}

type executeFunc func(ctx context.Context, call contracts.ToolCall) (map[string]any, []contracts.ArtifactRef, error)

func (f executeFunc) Execute(ctx context.Context, call contracts.ToolCall) (map[string]any, []contracts.ArtifactRef, error) {
	return f(ctx, call)
}

func permissionCheckTool(services Services) registry.Tool {
	return registry.Tool{
		Definition: contracts.ToolDefinition{
			ToolID:      "origin.permission.check",
			Name:        "origin.permission.check",
			Description: "Checks whether a group member can perform a governed Origin action.",
			InputSchema: objectSchema([]string{"group_id", "actor_id", "action"}, map[string]any{
				"group_id":       strSchema(),
				"actor_id":       strSchema(),
				"actor_type":     strSchema(),
				"roles":          arrSchema(),
				"action":         strSchema(),
				"resource_type":  strSchema(),
				"resource_id":    strSchema(),
				"resource_scope": strSchema(),
				"trace_id":       strSchema(),
			}),
			OutputSchema:     objectSchema([]string{"decision"}, map[string]any{"decision": map[string]any{"type": "object"}}),
			RiskLevel:        contracts.RiskLow,
			Visibility:       contracts.ToolProtected,
			ExecutionProfile: "local",
			Version:          "v1",
		},
		Executor: executeFunc(func(ctx context.Context, call contracts.ToolCall) (map[string]any, []contracts.ArtifactRef, error) {
			if services.Permissions == nil {
				return nil, nil, fmt.Errorf("permission service is not configured")
			}
			decision, err := services.Permissions.Check(ctx, contracts.PermissionCheckInput{
				TenantID:      call.TenantID,
				GroupID:       contracts.GroupID(stringArg(call, "group_id")),
				ActorID:       stringArg(call, "actor_id"),
				ActorType:     stringArg(call, "actor_type"),
				Roles:         stringSliceArg(call, "roles"),
				Action:        stringArg(call, "action"),
				ResourceType:  stringArg(call, "resource_type"),
				ResourceID:    stringArg(call, "resource_id"),
				ResourceScope: stringArg(call, "resource_scope"),
				TraceID:       contracts.TraceID(stringArg(call, "trace_id")),
				TaskID:        call.TaskID,
				RunID:         call.RunID,
			})
			return map[string]any{"decision": decision}, nil, err
		}),
		WhenToUse: []string{"check group member permission", "before governed skill/package/memory/cross-group actions"},
	}
}

func identityResolveTool(services Services) registry.Tool {
	return registry.Tool{
		Definition: contracts.ToolDefinition{
			ToolID:      "origin.identity.resolve_member",
			Name:        "origin.identity.resolve_member",
			Description: "Resolves a group member profile by external user id.",
			InputSchema: objectSchema([]string{"group_id", "external_user_id"}, map[string]any{
				"group_id":         strSchema(),
				"external_user_id": strSchema(),
			}),
			OutputSchema:     objectSchema([]string{"found"}, map[string]any{"found": map[string]any{"type": "boolean"}, "member": map[string]any{"type": "object"}}),
			RiskLevel:        contracts.RiskLow,
			Visibility:       contracts.ToolProtected,
			ExecutionProfile: "local",
			Version:          "v1",
		},
		Executor: executeFunc(func(ctx context.Context, call contracts.ToolCall) (map[string]any, []contracts.ArtifactRef, error) {
			if services.Identity == nil {
				return nil, nil, fmt.Errorf("identity service is not configured")
			}
			member, ok, err := services.Identity.ResolveMember(ctx, call.TenantID, contracts.GroupID(stringArg(call, "group_id")), stringArg(call, "external_user_id"))
			return map[string]any{"found": ok, "member": member}, nil, err
		}),
		WhenToUse: []string{"resolve group speaker", "inspect group member roles"},
	}
}

func skillProposeTool(services Services) registry.Tool {
	return registry.Tool{
		Definition: contracts.ToolDefinition{
			ToolID:      "origin.skill.propose_update",
			Name:        "origin.skill.propose_update",
			Description: "Creates a governed skill update request; it never edits skills directly.",
			InputSchema: objectSchema([]string{"group_id", "agent_id", "requested_by", "objective"}, map[string]any{
				"group_id":        strSchema(),
				"agent_id":        strSchema(),
				"requested_by":    strSchema(),
				"roles":           arrSchema(),
				"objective":       strSchema(),
				"target_skill_id": strSchema(),
				"proposed_patch":  map[string]any{"type": "object"},
				"trace_id":        strSchema(),
			}),
			OutputSchema:     objectSchema([]string{"request", "decision"}, map[string]any{"request": map[string]any{"type": "object"}, "decision": map[string]any{"type": "object"}}),
			RiskLevel:        contracts.RiskHigh,
			Visibility:       contracts.ToolProtected,
			ExecutionProfile: "local",
			Version:          "v1",
		},
		Executor: executeFunc(func(ctx context.Context, call contracts.ToolCall) (map[string]any, []contracts.ArtifactRef, error) {
			if services.SkillUpdates == nil {
				return nil, nil, fmt.Errorf("skill update service is not configured")
			}
			request, decision, err := services.SkillUpdates.Propose(ctx, skillupdate.ProposeInput{
				TenantID:      call.TenantID,
				GroupID:       contracts.GroupID(stringArg(call, "group_id")),
				AgentID:       contracts.AgentID(stringArg(call, "agent_id")),
				RequestedBy:   stringArg(call, "requested_by"),
				Roles:         stringSliceArg(call, "roles"),
				Objective:     stringArg(call, "objective"),
				TargetSkillID: stringArg(call, "target_skill_id"),
				ProposedPatch: mapArg(call, "proposed_patch"),
				TraceID:       contracts.TraceID(stringArg(call, "trace_id")),
				TaskID:        call.TaskID,
				RunID:         call.RunID,
			})
			return map[string]any{"request": request, "decision": decision}, nil, err
		}),
		WhenToUse: []string{"request skill improvement", "govern prompt or skill changes"},
	}
}

func knowledgeCreateTool(services Services) registry.Tool {
	return registry.Tool{
		Definition: contracts.ToolDefinition{
			ToolID:      "origin.knowledge.create",
			Name:        "origin.knowledge.create",
			Description: "Creates a governed knowledge base shell for a group.",
			InputSchema: objectSchema([]string{"group_id", "requested_by", "name"}, map[string]any{
				"group_id":     strSchema(),
				"requested_by": strSchema(),
				"roles":        arrSchema(),
				"name":         strSchema(),
				"visibility":   strSchema(),
				"source_type":  strSchema(),
				"index_type":   strSchema(),
				"trace_id":     strSchema(),
			}),
			OutputSchema:     objectSchema([]string{"knowledge_base"}, map[string]any{"knowledge_base": map[string]any{"type": "object"}}),
			RiskLevel:        contracts.RiskMedium,
			Visibility:       contracts.ToolProtected,
			ExecutionProfile: "local",
			Version:          "v1",
		},
		Executor: executeFunc(func(ctx context.Context, call contracts.ToolCall) (map[string]any, []contracts.ArtifactRef, error) {
			if services.Knowledge == nil {
				return nil, nil, fmt.Errorf("knowledge service is not configured")
			}
			base, err := services.Knowledge.CreateKnowledgeBase(ctx, knowledge.CreateKnowledgeBaseInput{
				TenantID:    call.TenantID,
				GroupID:     contracts.GroupID(stringArg(call, "group_id")),
				RequestedBy: stringArg(call, "requested_by"),
				Roles:       stringSliceArg(call, "roles"),
				Name:        stringArg(call, "name"),
				Visibility:  stringArg(call, "visibility"),
				SourceType:  stringArg(call, "source_type"),
				IndexType:   stringArg(call, "index_type"),
				TraceID:     contracts.TraceID(stringArg(call, "trace_id")),
				TaskID:      call.TaskID,
				RunID:       call.RunID,
			})
			return map[string]any{"knowledge_base": base}, nil, err
		}),
		WhenToUse: []string{"create external knowledge base", "prepare retrieval tool data source"},
	}
}

func knowledgeIngestTool(services Services) registry.Tool {
	return registry.Tool{
		Definition: contracts.ToolDefinition{
			ToolID:      "origin.knowledge.ingest",
			Name:        "origin.knowledge.ingest",
			Description: "Adds a text document to an existing governed knowledge base.",
			InputSchema: objectSchema([]string{"knowledge_base_id", "title", "content"}, map[string]any{
				"knowledge_base_id": strSchema(),
				"source_group_id":   strSchema(),
				"title":             strSchema(),
				"content":           strSchema(),
				"source_uri":        strSchema(),
				"visibility":        strSchema(),
			}),
			OutputSchema:     objectSchema([]string{"document"}, map[string]any{"document": map[string]any{"type": "object"}}),
			RiskLevel:        contracts.RiskMedium,
			Visibility:       contracts.ToolProtected,
			ExecutionProfile: "local",
			Version:          "v1",
		},
		Executor: executeFunc(func(ctx context.Context, call contracts.ToolCall) (map[string]any, []contracts.ArtifactRef, error) {
			if services.Knowledge == nil {
				return nil, nil, fmt.Errorf("knowledge service is not configured")
			}
			doc, err := services.Knowledge.IngestDocument(ctx, contracts.KnowledgeDocument{
				TenantID:        call.TenantID,
				KnowledgeBaseID: contracts.KnowledgeBaseID(stringArg(call, "knowledge_base_id")),
				SourceGroupID:   contracts.GroupID(stringArg(call, "source_group_id")),
				Title:           stringArg(call, "title"),
				Content:         stringArg(call, "content"),
				SourceURI:       stringArg(call, "source_uri"),
				Visibility:      stringArg(call, "visibility"),
			})
			return map[string]any{"document": doc}, nil, err
		}),
		WhenToUse: []string{"ingest approved text into knowledge base"},
	}
}

func knowledgeSearchTool(services Services) registry.Tool {
	return registry.Tool{
		Definition: contracts.ToolDefinition{
			ToolID:      "origin.knowledge.search",
			Name:        "origin.knowledge.search",
			Description: "Searches authorized knowledge bases and returns cited snippets.",
			InputSchema: objectSchema([]string{"requester_group_id", "requested_by", "query"}, map[string]any{
				"requester_group_id": strSchema(),
				"requested_by":       strSchema(),
				"roles":              arrSchema(),
				"query":              strSchema(),
				"knowledge_base_ids": arrSchema(),
				"source_group_id":    strSchema(),
				"limit":              map[string]any{"type": "number"},
				"allow_cross_group":  map[string]any{"type": "boolean"},
				"search_mode":        strSchema(),
				"trace_id":           strSchema(),
			}),
			OutputSchema:     objectSchema([]string{"results"}, map[string]any{"results": map[string]any{"type": "array"}}),
			RiskLevel:        contracts.RiskLow,
			Visibility:       contracts.ToolProtected,
			ExecutionProfile: "local",
			Version:          "v1",
		},
		Executor: executeFunc(func(ctx context.Context, call contracts.ToolCall) (map[string]any, []contracts.ArtifactRef, error) {
			if services.Knowledge == nil {
				return nil, nil, fmt.Errorf("knowledge service is not configured")
			}
			results, err := services.Knowledge.Search(ctx, knowledge.SearchInput{
				TenantID:         call.TenantID,
				RequesterGroupID: contracts.GroupID(stringArg(call, "requester_group_id")),
				RequestedBy:      stringArg(call, "requested_by"),
				Roles:            stringSliceArg(call, "roles"),
				Query:            stringArg(call, "query"),
				KnowledgeBaseIDs: knowledgeBaseIDs(call, "knowledge_base_ids"),
				SourceGroupID:    contracts.GroupID(stringArg(call, "source_group_id")),
				Limit:            intArg(call, "limit"),
				AllowCrossGroup:  boolArg(call, "allow_cross_group"),
				SearchMode:       stringArg(call, "search_mode"),
				TraceID:          contracts.TraceID(stringArg(call, "trace_id")),
				TaskID:           call.TaskID,
				RunID:            call.RunID,
			})
			return map[string]any{"results": results}, nil, err
		}),
		WhenToUse: []string{"search authorized knowledge base", "retrieve cited external knowledge"},
	}
}

func crossGroupSearchTool(services Services) registry.Tool {
	return registry.Tool{
		Definition: contracts.ToolDefinition{
			ToolID:      "origin.cross_group.search",
			Name:        "origin.cross_group.search",
			Description: "Searches another group's explicitly shared context after permission checks.",
			InputSchema: objectSchema([]string{"request_group_id", "source_group_id", "requested_by", "query"}, map[string]any{
				"request_group_id": strSchema(),
				"source_group_id":  strSchema(),
				"requested_by":     strSchema(),
				"roles":            arrSchema(),
				"query":            strSchema(),
				"limit":            map[string]any{"type": "number"},
				"trace_id":         strSchema(),
			}),
			OutputSchema:     objectSchema([]string{"results"}, map[string]any{"results": map[string]any{"type": "array"}}),
			RiskLevel:        contracts.RiskHigh,
			Visibility:       contracts.ToolProtected,
			ExecutionProfile: "local",
			Version:          "v1",
		},
		Executor: executeFunc(func(ctx context.Context, call contracts.ToolCall) (map[string]any, []contracts.ArtifactRef, error) {
			if services.CrossGroups == nil {
				return nil, nil, fmt.Errorf("cross-group service is not configured")
			}
			results, err := services.CrossGroups.Search(ctx, crossgroup.SearchInput{
				TenantID:       call.TenantID,
				RequestGroupID: contracts.GroupID(stringArg(call, "request_group_id")),
				SourceGroupID:  contracts.GroupID(stringArg(call, "source_group_id")),
				RequestedBy:    stringArg(call, "requested_by"),
				Roles:          stringSliceArg(call, "roles"),
				Query:          stringArg(call, "query"),
				Limit:          intArg(call, "limit"),
				TraceID:        contracts.TraceID(stringArg(call, "trace_id")),
				TaskID:         call.TaskID,
				RunID:          call.RunID,
			})
			return map[string]any{"results": results}, nil, err
		}),
		WhenToUse: []string{"cross-group shared information lookup", "query another group's authorized summaries"},
	}
}

func memoryShareTool(services Services) registry.Tool {
	return registry.Tool{
		Definition: contracts.ToolDefinition{
			ToolID:      "origin.memory.share",
			Name:        "origin.memory.share",
			Description: "Grants another group read access to a scoped memory item.",
			InputSchema: objectSchema([]string{"memory_id", "target_group_id", "actor_id"}, map[string]any{
				"memory_id":       strSchema(),
				"target_group_id": strSchema(),
				"actor_id":        strSchema(),
			}),
			OutputSchema:     objectSchema([]string{"scope"}, map[string]any{"scope": map[string]any{"type": "object"}}),
			RiskLevel:        contracts.RiskHigh,
			Visibility:       contracts.ToolProtected,
			ExecutionProfile: "local",
			Version:          "v1",
		},
		Executor: executeFunc(func(ctx context.Context, call contracts.ToolCall) (map[string]any, []contracts.ArtifactRef, error) {
			if services.MemoryScopes == nil {
				return nil, nil, fmt.Errorf("memory scope service is not configured")
			}
			scope, err := services.MemoryScopes.GrantShare(ctx, call.TenantID, contracts.MemoryID(stringArg(call, "memory_id")), contracts.GroupID(stringArg(call, "target_group_id")), stringArg(call, "actor_id"))
			return map[string]any{"scope": scope}, nil, err
		}),
		WhenToUse: []string{"share memory across groups after approval"},
	}
}

func agentProgressTool(services Services) registry.Tool {
	return registry.Tool{
		Definition: contracts.ToolDefinition{
			ToolID:      "origin.agent.progress_query",
			Name:        "origin.agent.progress_query",
			Description: "Returns natural-language progress for professional-agent work bound to a group.",
			InputSchema: objectSchema([]string{"group_id"}, map[string]any{
				"group_id": strSchema(),
				"task_id":  strSchema(),
				"query":    strSchema(),
				"actor_id": strSchema(),
				"trace_id": strSchema(),
			}),
			OutputSchema:     objectSchema([]string{"progress"}, map[string]any{"progress": map[string]any{"type": "object"}}),
			RiskLevel:        contracts.RiskLow,
			Visibility:       contracts.ToolProtected,
			ExecutionProfile: "local",
			Version:          "v1",
		},
		Executor: executeFunc(func(ctx context.Context, call contracts.ToolCall) (map[string]any, []contracts.ArtifactRef, error) {
			if services.TaskProgress == nil {
				return nil, nil, fmt.Errorf("task progress service is not configured")
			}
			summary, err := services.TaskProgress.Query(ctx, taskprogress.QueryInput{
				TenantID: call.TenantID,
				GroupID:  contracts.GroupID(stringArg(call, "group_id")),
				TaskID:   contracts.TaskID(stringArg(call, "task_id")),
				Query:    stringArg(call, "query"),
				ActorID:  stringArg(call, "actor_id"),
				TraceID:  contracts.TraceID(stringArg(call, "trace_id")),
				RunID:    call.RunID,
			})
			return map[string]any{"progress": summary}, nil, err
		}),
		WhenToUse: []string{"answer progress questions", "find status of delegated professional-agent work"},
	}
}

func agentCapabilityTool(services Services) registry.Tool {
	return registry.Tool{
		Definition: contracts.ToolDefinition{
			ToolID:      "origin.agent.capability_match",
			Name:        "origin.agent.capability_match",
			Description: "Finds existing professional agents that match a requested capability.",
			InputSchema: objectSchema([]string{"query"}, map[string]any{
				"query":    strSchema(),
				"limit":    map[string]any{"type": "number"},
				"trace_id": strSchema(),
			}),
			OutputSchema:     objectSchema([]string{"matches"}, map[string]any{"matches": map[string]any{"type": "array"}}),
			RiskLevel:        contracts.RiskLow,
			Visibility:       contracts.ToolProtected,
			ExecutionProfile: "local",
			Version:          "v1",
		},
		Executor: executeFunc(func(ctx context.Context, call contracts.ToolCall) (map[string]any, []contracts.ArtifactRef, error) {
			if services.AgentCapabilities == nil {
				return nil, nil, fmt.Errorf("agent capability service is not configured")
			}
			matches, err := services.AgentCapabilities.Match(ctx, call.TenantID, stringArg(call, "query"), intArg(call, "limit"), contracts.TraceID(stringArg(call, "trace_id")))
			return map[string]any{"matches": matches}, nil, err
		}),
		WhenToUse: []string{"find existing specialist agent before creating a new one"},
	}
}

func agentCreateDraftTool(services Services) registry.Tool {
	return registry.Tool{
		Definition: contracts.ToolDefinition{
			ToolID:      "origin.agent.create_draft",
			Name:        "origin.agent.create_draft",
			Description: "Creates a draft professional AgentPackage; it does not publish it.",
			InputSchema: objectSchema([]string{"group_id", "requested_by", "objective"}, map[string]any{
				"group_id":     strSchema(),
				"requested_by": strSchema(),
				"roles":        arrSchema(),
				"agent_id":     strSchema(),
				"name":         strSchema(),
				"objective":    strSchema(),
				"trace_id":     strSchema(),
			}),
			OutputSchema:     objectSchema([]string{"request"}, map[string]any{"request": map[string]any{"type": "object"}, "draft_id": strSchema()}),
			RiskLevel:        contracts.RiskHigh,
			Visibility:       contracts.ToolProtected,
			ExecutionProfile: "local",
			Version:          "v1",
		},
		Executor: executeFunc(func(ctx context.Context, call contracts.ToolCall) (map[string]any, []contracts.ArtifactRef, error) {
			if services.AgentFactory == nil {
				return nil, nil, fmt.Errorf("agent factory service is not configured")
			}
			result, err := services.AgentFactory.CreateDraft(ctx, agentfactory.CreateDraftInput{
				TenantID:    call.TenantID,
				GroupID:     contracts.GroupID(stringArg(call, "group_id")),
				RequestedBy: stringArg(call, "requested_by"),
				Roles:       stringSliceArg(call, "roles"),
				AgentID:     contracts.AgentID(stringArg(call, "agent_id")),
				Name:        stringArg(call, "name"),
				Objective:   stringArg(call, "objective"),
				TraceID:     contracts.TraceID(stringArg(call, "trace_id")),
				TaskID:      call.TaskID,
				RunID:       call.RunID,
			})
			out := map[string]any{"request": result.Request}
			if result.Draft != nil {
				out["draft_id"] = result.Draft.DraftID
			}
			return out, nil, err
		}),
		WhenToUse: []string{"create draft specialist agent when no existing capability matches"},
	}
}

func toneDecideTool(services Services) registry.Tool {
	return registry.Tool{
		Definition: contracts.ToolDefinition{
			ToolID:      "origin.tone.decide",
			Name:        "origin.tone.decide",
			Description: "Applies group tone policy to decide style and whether to reply.",
			InputSchema: objectSchema([]string{"group_id"}, map[string]any{
				"group_id":  strSchema(),
				"signals":   arrSchema(),
				"addressee": map[string]any{"type": "boolean"},
				"high_risk": map[string]any{"type": "boolean"},
				"trace_id":  strSchema(),
			}),
			OutputSchema:     objectSchema([]string{"tone"}, map[string]any{"tone": map[string]any{"type": "object"}}),
			RiskLevel:        contracts.RiskLow,
			Visibility:       contracts.ToolPrivate,
			ExecutionProfile: "local",
			Version:          "v1",
		},
		Executor: executeFunc(func(ctx context.Context, call contracts.ToolCall) (map[string]any, []contracts.ArtifactRef, error) {
			if services.Tones == nil {
				return nil, nil, fmt.Errorf("tone service is not configured")
			}
			decision := services.Tones.Decide(ctx, tone.DecideInput{
				TenantID:  call.TenantID,
				GroupID:   contracts.GroupID(stringArg(call, "group_id")),
				Signals:   stringSliceArg(call, "signals"),
				Addressee: boolArgDefault(call, "addressee", true),
				HighRisk:  boolArg(call, "high_risk"),
				TraceID:   contracts.TraceID(stringArg(call, "trace_id")),
				TaskID:    call.TaskID,
				RunID:     call.RunID,
			})
			return map[string]any{"tone": decision}, nil, nil
		}),
		WhenToUse: []string{"internal tone policy evaluation"},
	}
}

func objectSchema(required []string, properties map[string]any) map[string]any {
	req := make([]any, 0, len(required))
	for _, value := range required {
		req = append(req, value)
	}
	return map[string]any{"type": "object", "required": req, "properties": properties}
}

func strSchema() map[string]any {
	return map[string]any{"type": "string"}
}

func arrSchema() map[string]any {
	return map[string]any{"type": "array"}
}

func stringArg(call contracts.ToolCall, key string) string {
	value, _ := call.Arguments[key].(string)
	return value
}

func stringSliceArg(call contracts.ToolCall, key string) []string {
	raw, ok := call.Arguments[key]
	if !ok || raw == nil {
		return nil
	}
	switch values := raw.(type) {
	case []string:
		return append([]string(nil), values...)
	case []any:
		out := make([]string, 0, len(values))
		for _, value := range values {
			if str, ok := value.(string); ok && str != "" {
				out = append(out, str)
			}
		}
		return out
	default:
		return nil
	}
}

func mapArg(call contracts.ToolCall, key string) map[string]any {
	value, _ := call.Arguments[key].(map[string]any)
	if value == nil {
		return nil
	}
	out := map[string]any{}
	for k, v := range value {
		out[k] = v
	}
	return out
}

func intArg(call contracts.ToolCall, key string) int {
	switch value := call.Arguments[key].(type) {
	case int:
		return value
	case int64:
		return int(value)
	case float64:
		return int(value)
	case float32:
		return int(value)
	default:
		return 0
	}
}

func boolArg(call contracts.ToolCall, key string) bool {
	value, _ := call.Arguments[key].(bool)
	return value
}

func boolArgDefault(call contracts.ToolCall, key string, fallback bool) bool {
	value, ok := call.Arguments[key].(bool)
	if !ok {
		return fallback
	}
	return value
}

func knowledgeBaseIDs(call contracts.ToolCall, key string) []contracts.KnowledgeBaseID {
	values := stringSliceArg(call, key)
	out := make([]contracts.KnowledgeBaseID, 0, len(values))
	for _, value := range values {
		out = append(out, contracts.KnowledgeBaseID(value))
	}
	return out
}
