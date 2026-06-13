package handoff

import (
	"context"
	"strings"
	"testing"
	"time"

	"znt/internal/agentdef/loader"
	"znt/internal/contracts"
	taskhandoff "znt/internal/task/handoff"
	taskrepo "znt/internal/task/repository"
	taskruntime "znt/internal/task/runtime"
)

func TestResolveTargetAgentRequiresExplicitTarget(t *testing.T) {
	executor := Executor{Agents: loader.NewStaticLoader(loader.TestAgentDefinition())}

	_, _, err := executor.resolveTargetAgent(context.Background(), "tenant_1", "", "", "", "delegate this")
	if err == nil {
		t.Fatal("expected missing to_agent_id to fail")
	}
	runtimeErr, ok := err.(*contracts.RuntimeError)
	if !ok {
		t.Fatalf("expected RuntimeError, got %T: %v", err, err)
	}
	if runtimeErr.Code != contracts.CodeDecisionSchemaError {
		t.Fatalf("expected schema error, got %s", runtimeErr.Code)
	}
}

func TestExecuteRequiresExplicitTarget(t *testing.T) {
	tasks := taskrepo.NewInMemoryTaskRepository()
	now := time.Unix(1, 0).UTC()
	parent := taskrepo.NewTask("task_1", "tenant_1", "test-agent", "v1", "policy_default", "parent", "delegate", now)
	if err := tasks.Create(context.Background(), parent); err != nil {
		t.Fatal(err)
	}
	executor := Executor{
		Agents: loader.NewStaticLoader(loader.TestAgentDefinition()),
		Tasks:  tasks,
	}

	_, _, err := executor.Execute(context.Background(), contracts.ToolCall{
		TenantID: "tenant_1",
		RunID:    "run_1",
		TaskID:   "task_1",
		ToolID:   "origin.agent.delegate",
		Arguments: map[string]any{
			"parent_task_id": "task_1",
			"objective":      "delegate this",
		},
	})
	if err == nil {
		t.Fatal("expected missing to_agent_id to fail")
	}
	runtimeErr, ok := err.(*contracts.RuntimeError)
	if !ok {
		t.Fatalf("expected RuntimeError, got %T: %v", err, err)
	}
	if runtimeErr.Code != contracts.CodeDecisionSchemaError {
		t.Fatalf("expected schema error, got %s", runtimeErr.Code)
	}
}

func TestExecuteRejectsTargetNotRetrievedForStep(t *testing.T) {
	tasks := taskrepo.NewInMemoryTaskRepository()
	now := time.Unix(1, 0).UTC()
	parent := taskrepo.NewTask("task_1", "tenant_1", "source-agent", "v1", "policy_default", "parent", "delegate", now)
	if err := tasks.Create(context.Background(), parent); err != nil {
		t.Fatal(err)
	}
	source := contracts.AgentDefinition{
		AgentID: "source-agent",
		Version: "v1",
		Collaborators: []contracts.AgentCollaboratorRef{{
			AgentID: "target-agent",
		}},
	}
	target := contracts.AgentDefinition{AgentID: "target-agent", Version: "v1"}
	executor := Executor{
		Agents: loader.NewStaticLoader(source, target),
		Tasks:  tasks,
	}

	_, _, err := executor.Execute(context.Background(), contracts.ToolCall{
		TenantID: "tenant_1",
		RunID:    "run_1",
		TaskID:   "task_1",
		ToolID:   "origin.agent.delegate",
		Arguments: map[string]any{
			"parent_task_id":           "task_1",
			"objective":                "delegate this",
			"to_agent_id":              "target-agent",
			"_retrieved_collaborators": []any{"other-agent"},
		},
	})
	assertRuntimeErrorCode(t, err, contracts.CodeHandoffDenied)
}

func TestExecuteRejectsMissingRetrievedCollaboratorsForStep(t *testing.T) {
	tasks := taskrepo.NewInMemoryTaskRepository()
	now := time.Unix(1, 0).UTC()
	parent := taskrepo.NewTask("task_1", "tenant_1", "source-agent", "v1", "policy_default", "parent", "delegate", now)
	if err := tasks.Create(context.Background(), parent); err != nil {
		t.Fatal(err)
	}
	source := contracts.AgentDefinition{
		AgentID: "source-agent",
		Version: "v1",
		Collaborators: []contracts.AgentCollaboratorRef{{
			AgentID: "target-agent",
		}},
	}
	target := contracts.AgentDefinition{AgentID: "target-agent", Version: "v1"}
	executor := Executor{
		Agents: loader.NewStaticLoader(source, target),
		Tasks:  tasks,
	}

	_, _, err := executor.Execute(context.Background(), contracts.ToolCall{
		TenantID: "tenant_1",
		RunID:    "run_1",
		TaskID:   "task_1",
		ToolID:   "origin.agent.delegate",
		Arguments: map[string]any{
			"parent_task_id": "task_1",
			"objective":      "delegate this",
			"to_agent_id":    "target-agent",
		},
	})
	assertRuntimeErrorCode(t, err, contracts.CodeHandoffDenied)
}

func TestExecuteRejectsDelegationDisabledByStrategy(t *testing.T) {
	tasks := taskrepo.NewInMemoryTaskRepository()
	now := time.Unix(1, 0).UTC()
	parent := taskrepo.NewTask("task_1", "tenant_1", "source-agent", "v1", "policy_default", "parent", "delegate", now)
	if err := tasks.Create(context.Background(), parent); err != nil {
		t.Fatal(err)
	}
	source := contracts.AgentDefinition{
		AgentID: "source-agent",
		Version: "v1",
		Collaborators: []contracts.AgentCollaboratorRef{{
			AgentID: "target-agent",
		}},
		Strategies: contracts.AgentStrategies{
			Collaboration: contracts.CollaborationStrategy{
				DelegationMode: "disabled",
			},
		},
	}
	target := contracts.AgentDefinition{AgentID: "target-agent", Version: "v1"}
	executor := Executor{
		Agents: loader.NewStaticLoader(source, target),
		Tasks:  tasks,
	}

	_, _, err := executor.Execute(context.Background(), contracts.ToolCall{
		TenantID: "tenant_1",
		RunID:    "run_1",
		TaskID:   "task_1",
		ToolID:   "origin.agent.delegate",
		Arguments: map[string]any{
			"parent_task_id":           "task_1",
			"objective":                "delegate this",
			"to_agent_id":              "target-agent",
			"_retrieved_collaborators": []string{"target-agent"},
		},
	})
	assertRuntimeErrorCode(t, err, contracts.CodeHandoffDenied)
}

func TestExecuteRejectsNonRunnableTargetRelease(t *testing.T) {
	tasks := taskrepo.NewInMemoryTaskRepository()
	now := time.Unix(1, 0).UTC()
	parent := taskrepo.NewTask("task_1", "tenant_1", "source-agent", "v1", "policy_default", "parent", "delegate", now)
	if err := tasks.Create(context.Background(), parent); err != nil {
		t.Fatal(err)
	}
	source := contracts.AgentDefinition{
		AgentID: "source-agent",
		Version: "v1",
		Collaborators: []contracts.AgentCollaboratorRef{{
			AgentID: "target-agent",
		}},
	}
	target := contracts.AgentDefinition{AgentID: "target-agent", Version: "v1"}
	executor := Executor{
		Agents: loader.NewStaticLoader(source, target),
		Tasks:  tasks,
		TargetReleaseLookup: func(context.Context, contracts.TenantID, contracts.AgentID, contracts.AgentVersion) (contracts.AgentPackageVersion, bool, error) {
			return contracts.AgentPackageVersion{
				PackageVersionID: "pkgver_1",
				TenantID:         "tenant_1",
				AgentID:          "target-agent",
				Version:          "v1",
				Status:           contracts.ReleasePublished,
			}, true, nil
		},
	}

	_, _, err := executor.Execute(context.Background(), contracts.ToolCall{
		TenantID: "tenant_1",
		RunID:    "run_1",
		TaskID:   "task_1",
		ToolID:   "origin.agent.delegate",
		Arguments: map[string]any{
			"parent_task_id":           "task_1",
			"objective":                "delegate this",
			"to_agent_id":              "target-agent",
			"_retrieved_collaborators": []string{"target-agent"},
		},
	})
	assertRuntimeErrorCode(t, err, contracts.CodeHandoffDenied)
}

func TestExecuteRejectsHandoffCycle(t *testing.T) {
	tasks := taskrepo.NewInMemoryTaskRepository()
	now := time.Unix(1, 0).UTC()
	root := taskrepo.NewTask("task_root", "tenant_1", "agent-a", "v1", "policy_default", "root", "delegate", now)
	child := taskrepo.NewTask("task_child", "tenant_1", "agent-b", "v1", "policy_default", "child", "delegate", now)
	child.ParentTaskID = &root.TaskID
	if err := tasks.Create(context.Background(), root); err != nil {
		t.Fatal(err)
	}
	if err := tasks.Create(context.Background(), child); err != nil {
		t.Fatal(err)
	}
	agentB := contracts.AgentDefinition{
		AgentID: "agent-b",
		Version: "v1",
		Collaborators: []contracts.AgentCollaboratorRef{{
			AgentID: "agent-a",
		}},
	}
	agentA := contracts.AgentDefinition{AgentID: "agent-a", Version: "v1"}
	executor := Executor{
		Agents: loader.NewStaticLoader(agentA, agentB),
		Tasks:  tasks,
	}

	_, _, err := executor.Execute(context.Background(), contracts.ToolCall{
		TenantID: "tenant_1",
		RunID:    "run_1",
		TaskID:   "task_child",
		ToolID:   "origin.agent.delegate",
		Arguments: map[string]any{
			"parent_task_id":           "task_child",
			"objective":                "delegate back",
			"to_agent_id":              "agent-a",
			"_retrieved_collaborators": []string{"agent-a"},
		},
	})
	assertRuntimeErrorCode(t, err, contracts.CodeHandoffDenied)
}

func TestExecuteRejectsMaxHandoffDepth(t *testing.T) {
	tasks := taskrepo.NewInMemoryTaskRepository()
	now := time.Unix(1, 0).UTC()
	root := taskrepo.NewTask("task_root", "tenant_1", "agent-a", "v1", "policy_default", "root", "delegate", now)
	child := taskrepo.NewTask("task_child", "tenant_1", "agent-b", "v1", "policy_default", "child", "delegate", now)
	child.ParentTaskID = &root.TaskID
	if err := tasks.Create(context.Background(), root); err != nil {
		t.Fatal(err)
	}
	if err := tasks.Create(context.Background(), child); err != nil {
		t.Fatal(err)
	}
	agentB := contracts.AgentDefinition{
		AgentID: "agent-b",
		Version: "v1",
		Collaborators: []contracts.AgentCollaboratorRef{{
			AgentID: "agent-c",
		}},
		Strategies: contracts.AgentStrategies{
			Collaboration: contracts.CollaborationStrategy{
				MaxHandoffDepth: contracts.IntPtr(1),
			},
		},
	}
	agentC := contracts.AgentDefinition{AgentID: "agent-c", Version: "v1"}
	executor := Executor{
		Agents: loader.NewStaticLoader(agentB, agentC),
		Tasks:  tasks,
	}

	_, _, err := executor.Execute(context.Background(), contracts.ToolCall{
		TenantID: "tenant_1",
		RunID:    "run_1",
		TaskID:   "task_child",
		ToolID:   "origin.agent.delegate",
		Arguments: map[string]any{
			"parent_task_id":           "task_child",
			"objective":                "delegate deeper",
			"to_agent_id":              "agent-c",
			"_retrieved_collaborators": []string{"agent-c"},
		},
	})
	assertRuntimeErrorCode(t, err, contracts.CodeHandoffDenied)
}

func TestExecuteRejectsMaxChildTasksFromCollaborationStrategy(t *testing.T) {
	tasks := taskrepo.NewInMemoryTaskRepository()
	now := time.Unix(1, 0).UTC()
	parent := taskrepo.NewTask("task_parent", "tenant_1", "agent-a", "v1", "policy_default", "parent", "delegate", now)
	existingChild := taskrepo.NewTask("task_existing_child", "tenant_1", "agent-b", "v1", "policy_default", "child", "delegate", now)
	existingChild.ParentTaskID = &parent.TaskID
	if err := tasks.Create(context.Background(), parent); err != nil {
		t.Fatal(err)
	}
	if err := tasks.Create(context.Background(), existingChild); err != nil {
		t.Fatal(err)
	}
	agentA := contracts.AgentDefinition{
		AgentID: "agent-a",
		Version: "v1",
		Collaborators: []contracts.AgentCollaboratorRef{{
			AgentID: "agent-c",
		}},
		Strategies: contracts.AgentStrategies{
			Collaboration: contracts.CollaborationStrategy{
				MaxChildTasks: contracts.IntPtr(1),
			},
		},
	}
	agentC := contracts.AgentDefinition{AgentID: "agent-c", Version: "v1"}
	executor := Executor{
		Agents: loader.NewStaticLoader(agentA, agentC),
		Tasks:  tasks,
	}

	_, _, err := executor.Execute(context.Background(), contracts.ToolCall{
		TenantID: "tenant_1",
		RunID:    "run_1",
		TaskID:   "task_parent",
		ToolID:   "origin.agent.delegate",
		Arguments: map[string]any{
			"parent_task_id":           "task_parent",
			"objective":                "delegate another child",
			"to_agent_id":              "agent-c",
			"_retrieved_collaborators": []string{"agent-c"},
		},
	})
	assertRuntimeErrorCode(t, err, contracts.CodeHandoffDenied)
}

func TestExecuteAppliesCollaborationHandoffDefaults(t *testing.T) {
	tasks := taskrepo.NewInMemoryTaskRepository()
	events := taskrepo.NewInMemoryEventRepository()
	taskRuntime := taskruntime.NewService(tasks, events)
	now := time.Unix(1, 0).UTC()
	parent := taskrepo.NewTask("task_parent", "tenant_1", "agent-a", "v1", "policy_default", "parent", "delegate", now)
	if err := tasks.Create(context.Background(), parent); err != nil {
		t.Fatal(err)
	}
	agentA := contracts.AgentDefinition{
		AgentID: "agent-a",
		Version: "v1",
		Collaborators: []contracts.AgentCollaboratorRef{{
			AgentID: "agent-b",
		}},
		Strategies: contracts.AgentStrategies{
			Collaboration: contracts.CollaborationStrategy{
				DefaultHandoffMode: contracts.HandoffSummaryOnly,
				MaxContextTokens:   contracts.IntPtr(3),
			},
		},
	}
	agentB := contracts.AgentDefinition{AgentID: "agent-b", Version: "v1"}
	executor := Executor{
		Agents:   loader.NewStaticLoader(agentA, agentB),
		Tasks:    tasks,
		Handoffs: taskhandoff.NewService(taskRuntime, tasks, events),
		StartTaskRun: func(context.Context, contracts.AgentEnvelope, contracts.Task) (RunResult, error) {
			return RunResult{Status: contracts.RunRunning}, nil
		},
	}

	output, _, err := executor.Execute(context.Background(), contracts.ToolCall{
		TenantID: "tenant_1",
		RunID:    "run_1",
		TaskID:   "task_parent",
		ToolID:   "origin.agent.delegate",
		Arguments: map[string]any{
			"parent_task_id":           "task_parent",
			"objective":                "one two three four five",
			"to_agent_id":              "agent-b",
			"_retrieved_collaborators": []string{"agent-b"},
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	pkg, ok := output["package"].(contracts.HandoffContextPackage)
	if !ok {
		t.Fatalf("expected handoff package in output, got %#v", output["package"])
	}
	if pkg.Mode != contracts.HandoffSummaryOnly {
		t.Fatalf("expected collaboration default handoff mode, got %s", pkg.Mode)
	}
	if len(strings.Fields(pkg.Summary)) > 3 {
		t.Fatalf("expected collaboration max_context_tokens to compress summary, got %q", pkg.Summary)
	}
	if !containsHandoffConstraint(pkg.Constraints, "handoff context compressed by max_context_tokens policy") {
		t.Fatalf("expected max_context_tokens constraint, got %#v", pkg.Constraints)
	}
}

func assertRuntimeErrorCode(t *testing.T, err error, code contracts.ErrorCode) {
	t.Helper()
	if err == nil {
		t.Fatalf("expected %s error", code)
	}
	runtimeErr, ok := err.(*contracts.RuntimeError)
	if !ok {
		t.Fatalf("expected RuntimeError, got %T: %v", err, err)
	}
	if runtimeErr.Code != code {
		t.Fatalf("expected %s, got %s", code, runtimeErr.Code)
	}
}

func containsHandoffConstraint(values []string, target string) bool {
	for _, value := range values {
		if value == target {
			return true
		}
	}
	return false
}
