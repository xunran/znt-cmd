package server

import (
	"net/url"
	"strings"

	"znt/internal/contracts"
)

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

func firstQueryValue(query url.Values, keys ...string) string {
	for _, key := range keys {
		if value := strings.TrimSpace(query.Get(key)); value != "" {
			return value
		}
	}
	return ""
}

func queryLimitOffset(query url.Values, fallbackLimit int, maxLimit int) (int, int) {
	limit := queryInt(firstQueryValue(query, "limit", "pageSize", "page_size"), fallbackLimit, maxLimit)
	offset := queryInt(firstQueryValue(query, "offset", "cursor"), 0, 0)
	if offset == 0 {
		page := queryInt(query.Get("page"), 1, 0)
		if page > 1 && limit > 0 {
			offset = (page - 1) * limit
		}
	}
	return limit, offset
}
