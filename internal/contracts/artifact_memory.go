package contracts

import "time"

type ArtifactRef struct {
	ArtifactID ArtifactID `json:"artifact_id"`
	Type       string     `json:"type"`
	URI        string     `json:"uri,omitempty"`
	Summary    string     `json:"summary,omitempty"`
	Hash       string     `json:"hash,omitempty"`
}

type Artifact struct {
	ArtifactID ArtifactID `json:"artifact_id"`
	TenantID   TenantID   `json:"tenant_id"`

	Type string `json:"type"`
	Name string `json:"name"`

	StorageURI string `json:"storage_uri"`
	MimeType   string `json:"mime_type,omitempty"`
	SizeBytes  int64  `json:"size_bytes"`

	Hash string `json:"hash"`

	CreatedBy string    `json:"created_by"`
	CreatedAt time.Time `json:"created_at"`
}

type MemoryEvent struct {
	MemoryID MemoryID `json:"memory_id"`
	TenantID TenantID `json:"tenant_id"`
	AgentID  AgentID  `json:"agent_id,omitempty"`
	UserID   UserID   `json:"user_id,omitempty"`

	Scope   string `json:"scope"`
	Content string `json:"content"`
	Summary string `json:"summary,omitempty"`

	SourceEventID string `json:"source_event_id,omitempty"`

	Visibility string  `json:"visibility"`
	Confidence float64 `json:"confidence"`

	CreatedAt time.Time `json:"created_at"`
}

type MemorySummary struct {
	MemoryID MemoryID `json:"memory_id"`
	Summary  string   `json:"summary"`
}
