package engine

import (
	"fmt"
	"strings"
	"time"

	"znt/internal/contracts"
)

type ReleaseRequest struct {
	Action        string
	CurrentStatus contracts.ReleaseStatus
	CanaryPercent int
	Approved      bool
	Reason        string
	Now           time.Time
}

func EvaluateReleaseAction(policy contracts.ReleasePolicy, req ReleaseRequest) (contracts.PolicyDecision, error) {
	normalizeReleasePolicy(&policy)
	if req.Now.IsZero() {
		req.Now = time.Now().UTC()
	}
	decision := contracts.PolicyDecision{
		Decision:         contracts.PolicyDecisionAllowed,
		Reason:           "release policy allowed",
		RiskLevel:        contracts.RiskMedium,
		AppliedPolicyIDs: []string{"release_policy"},
	}
	if !withinReleaseWindow(policy.AllowedWindowsUTC, req.Now) && !req.Approved {
		return approvalRequired("release action is outside allowed deployment window")
	}
	switch req.Action {
	case "canary":
		percent := req.CanaryPercent
		if percent == 0 {
			percent = policy.DefaultCanaryPercent
		}
		if percent <= 0 || percent > policy.MaxCanaryPercent {
			return denied(fmt.Sprintf("canary percent must be between 1 and %d", policy.MaxCanaryPercent))
		}
		if policy.MaxCanaryPercentWithoutApproval > 0 && percent > policy.MaxCanaryPercentWithoutApproval && !req.Approved {
			return approvalRequired(fmt.Sprintf("canary percent %d exceeds approval-free limit %d", percent, policy.MaxCanaryPercentWithoutApproval))
		}
	case "stable":
		if policy.RequireCanaryBeforeStable && req.CurrentStatus != contracts.ReleaseCanary {
			return denied("stable release requires a prior canary state")
		}
		if policy.RequireApprovalForStable && !req.Approved {
			return approvalRequired("stable release requires approval")
		}
	case "rollback":
		if policy.RequireRollbackReason && strings.TrimSpace(req.Reason) == "" {
			return denied("rollback reason is required by release policy")
		}
	default:
		return denied("unknown release action")
	}
	return decision, nil
}

func normalizeReleasePolicy(policy *contracts.ReleasePolicy) {
	if policy.DefaultCanaryPercent == 0 {
		policy.DefaultCanaryPercent = 10
	}
	if policy.MaxCanaryPercent == 0 {
		policy.MaxCanaryPercent = 100
	}
}

func approvalRequired(reason string) (contracts.PolicyDecision, error) {
	decision := contracts.PolicyDecision{
		Decision:         contracts.PolicyDecisionApprovalRequired,
		Reason:           reason,
		RiskLevel:        contracts.RiskHigh,
		AppliedPolicyIDs: []string{"release_policy"},
	}
	return decision, contracts.NewRuntimeError(contracts.CodeToolApprovalRequired, reason, nil)
}

func denied(reason string) (contracts.PolicyDecision, error) {
	decision := contracts.PolicyDecision{
		Decision:         contracts.PolicyDecisionDenied,
		Reason:           reason,
		RiskLevel:        contracts.RiskHigh,
		AppliedPolicyIDs: []string{"release_policy"},
	}
	return decision, contracts.NewRuntimeError(contracts.CodeToolPolicyDenied, reason, nil)
}

func withinReleaseWindow(windows []contracts.ReleaseWindow, now time.Time) bool {
	if len(windows) == 0 {
		return true
	}
	now = now.UTC()
	day := strings.ToLower(now.Weekday().String())
	for _, window := range windows {
		if len(window.Days) > 0 && !containsDay(window.Days, day) {
			continue
		}
		start := clampHour(window.StartHourUTC)
		end := clampHour(window.EndHourUTC)
		hour := now.Hour()
		if start == end {
			return true
		}
		if start < end && hour >= start && hour < end {
			return true
		}
		if start > end && (hour >= start || hour < end) {
			return true
		}
	}
	return false
}

func containsDay(days []string, current string) bool {
	for _, day := range days {
		day = strings.ToLower(strings.TrimSpace(day))
		if day == current || strings.HasPrefix(current, day) || strings.HasPrefix(day, current[:3]) {
			return true
		}
	}
	return false
}

func clampHour(hour int) int {
	if hour < 0 {
		return 0
	}
	if hour > 23 {
		return 23
	}
	return hour
}
