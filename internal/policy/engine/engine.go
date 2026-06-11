package engine

import (
	"context"
	"fmt"
	"sync"
	"time"

	"znt/internal/contracts"
	"znt/internal/governance/audit"
	handoffpolicy "znt/internal/policy/handoff"
	"znt/internal/policy/toolpolicy"
	"znt/pkg/hash"
	"znt/pkg/idgen"
)

type Store interface {
	Get(ctx context.Context, tenantID contracts.TenantID, policySetID contracts.PolicySetID) (contracts.PolicySet, bool, error)
	Put(ctx context.Context, policy contracts.PolicySet) error
	SaveDraft(ctx context.Context, draft contracts.PolicyDraft) error
	GetDraft(ctx context.Context, draftID string) (contracts.PolicyDraft, bool, error)
	SaveVersion(ctx context.Context, version contracts.PolicyVersion, policy contracts.PolicySet) error
	UpdateVersionStatus(ctx context.Context, policyVersionID contracts.PolicyVersionID, status contracts.ReleaseStatus) error
	GetVersion(ctx context.Context, policyVersionID contracts.PolicyVersionID) (contracts.PolicyVersion, contracts.PolicySet, bool, error)
	ListVersions(ctx context.Context, tenantID contracts.TenantID, policySetID contracts.PolicySetID) ([]contracts.PolicyVersion, error)
}

type InMemoryStore struct {
	mu       sync.RWMutex
	policies map[string]contracts.PolicySet
	drafts   map[string]contracts.PolicyDraft
	versions map[contracts.PolicyVersionID]policyVersionRecord
}

type policyVersionRecord struct {
	version contracts.PolicyVersion
	policy  contracts.PolicySet
}

func NewInMemoryStore(policies ...contracts.PolicySet) *InMemoryStore {
	store := &InMemoryStore{
		policies: map[string]contracts.PolicySet{},
		drafts:   map[string]contracts.PolicyDraft{},
		versions: map[contracts.PolicyVersionID]policyVersionRecord{},
	}
	for _, policy := range policies {
		_ = store.Put(context.Background(), policy)
	}
	return store
}

func (s *InMemoryStore) Get(_ context.Context, tenantID contracts.TenantID, policySetID contracts.PolicySetID) (contracts.PolicySet, bool, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	if policy, ok := s.policies[key(tenantID, policySetID)]; ok {
		return policy, true, nil
	}
	policy, ok := s.policies[key("", policySetID)]
	return policy, ok, nil
}

func (s *InMemoryStore) Put(_ context.Context, policy contracts.PolicySet) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.policies[key(policy.TenantID, policy.PolicySetID)] = policy
	return nil
}

func (s *InMemoryStore) SaveDraft(_ context.Context, draft contracts.PolicyDraft) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.drafts[draft.DraftID] = draft
	return nil
}

func (s *InMemoryStore) GetDraft(_ context.Context, draftID string) (contracts.PolicyDraft, bool, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	draft, ok := s.drafts[draftID]
	return draft, ok, nil
}

func (s *InMemoryStore) SaveVersion(_ context.Context, version contracts.PolicyVersion, policy contracts.PolicySet) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.versions[version.PolicyVersionID] = policyVersionRecord{version: version, policy: policy}
	if version.Status == contracts.ReleaseStable {
		s.policies[key(policy.TenantID, policy.PolicySetID)] = policy
	}
	return nil
}

func (s *InMemoryStore) UpdateVersionStatus(_ context.Context, policyVersionID contracts.PolicyVersionID, status contracts.ReleaseStatus) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	record, ok := s.versions[policyVersionID]
	if !ok {
		return fmt.Errorf("policy version %s not found", policyVersionID)
	}
	record.version.Status = status
	s.versions[policyVersionID] = record
	if status == contracts.ReleaseStable {
		s.policies[key(record.policy.TenantID, record.policy.PolicySetID)] = record.policy
	}
	return nil
}

func (s *InMemoryStore) GetVersion(_ context.Context, policyVersionID contracts.PolicyVersionID) (contracts.PolicyVersion, contracts.PolicySet, bool, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	record, ok := s.versions[policyVersionID]
	return record.version, record.policy, ok, nil
}

func (s *InMemoryStore) ListVersions(_ context.Context, tenantID contracts.TenantID, policySetID contracts.PolicySetID) ([]contracts.PolicyVersion, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	out := make([]contracts.PolicyVersion, 0)
	for _, record := range s.versions {
		if record.version.TenantID == tenantID && record.version.PolicySetID == policySetID {
			out = append(out, record.version)
		}
	}
	return out, nil
}

type Engine struct {
	Store   Store
	Tool    toolpolicy.Evaluator
	Handoff handoffpolicy.Evaluator
}

func New(store Store, auditLogger audit.Logger) Engine {
	return Engine{
		Store:   store,
		Tool:    toolpolicy.New(auditLogger),
		Handoff: handoffpolicy.New(),
	}
}

func DefaultPolicySet() contracts.PolicySet {
	return contracts.PolicySet{
		PolicySetID: "policy_default",
		Version:     "v1",
		RuntimePolicy: contracts.RuntimePolicy{
			MaxSteps:                   4,
			MaxToolCalls:               2,
			MaxModelRetries:            1,
			MaxConsecutiveToolFailures: 2,
		},
		ToolPolicy: contracts.ToolPolicy{},
		ToolRepairPolicy: contracts.ToolRepairPolicy{
			Enabled:                  true,
			MaxRepairAttempts:        1,
			RepairableErrorCodes:     []string{string(contracts.CodeToolArgumentInvalid), string(contracts.CodeToolExecutionFailed)},
			StopOnDenied:             true,
			StopAtOrAboveRiskLevel:   contracts.RiskCritical,
			RequestModelRepairOnFail: true,
		},
		ApprovalPolicy: contracts.ApprovalPolicy{
			RequireApprovalForHighRisk:      true,
			RequireApprovalForExternalWrite: true,
		},
		PromptPolicy: contracts.PromptPolicy{
			MaxPromptTokens: 4000,
		},
		CompressionPolicy: contracts.ContextCompressionPolicy{
			Enabled:         true,
			MaxContextItems: 20,
		},
		RecoveryPolicy: contracts.TaskRecoveryPolicy{
			AllowResumeFromEvents: true,
		},
		TaskUpgradePolicy: contracts.TaskUpgradePolicy{},
		HandoffPolicy: contracts.HandoffPolicy{
			DefaultMode:                          contracts.HandoffHybrid,
			AllowFullContext:                     false,
			MaxContextTokens:                     4000,
			RequireApprovalForCrossAgent:         false,
			RequireApprovalForSensitiveArtifacts: true,
			AllowArtifactRead:                    true,
			AllowTaskEventRead:                   true,
		},
		ReleasePolicy: contracts.ReleasePolicy{
			RequireRollbackReason:           true,
			DefaultCanaryPercent:            10,
			MaxCanaryPercent:                100,
			MaxCanaryPercentWithoutApproval: 25,
		},
		MemoryPolicy: contracts.MemoryPolicy{
			AllowWrite: true,
			AllowRead:  true,
		},
		ArtifactPolicy: contracts.ArtifactPolicy{
			AllowRead:   true,
			AllowDelete: false,
		},
		CreatedAt: time.Now().UTC(),
	}
}

func FallbackPolicySet(tenantID contracts.TenantID, policySetID contracts.PolicySetID) contracts.PolicySet {
	policy := DefaultPolicySet()
	policy.TenantID = tenantID
	if policySetID != "" {
		policy.PolicySetID = policySetID
	}
	return policy
}

func key(tenantID contracts.TenantID, policySetID contracts.PolicySetID) string {
	return string(tenantID) + "\x00" + string(policySetID)
}

type PolicyManager struct {
	Store Store
	Audit audit.Logger
	Now   func() time.Time
}

func NewPolicyManager(store Store, auditLogger audit.Logger) PolicyManager {
	return PolicyManager{Store: store, Audit: auditLogger, Now: func() time.Time { return time.Now().UTC() }}
}

func (m PolicyManager) CreateDraft(ctx context.Context, tenantID contracts.TenantID, policy contracts.PolicySet, actorID string) (contracts.PolicyDraft, error) {
	if m.Store == nil {
		return contracts.PolicyDraft{}, fmt.Errorf("policy store is not configured")
	}
	if policy.PolicySetID == "" {
		policy.PolicySetID = "policy_default"
	}
	if policy.TenantID == "" {
		policy.TenantID = tenantID
	}
	if policy.TenantID != tenantID {
		return contracts.PolicyDraft{}, contracts.NewRuntimeError(contracts.CodeToolPolicyDenied, "policy tenant does not match caller tenant", nil)
	}
	if policy.Version == "" {
		policy.Version = nextPolicyVersion(ctx, m.Store, tenantID, policy.PolicySetID)
	}
	if policy.CreatedAt.IsZero() {
		policy.CreatedAt = m.now()
	}
	now := m.now()
	draft := contracts.PolicyDraft{
		DraftID:     idgen.New("policydraft"),
		TenantID:    tenantID,
		PolicySetID: policy.PolicySetID,
		Version:     policy.Version,
		Policy:      policy,
		Status:      contracts.ReleaseDraft,
		CreatedBy:   actorID,
		CreatedAt:   now,
		UpdatedAt:   now,
	}
	if err := m.Store.SaveDraft(ctx, draft); err != nil {
		return contracts.PolicyDraft{}, err
	}
	m.audit(ctx, tenantID, actorID, "policy.draft.create", draft.DraftID, "allowed", "")
	return draft, nil
}

func (m PolicyManager) UpdateDraft(ctx context.Context, draftID string, policy contracts.PolicySet, actorID string) (contracts.PolicyDraft, error) {
	if m.Store == nil {
		return contracts.PolicyDraft{}, fmt.Errorf("policy store is not configured")
	}
	draft, ok, err := m.Store.GetDraft(ctx, draftID)
	if err != nil {
		return contracts.PolicyDraft{}, err
	}
	if !ok {
		return contracts.PolicyDraft{}, fmt.Errorf("policy draft %s not found", draftID)
	}
	if policy.PolicySetID == "" {
		policy.PolicySetID = draft.PolicySetID
	}
	if policy.TenantID == "" {
		policy.TenantID = draft.TenantID
	}
	if policy.Version == "" {
		policy.Version = draft.Version
	}
	if policy.TenantID != draft.TenantID || policy.PolicySetID != draft.PolicySetID {
		return contracts.PolicyDraft{}, contracts.NewRuntimeError(contracts.CodeToolPolicyDenied, "policy draft identity cannot be changed", nil)
	}
	if policy.CreatedAt.IsZero() {
		policy.CreatedAt = draft.CreatedAt
	}
	draft.Policy = policy
	draft.Version = policy.Version
	draft.Status = contracts.ReleaseDraft
	draft.UpdatedAt = m.now()
	if err := m.Store.SaveDraft(ctx, draft); err != nil {
		return contracts.PolicyDraft{}, err
	}
	m.audit(ctx, draft.TenantID, actorID, contracts.AuditPolicyUpdate, draft.DraftID, "allowed", "draft updated")
	return draft, nil
}

func (m PolicyManager) ValidateDraft(ctx context.Context, draftID string, actorID string) (contracts.PolicyDraft, error) {
	draft, err := m.patchDraft(ctx, draftID, actorID, contracts.ReleaseValidated, "policy.draft.validate", "")
	if err != nil {
		return contracts.PolicyDraft{}, err
	}
	if draft.Policy.PolicySetID == "" || draft.Policy.Version == "" {
		return contracts.PolicyDraft{}, fmt.Errorf("policy draft %s requires policy_set_id and version", draftID)
	}
	return draft, nil
}

func (m PolicyManager) ValidateDraftForTenant(ctx context.Context, tenantID contracts.TenantID, draftID string, actorID string) (contracts.PolicyDraft, error) {
	if _, err := m.draftForTenant(ctx, tenantID, draftID); err != nil {
		return contracts.PolicyDraft{}, err
	}
	return m.ValidateDraft(ctx, draftID, actorID)
}

func (m PolicyManager) PublishDraft(ctx context.Context, draftID string, actorID string) (contracts.PolicyVersion, error) {
	if m.Store == nil {
		return contracts.PolicyVersion{}, fmt.Errorf("policy store is not configured")
	}
	draft, ok, err := m.Store.GetDraft(ctx, draftID)
	if err != nil {
		return contracts.PolicyVersion{}, err
	}
	if !ok {
		return contracts.PolicyVersion{}, fmt.Errorf("policy draft %s not found", draftID)
	}
	if draft.Status != contracts.ReleaseValidated && draft.Status != contracts.ReleaseEvaluated && draft.Status != contracts.ReleaseReviewed {
		return contracts.PolicyVersion{}, fmt.Errorf("policy draft %s is not ready to publish", draftID)
	}
	policyHash, err := hash.StableJSON(draft.Policy)
	if err != nil {
		return contracts.PolicyVersion{}, err
	}
	now := m.now()
	version := contracts.PolicyVersion{
		PolicyVersionID: contracts.PolicyVersionID(idgen.New("policyver")),
		TenantID:        draft.TenantID,
		PolicySetID:     draft.PolicySetID,
		Version:         draft.Version,
		Status:          contracts.ReleasePublished,
		PolicyHash:      policyHash,
		CreatedBy:       draft.CreatedBy,
		CreatedAt:       draft.CreatedAt,
		PublishedAt:     &now,
	}
	if err := m.Store.SaveVersion(ctx, version, draft.Policy); err != nil {
		return contracts.PolicyVersion{}, err
	}
	draft.Status = contracts.ReleasePublished
	draft.UpdatedAt = now
	if err := m.Store.SaveDraft(ctx, draft); err != nil {
		return contracts.PolicyVersion{}, err
	}
	m.audit(ctx, draft.TenantID, actorID, "policy.publish", string(version.PolicyVersionID), "allowed", "")
	return version, nil
}

func (m PolicyManager) PublishDraftForTenant(ctx context.Context, tenantID contracts.TenantID, draftID string, actorID string) (contracts.PolicyVersion, error) {
	if _, err := m.draftForTenant(ctx, tenantID, draftID); err != nil {
		return contracts.PolicyVersion{}, err
	}
	return m.PublishDraft(ctx, draftID, actorID)
}

func (m PolicyManager) MarkEvalResult(ctx context.Context, policyVersionID contracts.PolicyVersionID, passed bool, actorID string, reason string) (contracts.PolicyVersion, error) {
	version, policy, ok, err := m.version(ctx, policyVersionID)
	if err != nil {
		return contracts.PolicyVersion{}, err
	}
	if !ok {
		return contracts.PolicyVersion{}, fmt.Errorf("policy version %s not found", policyVersionID)
	}
	if passed && version.Status != contracts.ReleaseStable {
		version.Status = contracts.ReleaseEvaluated
		if err := m.Store.SaveVersion(ctx, version, policy); err != nil {
			return contracts.PolicyVersion{}, err
		}
	}
	decision := "denied"
	if passed {
		decision = "allowed"
	}
	m.audit(ctx, version.TenantID, actorID, "policy.eval", string(policyVersionID), decision, reason)
	return version, nil
}

func (m PolicyManager) MarkReviewed(ctx context.Context, draftID string, actorID string) (contracts.PolicyDraft, error) {
	draft, ok, err := m.Store.GetDraft(ctx, draftID)
	if err != nil {
		return contracts.PolicyDraft{}, err
	}
	if !ok {
		return contracts.PolicyDraft{}, fmt.Errorf("policy draft %s not found", draftID)
	}
	if draft.Status != contracts.ReleaseValidated && draft.Status != contracts.ReleaseEvaluated {
		return contracts.PolicyDraft{}, fmt.Errorf("policy draft %s is not ready for review", draftID)
	}
	return m.patchDraft(ctx, draftID, actorID, contracts.ReleaseReviewed, "policy.review", "")
}

func (m PolicyManager) MarkReviewedForTenant(ctx context.Context, tenantID contracts.TenantID, draftID string, actorID string) (contracts.PolicyDraft, error) {
	if _, err := m.draftForTenant(ctx, tenantID, draftID); err != nil {
		return contracts.PolicyDraft{}, err
	}
	return m.MarkReviewed(ctx, draftID, actorID)
}

func (m PolicyManager) MarkCanary(ctx context.Context, policyVersionID contracts.PolicyVersionID, actorID string) (contracts.PolicyVersion, error) {
	version, _, ok, err := m.version(ctx, policyVersionID)
	if err != nil {
		return contracts.PolicyVersion{}, err
	}
	if !ok {
		return contracts.PolicyVersion{}, fmt.Errorf("policy version %s not found", policyVersionID)
	}
	if version.Status != contracts.ReleaseEvaluated {
		return contracts.PolicyVersion{}, fmt.Errorf("policy version %s cannot become canary before passing eval", policyVersionID)
	}
	return m.markVersion(ctx, policyVersionID, contracts.ReleaseCanary, actorID, "policy.canary", "")
}

func (m PolicyManager) MarkStable(ctx context.Context, policyVersionID contracts.PolicyVersionID, actorID string) (contracts.PolicyVersion, error) {
	version, policy, ok, err := m.version(ctx, policyVersionID)
	if err != nil {
		return contracts.PolicyVersion{}, err
	}
	if !ok {
		return contracts.PolicyVersion{}, fmt.Errorf("policy version %s not found", policyVersionID)
	}
	if version.Status != contracts.ReleaseEvaluated && version.Status != contracts.ReleaseCanary {
		return contracts.PolicyVersion{}, fmt.Errorf("policy version %s cannot become stable before passing eval", policyVersionID)
	}
	version.Status = contracts.ReleaseStable
	if err := m.Store.SaveVersion(ctx, version, policy); err != nil {
		return contracts.PolicyVersion{}, err
	}
	if err := m.Store.Put(ctx, policy); err != nil {
		return contracts.PolicyVersion{}, err
	}
	m.audit(ctx, version.TenantID, actorID, "policy.stable", string(policyVersionID), "allowed", "")
	return version, nil
}

func (m PolicyManager) Rollback(ctx context.Context, policyVersionID contracts.PolicyVersionID, actorID string, reason string) (contracts.PolicyVersion, error) {
	if reason == "" {
		return contracts.PolicyVersion{}, contracts.NewRuntimeError(contracts.CodeToolPolicyDenied, "policy rollback reason is required", nil)
	}
	version, _, ok, err := m.version(ctx, policyVersionID)
	if err != nil {
		return contracts.PolicyVersion{}, err
	}
	if !ok {
		return contracts.PolicyVersion{}, fmt.Errorf("policy version %s not found", policyVersionID)
	}
	rolledBack, err := m.markVersion(ctx, policyVersionID, contracts.ReleaseRolledBack, actorID, "policy.rollback", reason)
	if err != nil {
		return contracts.PolicyVersion{}, err
	}
	if fallback, fallbackPolicy, ok := m.rollbackFallback(ctx, version); ok {
		fallback.Status = contracts.ReleaseStable
		if err := m.Store.SaveVersion(ctx, fallback, fallbackPolicy); err != nil {
			return contracts.PolicyVersion{}, err
		}
		if err := m.Store.Put(ctx, fallbackPolicy); err != nil {
			return contracts.PolicyVersion{}, err
		}
		m.audit(ctx, fallback.TenantID, actorID, "policy.rollback.restore", string(fallback.PolicyVersionID), "allowed", reason)
	}
	return rolledBack, nil
}

func (m PolicyManager) GetVersion(ctx context.Context, policyVersionID contracts.PolicyVersionID) (contracts.PolicyVersion, contracts.PolicySet, bool, error) {
	return m.version(ctx, policyVersionID)
}

func (m PolicyManager) CurrentVersion(ctx context.Context, tenantID contracts.TenantID, policySetID contracts.PolicySetID) (contracts.PolicyVersion, contracts.PolicySet, bool, error) {
	if m.Store == nil {
		return contracts.PolicyVersion{}, contracts.PolicySet{}, false, fmt.Errorf("policy store is not configured")
	}
	versions, err := m.Store.ListVersions(ctx, tenantID, policySetID)
	if err != nil {
		return contracts.PolicyVersion{}, contracts.PolicySet{}, false, err
	}
	var selected contracts.PolicyVersion
	for _, candidate := range versions {
		if candidate.Status != contracts.ReleaseStable {
			continue
		}
		if selected.PolicyVersionID == "" || candidate.CreatedAt.After(selected.CreatedAt) {
			selected = candidate
		}
	}
	if selected.PolicyVersionID == "" {
		return contracts.PolicyVersion{}, contracts.PolicySet{}, false, nil
	}
	version, policy, ok, err := m.Store.GetVersion(ctx, selected.PolicyVersionID)
	if err != nil || !ok {
		return contracts.PolicyVersion{}, contracts.PolicySet{}, ok, err
	}
	return version, policy, true, nil
}

func (m PolicyManager) ListVersions(ctx context.Context, tenantID contracts.TenantID, policySetID contracts.PolicySetID) ([]contracts.PolicyVersion, error) {
	if m.Store == nil {
		return nil, fmt.Errorf("policy store is not configured")
	}
	return m.Store.ListVersions(ctx, tenantID, policySetID)
}

func (m PolicyManager) DraftForTenant(ctx context.Context, tenantID contracts.TenantID, draftID string) (contracts.PolicyDraft, error) {
	return m.draftForTenant(ctx, tenantID, draftID)
}

func (m PolicyManager) draftForTenant(ctx context.Context, tenantID contracts.TenantID, draftID string) (contracts.PolicyDraft, error) {
	if m.Store == nil {
		return contracts.PolicyDraft{}, fmt.Errorf("policy store is not configured")
	}
	draft, ok, err := m.Store.GetDraft(ctx, draftID)
	if err != nil {
		return contracts.PolicyDraft{}, err
	}
	if !ok {
		return contracts.PolicyDraft{}, fmt.Errorf("policy draft %s not found", draftID)
	}
	if draft.TenantID != tenantID {
		return contracts.PolicyDraft{}, contracts.NewRuntimeError(contracts.CodeToolPolicyDenied, "policy draft tenant does not match caller tenant", nil)
	}
	return draft, nil
}

func (m PolicyManager) patchDraft(ctx context.Context, draftID string, actorID string, status contracts.ReleaseStatus, action string, reason string) (contracts.PolicyDraft, error) {
	if m.Store == nil {
		return contracts.PolicyDraft{}, fmt.Errorf("policy store is not configured")
	}
	draft, ok, err := m.Store.GetDraft(ctx, draftID)
	if err != nil {
		return contracts.PolicyDraft{}, err
	}
	if !ok {
		return contracts.PolicyDraft{}, fmt.Errorf("policy draft %s not found", draftID)
	}
	draft.Status = status
	draft.UpdatedAt = m.now()
	if err := m.Store.SaveDraft(ctx, draft); err != nil {
		return contracts.PolicyDraft{}, err
	}
	m.audit(ctx, draft.TenantID, actorID, action, draft.DraftID, "allowed", reason)
	return draft, nil
}

func (m PolicyManager) markVersion(ctx context.Context, policyVersionID contracts.PolicyVersionID, status contracts.ReleaseStatus, actorID string, action string, reason string) (contracts.PolicyVersion, error) {
	version, policy, ok, err := m.version(ctx, policyVersionID)
	if err != nil {
		return contracts.PolicyVersion{}, err
	}
	if !ok {
		return contracts.PolicyVersion{}, fmt.Errorf("policy version %s not found", policyVersionID)
	}
	version.Status = status
	if err := m.Store.SaveVersion(ctx, version, policy); err != nil {
		return contracts.PolicyVersion{}, err
	}
	if status == contracts.ReleaseStable {
		if err := m.Store.Put(ctx, policy); err != nil {
			return contracts.PolicyVersion{}, err
		}
	}
	m.audit(ctx, version.TenantID, actorID, action, string(policyVersionID), "allowed", reason)
	return version, nil
}

func (m PolicyManager) version(ctx context.Context, policyVersionID contracts.PolicyVersionID) (contracts.PolicyVersion, contracts.PolicySet, bool, error) {
	if m.Store == nil {
		return contracts.PolicyVersion{}, contracts.PolicySet{}, false, fmt.Errorf("policy store is not configured")
	}
	return m.Store.GetVersion(ctx, policyVersionID)
}

func (m PolicyManager) rollbackFallback(ctx context.Context, rolledBack contracts.PolicyVersion) (contracts.PolicyVersion, contracts.PolicySet, bool) {
	versions, err := m.Store.ListVersions(ctx, rolledBack.TenantID, rolledBack.PolicySetID)
	if err != nil {
		return contracts.PolicyVersion{}, contracts.PolicySet{}, false
	}
	var selected contracts.PolicyVersion
	selectedRank := 0
	for _, candidate := range versions {
		if candidate.PolicyVersionID == rolledBack.PolicyVersionID {
			continue
		}
		rank := rollbackCandidateRank(candidate.Status)
		if rank == 0 {
			continue
		}
		if selected.PolicyVersionID == "" || rank > selectedRank || (rank == selectedRank && candidate.CreatedAt.After(selected.CreatedAt)) {
			selected = candidate
			selectedRank = rank
		}
	}
	if selected.PolicyVersionID == "" {
		return contracts.PolicyVersion{}, contracts.PolicySet{}, false
	}
	version, policy, ok, err := m.Store.GetVersion(ctx, selected.PolicyVersionID)
	if err != nil || !ok {
		return contracts.PolicyVersion{}, contracts.PolicySet{}, false
	}
	return version, policy, true
}

func rollbackCandidateRank(status contracts.ReleaseStatus) int {
	switch status {
	case contracts.ReleaseStable:
		return 3
	case contracts.ReleaseCanary:
		return 2
	case contracts.ReleaseEvaluated:
		return 1
	default:
		return 0
	}
}

func (m PolicyManager) audit(ctx context.Context, tenantID contracts.TenantID, actorID string, action string, resourceID string, decision string, reason string) {
	if m.Audit == nil {
		return
	}
	_ = m.Audit.Log(ctx, contracts.AuditEvent{
		AuditID:      idgen.New("audit"),
		TenantID:     tenantID,
		ActorID:      actorID,
		ActorType:    "optimizer",
		Action:       action,
		ResourceType: "policy",
		ResourceID:   resourceID,
		Decision:     decision,
		Reason:       reason,
		CreatedAt:    m.now(),
	})
}

func (m PolicyManager) now() time.Time {
	if m.Now != nil {
		return m.Now().UTC()
	}
	return time.Now().UTC()
}

func nextPolicyVersion(ctx context.Context, store Store, tenantID contracts.TenantID, policySetID contracts.PolicySetID) string {
	versions, err := store.ListVersions(ctx, tenantID, policySetID)
	if err != nil || len(versions) == 0 {
		return "v1"
	}
	return fmt.Sprintf("v%d", len(versions)+1)
}
