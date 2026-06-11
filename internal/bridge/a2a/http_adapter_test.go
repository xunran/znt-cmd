package a2a

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"

	"znt/internal/contracts"
)

func TestHTTPAdapterUsesA2AJSONRPCAndAgentCard(t *testing.T) {
	var messages int
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer token_1" {
			t.Fatalf("missing bearer token: %q", r.Header.Get("Authorization"))
		}
		switch r.URL.Path {
		case "/.well-known/agent-card.json":
			_ = json.NewEncoder(w).Encode(map[string]any{
				"name":        "Remote Planner",
				"url":         "https://remote-agent.example/a2a",
				"description": "Remote A2A planner.",
			})
		case "/tasks/get":
			req := decodeRPC(t, r)
			if req.Method != "tasks/get" || req.Params["id"] != "task_remote_1" {
				t.Fatalf("unexpected tasks/get request: %#v", req)
			}
			_ = json.NewEncoder(w).Encode(map[string]any{
				"jsonrpc": "2.0",
				"id":      req.ID,
				"result": map[string]any{
					"id":     "task_remote_1",
					"status": map[string]any{"state": "working"},
					"metadata": map[string]any{
						"title":   "Remote task",
						"summary": "Remote task summary",
					},
				},
			})
		case "/message/send":
			req := decodeRPC(t, r)
			if req.Method != "message/send" || req.Params["taskId"] != "task_remote_1" {
				t.Fatalf("unexpected message/send request: %#v", req)
			}
			messages++
			_ = json.NewEncoder(w).Encode(map[string]any{
				"jsonrpc": "2.0",
				"id":      req.ID,
				"result":  map[string]any{"id": "message_1"},
			})
		default:
			http.NotFound(w, r)
		}
	}))
	defer server.Close()

	adapter := NewHTTPAdapter(server.URL, "token_1")
	ref := contracts.ExternalTaskRef{Provider: "a2a", ExternalTaskID: "task_remote_1"}
	summary, err := adapter.GetTask(context.Background(), ref)
	if err != nil {
		t.Fatal(err)
	}
	if summary.Title != "Remote task" || summary.Status != "working" || summary.Summary != "Remote task summary" {
		t.Fatalf("unexpected task summary: %#v", summary)
	}
	participants, err := adapter.GetParticipants(context.Background(), ref)
	if err != nil {
		t.Fatal(err)
	}
	if len(participants) != 1 || participants[0].Type != "a2a_agent" || participants[0].Name != "Remote Planner" {
		t.Fatalf("unexpected participants: %#v", participants)
	}
	if err := adapter.SendMessage(context.Background(), contracts.SendExternalMessageRequest{Ref: ref, Message: "hello"}); err != nil {
		t.Fatal(err)
	}
	if err := adapter.AttachArtifact(context.Background(), contracts.AttachArtifactRequest{Ref: ref, ArtifactRef: contracts.ArtifactRef{ArtifactID: "artifact_1", Type: "report"}}); err != nil {
		t.Fatal(err)
	}
	decision, err := adapter.CheckAccess(context.Background(), contracts.CollaborationAccessRequest{Ref: ref, Action: "read"})
	if err != nil {
		t.Fatal(err)
	}
	if !decision.Allowed || messages != 2 {
		t.Fatalf("unexpected decision=%#v message_count=%d", decision, messages)
	}
}

func TestHTTPAdapterMapsJSONRPCError(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		req := decodeRPC(t, r)
		_ = json.NewEncoder(w).Encode(map[string]any{
			"jsonrpc": "2.0",
			"id":      req.ID,
			"error": map[string]any{
				"code":    -32001,
				"message": "task unavailable",
			},
		})
	}))
	defer server.Close()

	adapter := NewHTTPAdapter(server.URL, "")
	_, err := adapter.GetTask(context.Background(), contracts.ExternalTaskRef{Provider: "a2a", ExternalTaskID: "missing"})
	var runtimeErr *contracts.RuntimeError
	if !errors.As(err, &runtimeErr) || runtimeErr.Code != contracts.CodeExternalBridgeError {
		t.Fatalf("expected external bridge runtime error, got %T %v", err, err)
	}
}

type decodedRPC struct {
	ID     any            `json:"id"`
	Method string         `json:"method"`
	Params map[string]any `json:"params"`
}

func decodeRPC(t *testing.T, r *http.Request) decodedRPC {
	t.Helper()
	var req decodedRPC
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		t.Fatal(err)
	}
	return req
}
