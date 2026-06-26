package server

import (
	"net/http"

	"znt/internal/app/auth"
	"znt/internal/app/core"
	"znt/internal/contracts"
)

func externalDeliveryReplay(r *http.Request, appCore *core.Core, envelope contracts.AgentEnvelope, caller auth.CallerIdentity) (any, error) {
	if appCore.ArrayBridge == nil {
		return nil, contracts.NewRuntimeError(contracts.CodeExecutionDomainUnavailable, "external bridge is not configured", nil)
	}
	outboxID := payloadString(envelope.Payload, "outbox_id")
	if outboxID != "" {
		item, err := appCore.ArrayBridge.ReplayDelivery(r.Context(), caller.TenantID, outboxID)
		if err != nil {
			return nil, err
		}
		return map[string]any{"replayed": []contracts.ExternalDeliveryOutboxItem{item}, "count": 1}, nil
	}
	statuses := stringSlice(envelope.Payload["statuses"])
	limit := payloadInt(envelope.Payload, "limit")
	items, err := appCore.ArrayBridge.ReplayDueDeliveries(r.Context(), caller.TenantID, statuses, limit)
	if err != nil {
		return nil, err
	}
	return map[string]any{"replayed": items, "count": len(items)}, nil
}
