package registry

import (
	"context"
	"fmt"
	"sync"

	"znt/internal/contracts"
)

type Executor interface {
	Execute(ctx context.Context, call contracts.ToolCall) (map[string]any, []contracts.ArtifactRef, error)
}

type Tool struct {
	Definition contracts.ToolDefinition
	Executor   Executor
	WhenToUse  []string
	TenantID   contracts.TenantID
}

type Registry interface {
	Register(tool Tool) error
	Upsert(tool Tool) error
	Unregister(toolID string)
	UnregisterForTenant(tenantID contracts.TenantID, toolID string)
	Get(toolID string) (Tool, bool)
	GetForTenant(tenantID contracts.TenantID, toolID string) (Tool, bool)
	Card(toolID string) (contracts.ToolCard, bool)
	Cards() []contracts.ToolCard
	CardsForTenant(tenantID contracts.TenantID) []contracts.ToolCard
}

type InMemoryRegistry struct {
	mu    sync.RWMutex
	tools map[string]Tool
}

func NewInMemoryRegistry() *InMemoryRegistry {
	return &InMemoryRegistry{tools: map[string]Tool{}}
}

func (r *InMemoryRegistry) Register(tool Tool) error {
	if err := validateTool(tool); err != nil {
		return err
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	if err := r.checkConflictLocked(tool); err != nil {
		return err
	}
	key := toolKey(tool.TenantID, tool.Definition.ToolID)
	if _, exists := r.tools[key]; exists {
		return fmt.Errorf("tool %s is already registered", tool.Definition.ToolID)
	}
	r.tools[key] = tool
	return nil
}

func (r *InMemoryRegistry) Upsert(tool Tool) error {
	if err := validateTool(tool); err != nil {
		return err
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	if err := r.checkConflictLocked(tool); err != nil {
		return err
	}
	r.tools[toolKey(tool.TenantID, tool.Definition.ToolID)] = tool
	return nil
}

func (r *InMemoryRegistry) Unregister(toolID string) {
	r.mu.Lock()
	defer r.mu.Unlock()
	delete(r.tools, toolKey("", toolID))
}

func (r *InMemoryRegistry) UnregisterForTenant(tenantID contracts.TenantID, toolID string) {
	r.mu.Lock()
	defer r.mu.Unlock()
	delete(r.tools, toolKey(tenantID, toolID))
}

func (r *InMemoryRegistry) Get(toolID string) (Tool, bool) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	tool, ok := r.tools[toolKey("", toolID)]
	return tool, ok
}

func (r *InMemoryRegistry) GetForTenant(tenantID contracts.TenantID, toolID string) (Tool, bool) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	if tenantID != "" {
		if tool, ok := r.tools[toolKey(tenantID, toolID)]; ok {
			return tool, true
		}
	}
	tool, ok := r.tools[toolKey("", toolID)]
	return tool, ok
}

func (r *InMemoryRegistry) Card(toolID string) (contracts.ToolCard, bool) {
	tool, ok := r.Get(toolID)
	if !ok {
		return contracts.ToolCard{}, false
	}
	return RenderCard(tool), true
}

func (r *InMemoryRegistry) Cards() []contracts.ToolCard {
	r.mu.RLock()
	defer r.mu.RUnlock()
	out := make([]contracts.ToolCard, 0, len(r.tools))
	for _, tool := range r.tools {
		if tool.TenantID != "" {
			continue
		}
		out = append(out, RenderCard(tool))
	}
	return out
}

func (r *InMemoryRegistry) CardsForTenant(tenantID contracts.TenantID) []contracts.ToolCard {
	r.mu.RLock()
	defer r.mu.RUnlock()
	out := make([]contracts.ToolCard, 0, len(r.tools))
	for _, tool := range r.tools {
		if tool.TenantID != "" && tool.TenantID != tenantID {
			continue
		}
		out = append(out, RenderCard(tool))
	}
	return out
}

func validateTool(tool Tool) error {
	if tool.Definition.ToolID == "" || tool.Definition.Name == "" {
		return fmt.Errorf("tool_id and name are required")
	}
	if tool.Executor == nil {
		return fmt.Errorf("tool executor is required")
	}
	return nil
}

func (r *InMemoryRegistry) checkConflictLocked(tool Tool) error {
	if tool.TenantID != "" {
		if _, exists := r.tools[toolKey("", tool.Definition.ToolID)]; exists {
			return fmt.Errorf("tool %s conflicts with a global tool", tool.Definition.ToolID)
		}
		return nil
	}
	for _, existing := range r.tools {
		if existing.TenantID != "" && existing.Definition.ToolID == tool.Definition.ToolID {
			return fmt.Errorf("tool %s conflicts with a tenant scoped tool", tool.Definition.ToolID)
		}
	}
	return nil
}

func toolKey(tenantID contracts.TenantID, toolID string) string {
	return string(tenantID) + "\x00" + toolID
}

func RenderCard(tool Tool) contracts.ToolCard {
	return contracts.ToolCard{
		ToolID:      tool.Definition.ToolID,
		GroupID:     tool.Definition.GroupID,
		Name:        tool.Definition.Name,
		Description: tool.Definition.Description,
		WhenToUse:   tool.WhenToUse,
		RiskLevel:   tool.Definition.RiskLevel,
		Visibility:  tool.Definition.Visibility,
		Version:     tool.Definition.Version,
	}
}
