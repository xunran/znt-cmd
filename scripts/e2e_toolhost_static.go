package main

import (
	"bufio"
	"encoding/json"
	"flag"
	"fmt"
	"log"
	"net/http"
	"os"
	"sync/atomic"
	"time"
)

type catalogResponse struct {
	Tools []catalogTool `json:"tools"`
}

type catalogTool struct {
	ToolID       string         `json:"tool_id"`
	GroupID      string         `json:"group_id"`
	Operation    string         `json:"operation"`
	Name         string         `json:"name"`
	Description  string         `json:"description"`
	WhenToUse    []string       `json:"when_to_use"`
	InputSchema  map[string]any `json:"input_schema"`
	OutputSchema map[string]any `json:"output_schema"`
	RiskLevel    string         `json:"risk_level"`
	Visibility   string         `json:"visibility"`
	Version      string         `json:"version"`
}

type invokeRequest struct {
	ToolID         string         `json:"tool_id"`
	Operation      string         `json:"operation"`
	ToolCallID     string         `json:"tool_call_id"`
	TenantID       string         `json:"tenant_id"`
	IdempotencyKey string         `json:"idempotency_key"`
	Arguments      map[string]any `json:"arguments"`
}

type invokeResponse struct {
	Output       map[string]any `json:"output"`
	ArtifactRefs []any          `json:"artifact_refs"`
}

func main() {
	addr := flag.String("addr", "127.0.0.1:0", "listen address")
	hostName := flag.String("name", "toolhost", "host name")
	toolID := flag.String("tool-id", "", "tool id")
	groupID := flag.String("group-id", "", "group id")
	operation := flag.String("operation", "", "operation")
	delayMS := flag.Int("delay-ms", 0, "invoke delay in milliseconds")
	logPath := flag.String("log-path", "", "ndjson invocation log path")
	flag.Parse()

	if *toolID == "" || *groupID == "" || *operation == "" {
		log.Fatal("tool-id, group-id and operation are required")
	}

	var logFile *os.File
	var logWriter *bufio.Writer
	if *logPath != "" {
		var err error
		logFile, err = os.OpenFile(*logPath, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o666)
		if err != nil {
			log.Fatalf("open log: %v", err)
		}
		defer logFile.Close()
		logWriter = bufio.NewWriter(logFile)
		defer logWriter.Flush()
	}

	var invokeCount int64
	mux := http.NewServeMux()
	mux.HandleFunc("/healthz", func(w http.ResponseWriter, r *http.Request) {
		writeJSON(w, http.StatusOK, map[string]any{"status": "ok", "service": *hostName})
	})
	mux.HandleFunc("/tools/catalog", func(w http.ResponseWriter, r *http.Request) {
		writeJSON(w, http.StatusOK, catalogResponse{Tools: []catalogTool{{
			ToolID:       *toolID,
			GroupID:      *groupID,
			Operation:    *operation,
			Name:         fmt.Sprintf("%s tool", *hostName),
			Description:  fmt.Sprintf("%s ToolHost operation", *hostName),
			WhenToUse:    []string{*operation},
			InputSchema:  map[string]any{"type": "object"},
			OutputSchema: map[string]any{"type": "object"},
			RiskLevel:    "low",
			Visibility:   "protected",
			Version:      "v1",
		}}})
	})
	mux.HandleFunc("/tools/invoke", func(w http.ResponseWriter, r *http.Request) {
		defer r.Body.Close()
		var req invokeRequest
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			writeJSON(w, http.StatusBadRequest, map[string]any{"error": map[string]any{"code": "invalid_json", "message": err.Error()}})
			return
		}
		count := atomic.AddInt64(&invokeCount, 1)
		if logWriter != nil {
			entry := map[string]any{
				"at":              time.Now().UTC().Format(time.RFC3339Nano),
				"host":            *hostName,
				"tool_id":         req.ToolID,
				"operation":       req.Operation,
				"idempotency_key": req.IdempotencyKey,
				"delay_ms":        *delayMS,
			}
			_ = json.NewEncoder(logWriter).Encode(entry)
			_ = logWriter.Flush()
		}
		if *delayMS > 0 {
			time.Sleep(time.Duration(*delayMS) * time.Millisecond)
		}
		writeJSON(w, http.StatusOK, invokeResponse{
			Output: map[string]any{
				"host":         *hostName,
				"operation":    req.Operation,
				"tool_id":      req.ToolID,
				"tenant_id":    req.TenantID,
				"delay_ms":     *delayMS,
				"invoke_count": count,
			},
			ArtifactRefs: []any{},
		})
	})

	log.Printf("starting %s at %s", *hostName, *addr)
	if err := http.ListenAndServe(*addr, mux); err != nil {
		log.Fatal(err)
	}
}

func writeJSON(w http.ResponseWriter, status int, value any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(value)
}
