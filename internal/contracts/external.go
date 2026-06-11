package contracts

import "context"
import "time"

type ExternalTaskRef struct {
	Provider       string         `json:"provider"`
	ExternalTaskID ExternalTaskID `json:"external_task_id"`
}

type ExternalTaskSummary struct {
	Ref     ExternalTaskRef `json:"ref"`
	Title   string          `json:"title"`
	Status  string          `json:"status"`
	Summary string          `json:"summary,omitempty"`
}

type ExternalTaskBinding struct {
	Provider       string         `json:"provider"`
	ExternalTaskID ExternalTaskID `json:"external_task_id"`
	CoreTaskID     TaskID         `json:"core_task_id"`
	TenantID       TenantID       `json:"tenant_id"`
	SyncMode       string         `json:"sync_mode"`
	Status         string         `json:"status"`
	LastError      string         `json:"last_error,omitempty"`
	CreatedAt      time.Time      `json:"created_at"`
	UpdatedAt      time.Time      `json:"updated_at"`
}

type ParticipantSummary struct {
	ID   string `json:"id"`
	Type string `json:"type"`
	Name string `json:"name,omitempty"`
}

type SendExternalMessageRequest struct {
	Ref     ExternalTaskRef `json:"ref"`
	Message string          `json:"message"`
}

type AttachArtifactRequest struct {
	Ref         ExternalTaskRef `json:"ref"`
	ArtifactRef ArtifactRef     `json:"artifact_ref"`
}

type CollaborationAccessRequest struct {
	Ref      ExternalTaskRef `json:"ref"`
	TenantID TenantID        `json:"tenant_id,omitempty"`
	CallerID string          `json:"caller_id"`
	Action   string          `json:"action"`
}

type AccessDecision struct {
	Allowed bool   `json:"allowed"`
	Reason  string `json:"reason,omitempty"`
}

type CollaborationProvider interface {
	GetTask(ctx context.Context, ref ExternalTaskRef) (*ExternalTaskSummary, error)
	GetParticipants(ctx context.Context, ref ExternalTaskRef) ([]ParticipantSummary, error)
	SendMessage(ctx context.Context, req SendExternalMessageRequest) error
	AttachArtifact(ctx context.Context, req AttachArtifactRequest) error
	CheckAccess(ctx context.Context, req CollaborationAccessRequest) (*AccessDecision, error)
}
