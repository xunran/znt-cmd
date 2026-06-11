package agentcapability

import (
	"context"
	"testing"

	"znt/internal/contracts"
)

func TestCapabilityMatch(t *testing.T) {
	ctx := context.Background()
	svc := NewService(nil)
	if _, err := svc.Upsert(ctx, contracts.AgentCapability{
		TenantID:    "tenant",
		AgentID:     "risk-agent",
		Name:        "上线风险分析",
		Description: "负责上线风险、回归测试和验收建议",
	}); err != nil {
		t.Fatal(err)
	}
	matches, err := svc.Match(ctx, "tenant", "上线风险", 3, "")
	if err != nil {
		t.Fatal(err)
	}
	if len(matches) != 1 || matches[0].Capability.AgentID != "risk-agent" {
		t.Fatalf("unexpected matches %#v", matches)
	}
}
