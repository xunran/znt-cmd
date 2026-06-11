package server

import (
	"context"
	"net/http"
	"time"

	"znt/internal/app/auth"
	"znt/internal/app/core"
	"znt/internal/contracts"
	"znt/pkg/idgen"
)

type agentRoute struct {
	RequestedVersion contracts.AgentVersion
	ResolvedVersion  contracts.AgentVersion
	Release          contracts.AgentPackageVersion
	Canary           bool
}

func resolveRunnableAgentTarget(r *http.Request, appCore *core.Core, tenantID contracts.TenantID, target contracts.AgentTarget, traceID contracts.TraceID, caller auth.CallerIdentity) (agentRoute, error) {
	route := agentRoute{RequestedVersion: target.Version, ResolvedVersion: target.Version}
	if target.Version != "" {
		if err := ensureRunnableAgentVersion(appCore, tenantID, target); err != nil {
			return route, err
		}
		route.Release, _ = releaseForAgentVersion(appCore.Packages.ListReleases(), tenantID, target.AgentID, target.Version)
		return route, nil
	}
	defaultVersion := contracts.AgentVersion("")
	if appCore.AgentRegistry != nil {
		defaultVersion = appCore.AgentRegistry.DefaultVersionForTenant(tenantID, target.AgentID)
	}
	route.ResolvedVersion = defaultVersion
	releases := appCore.Packages.ListReleases()
	stable, stableOK := latestReleaseWithStatus(releases, tenantID, target.AgentID, contracts.ReleaseStable)
	canary, canaryOK := latestReleaseWithStatus(releases, tenantID, target.AgentID, contracts.ReleaseCanary)
	if stableOK {
		route.ResolvedVersion = stable.Version
		route.Release = stable
	}
	if canaryOK && shouldRouteCanary(canary, caller, traceID, target.AgentID) {
		route.ResolvedVersion = canary.Version
		route.Release = canary
		route.Canary = true
	}
	if route.ResolvedVersion == "" {
		return route, nil
	}
	if err := ensureRunnableAgentVersion(appCore, tenantID, contracts.AgentTarget{AgentID: target.AgentID, Version: route.ResolvedVersion}); err != nil {
		return route, err
	}
	return route, nil
}

func ensureRunnableAgentVersion(appCore *core.Core, tenantID contracts.TenantID, target contracts.AgentTarget) error {
	if err := appCore.EnsureAgentRunnable(context.Background(), tenantID, target.AgentID); err != nil {
		return err
	}
	version := target.Version
	if version == "" && appCore.AgentRegistry != nil {
		version = appCore.AgentRegistry.DefaultVersionForTenant(tenantID, target.AgentID)
	}
	if version == "" {
		return nil
	}
	release, ok := releaseForAgentVersion(appCore.Packages.ListReleases(), tenantID, target.AgentID, version)
	if !ok {
		return nil
	}
	if release.Status == contracts.ReleaseCanary || release.Status == contracts.ReleaseStable {
		return nil
	}
	return contracts.NewRuntimeError(contracts.CodeAgentVersionNotFound, "agent package version is not runnable before canary or stable", map[string]any{
		"agent_id":           target.AgentID,
		"agent_version":      version,
		"package_version_id": release.PackageVersionID,
		"release_status":     release.Status,
	})
}

func latestReleaseWithStatus(releases []contracts.AgentPackageVersion, tenantID contracts.TenantID, agentID contracts.AgentID, status contracts.ReleaseStatus) (contracts.AgentPackageVersion, bool) {
	var selected contracts.AgentPackageVersion
	var selectedAt time.Time
	for _, release := range releases {
		if release.TenantID != tenantID || release.AgentID != agentID || release.Status != status {
			continue
		}
		at := release.CreatedAt
		if release.PublishedAt != nil {
			at = *release.PublishedAt
		}
		if selected.PackageVersionID == "" || at.After(selectedAt) {
			selected = release
			selectedAt = at
		}
	}
	return selected, selected.PackageVersionID != ""
}

func shouldRouteCanary(release contracts.AgentPackageVersion, caller auth.CallerIdentity, traceID contracts.TraceID, agentID contracts.AgentID) bool {
	percent := release.CanaryPercent
	if percent <= 0 {
		return false
	}
	if percent >= 100 {
		return true
	}
	key := string(release.TenantID) + "|" + string(agentID) + "|" + string(release.PackageVersionID) + "|" + caller.CallerID + "|" + string(traceID)
	return stablePercent(key) < percent
}

func stablePercent(value string) int {
	var sum uint32 = 2166136261
	for _, b := range []byte(value) {
		sum ^= uint32(b)
		sum *= 16777619
	}
	return int(sum % 100)
}

func recordCanaryRoute(r *http.Request, appCore *core.Core, tenantID contracts.TenantID, traceID contracts.TraceID, caller auth.CallerIdentity, agentID contracts.AgentID, runID contracts.AgentRunID, release contracts.AgentPackageVersion) error {
	hit := contracts.CanaryHit{
		TenantID:         tenantID,
		AgentID:          agentID,
		ResolvedVersion:  release.Version,
		PackageVersionID: release.PackageVersionID,
		RunID:            runID,
		TraceID:          traceID,
		CallerID:         caller.CallerID,
		CanaryPercent:    release.CanaryPercent,
		Reason:           "default_version_canary_route",
		CreatedAt:        time.Now().UTC(),
	}
	if err := appCore.Packages.RecordCanaryHit(r.Context(), hit); err != nil {
		return err
	}
	if appCore.Trace != nil && traceID != "" {
		_ = appCore.Trace.Record(r.Context(), contracts.TraceEvent{
			TraceID:  traceID,
			TenantID: tenantID,
			SpanID:   contracts.SpanID(idgen.New("span")),
			Type:     contracts.TraceCanaryRouted,
			Payload: map[string]any{
				"agent_id":           agentID,
				"agent_version":      release.Version,
				"package_version_id": release.PackageVersionID,
				"canary_percent":     release.CanaryPercent,
			},
			CreatedAt: time.Now().UTC(),
		})
	}
	return nil
}

func releaseForAgentVersion(releases []contracts.AgentPackageVersion, tenantID contracts.TenantID, agentID contracts.AgentID, version contracts.AgentVersion) (contracts.AgentPackageVersion, bool) {
	var selected contracts.AgentPackageVersion
	var selectedAt time.Time
	for _, release := range releases {
		if release.TenantID == tenantID && release.AgentID == agentID && release.Version == version {
			at := release.CreatedAt
			if release.PublishedAt != nil {
				at = *release.PublishedAt
			}
			if selected.PackageVersionID == "" || at.After(selectedAt) {
				selected = release
				selectedAt = at
			}
		}
	}
	if selected.PackageVersionID == "" {
		return contracts.AgentPackageVersion{}, false
	}
	return selected, true
}

func ensurePackageReleaseTenant(appCore *core.Core, packageVersionID contracts.PackageVersionID, tenantID contracts.TenantID) (contracts.AgentPackageVersion, error) {
	release, ok := appCore.Packages.GetRelease(packageVersionID)
	if !ok {
		return contracts.AgentPackageVersion{}, contracts.NewRuntimeError(contracts.CodeAgentVersionNotFound, "package version not found", map[string]any{"package_version_id": packageVersionID})
	}
	if release.TenantID != tenantID {
		return contracts.AgentPackageVersion{}, contracts.NewRuntimeError(contracts.CodeToolPolicyDenied, "package tenant does not match caller tenant", map[string]any{"package_version_id": packageVersionID})
	}
	return release, nil
}
