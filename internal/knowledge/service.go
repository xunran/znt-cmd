package knowledge

import (
	"context"
	"fmt"
	"sort"
	"strings"
	"sync"
	"time"
	"unicode/utf8"

	"znt/internal/contracts"
	"znt/internal/governance/audit"
	"znt/internal/governance/trace"
	"znt/pkg/idgen"
)

type PermissionChecker interface {
	Check(ctx context.Context, input contracts.PermissionCheckInput) (contracts.PermissionDecision, error)
}

type Service interface {
	CreateKnowledgeBase(ctx context.Context, input CreateKnowledgeBaseInput) (contracts.KnowledgeBase, error)
	ListKnowledgeBases(ctx context.Context, tenantID contracts.TenantID, ownerGroupID contracts.GroupID) ([]contracts.KnowledgeBase, error)
	GetKnowledgeBase(ctx context.Context, tenantID contracts.TenantID, knowledgeBaseID contracts.KnowledgeBaseID) (contracts.KnowledgeBase, bool, error)
	IngestDocument(ctx context.Context, doc contracts.KnowledgeDocument) (contracts.KnowledgeDocument, error)
	ListIngestionJobs(ctx context.Context, tenantID contracts.TenantID, knowledgeBaseID contracts.KnowledgeBaseID) ([]contracts.KnowledgeIngestionJob, error)
	GetIngestionJob(ctx context.Context, tenantID contracts.TenantID, jobID contracts.KnowledgeIngestionJobID) (contracts.KnowledgeIngestionJob, bool, error)
	Search(ctx context.Context, input SearchInput) ([]contracts.KnowledgeSearchResult, error)
}

type Store interface {
	SaveKnowledgeBase(ctx context.Context, base contracts.KnowledgeBase) error
	GetKnowledgeBase(ctx context.Context, tenantID contracts.TenantID, knowledgeBaseID contracts.KnowledgeBaseID) (contracts.KnowledgeBase, bool, error)
	ListKnowledgeBases(ctx context.Context, tenantID contracts.TenantID) ([]contracts.KnowledgeBase, error)
	SaveDocument(ctx context.Context, doc contracts.KnowledgeDocument) error
	ListDocuments(ctx context.Context, tenantID contracts.TenantID) ([]contracts.KnowledgeDocument, error)
	SaveIngestionJob(ctx context.Context, job contracts.KnowledgeIngestionJob) error
	GetIngestionJob(ctx context.Context, tenantID contracts.TenantID, jobID contracts.KnowledgeIngestionJobID) (contracts.KnowledgeIngestionJob, bool, error)
	ListIngestionJobs(ctx context.Context, tenantID contracts.TenantID, knowledgeBaseID contracts.KnowledgeBaseID) ([]contracts.KnowledgeIngestionJob, error)
}

type SearchAdapter interface {
	Search(ctx context.Context, input AdapterSearchInput) ([]contracts.KnowledgeSearchResult, error)
}

type AdapterSearchInput struct {
	Query      string
	SearchMode string
	Limit      int
	Bases      map[contracts.KnowledgeBaseID]contracts.KnowledgeBase
	Documents  []contracts.KnowledgeDocument
}

type CreateKnowledgeBaseInput struct {
	TenantID    contracts.TenantID
	GroupID     contracts.GroupID
	RequestedBy string
	Roles       []string
	Name        string
	Visibility  string
	SourceType  string
	IndexType   string
	TraceID     contracts.TraceID
	TaskID      contracts.TaskID
	RunID       contracts.AgentRunID
}

type SearchInput struct {
	TenantID         contracts.TenantID
	RequesterGroupID contracts.GroupID
	RequestedBy      string
	Roles            []string
	Query            string
	KnowledgeBaseIDs []contracts.KnowledgeBaseID
	SourceGroupID    contracts.GroupID
	Limit            int
	AllowCrossGroup  bool
	SearchMode       string
	TraceID          contracts.TraceID
	TaskID           contracts.TaskID
	RunID            contracts.AgentRunID
}

type InMemoryService struct {
	mu          sync.RWMutex
	bases       map[contracts.KnowledgeBaseID]contracts.KnowledgeBase
	documents   map[contracts.KnowledgeDocumentID]contracts.KnowledgeDocument
	jobs        map[contracts.KnowledgeIngestionJobID]contracts.KnowledgeIngestionJob
	adapter     SearchAdapter
	store       Store
	permissions PermissionChecker
	audit       audit.Logger
	trace       trace.Recorder
	now         func() time.Time
}

func NewInMemoryService(permissions PermissionChecker, auditLogger audit.Logger, traceRecorder trace.Recorder) *InMemoryService {
	return NewInMemoryServiceWithStore(nil, permissions, auditLogger, traceRecorder)
}

func NewInMemoryServiceWithStore(store Store, permissions PermissionChecker, auditLogger audit.Logger, traceRecorder trace.Recorder) *InMemoryService {
	return &InMemoryService{
		bases:       map[contracts.KnowledgeBaseID]contracts.KnowledgeBase{},
		documents:   map[contracts.KnowledgeDocumentID]contracts.KnowledgeDocument{},
		jobs:        map[contracts.KnowledgeIngestionJobID]contracts.KnowledgeIngestionJob{},
		adapter:     LocalSearchAdapter{},
		store:       store,
		permissions: permissions,
		audit:       auditLogger,
		trace:       traceRecorder,
		now:         func() time.Time { return time.Now().UTC() },
	}
}

func (s *InMemoryService) CreateKnowledgeBase(ctx context.Context, input CreateKnowledgeBaseInput) (contracts.KnowledgeBase, error) {
	if input.TenantID == "" || input.GroupID == "" || input.Name == "" {
		return contracts.KnowledgeBase{}, fmt.Errorf("tenant_id, group_id and name are required")
	}
	if err := s.requireAllowed(ctx, contracts.PermissionCheckInput{
		TenantID:     input.TenantID,
		GroupID:      input.GroupID,
		ActorID:      input.RequestedBy,
		ActorType:    "member",
		Roles:        input.Roles,
		Action:       contracts.PermissionActionKnowledgeCreate,
		ResourceType: "knowledge_base",
		TraceID:      input.TraceID,
		TaskID:       input.TaskID,
		RunID:        input.RunID,
	}); err != nil {
		return contracts.KnowledgeBase{}, err
	}
	visibility := firstNonEmpty(input.Visibility, contracts.VisibilityGroup)
	sourceType := firstNonEmpty(input.SourceType, "manual")
	indexType := normalizeIndexType(input.IndexType)
	now := s.now()
	base := contracts.KnowledgeBase{
		KnowledgeBaseID: contracts.KnowledgeBaseID(idgen.New("kb")),
		TenantID:        input.TenantID,
		Name:            input.Name,
		OwnerGroupID:    input.GroupID,
		Visibility:      visibility,
		SourceType:      sourceType,
		IndexType:       indexType,
		SearchMode:      searchModeForIndex(indexType),
		Status:          contracts.KnowledgeBaseStatusReady,
		CreatedBy:       input.RequestedBy,
		CreatedAt:       now,
		UpdatedAt:       now,
	}
	s.mu.Lock()
	s.bases[base.KnowledgeBaseID] = base
	s.mu.Unlock()
	if s.store != nil {
		if err := s.store.SaveKnowledgeBase(ctx, base); err != nil {
			return contracts.KnowledgeBase{}, err
		}
	}
	s.recordKnowledgeCreated(ctx, input, base)
	return base, nil
}

func (s *InMemoryService) ListKnowledgeBases(ctx context.Context, tenantID contracts.TenantID, ownerGroupID contracts.GroupID) ([]contracts.KnowledgeBase, error) {
	if tenantID == "" {
		return nil, fmt.Errorf("tenant_id is required")
	}
	bases, _, err := s.snapshot(ctx, tenantID)
	if err != nil {
		return nil, err
	}
	out := make([]contracts.KnowledgeBase, 0, len(bases))
	for _, base := range bases {
		if ownerGroupID != "" && base.OwnerGroupID != ownerGroupID {
			continue
		}
		out = append(out, base)
	}
	sort.SliceStable(out, func(i, j int) bool {
		if out[i].UpdatedAt.Equal(out[j].UpdatedAt) {
			return out[i].KnowledgeBaseID < out[j].KnowledgeBaseID
		}
		return out[i].UpdatedAt.After(out[j].UpdatedAt)
	})
	return out, nil
}

func (s *InMemoryService) GetKnowledgeBase(ctx context.Context, tenantID contracts.TenantID, knowledgeBaseID contracts.KnowledgeBaseID) (contracts.KnowledgeBase, bool, error) {
	if tenantID == "" || knowledgeBaseID == "" {
		return contracts.KnowledgeBase{}, false, fmt.Errorf("tenant_id and knowledge_base_id are required")
	}
	s.mu.RLock()
	base, ok := s.bases[knowledgeBaseID]
	s.mu.RUnlock()
	if ok && base.TenantID == tenantID {
		return base, true, nil
	}
	if s.store != nil {
		return s.store.GetKnowledgeBase(ctx, tenantID, knowledgeBaseID)
	}
	return contracts.KnowledgeBase{}, false, nil
}

func (s *InMemoryService) IngestDocument(ctx context.Context, doc contracts.KnowledgeDocument) (contracts.KnowledgeDocument, error) {
	if doc.TenantID == "" || doc.KnowledgeBaseID == "" || doc.Content == "" {
		return contracts.KnowledgeDocument{}, fmt.Errorf("tenant_id, knowledge_base_id and content are required")
	}
	if doc.DocumentID == "" {
		doc.DocumentID = contracts.KnowledgeDocumentID(idgen.New("kbdoc"))
	}
	if doc.CreatedAt.IsZero() {
		doc.CreatedAt = s.now()
	}
	s.mu.RLock()
	base, ok := s.bases[doc.KnowledgeBaseID]
	s.mu.RUnlock()
	if !ok && s.store != nil {
		stored, storedOK, err := s.store.GetKnowledgeBase(ctx, doc.TenantID, doc.KnowledgeBaseID)
		if err != nil {
			return contracts.KnowledgeDocument{}, err
		}
		if storedOK {
			base, ok = stored, true
		}
	}
	if !ok {
		return contracts.KnowledgeDocument{}, fmt.Errorf("knowledge base %s not found", doc.KnowledgeBaseID)
	}
	if doc.SourceGroupID == "" {
		doc.SourceGroupID = base.OwnerGroupID
	}
	if doc.Visibility == "" {
		doc.Visibility = base.Visibility
	}
	now := s.now()
	doc.IndexStatus = contracts.KnowledgeDocumentIndexReady
	doc.IndexedAt = &now
	s.mu.Lock()
	s.documents[doc.DocumentID] = cloneDocument(doc)
	base.DocumentCount = s.documentCountLocked(doc.TenantID, doc.KnowledgeBaseID)
	if base.SearchMode == "" {
		base.SearchMode = searchModeForIndex(base.IndexType)
	}
	base.Status = contracts.KnowledgeBaseStatusReady
	base.LastIndexedAt = &now
	base.UpdatedAt = now
	s.bases[base.KnowledgeBaseID] = base
	s.mu.Unlock()
	if s.store != nil {
		if err := s.store.SaveDocument(ctx, doc); err != nil {
			return contracts.KnowledgeDocument{}, err
		}
		if err := s.store.SaveKnowledgeBase(ctx, base); err != nil {
			return contracts.KnowledgeDocument{}, err
		}
	}
	job := contracts.KnowledgeIngestionJob{
		JobID:           contracts.KnowledgeIngestionJobID(idgen.New("kbjob")),
		TenantID:        doc.TenantID,
		KnowledgeBaseID: doc.KnowledgeBaseID,
		DocumentID:      doc.DocumentID,
		SourceGroupID:   doc.SourceGroupID,
		Status:          contracts.KnowledgeIngestionCompleted,
		IndexType:       base.IndexType,
		SearchMode:      searchModeForIndex(base.IndexType),
		CreatedAt:       now,
		UpdatedAt:       now,
		CompletedAt:     &now,
	}
	if err := s.saveJob(ctx, job); err != nil {
		return contracts.KnowledgeDocument{}, err
	}
	return cloneDocument(doc), nil
}

func (s *InMemoryService) ListIngestionJobs(ctx context.Context, tenantID contracts.TenantID, knowledgeBaseID contracts.KnowledgeBaseID) ([]contracts.KnowledgeIngestionJob, error) {
	if tenantID == "" {
		return nil, fmt.Errorf("tenant_id is required")
	}
	if s.store != nil {
		return s.store.ListIngestionJobs(ctx, tenantID, knowledgeBaseID)
	}
	s.mu.RLock()
	defer s.mu.RUnlock()
	out := make([]contracts.KnowledgeIngestionJob, 0, len(s.jobs))
	for _, job := range s.jobs {
		if job.TenantID != tenantID {
			continue
		}
		if knowledgeBaseID != "" && job.KnowledgeBaseID != knowledgeBaseID {
			continue
		}
		out = append(out, job)
	}
	sort.SliceStable(out, func(i, j int) bool {
		if out[i].CreatedAt.Equal(out[j].CreatedAt) {
			return out[i].JobID < out[j].JobID
		}
		return out[i].CreatedAt.After(out[j].CreatedAt)
	})
	return out, nil
}

func (s *InMemoryService) GetIngestionJob(ctx context.Context, tenantID contracts.TenantID, jobID contracts.KnowledgeIngestionJobID) (contracts.KnowledgeIngestionJob, bool, error) {
	if tenantID == "" || jobID == "" {
		return contracts.KnowledgeIngestionJob{}, false, fmt.Errorf("tenant_id and job_id are required")
	}
	if s.store != nil {
		return s.store.GetIngestionJob(ctx, tenantID, jobID)
	}
	s.mu.RLock()
	defer s.mu.RUnlock()
	job, ok := s.jobs[jobID]
	if !ok || job.TenantID != tenantID {
		return contracts.KnowledgeIngestionJob{}, false, nil
	}
	return job, true, nil
}

func (s *InMemoryService) Search(ctx context.Context, input SearchInput) ([]contracts.KnowledgeSearchResult, error) {
	if input.TenantID == "" || strings.TrimSpace(input.Query) == "" {
		return nil, fmt.Errorf("tenant_id and query are required")
	}
	if err := s.requireAllowed(ctx, contracts.PermissionCheckInput{
		TenantID:      input.TenantID,
		GroupID:       input.RequesterGroupID,
		ActorID:       input.RequestedBy,
		ActorType:     "member",
		Roles:         input.Roles,
		Action:        contracts.PermissionActionKnowledgeSearch,
		ResourceType:  "knowledge_base",
		ResourceScope: string(input.RequesterGroupID),
		TraceID:       input.TraceID,
		TaskID:        input.TaskID,
		RunID:         input.RunID,
	}); err != nil {
		return nil, err
	}
	s.recordSearch(ctx, input, contracts.TraceKnowledgeSearchRequested, 0)
	bases, docs, err := s.snapshot(ctx, input.TenantID)
	if err != nil {
		return nil, err
	}
	allowedBases := map[contracts.KnowledgeBaseID]struct{}{}
	for _, id := range input.KnowledgeBaseIDs {
		allowedBases[id] = struct{}{}
	}
	candidates := make([]contracts.KnowledgeDocument, 0)
	for _, doc := range docs {
		if doc.TenantID != input.TenantID {
			continue
		}
		if len(allowedBases) > 0 {
			if _, ok := allowedBases[doc.KnowledgeBaseID]; !ok {
				continue
			}
		}
		base, ok := bases[doc.KnowledgeBaseID]
		if !ok || !visibleToGroup(base, doc, input.RequesterGroupID, input.SourceGroupID, input.AllowCrossGroup) {
			continue
		}
		if base.Status == contracts.KnowledgeBaseStatusDisabled {
			continue
		}
		if doc.IndexStatus != "" && doc.IndexStatus != contracts.KnowledgeDocumentIndexReady {
			continue
		}
		candidates = append(candidates, doc)
	}
	limit := input.Limit
	if limit <= 0 || limit > 10 {
		limit = 10
	}
	mode := firstNonEmpty(input.SearchMode, dominantSearchMode(bases, candidates), contracts.KnowledgeSearchBM25)
	adapter := s.adapter
	if adapter == nil {
		adapter = LocalSearchAdapter{}
	}
	results, err := adapter.Search(ctx, AdapterSearchInput{
		Query:      input.Query,
		SearchMode: normalizeSearchMode(mode),
		Limit:      limit,
		Bases:      bases,
		Documents:  candidates,
	})
	if err != nil {
		return nil, err
	}
	s.recordSearch(ctx, input, contracts.TraceKnowledgeSearchCompleted, len(results))
	if s.audit != nil {
		_ = s.audit.Log(ctx, contracts.AuditEvent{
			AuditID:      idgen.New("audit"),
			TenantID:     input.TenantID,
			ActorID:      input.RequestedBy,
			ActorType:    "member",
			Action:       contracts.AuditKnowledgeSearch,
			ResourceType: "knowledge_base",
			ResourceID:   string(input.SourceGroupID),
			Decision:     "allowed",
			Reason:       fmt.Sprintf("results=%d", len(results)),
			TraceID:      input.TraceID,
			TaskID:       input.TaskID,
			RunID:        input.RunID,
			CreatedAt:    s.now(),
		})
	}
	return results, nil
}

func (s *InMemoryService) saveJob(ctx context.Context, job contracts.KnowledgeIngestionJob) error {
	s.mu.Lock()
	s.jobs[job.JobID] = job
	s.mu.Unlock()
	if s.store != nil {
		return s.store.SaveIngestionJob(ctx, job)
	}
	return nil
}

func (s *InMemoryService) documentCountLocked(tenantID contracts.TenantID, knowledgeBaseID contracts.KnowledgeBaseID) int {
	count := 0
	for _, current := range s.documents {
		if current.TenantID == tenantID && current.KnowledgeBaseID == knowledgeBaseID {
			count++
		}
	}
	return count
}

func (s *InMemoryService) snapshot(ctx context.Context, tenantID contracts.TenantID) (map[contracts.KnowledgeBaseID]contracts.KnowledgeBase, []contracts.KnowledgeDocument, error) {
	if s.store != nil {
		bases, err := s.store.ListKnowledgeBases(ctx, tenantID)
		if err != nil {
			return nil, nil, err
		}
		docs, err := s.store.ListDocuments(ctx, tenantID)
		if err != nil {
			return nil, nil, err
		}
		byID := make(map[contracts.KnowledgeBaseID]contracts.KnowledgeBase, len(bases))
		for _, base := range bases {
			byID[base.KnowledgeBaseID] = base
		}
		return byID, docs, nil
	}
	s.mu.RLock()
	defer s.mu.RUnlock()
	bases := make(map[contracts.KnowledgeBaseID]contracts.KnowledgeBase, len(s.bases))
	for id, base := range s.bases {
		bases[id] = base
	}
	docs := make([]contracts.KnowledgeDocument, 0, len(s.documents))
	for _, doc := range s.documents {
		docs = append(docs, cloneDocument(doc))
	}
	return bases, docs, nil
}

func (s *InMemoryService) requireAllowed(ctx context.Context, input contracts.PermissionCheckInput) error {
	if s.permissions == nil {
		return nil
	}
	decision, err := s.permissions.Check(ctx, input)
	if err != nil {
		return err
	}
	if decision.Decision == contracts.PermissionDecisionDenied {
		return contracts.NewRuntimeError(contracts.CodeToolPolicyDenied, decision.Reason, map[string]any{"reason_code": decision.ReasonCode})
	}
	if decision.Decision == contracts.PermissionDecisionApprovalRequired {
		return contracts.NewRuntimeError(contracts.CodeToolApprovalRequired, decision.Reason, map[string]any{"reason_code": decision.ReasonCode})
	}
	return nil
}

func visibleToGroup(base contracts.KnowledgeBase, doc contracts.KnowledgeDocument, requesterGroup contracts.GroupID, sourceGroup contracts.GroupID, allowCrossGroup bool) bool {
	visibility := firstNonEmpty(doc.Visibility, base.Visibility)
	docGroup := doc.SourceGroupID
	if docGroup == "" {
		docGroup = base.OwnerGroupID
	}
	if sourceGroup != "" && docGroup != sourceGroup {
		return false
	}
	if docGroup == requesterGroup || base.OwnerGroupID == requesterGroup {
		return true
	}
	switch visibility {
	case contracts.VisibilityTenant:
		return true
	case contracts.VisibilityShared, contracts.VisibilitySharedGroups:
		return allowCrossGroup
	default:
		return false
	}
}

type LocalSearchAdapter struct{}

func (LocalSearchAdapter) Search(_ context.Context, input AdapterSearchInput) ([]contracts.KnowledgeSearchResult, error) {
	queryTerms := tokenize(input.Query)
	results := make([]contracts.KnowledgeSearchResult, 0)
	for _, doc := range input.Documents {
		base, ok := input.Bases[doc.KnowledgeBaseID]
		if !ok {
			continue
		}
		score := scoreText(queryTerms, doc.Title+" "+doc.Content)
		if input.SearchMode == contracts.KnowledgeSearchHybrid {
			score += lexicalProximityScore(queryTerms, doc.Title)
		}
		if input.SearchMode == contracts.KnowledgeSearchEmbedding {
			score = embeddingFallbackScore(queryTerms, doc.Title+" "+doc.Content)
		}
		if score <= 0 {
			continue
		}
		results = append(results, contracts.KnowledgeSearchResult{
			DocumentID:      doc.DocumentID,
			KnowledgeBaseID: doc.KnowledgeBaseID,
			SourceGroupID:   doc.SourceGroupID,
			Title:           doc.Title,
			Snippet:         snippet(doc.Content, input.Query, 220),
			Score:           score,
			SourceURI:       doc.SourceURI,
			Visibility:      firstNonEmpty(doc.Visibility, base.Visibility),
			SearchMode:      input.SearchMode,
		})
	}
	sort.SliceStable(results, func(i, j int) bool {
		if results[i].Score == results[j].Score {
			return results[i].DocumentID < results[j].DocumentID
		}
		return results[i].Score > results[j].Score
	})
	if input.Limit > 0 && len(results) > input.Limit {
		results = results[:input.Limit]
	}
	return results, nil
}

func tokenize(value string) []string {
	fields := strings.Fields(strings.ToLower(value))
	out := make([]string, 0, len(fields))
	seen := map[string]struct{}{}
	for _, field := range fields {
		field = strings.Trim(field, ".,;:!?()[]{}\"'")
		if field == "" {
			continue
		}
		if _, ok := seen[field]; ok {
			continue
		}
		seen[field] = struct{}{}
		out = append(out, field)
	}
	if len(out) == 0 && strings.TrimSpace(value) != "" {
		out = []string{strings.ToLower(strings.TrimSpace(value))}
	}
	return out
}

func scoreText(queryTerms []string, text string) float64 {
	lower := strings.ToLower(text)
	score := 0.0
	for _, term := range queryTerms {
		if strings.Contains(lower, term) {
			score += 1
		}
	}
	if score == 0 && len(queryTerms) == 1 {
		for _, r := range []rune(queryTerms[0]) {
			if strings.ContainsRune(lower, r) {
				score += 0.05
			}
		}
	}
	return score
}

func lexicalProximityScore(queryTerms []string, title string) float64 {
	if title == "" {
		return 0
	}
	lower := strings.ToLower(title)
	score := 0.0
	for _, term := range queryTerms {
		if strings.Contains(lower, term) {
			score += 0.25
		}
	}
	return score
}

func embeddingFallbackScore(queryTerms []string, text string) float64 {
	score := scoreText(queryTerms, text)
	if score > 0 {
		return score * 0.8
	}
	lower := strings.ToLower(text)
	for _, term := range queryTerms {
		for _, r := range []rune(term) {
			if strings.ContainsRune(lower, r) {
				score += 0.03
			}
		}
	}
	return score
}

func normalizeIndexType(value string) string {
	switch strings.ToLower(strings.TrimSpace(value)) {
	case contracts.KnowledgeIndexEmbedding:
		return contracts.KnowledgeIndexEmbedding
	case contracts.KnowledgeIndexHybrid:
		return contracts.KnowledgeIndexHybrid
	default:
		return contracts.KnowledgeIndexBM25
	}
}

func normalizeSearchMode(value string) string {
	switch strings.ToLower(strings.TrimSpace(value)) {
	case contracts.KnowledgeSearchEmbedding:
		return contracts.KnowledgeSearchEmbedding
	case contracts.KnowledgeSearchHybrid:
		return contracts.KnowledgeSearchHybrid
	default:
		return contracts.KnowledgeSearchBM25
	}
}

func searchModeForIndex(indexType string) string {
	switch normalizeIndexType(indexType) {
	case contracts.KnowledgeIndexEmbedding:
		return contracts.KnowledgeSearchEmbedding
	case contracts.KnowledgeIndexHybrid:
		return contracts.KnowledgeSearchHybrid
	default:
		return contracts.KnowledgeSearchBM25
	}
}

func dominantSearchMode(bases map[contracts.KnowledgeBaseID]contracts.KnowledgeBase, docs []contracts.KnowledgeDocument) string {
	for _, doc := range docs {
		if base, ok := bases[doc.KnowledgeBaseID]; ok {
			return firstNonEmpty(base.SearchMode, searchModeForIndex(base.IndexType))
		}
	}
	return contracts.KnowledgeSearchBM25
}

func snippet(content string, query string, maxRunes int) string {
	content = strings.TrimSpace(content)
	if content == "" {
		return ""
	}
	if utf8.RuneCountInString(content) <= maxRunes {
		return content
	}
	lower := strings.ToLower(content)
	pos := strings.Index(lower, strings.ToLower(strings.TrimSpace(query)))
	if pos < 0 {
		return string([]rune(content)[:maxRunes])
	}
	runes := []rune(content)
	start := 0
	byteCount := 0
	for i, r := range content {
		if i >= pos {
			start = byteCount
			break
		}
		_ = r
		byteCount++
	}
	if start > maxRunes/3 {
		start -= maxRunes / 3
	}
	end := start + maxRunes
	if end > len(runes) {
		end = len(runes)
	}
	return string(runes[start:end])
}

func (s *InMemoryService) recordKnowledgeCreated(ctx context.Context, input CreateKnowledgeBaseInput, base contracts.KnowledgeBase) {
	if s.trace != nil && input.TraceID != "" {
		_ = s.trace.Record(ctx, contracts.TraceEvent{
			TraceID:  input.TraceID,
			TenantID: input.TenantID,
			SpanID:   contracts.SpanID(idgen.New("span")),
			RunID:    input.RunID,
			TaskID:   input.TaskID,
			Type:     contracts.TraceKnowledgeCreated,
			Payload: map[string]any{
				"knowledge_base_id": base.KnowledgeBaseID,
				"group_id":          base.OwnerGroupID,
				"visibility":        base.Visibility,
				"index_type":        base.IndexType,
				"search_mode":       base.SearchMode,
			},
			CreatedAt: s.now(),
		})
	}
	if s.audit != nil {
		_ = s.audit.Log(ctx, contracts.AuditEvent{
			AuditID:      idgen.New("audit"),
			TenantID:     input.TenantID,
			ActorID:      input.RequestedBy,
			ActorType:    "member",
			Action:       contracts.AuditKnowledgeCreated,
			ResourceType: "knowledge_base",
			ResourceID:   string(base.KnowledgeBaseID),
			Decision:     "allowed",
			Reason:       "visibility=" + base.Visibility,
			TraceID:      input.TraceID,
			TaskID:       input.TaskID,
			RunID:        input.RunID,
			CreatedAt:    s.now(),
		})
	}
}

func (s *InMemoryService) recordSearch(ctx context.Context, input SearchInput, eventType string, count int) {
	if s.trace == nil || input.TraceID == "" {
		return
	}
	_ = s.trace.Record(ctx, contracts.TraceEvent{
		TraceID:  input.TraceID,
		TenantID: input.TenantID,
		SpanID:   contracts.SpanID(idgen.New("span")),
		RunID:    input.RunID,
		TaskID:   input.TaskID,
		Type:     eventType,
		Payload: map[string]any{
			"requester_group_id": input.RequesterGroupID,
			"source_group_id":    input.SourceGroupID,
			"allow_cross_group":  input.AllowCrossGroup,
			"search_mode":        input.SearchMode,
			"result_count":       count,
		},
		CreatedAt: s.now(),
	})
}

func firstNonEmpty(values ...string) string {
	for _, value := range values {
		if value != "" {
			return value
		}
	}
	return ""
}

func cloneDocument(doc contracts.KnowledgeDocument) contracts.KnowledgeDocument {
	if doc.Metadata != nil {
		metadata := map[string]any{}
		for key, value := range doc.Metadata {
			metadata[key] = value
		}
		doc.Metadata = metadata
	}
	return doc
}
