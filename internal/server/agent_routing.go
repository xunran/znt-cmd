package server

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
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
	RouteReason      string
	ReleaseStatus    contracts.ReleaseStatus
	AssignmentKey    string
}

func resolveRunnableAgentTarget(r *http.Request, appCore *core.Core, tenantID contracts.TenantID, target contracts.AgentTarget, runtimeContext contracts.RuntimeContext, traceID contracts.TraceID, caller auth.CallerIdentity) (agentRoute, error) {
	route := agentRoute{RequestedVersion: target.Version, ResolvedVersion: target.Version}
	if target.Version != "" {
		if err := ensureRunnableAgentVersion(appCore, tenantID, target); err != nil {
			return route, err
		}
		route.Release, _ = releaseForAgentVersion(appCore.Packages.ListReleases(), tenantID, target.AgentID, target.Version)
		route.RouteReason = "explicit"
		route.ReleaseStatus = route.Release.Status
		return route, nil
	}
	defaultVersion := contracts.AgentVersion("")
	if version, ok, err := activeAgentVersion(r.Context(), appCore, tenantID, target.AgentID); err != nil {
		return route, err
	} else if ok {
		defaultVersion = version
		route.RouteReason = "active_default"
	} else if appCore.AgentRegistry != nil {
		defaultVersion = appCore.AgentRegistry.DefaultVersionForTenant(tenantID, target.AgentID)
		if defaultVersion != "" {
			route.RouteReason = "registry_default"
		}
	}
	route.ResolvedVersion = defaultVersion
	releases := appCore.Packages.ListReleases()
	if route.ResolvedVersion != "" {
		route.Release, _ = releaseForAgentVersion(releases, tenantID, target.AgentID, route.ResolvedVersion)
		route.ReleaseStatus = route.Release.Status
	}
	if route.ResolvedVersion == "" {
		if release, ok := latestRunnableRelease(releases, tenantID, target.AgentID); ok {
			route.ResolvedVersion = release.Version
			route.Release = release
			route.ReleaseStatus = release.Status
			route.RouteReason = "latest_runnable_fallback"
		}
	}
	if route.ResolvedVersion == "" {
		return route, nil
	}
	route.AssignmentKey = canaryAssignmentKey(caller, runtimeContext, traceID)
	if route.Release.Status == contracts.ReleaseCanary {
		route.Canary = true
		route.ReleaseStatus = route.Release.Status
	}
	if err := ensureRunnableAgentVersion(appCore, tenantID, contracts.AgentTarget{AgentID: target.AgentID, Version: route.ResolvedVersion}); err != nil {
		return route, err
	}
	return route, nil
}

func activeAgentVersion(ctx context.Context, appCore *core.Core, tenantID contracts.TenantID, agentID contracts.AgentID) (contracts.AgentVersion, bool, error) {
	if appCore.Packages == nil {
		return "", false, nil
	}
	asset, ok, err := appCore.Packages.GetAgentAsset(ctx, tenantID, agentID)
	if err != nil || !ok {
		return "", false, err
	}
	if asset.ActiveVersion != "" {
		return asset.ActiveVersion, true, nil
	}
	if asset.DefaultVersion != "" {
		return asset.DefaultVersion, true, nil
	}
	return "", false, nil
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
	if isRunnableAgentReleaseStatus(release.Status) {
		return nil
	}
	return contracts.NewRuntimeError(contracts.CodeAgentVersionNotFound, "agent package version is not runnable before publish", map[string]any{
		"agent_id":           target.AgentID,
		"agent_version":      version,
		"package_version_id": release.PackageVersionID,
		"release_status":     release.Status,
	})
}

func isRunnableAgentReleaseStatus(status contracts.ReleaseStatus) bool {
	switch status {
	case contracts.ReleasePublished, contracts.ReleaseEvaluated, contracts.ReleaseCanary, contracts.ReleaseStable:
		return true
	default:
		return false
	}
}

func latestRunnableRelease(releases []contracts.AgentPackageVersion, tenantID contracts.TenantID, agentID contracts.AgentID) (contracts.AgentPackageVersion, bool) {
	var selected contracts.AgentPackageVersion
	var selectedAt time.Time
	for _, release := range releases {
		if release.TenantID != tenantID || release.AgentID != agentID || !isRunnableAgentReleaseStatus(release.Status) {
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

func shouldRouteCanary(release contracts.AgentPackageVersion, caller auth.CallerIdentity, runtimeContext contracts.RuntimeContext, traceID contracts.TraceID, agentID contracts.AgentID) bool {
	percent := release.CanaryPercent
	if percent <= 0 {
		return false
	}
	if percent >= 100 {
		return true
	}
	key := string(release.TenantID) + "|" + string(agentID) + "|" + string(release.PackageVersionID) + "|" + canaryAssignmentKey(caller, runtimeContext, traceID)
	return stablePercent(key) < percent
}

func canaryAssignmentKey(caller auth.CallerIdentity, runtimeContext contracts.RuntimeContext, traceID contracts.TraceID) string {
	if runtimeContext.UserID != "" {
		return string(runtimeContext.UserID)
	}
	if caller.CallerID != "" {
		return caller.CallerID
	}
	if runtimeContext.Conversation != nil {
		if runtimeContext.Conversation.ThreadID != "" {
			return runtimeContext.Conversation.ThreadID
		}
		if runtimeContext.Conversation.ConversationID != "" {
			return runtimeContext.Conversation.ConversationID
		}
		if runtimeContext.Conversation.CurrentMessage != nil && runtimeContext.Conversation.CurrentMessage.ThreadID != "" {
			return runtimeContext.Conversation.CurrentMessage.ThreadID
		}
	}
	if runtimeContext.ExternalTask != nil {
		return runtimeContext.ExternalTask.Provider + ":" + string(runtimeContext.ExternalTask.ExternalTaskID)
	}
	if runtimeContext.RequestID != "" {
		return runtimeContext.RequestID
	}
	return string(traceID)
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
			RunID:    runID,
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

func recordAgentRouteResolved(r *http.Request, appCore *core.Core, tenantID contracts.TenantID, traceID contracts.TraceID, agentID contracts.AgentID, runID contracts.AgentRunID, route agentRoute) error {
	if appCore.Trace == nil || traceID == "" {
		return nil
	}
	releaseStatus := route.ReleaseStatus
	if releaseStatus == "" {
		releaseStatus = route.Release.Status
	}
	payload := map[string]any{
		"agent_id":            agentID,
		"requested_version":   route.RequestedVersion,
		"resolved_version":    route.ResolvedVersion,
		"release_status":      releaseStatus,
		"package_version_id":  route.Release.PackageVersionID,
		"route_reason":        route.RouteReason,
		"canary":              route.Canary,
		"canary_percent":      route.Release.CanaryPercent,
		"assignment_key_hash": hashRouteAssignmentKey(route.AssignmentKey),
	}
	_ = appCore.Trace.Record(r.Context(), contracts.TraceEvent{
		TraceID:   traceID,
		TenantID:  tenantID,
		SpanID:    contracts.SpanID(idgen.New("span")),
		RunID:     runID,
		Type:      contracts.TraceAgentRouteResolved,
		Payload:   payload,
		CreatedAt: time.Now().UTC(),
	})
	return nil
}

func hashRouteAssignmentKey(value string) string {
	if value == "" {
		return ""
	}
	sum := sha256.Sum256([]byte(value))
	return hex.EncodeToString(sum[:])[:16]
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
