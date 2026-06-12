package handoff

import (
	"context"
	"testing"
	"time"

	"znt/internal/agentdef/loader"
	"znt/internal/contracts"
	taskrepo "znt/internal/task/repository"
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
				Status:           contracts.ReleaseDraft,
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
		Runtime: contracts.RuntimeLimits{MaxHandoffDepth: 1},
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
