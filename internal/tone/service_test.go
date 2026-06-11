package tone

import (
	"context"
	"testing"
)

func TestToneDecideSilenceAndHighRisk(t *testing.T) {
	ctx := context.Background()
	svc := NewService(nil)
	silent := svc.Decide(ctx, DecideInput{TenantID: "tenant", GroupID: "group-a", Addressee: false})
	if silent.ShouldReply || silent.Style != "silent" {
		t.Fatalf("expected silent no-reply, got %#v", silent)
	}
	formal := svc.Decide(ctx, DecideInput{TenantID: "tenant", GroupID: "group-a", Addressee: true, HighRisk: true})
	if !formal.ShouldReply || formal.Style != "formal_confirmation" {
		t.Fatalf("expected formal confirmation, got %#v", formal)
	}
	direct := svc.Decide(ctx, DecideInput{TenantID: "tenant", GroupID: "group-a", Addressee: true, Signals: []string{"direct_question"}})
	if !direct.ShouldReply || direct.Style != "clear_short_answer" {
		t.Fatalf("expected direct answer, got %#v", direct)
	}
}
