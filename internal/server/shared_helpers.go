package server

import "znt/internal/contracts"

func sameTenant(resourceTenant contracts.TenantID, callerTenant contracts.TenantID) bool {
	return resourceTenant != "" && resourceTenant == callerTenant
}

func traceEventsForTenant(events []contracts.TraceEvent, tenantID contracts.TenantID) ([]contracts.TraceEvent, bool) {
	if len(events) == 0 {
		return events, true
	}
	filtered := make([]contracts.TraceEvent, 0, len(events))
	for _, event := range events {
		if event.TenantID != tenantID {
			return nil, false
		}
		filtered = append(filtered, event)
	}
	return filtered, true
}

func contains(values []string, target string) bool {
	for _, value := range values {
		if value == target {
			return true
		}
	}
	return false
}
