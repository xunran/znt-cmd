package server

import (
	"net/http"
	"time"

	"znt/internal/app/auth"
	"znt/internal/app/core"
	"znt/internal/contracts"
	"znt/pkg/idgen"
)

func handleTaskStartResource(w http.ResponseWriter, r *http.Request, appCore *core.Core, caller auth.CallerIdentity) {
	if r.Method != http.MethodPost {
		writeError(w, contracts.NewRuntimeError(contracts.CodeDecisionSchemaError, "unsupported tasks/start method", nil), http.StatusMethodNotAllowed)
		return
	}
	payload, ok := decodeMapPayload(w, r, "invalid task start json")
	if !ok {
		return
	}
	traceID := contracts.TraceID(payloadString(payload, "trace_id"))
	if traceID == "" {
		traceID = contracts.TraceID(idgen.New("trace"))
	}
	agentID := contracts.AgentID(payloadString(payload, "agent_id"))
	version := contracts.AgentVersion(payloadString(payload, "agent_version"))
	envelope := contracts.AgentEnvelope{
		EnvelopeID: idgen.New("env"),
		TraceID:    traceID,
		Command:    "task.start",
		Target:     contracts.AgentTarget{AgentID: agentID, Version: version},
		Payload:    payload,
		Context:    contracts.RuntimeContext{TenantID: caller.TenantID},
		Caller:     contracts.AgentCaller{CallerID: caller.CallerID, CallerType: caller.CallerType, DisplayName: caller.DisplayName, TenantID: caller.TenantID},
		CreatedAt:  time.Now().UTC(),
	}
	result, err := taskStart(r, appCore, envelope, caller)
	if err != nil {
		writeRuntimeError(w, err)
		return
	}
	writeJSON(w, result, http.StatusCreated)
}
