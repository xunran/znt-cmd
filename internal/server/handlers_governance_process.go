package server

import (
	"net/http"
	"strings"

	"znt/internal/app/auth"
	"znt/internal/app/core"
	"znt/internal/contracts"
	processgovernance "znt/internal/governance/process"
)

func handleGovernanceTemplates(w http.ResponseWriter, r *http.Request, appCore *core.Core, caller auth.CallerIdentity) {
	if appCore.GovernanceProcesses == nil {
		writeRuntimeError(w, contracts.NewRuntimeError(contracts.CodeToolPolicyDenied, "governance process service is unavailable", nil))
		return
	}
	if r.Method != http.MethodPost && r.Method != http.MethodPut {
		writeError(w, contracts.NewRuntimeError(contracts.CodeDecisionSchemaError, "unsupported governance template method", nil), http.StatusMethodNotAllowed)
		return
	}
	payload, ok := decodeMapPayload(w, r, "invalid governance template json")
	if !ok {
		return
	}
	template, err := appCore.GovernanceProcesses.UpsertTemplate(r.Context(), processgovernance.UpsertTemplateInput{
		TenantID:   caller.TenantID,
		TemplateID: contracts.GovernanceProcessTemplateID(payloadString(payload, "template_id")),
		Name:       payloadString(payload, "name"),
		Version:    payloadString(payload, "version"),
		Status:     payloadString(payload, "status"),
		Gates:      governanceGateDefinitions(payload["gates"]),
		Metadata:   mapPayload(payload["metadata"]),
		ActorID:    caller.CallerID,
	})
	if err != nil {
		writeRuntimeError(w, err)
		return
	}
	writeJSON(w, map[string]any{"template": template}, http.StatusCreated)
}

func handleGovernanceRuns(w http.ResponseWriter, r *http.Request, appCore *core.Core, caller auth.CallerIdentity) {
	if appCore.GovernanceProcesses == nil {
		writeRuntimeError(w, contracts.NewRuntimeError(contracts.CodeToolPolicyDenied, "governance process service is unavailable", nil))
		return
	}
	if r.Method != http.MethodPost {
		writeError(w, contracts.NewRuntimeError(contracts.CodeDecisionSchemaError, "unsupported governance run method", nil), http.StatusMethodNotAllowed)
		return
	}
	payload, ok := decodeMapPayload(w, r, "invalid governance run json")
	if !ok {
		return
	}
	snapshot, err := appCore.GovernanceProcesses.StartRun(r.Context(), processgovernance.StartRunInput{
		TenantID:    caller.TenantID,
		TemplateID:  contracts.GovernanceProcessTemplateID(payloadString(payload, "template_id")),
		SubjectType: payloadString(payload, "subject_type"),
		SubjectID:   payloadString(payload, "subject_id"),
		TaskID:      contracts.TaskID(payloadString(payload, "task_id")),
		AgentRunID:  contracts.AgentRunID(payloadString(payload, "agent_run_id")),
		TraceID:     contracts.TraceID(payloadString(payload, "trace_id")),
		PolicySetID: contracts.PolicySetID(payloadString(payload, "policy_set_id")),
		Actors:      governanceActorRefs(payload["actors"]),
		Gates:       governanceGateDefinitions(payload["gates"]),
		Metadata:    mapPayload(payload["metadata"]),
		ActorID:     caller.CallerID,
	})
	if err != nil {
		writeRuntimeError(w, err)
		return
	}
	writeJSON(w, map[string]any{"snapshot": snapshot}, http.StatusCreated)
}

func handleGovernanceRunResource(w http.ResponseWriter, r *http.Request, appCore *core.Core, caller auth.CallerIdentity, path string) {
	if appCore.GovernanceProcesses == nil {
		writeRuntimeError(w, contracts.NewRuntimeError(contracts.CodeToolPolicyDenied, "governance process service is unavailable", nil))
		return
	}
	runIDRaw, suffix, _ := strings.Cut(strings.Trim(path, "/"), "/")
	runID := contracts.GovernanceProcessRunID(runIDRaw)
	if runID == "" {
		writeError(w, contracts.NewRuntimeError(contracts.CodeDecisionSchemaError, "governance run_id is required", nil), http.StatusBadRequest)
		return
	}
	if suffix == "" {
		if r.Method != http.MethodGet {
			writeError(w, contracts.NewRuntimeError(contracts.CodeDecisionSchemaError, "unsupported governance run resource method", nil), http.StatusMethodNotAllowed)
			return
		}
		snapshot, err := appCore.GovernanceProcesses.Snapshot(r.Context(), caller.TenantID, runID)
		if err != nil {
			writeRuntimeError(w, err)
			return
		}
		writeJSON(w, map[string]any{"snapshot": snapshot}, http.StatusOK)
		return
	}
	if suffix != "gates/open" || r.Method != http.MethodPost {
		writeError(w, contracts.NewRuntimeError(contracts.CodeDecisionSchemaError, "unknown governance run action", map[string]any{"path": path}), http.StatusNotFound)
		return
	}
	payload, ok := decodeMapPayload(w, r, "invalid governance gate open json")
	if !ok {
		return
	}
	snapshot, err := appCore.GovernanceProcesses.OpenGate(r.Context(), processgovernance.OpenGateInput{
		TenantID:     caller.TenantID,
		ProcessRunID: runID,
		GateRunID:    contracts.GovernanceGateRunID(payloadString(payload, "gate_run_id")),
		GateID:       payloadString(payload, "gate_id"),
		ArtifactRefs: governanceArtifactRefs(payload["artifact_refs"]),
		EvidenceRefs: governanceEvidenceRefs(payload["evidence_refs"]),
		Metadata:     mapPayload(payload["metadata"]),
		ActorID:      caller.CallerID,
	})
	if err != nil {
		writeRuntimeError(w, err)
		return
	}
	writeJSON(w, map[string]any{"snapshot": snapshot}, http.StatusOK)
}

func handleGovernanceGateResource(w http.ResponseWriter, r *http.Request, appCore *core.Core, caller auth.CallerIdentity, path string) {
	if appCore.GovernanceProcesses == nil {
		writeRuntimeError(w, contracts.NewRuntimeError(contracts.CodeToolPolicyDenied, "governance process service is unavailable", nil))
		return
	}
	gateRunIDRaw, suffix, _ := strings.Cut(strings.Trim(path, "/"), "/")
	gateRunID := contracts.GovernanceGateRunID(gateRunIDRaw)
	if gateRunID == "" {
		writeError(w, contracts.NewRuntimeError(contracts.CodeDecisionSchemaError, "governance gate_run_id is required", nil), http.StatusBadRequest)
		return
	}
	if r.Method != http.MethodPost {
		writeError(w, contracts.NewRuntimeError(contracts.CodeDecisionSchemaError, "unsupported governance gate action method", nil), http.StatusMethodNotAllowed)
		return
	}
	payload, ok := decodeMapPayload(w, r, "invalid governance gate action json")
	if !ok {
		return
	}
	switch suffix {
	case "reviews":
		snapshot, err := appCore.GovernanceProcesses.SubmitReview(r.Context(), processgovernance.ReviewInput{
			TenantID:     caller.TenantID,
			GateRunID:    gateRunID,
			ReviewerID:   firstNonEmpty(payloadString(payload, "reviewer_id"), caller.CallerID),
			ReviewerType: firstNonEmpty(payloadString(payload, "reviewer_type"), caller.CallerType),
			Decision:     contracts.GovernanceReviewDecision(payloadString(payload, "decision")),
			Reason:       payloadString(payload, "reason"),
			EvidenceRefs: governanceEvidenceRefs(payload["evidence_refs"]),
			Independent:  boolValue(payload["independent"], true),
		})
		if err != nil {
			writeRuntimeError(w, err)
			return
		}
		writeJSON(w, map[string]any{"snapshot": snapshot}, http.StatusOK)
	case "escalate":
		snapshot, err := appCore.GovernanceProcesses.Escalate(r.Context(), processgovernance.EscalateInput{
			TenantID:    caller.TenantID,
			GateRunID:   gateRunID,
			Issue:       payloadString(payload, "issue"),
			Arguments:   governanceEvidenceRefs(payload["arguments"]),
			EscalatedTo: payloadString(payload, "escalated_to"),
			ActorID:     caller.CallerID,
		})
		if err != nil {
			writeRuntimeError(w, err)
			return
		}
		writeJSON(w, map[string]any{"snapshot": snapshot}, http.StatusOK)
	default:
		writeError(w, contracts.NewRuntimeError(contracts.CodeDecisionSchemaError, "unknown governance gate action", map[string]any{"path": path}), http.StatusNotFound)
	}
}

func handleGovernanceConflictResource(w http.ResponseWriter, r *http.Request, appCore *core.Core, caller auth.CallerIdentity, path string) {
	if appCore.GovernanceProcesses == nil {
		writeRuntimeError(w, contracts.NewRuntimeError(contracts.CodeToolPolicyDenied, "governance process service is unavailable", nil))
		return
	}
	conflictIDRaw, suffix, _ := strings.Cut(strings.Trim(path, "/"), "/")
	conflictID := contracts.GovernanceConflictID(conflictIDRaw)
	if conflictID == "" {
		writeError(w, contracts.NewRuntimeError(contracts.CodeDecisionSchemaError, "governance conflict_id is required", nil), http.StatusBadRequest)
		return
	}
	if r.Method != http.MethodPost || suffix != "arbitrate" {
		writeError(w, contracts.NewRuntimeError(contracts.CodeDecisionSchemaError, "unknown governance conflict action", map[string]any{"path": path}), http.StatusNotFound)
		return
	}
	payload, ok := decodeMapPayload(w, r, "invalid governance arbitration json")
	if !ok {
		return
	}
	snapshot, err := appCore.GovernanceProcesses.Arbitrate(r.Context(), processgovernance.ArbitrateInput{
		TenantID:     caller.TenantID,
		ConflictID:   conflictID,
		Decision:     contracts.GovernanceReviewDecision(payloadString(payload, "decision")),
		Reason:       payloadString(payload, "reason"),
		ArbitratorID: firstNonEmpty(payloadString(payload, "arbitrator_id"), caller.CallerID),
	})
	if err != nil {
		writeRuntimeError(w, err)
		return
	}
	writeJSON(w, map[string]any{"snapshot": snapshot}, http.StatusOK)
}

func governanceGateDefinitions(value any) []contracts.GovernanceGateDefinition {
	raw, ok := value.([]any)
	if !ok {
		return nil
	}
	gates := make([]contracts.GovernanceGateDefinition, 0, len(raw))
	for _, item := range raw {
		row, ok := item.(map[string]any)
		if !ok {
			continue
		}
		gates = append(gates, contracts.GovernanceGateDefinition{
			GateID:            payloadString(row, "gate_id"),
			Name:              payloadString(row, "name"),
			SubjectType:       payloadString(row, "subject_type"),
			ReviewMode:        contracts.GovernanceReviewMode(payloadString(row, "review_mode")),
			ConsensusPolicy:   contracts.GovernanceConsensusPolicy(payloadString(row, "consensus_policy")),
			EscalationPolicy:  contracts.GovernanceEscalationPolicy(payloadString(row, "escalation_policy")),
			RequiredReviewers: intValue(row["required_reviewers"], 0),
			MinApprovals:      intValue(row["min_approvals"], 0),
			ReviewerRoles:     stringSlice(row["reviewer_roles"]),
			EvidenceTypes:     stringSlice(row["evidence_types"]),
			Metadata:          mapPayload(row["metadata"]),
		})
	}
	return gates
}

func governanceActorRefs(value any) []contracts.GovernanceActorRef {
	raw, ok := value.([]any)
	if !ok {
		return nil
	}
	actors := make([]contracts.GovernanceActorRef, 0, len(raw))
	for _, item := range raw {
		row, ok := item.(map[string]any)
		if !ok {
			continue
		}
		actors = append(actors, contracts.GovernanceActorRef{
			ActorID:   payloadString(row, "actor_id"),
			ActorType: payloadString(row, "actor_type"),
			Role:      payloadString(row, "role"),
		})
	}
	return actors
}

func governanceArtifactRefs(value any) []contracts.GovernanceArtifactRef {
	raw, ok := value.([]any)
	if !ok {
		return nil
	}
	refs := make([]contracts.GovernanceArtifactRef, 0, len(raw))
	for _, item := range raw {
		row, ok := item.(map[string]any)
		if !ok {
			continue
		}
		refs = append(refs, contracts.GovernanceArtifactRef{
			ArtifactID: contracts.ArtifactID(payloadString(row, "artifact_id")),
			Type:       payloadString(row, "type"),
			URI:        payloadString(row, "uri"),
			Summary:    payloadString(row, "summary"),
			Hash:       payloadString(row, "hash"),
		})
	}
	return refs
}

func governanceEvidenceRefs(value any) []contracts.GovernanceEvidenceRef {
	raw, ok := value.([]any)
	if !ok {
		return nil
	}
	refs := make([]contracts.GovernanceEvidenceRef, 0, len(raw))
	for _, item := range raw {
		row, ok := item.(map[string]any)
		if !ok {
			continue
		}
		refs = append(refs, contracts.GovernanceEvidenceRef{
			EvidenceID:  payloadString(row, "evidence_id"),
			Type:        payloadString(row, "type"),
			TraceID:     contracts.TraceID(payloadString(row, "trace_id")),
			AuditID:     payloadString(row, "audit_id"),
			ArtifactID:  contracts.ArtifactID(payloadString(row, "artifact_id")),
			TaskID:      contracts.TaskID(payloadString(row, "task_id")),
			RunID:       contracts.AgentRunID(payloadString(row, "run_id")),
			URI:         payloadString(row, "uri"),
			Summary:     payloadString(row, "summary"),
			SubmittedBy: payloadString(row, "submitted_by"),
			Metadata:    mapPayload(row["metadata"]),
		})
	}
	return refs
}
