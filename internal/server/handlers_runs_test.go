package server

import "testing"

func TestSummarizeDiagnosticErrorSanitizesInternalText(t *testing.T) {
	for _, value := range []any{
		"capability_not_available trace_id=trace_1",
		map[string]any{"message": "agent exported tool is not enabled run_id=run_1"},
	} {
		if got := summarizeDiagnosticError(value); got != "internal tool error hidden" {
			t.Fatalf("expected sanitized diagnostic error, got %q for %#v", got, value)
		}
	}
}
