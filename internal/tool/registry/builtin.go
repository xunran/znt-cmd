package registry

import (
	"context"
	"fmt"
	"time"

	"znt/internal/asset/artifact"
	"znt/internal/contracts"
	"znt/pkg/hash"
	"znt/pkg/idgen"
)

type EchoExecutor struct{}

func (EchoExecutor) Execute(_ context.Context, call contracts.ToolCall) (map[string]any, []contracts.ArtifactRef, error) {
	return map[string]any{"echo": call.Arguments}, nil, nil
}

func RegisterBuiltins(registry Registry) error {
	return RegisterBuiltinsWithArtifacts(registry, nil)
}

type ArtifactCreateExecutor struct {
	Store artifact.Store
	Now   func() time.Time
}

func (e ArtifactCreateExecutor) Execute(ctx context.Context, call contracts.ToolCall) (map[string]any, []contracts.ArtifactRef, error) {
	name, _ := call.Arguments["name"].(string)
	if name == "" {
		name = "generated artifact"
	}
	artifactType, _ := call.Arguments["type"].(string)
	if artifactType == "" {
		artifactType = "text"
	}
	content, _ := call.Arguments["content"].(string)
	if content == "" {
		return nil, nil, fmt.Errorf("artifact.create requires content")
	}
	now := time.Now().UTC()
	if e.Now != nil {
		now = e.Now()
	}
	contentHash := hash.String(content)
	artifactID := contracts.ArtifactID(idgen.New("artifact"))
	stored := contracts.Artifact{
		ArtifactID: artifactID,
		TenantID:   call.TenantID,
		Type:       artifactType,
		Name:       name,
		StorageURI: fmt.Sprintf("memory://artifacts/%s", artifactID),
		MimeType:   "text/plain",
		SizeBytes:  int64(len(content)),
		Hash:       contentHash,
		CreatedBy:  string(call.RunID),
		CreatedAt:  now,
	}
	if e.Store != nil {
		if contentStore, ok := e.Store.(artifact.ContentStore); ok {
			if err := contentStore.CreateArtifactWithContent(ctx, stored, content); err != nil {
				return nil, nil, err
			}
		} else {
			if err := e.Store.CreateArtifact(ctx, stored); err != nil {
				return nil, nil, err
			}
		}
	}
	ref := contracts.ArtifactRef{
		ArtifactID: artifactID,
		Type:       artifactType,
		URI:        stored.StorageURI,
		Summary:    name,
		Hash:       contentHash,
	}
	return map[string]any{"artifact_id": artifactID, "uri": stored.StorageURI, "hash": contentHash}, []contracts.ArtifactRef{ref}, nil
}

func RegisterBuiltinsWithArtifacts(registry Registry, store artifact.Store) error {
	if err := registry.Register(Tool{
		Definition: contracts.ToolDefinition{
			ToolID:           "echo",
			GroupID:          "core",
			Name:             "echo",
			Description:      "Returns input arguments. Used for runtime and contract tests.",
			InputSchema:      map[string]any{"type": "object"},
			OutputSchema:     map[string]any{"type": "object"},
			RiskLevel:        contracts.RiskLow,
			Visibility:       contracts.ToolExposed,
			ExecutionProfile: "local",
			Version:          "v1",
		},
		Executor:  EchoExecutor{},
		WhenToUse: []string{"smoke tests", "debugging"},
	}); err != nil {
		return err
	}
	if err := registry.Register(Tool{
		Definition: contracts.ToolDefinition{
			ToolID:      "artifact.create",
			GroupID:     "core",
			Name:        "artifact.create",
			Description: "Creates a text artifact and returns an ArtifactRef for later context.",
			InputSchema: map[string]any{
				"type":     "object",
				"required": []any{"content"},
				"properties": map[string]any{
					"name":    map[string]any{"type": "string"},
					"type":    map[string]any{"type": "string"},
					"content": map[string]any{"type": "string"},
				},
			},
			OutputSchema:     map[string]any{"type": "object"},
			RiskLevel:        contracts.RiskLow,
			Visibility:       contracts.ToolProtected,
			ExecutionProfile: "local",
			Version:          "v1",
		},
		Executor:  ArtifactCreateExecutor{Store: store},
		WhenToUse: []string{"create durable summaries", "produce report artifacts"},
	}); err != nil {
		return err
	}
	return nil
}

func RegisterInternal(registry Registry, tool Tool) error {
	return registry.Register(tool)
}
