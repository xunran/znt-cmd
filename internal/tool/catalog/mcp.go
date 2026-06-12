package catalog

import (
	"context"
	"encoding/json"
	"net/http"
	"strings"
	"time"

	"znt/internal/contracts"
	"znt/internal/governance/trace"
	"znt/pkg/idgen"
)

const mcpMaxCatalogPages = 100

type mcpJSONRPCRequest struct {
	JSONRPC string `json:"jsonrpc"`
	ID      string `json:"id"`
	Method  string `json:"method"`
	Params  any    `json:"params,omitempty"`
}

type mcpJSONRPCResponse struct {
	JSONRPC string          `json:"jsonrpc"`
	ID      any             `json:"id,omitempty"`
	Result  json.RawMessage `json:"result,omitempty"`
	Error   *mcpRPCError    `json:"error,omitempty"`
}

type mcpRPCError struct {
	Code    int    `json:"code"`
	Message string `json:"message"`
	Data    any    `json:"data,omitempty"`
}

type mcpToolsListResult struct {
	Tools      []mcpTool `json:"tools"`
	NextCursor string    `json:"nextCursor,omitempty"`
}

type mcpTool struct {
	Name         string         `json:"name"`
	Title        string         `json:"title,omitempty"`
	Description  string         `json:"description,omitempty"`
	InputSchema  map[string]any `json:"inputSchema,omitempty"`
	OutputSchema map[string]any `json:"outputSchema,omitempty"`
	Annotations  map[string]any `json:"annotations,omitempty"`
}

type mcpToolCallResult struct {
	Content           []map[string]any `json:"content,omitempty"`
	StructuredContent map[string]any   `json:"structuredContent,omitempty"`
	IsError           bool             `json:"isError,omitempty"`
	Meta              map[string]any   `json:"_meta,omitempty"`
}

func executorUsesProvider(executorType string) bool {
	switch executorType {
	case ExecutorTypeStaticToolHost, ExecutorTypeAgentPlugin, ExecutorTypeMCP, ExecutorTypeHTTPAPIAdapter, ExecutorTypeDatabaseAdapter:
		return true
	default:
		return false
	}
}

func executorTypeForProvider(providerType string) string {
	switch providerType {
	case ProviderTypeStaticToolHost:
		return ExecutorTypeStaticToolHost
	case ProviderTypeAgentPlugin:
		return ExecutorTypeAgentPlugin
	case ProviderTypeMCP:
		return ExecutorTypeMCP
	case ProviderTypeHTTPAPIAdapter:
		return ExecutorTypeHTTPAPIAdapter
	case ProviderTypeDatabaseAdapter:
		return ExecutorTypeDatabaseAdapter
	default:
		return ""
	}
}

func (s *Service) fetchMCPCatalog(ctx context.Context, provider ToolProvider) (providerCatalog, error) {
	var out providerCatalog
	cursor := ""
	for page := 0; page < mcpMaxCatalogPages; page++ {
		params := map[string]any{}
		if cursor != "" {
			params["cursor"] = cursor
		}
		var result mcpToolsListResult
		if err := s.mcpRequest(ctx, provider, "tools/list", params, &result); err != nil {
			return providerCatalog{}, err
		}
		for _, tool := range result.Tools {
			mapped, ok := mapMCPTool(provider, tool)
			if ok {
				out.Tools = append(out.Tools, mapped)
			}
		}
		cursor = strings.TrimSpace(result.NextCursor)
		if cursor == "" {
			return out, nil
		}
	}
	return providerCatalog{}, contracts.NewRuntimeError(contracts.CodeToolExecutionFailed, "mcp tools/list pagination did not finish", map[string]any{"provider_id": provider.ProviderID})
}

func (s *Service) mcpRequest(ctx context.Context, provider ToolProvider, method string, params any, target any) error {
	connection, err := s.providerConnection(ctx, provider)
	if err != nil {
		return err
	}
	return mcpRequest(ctx, s.client, provider.ProviderID, connection.BaseURL, connectionHeaders(connection), connection.TimeoutMS, connection.RetryMax, method, params, target)
}

func mcpRequest(ctx context.Context, client *http.Client, providerID string, endpoint string, headers map[string]string, timeoutMS int, retryMax int, method string, params any, target any) error {
	payload := mcpJSONRPCRequest{
		JSONRPC: "2.0",
		ID:      idgen.New("mcprpc"),
		Method:  method,
		Params:  params,
	}
	var response mcpJSONRPCResponse
	if err := requestJSON(ctx, client, http.MethodPost, endpoint, nil, payload, &response, requestOptions{
		Headers:   headers,
		TimeoutMS: timeoutMS,
		RetryMax:  retryMax,
	}); err != nil {
		return err
	}
	if response.Error != nil {
		return contracts.NewRuntimeError(contracts.CodeToolExecutionFailed, "mcp json-rpc error", map[string]any{
			"provider_id": providerID,
			"method":      method,
			"rpc_code":    response.Error.Code,
			"rpc_message": response.Error.Message,
			"rpc_data":    response.Error.Data,
		})
	}
	if target == nil {
		return nil
	}
	if len(response.Result) == 0 {
		return contracts.NewRuntimeError(contracts.CodeToolExecutionFailed, "mcp json-rpc response result is empty", map[string]any{
			"provider_id": providerID,
			"method":      method,
		})
	}
	if err := json.Unmarshal(response.Result, target); err != nil {
		return contracts.NewRuntimeError(contracts.CodeToolExecutionFailed, "decode mcp json-rpc result failed", map[string]any{
			"provider_id": providerID,
			"method":      method,
			"error":       err.Error(),
		})
	}
	return nil
}

func mapMCPTool(provider ToolProvider, tool mcpTool) (providerCatalogTool, bool) {
	operation := strings.TrimSpace(tool.Name)
	if operation == "" {
		return providerCatalogTool{}, false
	}
	name := strings.TrimSpace(tool.Title)
	if name == "" {
		name = operation
	}
	description := strings.TrimSpace(tool.Description)
	if description == "" {
		description = "MCP tool " + operation
	}
	return providerCatalogTool{
		ToolID:       strings.TrimSpace(provider.ProviderID) + "." + sanitizeToolIDSegment(operation),
		GroupID:      strings.TrimSpace(provider.ProviderID),
		Operation:    operation,
		Name:         name,
		Description:  description,
		WhenToUse:    []string{description},
		InputSchema:  tool.InputSchema,
		OutputSchema: tool.OutputSchema,
		RiskLevel:    riskFromMCPAnnotations(tool.Annotations),
		Visibility:   contracts.ToolProtected,
		Version:      "v1",
	}, true
}

func sanitizeToolIDSegment(value string) string {
	value = strings.TrimSpace(value)
	replacer := strings.NewReplacer(" ", "_", "/", "_", "\\", "_", ":", "_", "\t", "_", "\n", "_", "\r", "_")
	return replacer.Replace(value)
}

func riskFromMCPAnnotations(annotations map[string]any) contracts.RiskLevel {
	if boolAnnotation(annotations, "destructiveHint") {
		return contracts.RiskHigh
	}
	if boolAnnotation(annotations, "openWorldHint") {
		return contracts.RiskMedium
	}
	return contracts.RiskLow
}

func boolAnnotation(values map[string]any, key string) bool {
	if len(values) == 0 {
		return false
	}
	switch value := values[key].(type) {
	case bool:
		return value
	case string:
		return strings.EqualFold(value, "true")
	default:
		return false
	}
}

type MCPExecutor struct {
	Endpoint     string
	ProviderID   string
	ConnectionID string
	Operation    string
	Headers      map[string]string
	TimeoutMS    int
	RetryMax     int
	Client       *http.Client
	TenantID     contracts.TenantID
	Trace        trace.Recorder
	Now          func() time.Time
}

func (e MCPExecutor) NetworkTargetHost() string {
	return urlHost(e.Endpoint)
}

func (e MCPExecutor) Execute(ctx context.Context, call contracts.ToolCall) (map[string]any, []contracts.ArtifactRef, error) {
	if e.TenantID != "" && call.TenantID != e.TenantID {
		return nil, nil, contracts.NewRuntimeError(contracts.CodeToolPolicyDenied, "tool tenant does not match call tenant", nil)
	}
	started := executorNow(e.Now)
	e.recordTrace(ctx, call, contracts.TraceToolProviderInvoked, map[string]any{
		"provider_id":   e.ProviderID,
		"provider_type": ProviderTypeMCP,
		"tool_id":       call.ToolID,
		"tool_call_id":  call.ToolCallID,
		"operation":     e.Operation,
		"connection_id": e.ConnectionID,
	})
	params := map[string]any{
		"name":      e.Operation,
		"arguments": call.Arguments,
	}
	var result mcpToolCallResult
	if err := mcpRequest(ctx, e.Client, e.ProviderID, e.Endpoint, e.Headers, e.TimeoutMS, e.RetryMax, "tools/call", params, &result); err != nil {
		e.recordFailed(ctx, call, started, err)
		return nil, nil, err
	}
	output := normalizeMCPToolOutput(result)
	if result.IsError {
		output["isError"] = true
		err := contracts.NewRuntimeError(contracts.CodeToolExecutionFailed, "mcp tool returned an error result", map[string]any{
			"provider_id": e.ProviderID,
			"operation":   e.Operation,
			"output":      output,
		})
		e.recordFailed(ctx, call, started, err)
		return nil, nil, err
	}
	e.recordTrace(ctx, call, contracts.TraceToolProviderCompleted, map[string]any{
		"provider_id":   e.ProviderID,
		"provider_type": ProviderTypeMCP,
		"tool_id":       call.ToolID,
		"tool_call_id":  call.ToolCallID,
		"operation":     e.Operation,
		"connection_id": e.ConnectionID,
		"latency_ms":    int(executorNow(e.Now).Sub(started).Milliseconds()),
	})
	return output, nil, nil
}

func normalizeMCPToolOutput(result mcpToolCallResult) map[string]any {
	if result.StructuredContent != nil {
		return cloneMap(result.StructuredContent)
	}
	output := map[string]any{}
	if len(result.Content) > 0 {
		output["content"] = result.Content
	}
	if result.Meta != nil {
		output["_meta"] = result.Meta
	}
	return output
}

func cloneMap(values map[string]any) map[string]any {
	if len(values) == 0 {
		return map[string]any{}
	}
	out := make(map[string]any, len(values))
	for key, value := range values {
		out[key] = value
	}
	return out
}

func (e MCPExecutor) recordFailed(ctx context.Context, call contracts.ToolCall, started time.Time, err error) {
	e.recordTrace(ctx, call, contracts.TraceToolProviderFailed, map[string]any{
		"provider_id":   e.ProviderID,
		"provider_type": ProviderTypeMCP,
		"tool_id":       call.ToolID,
		"tool_call_id":  call.ToolCallID,
		"operation":     e.Operation,
		"connection_id": e.ConnectionID,
		"latency_ms":    int(executorNow(e.Now).Sub(started).Milliseconds()),
		"error_code":    errorCode(err),
	})
}

func (e MCPExecutor) recordTrace(ctx context.Context, call contracts.ToolCall, eventType string, payload map[string]any) {
	if e.Trace == nil || call.TraceID == "" {
		return
	}
	_ = e.Trace.Record(ctx, contracts.TraceEvent{
		TraceID:   call.TraceID,
		TenantID:  call.TenantID,
		SpanID:    contracts.SpanID(idgen.New("span")),
		RunID:     call.RunID,
		TaskID:    call.TaskID,
		Type:      eventType,
		Payload:   payload,
		CreatedAt: executorNow(e.Now),
	})
}
