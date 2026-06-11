package engine

import "znt/internal/contracts"

type RepairAction string

const (
	RepairActionContinue RepairAction = "continue"
	RepairActionStop     RepairAction = "stop"
)

type RepairRequest struct {
	Policy      contracts.PolicySet
	Tool        contracts.ToolDefinition
	Result      contracts.ToolResult
	FailureSeen int
}

type RepairDecision struct {
	Action string `json:"action"`
	Reason string `json:"reason,omitempty"`
}

func EvaluateRepair(policy contracts.ToolRepairPolicy, req RepairRequest) RepairDecision {
	normalizeRepairPolicy(&policy)
	if !policy.Enabled {
		return RepairDecision{Action: string(RepairActionStop), Reason: "tool repair policy disabled"}
	}
	if req.Result.Status == contracts.ToolResultSucceeded || req.Result.Status == contracts.ToolResultPendingApproval {
		return RepairDecision{Action: string(RepairActionContinue), Reason: "tool result does not need repair"}
	}
	if req.Result.Status == contracts.ToolResultDenied && policy.StopOnDenied {
		return RepairDecision{Action: string(RepairActionStop), Reason: "tool policy denial is not repairable"}
	}
	if repairRiskAtLeast(req.Tool.RiskLevel, policy.StopAtOrAboveRiskLevel) {
		return RepairDecision{Action: string(RepairActionStop), Reason: "tool risk is above repair threshold"}
	}
	if policy.MaxRepairAttempts >= 0 && req.FailureSeen > policy.MaxRepairAttempts {
		return RepairDecision{Action: string(RepairActionStop), Reason: "tool repair attempt limit exceeded"}
	}
	if !policy.RequestModelRepairOnFail {
		return RepairDecision{Action: string(RepairActionStop), Reason: "model repair is disabled for tool failures"}
	}
	if len(policy.RepairableErrorCodes) > 0 && req.Result.Error != nil && !containsString(policy.RepairableErrorCodes, string(req.Result.Error.Code)) {
		return RepairDecision{Action: string(RepairActionStop), Reason: "tool error code is not repairable"}
	}
	return RepairDecision{Action: string(RepairActionContinue), Reason: "request model repair for tool failure"}
}

func normalizeRepairPolicy(policy *contracts.ToolRepairPolicy) {
	if policy.MaxRepairAttempts == 0 {
		policy.MaxRepairAttempts = 1
	}
}

func repairRiskAtLeast(actual contracts.RiskLevel, threshold contracts.RiskLevel) bool {
	if threshold == "" {
		return false
	}
	rank := map[contracts.RiskLevel]int{
		contracts.RiskLow:      1,
		contracts.RiskMedium:   2,
		contracts.RiskHigh:     3,
		contracts.RiskCritical: 4,
	}
	return rank[actual] >= rank[threshold]
}

func containsString(values []string, target string) bool {
	for _, value := range values {
		if value == target {
			return true
		}
	}
	return false
}
