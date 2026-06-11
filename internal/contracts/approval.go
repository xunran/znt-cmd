package contracts

import "time"

type ApprovalStatus string

const (
	ApprovalPending  ApprovalStatus = "pending"
	ApprovalApproved ApprovalStatus = "approved"
	ApprovalRejected ApprovalStatus = "rejected"
	ApprovalUsed     ApprovalStatus = "used"
)

type ApprovalRequest struct {
	ApprovalID   ApprovalID     `json:"approval_id"`
	TenantID     TenantID       `json:"tenant_id"`
	ResourceType string         `json:"resource_type"`
	ResourceID   string         `json:"resource_id"`
	Action       string         `json:"action"`
	RiskLevel    RiskLevel      `json:"risk_level,omitempty"`
	Reason       string         `json:"reason,omitempty"`
	Status       ApprovalStatus `json:"status"`
	RequestedBy  string         `json:"requested_by"`
	ResolvedBy   string         `json:"resolved_by,omitempty"`
	CreatedAt    time.Time      `json:"created_at"`
	ResolvedAt   *time.Time     `json:"resolved_at,omitempty"`
	ExpiresAt    *time.Time     `json:"expires_at,omitempty"`
	TraceID      TraceID        `json:"trace_id,omitempty"`
}
