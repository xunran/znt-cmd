package release

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"znt/internal/app/config"
	"znt/internal/app/core"
)

func TestBuildGoNoGo(t *testing.T) {
	appCore, err := core.New(config.Config{ServiceName: "clean-core", Version: "test", Env: "test", HTTPAddr: ":0", Readiness: true})
	if err != nil {
		t.Fatal(err)
	}
	report := BuildGoNoGo(context.Background(), appCore, "../../migrations", nil)
	if report.Decision != "go" {
		t.Fatalf("expected go, got %#v", report)
	}
	if len(report.Contract.Frozen) == 0 {
		t.Fatal("expected frozen contract list")
	}
}

func TestCapabilityGateRequiresManifestHashForCanaryEvidence(t *testing.T) {
	path := filepath.Join(t.TempDir(), "matrix.md")
	content := "TestPackageCanaryRoutesDefaultTrafficAndRecordsHit canary.routed strategy_hash"
	if err := os.WriteFile(path, []byte(content), 0644); err != nil {
		t.Fatal(err)
	}
	gate := releaseGateByName(t, capabilityGates(path), "release.canary_hits")
	if gate.Status != "fail" || !strings.Contains(gate.Details, "manifest_hash") {
		t.Fatalf("expected release canary gate to require manifest_hash, got %#v", gate)
	}
	if err := os.WriteFile(path, []byte(content+" manifest_hash"), 0644); err != nil {
		t.Fatal(err)
	}
	gate = releaseGateByName(t, capabilityGates(path), "release.canary_hits")
	if gate.Status != "pass" {
		t.Fatalf("expected release canary gate to pass with manifest_hash evidence, got %#v", gate)
	}
}

func TestCapabilityGateRequiresOpenAPIFinalAlignmentEvidence(t *testing.T) {
	path := filepath.Join(t.TempDir(), "matrix.md")
	content := "openapi.clean-core.v1.json ModelStreamEvent hook_effects RunDiagnosticsResponse AgentPluginManifest"
	if err := os.WriteFile(path, []byte(content), 0644); err != nil {
		t.Fatal(err)
	}
	gate := releaseGateByName(t, capabilityGates(path), "openapi.final_alignment")
	if gate.Status != "fail" || !strings.Contains(gate.Details, "additionalProperties=false") {
		t.Fatalf("expected OpenAPI gate to require strict schema evidence, got %#v", gate)
	}
	if err := os.WriteFile(path, []byte(content+" additionalProperties=false"), 0644); err != nil {
		t.Fatal(err)
	}
	gate = releaseGateByName(t, capabilityGates(path), "openapi.final_alignment")
	if gate.Status != "pass" {
		t.Fatalf("expected OpenAPI gate to pass with final alignment evidence, got %#v", gate)
	}
}

func releaseGateByName(t *testing.T, gates []Gate, name string) Gate {
	t.Helper()
	for _, gate := range gates {
		if gate.Name == name {
			return gate
		}
	}
	t.Fatalf("gate %s not found in %#v", name, gates)
	return Gate{}
}
