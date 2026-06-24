package conversation

import (
	"context"
	"sort"
	"strings"
	"sync"

	"znt/internal/contracts"
	storagerepo "znt/internal/storage/repository"
)

type InMemoryStore struct {
	mu       sync.RWMutex
	threads  map[string]Thread
	messages map[string]MessageRecord
	order    map[string][]string
}

func NewInMemoryStore() *InMemoryStore {
	return &InMemoryStore{
		threads:  map[string]Thread{},
		messages: map[string]MessageRecord{},
		order:    map[string][]string{},
	}
}

func (s *InMemoryStore) UpsertThread(_ context.Context, thread Thread) error {
	if thread.TenantID == "" || strings.TrimSpace(thread.ConversationID) == "" {
		return contracts.NewRuntimeError(contracts.CodeDecisionSchemaError, "conversation thread requires tenant_id and conversation_id", nil)
	}
	if strings.TrimSpace(thread.ThreadID) == "" {
		thread.ThreadID = thread.ConversationID
	}
	key := threadKey(thread.TenantID, thread.ConversationID, thread.ThreadID)
	s.mu.Lock()
	defer s.mu.Unlock()
	existing, ok := s.threads[key]
	if ok && !existing.CreatedAt.IsZero() && thread.CreatedAt.IsZero() {
		thread.CreatedAt = existing.CreatedAt
	}
	s.threads[key] = thread
	return nil
}

func (s *InMemoryStore) GetThread(_ context.Context, tenantID contracts.TenantID, conversationID string, threadID string) (Thread, error) {
	if threadID == "" {
		threadID = conversationID
	}
	s.mu.RLock()
	defer s.mu.RUnlock()
	thread, ok := s.threads[threadKey(tenantID, conversationID, threadID)]
	if !ok {
		return Thread{}, storagerepo.ErrNotFound
	}
	return cloneThread(thread), nil
}

func (s *InMemoryStore) ListThreads(_ context.Context, tenantID contracts.TenantID, kind string, limit int, offset int) ([]Thread, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	out := make([]Thread, 0)
	for _, thread := range s.threads {
		if thread.TenantID != tenantID {
			continue
		}
		if kind != "" && thread.Kind != kind {
			continue
		}
		out = append(out, cloneThread(thread))
	}
	sort.SliceStable(out, func(i, j int) bool {
		left := out[i].UpdatedAt
		if left.IsZero() {
			left = out[i].CreatedAt
		}
		right := out[j].UpdatedAt
		if right.IsZero() {
			right = out[j].CreatedAt
		}
		if left.Equal(right) {
			return out[i].ConversationID > out[j].ConversationID
		}
		return left.After(right)
	})
	if offset < 0 {
		offset = 0
	}
	if offset >= len(out) {
		return []Thread{}, nil
	}
	if limit <= 0 {
		limit = len(out)
	}
	end := offset + limit
	if end > len(out) {
		end = len(out)
	}
	return append([]Thread(nil), out[offset:end]...), nil
}

func (s *InMemoryStore) AppendMessage(_ context.Context, message MessageRecord) error {
	if message.TenantID == "" || strings.TrimSpace(message.ConversationID) == "" || strings.TrimSpace(message.Message.MessageID) == "" {
		return contracts.NewRuntimeError(contracts.CodeDecisionSchemaError, "conversation message requires tenant_id, conversation_id and message_id", nil)
	}
	if strings.TrimSpace(message.ThreadID) == "" {
		message.ThreadID = message.ConversationID
	}
	if strings.TrimSpace(message.Message.ThreadID) == "" {
		message.Message.ThreadID = message.ThreadID
	}
	key := messageKey(message.TenantID, message.ConversationID, message.Message.MessageID)
	s.mu.Lock()
	defer s.mu.Unlock()
	if existing, ok := s.messages[key]; ok {
		if sameMessage(existing, message) {
			return nil
		}
		return contracts.NewRuntimeError(contracts.CodeTaskConflict, "conversation message id already exists with different content", map[string]any{
			"conversation_id": message.ConversationID,
			"message_id":      message.Message.MessageID,
		})
	}
	s.messages[key] = message
	tkey := threadKey(message.TenantID, message.ConversationID, message.ThreadID)
	s.order[tkey] = append(s.order[tkey], key)
	return nil
}

func (s *InMemoryStore) RecentMessages(_ context.Context, tenantID contracts.TenantID, conversationID string, threadID string, limit int) ([]contracts.ConversationMessage, error) {
	if threadID == "" {
		threadID = conversationID
	}
	s.mu.RLock()
	defer s.mu.RUnlock()
	keys := append([]string(nil), s.order[threadKey(tenantID, conversationID, threadID)]...)
	sort.SliceStable(keys, func(i, j int) bool {
		left := s.messages[keys[i]].Message.CreatedAt
		right := s.messages[keys[j]].Message.CreatedAt
		if left.Equal(right) {
			return keys[i] < keys[j]
		}
		return left.Before(right)
	})
	if limit > 0 && len(keys) > limit {
		keys = keys[len(keys)-limit:]
	}
	out := make([]contracts.ConversationMessage, 0, len(keys))
	for _, key := range keys {
		out = append(out, messageWithMetadata(s.messages[key]))
	}
	return out, nil
}

func (s *InMemoryStore) GetMessage(_ context.Context, tenantID contracts.TenantID, conversationID string, messageID string) (contracts.ConversationMessage, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	record, ok := s.messages[messageKey(tenantID, conversationID, messageID)]
	if !ok {
		return contracts.ConversationMessage{}, storagerepo.ErrNotFound
	}
	return messageWithMetadata(record), nil
}

func cloneThread(thread Thread) Thread {
	if thread.ExternalRefs != nil {
		refs := map[string]string{}
		for key, value := range thread.ExternalRefs {
			refs[key] = value
		}
		thread.ExternalRefs = refs
	}
	return thread
}

func messageWithMetadata(record MessageRecord) contracts.ConversationMessage {
	message := record.Message
	if record.Metadata != nil {
		metadata := map[string]any{}
		for key, value := range record.Metadata {
			metadata[key] = value
		}
		message.Metadata = metadata
	}
	return message
}

func sameMessage(left MessageRecord, right MessageRecord) bool {
	return left.ThreadID == right.ThreadID &&
		left.Message.Text == right.Message.Text &&
		left.Message.SpeakerID == right.Message.SpeakerID &&
		left.Message.SpeakerType == right.Message.SpeakerType &&
		left.Message.ThreadID == right.Message.ThreadID
}

func threadKey(tenantID contracts.TenantID, conversationID string, threadID string) string {
	return strings.Join([]string{string(tenantID), conversationID, threadID}, "\x00")
}

func messageKey(tenantID contracts.TenantID, conversationID string, messageID string) string {
	return strings.Join([]string{string(tenantID), conversationID, messageID}, "\x00")
}
