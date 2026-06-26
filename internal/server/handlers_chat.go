package server

import (
	"errors"
	"net/http"
	"sort"
	"strconv"
	"strings"
	"time"

	"znt/internal/app/auth"
	"znt/internal/app/core"
	contextconversation "znt/internal/context/conversation"
	"znt/internal/contracts"
	conversationstore "znt/internal/conversation"
	"znt/internal/runtime/kernel"
	storagerepo "znt/internal/storage/repository"
	"znt/pkg/idgen"
)

const chatProvider = "znt-cmd"
const chatAllowedToolIDsExternalRef = "chat_allowed_tool_ids"
const chatConversationStatusActive = "active"
const chatConversationStatusArchived = "archived"

type createChatConversationRequest struct {
	Name         string                   `json:"name"`
	MainAgentID  contracts.AgentID        `json:"main_agent_id"`
	MainAgent    contracts.AgentID        `json:"main_agent"`
	AgentID      contracts.AgentID        `json:"agent_id"`
	Version      contracts.AgentVersion   `json:"version,omitempty"`
	MemberAgents []chatMemberAgentRequest `json:"member_agents,omitempty"`
	Members      []chatMemberAgentRequest `json:"members,omitempty"`
}

type chatMemberAgentRequest struct {
	AgentID     contracts.AgentID      `json:"agent_id"`
	Version     contracts.AgentVersion `json:"version,omitempty"`
	Name        string                 `json:"name,omitempty"`
	DisplayName string                 `json:"display_name,omitempty"`
	Status      string                 `json:"status,omitempty"`
}

type sendChatMessageRequest struct {
	Text        string `json:"text"`
	MessageID   string `json:"message_id,omitempty"`
	SpeakerName string `json:"speaker_name,omitempty"`
	DisplayName string `json:"display_name,omitempty"`
	UserName    string `json:"user_name,omitempty"`
}

func handleChatConversations(w http.ResponseWriter, r *http.Request, appCore *core.Core, caller auth.CallerIdentity) {
	switch r.Method {
	case http.MethodGet:
		handleChatConversationList(w, r, appCore, caller)
	case http.MethodPost:
		handleChatConversationCreate(w, r, appCore, caller)
	default:
		writeError(w, contracts.NewRuntimeError(contracts.CodeDecisionSchemaError, "unsupported chat conversations method", nil), http.StatusMethodNotAllowed)
	}
}

func handleChatConversationResource(w http.ResponseWriter, r *http.Request, appCore *core.Core, caller auth.CallerIdentity, path string) {
	conversationID, suffix, _ := strings.Cut(strings.Trim(path, "/"), "/")
	if conversationID == "" {
		writeError(w, contracts.NewRuntimeError(contracts.CodeDecisionSchemaError, "conversation_id is required", nil), http.StatusBadRequest)
		return
	}
	if suffix == "" {
		switch r.Method {
		case http.MethodGet:
			handleChatConversationDetail(w, r, appCore, caller, conversationID)
		case http.MethodDelete:
			handleChatConversationArchive(w, r, appCore, caller, conversationID)
		default:
			writeError(w, contracts.NewRuntimeError(contracts.CodeDecisionSchemaError, "unsupported chat conversation method", nil), http.StatusMethodNotAllowed)
		}
		return
	}
	switch suffix {
	case "messages":
		switch r.Method {
		case http.MethodGet:
			handleChatMessages(w, r, appCore, caller, conversationID)
		case http.MethodPost:
			handleChatMessageSend(w, r, appCore, caller, conversationID)
		default:
			writeError(w, contracts.NewRuntimeError(contracts.CodeDecisionSchemaError, "unsupported chat messages method", nil), http.StatusMethodNotAllowed)
		}
	case "members":
		switch r.Method {
		case http.MethodGet:
			handleChatMembers(w, r, appCore, caller, conversationID)
		case http.MethodPost:
			handleChatMemberAdd(w, r, appCore, caller, conversationID)
		default:
			writeError(w, contracts.NewRuntimeError(contracts.CodeDecisionSchemaError, "unsupported chat members method", nil), http.StatusMethodNotAllowed)
		}
	case "members/agents":
		if r.Method != http.MethodPost {
			writeError(w, contracts.NewRuntimeError(contracts.CodeDecisionSchemaError, "unsupported chat member agents method", nil), http.StatusMethodNotAllowed)
			return
		}
		handleChatMemberAdd(w, r, appCore, caller, conversationID)
	default:
		if strings.HasPrefix(suffix, "members/agents/") {
			memberID := strings.TrimPrefix(suffix, "members/agents/")
			if r.Method != http.MethodDelete {
				writeError(w, contracts.NewRuntimeError(contracts.CodeDecisionSchemaError, "unsupported chat member agent method", nil), http.StatusMethodNotAllowed)
				return
			}
			handleChatMemberRemove(w, r, appCore, caller, conversationID, memberID)
			return
		}
		if strings.HasPrefix(suffix, "members/") {
			memberID := strings.TrimPrefix(suffix, "members/")
			if r.Method != http.MethodDelete {
				writeError(w, contracts.NewRuntimeError(contracts.CodeDecisionSchemaError, "unsupported chat member method", nil), http.StatusMethodNotAllowed)
				return
			}
			handleChatMemberRemove(w, r, appCore, caller, conversationID, memberID)
			return
		}
		writeError(w, contracts.NewRuntimeError(contracts.CodeDecisionSchemaError, "unknown chat conversation resource", nil), http.StatusNotFound)
	}
}

func handleChatConversationCreate(w http.ResponseWriter, r *http.Request, appCore *core.Core, caller auth.CallerIdentity) {
	var payload createChatConversationRequest
	if !decodeJSONPayload(w, r, &payload, "invalid chat conversation json") {
		return
	}
	mainAgentID := firstAgentID(payload.MainAgentID, payload.MainAgent, payload.AgentID)
	if mainAgentID == "" {
		writeError(w, contracts.NewRuntimeError(contracts.CodeDecisionSchemaError, "main_agent_id is required", nil), http.StatusBadRequest)
		return
	}
	now := time.Now().UTC()
	conversationID := idgen.New("chat")
	name := strings.TrimSpace(payload.Name)
	if name == "" {
		name = "群聊会话"
	}
	thread := conversationstore.Thread{
		TenantID:       caller.TenantID,
		ConversationID: conversationID,
		ThreadID:       conversationID,
		Kind:           contextconversation.KindGroup,
		Provider:       chatProvider,
		ExternalRefs: map[string]string{
			"name":               name,
			"main_agent_id":      string(mainAgentID),
			"main_agent_version": string(payload.Version),
			"status":             chatConversationStatusActive,
		},
		CreatedAt: now,
		UpdatedAt: now,
	}
	if err := appCore.Conversations.UpsertThread(r.Context(), thread); err != nil {
		writeRuntimeError(w, err)
		return
	}
	if err := upsertChatAgentMember(r, appCore, caller, conversationID, mainAgentID, payload.Version, nameForAgent(r, appCore, caller.TenantID, mainAgentID, payload.Version), "main_agent", contracts.MemberStatusActive, nil, "main_agent"); err != nil {
		writeRuntimeError(w, err)
		return
	}
	for _, member := range append(payload.MemberAgents, payload.Members...) {
		if member.AgentID == "" {
			continue
		}
		displayName := firstNonEmpty(strings.TrimSpace(member.DisplayName), strings.TrimSpace(member.Name), nameForAgent(r, appCore, caller.TenantID, member.AgentID, member.Version))
		status := firstNonEmpty(strings.TrimSpace(member.Status), contracts.MemberStatusActive)
		toolIDs, bindingStatus, err := syncChatAgentToolBinding(r, appCore, caller, member.AgentID, member.Version)
		if err != nil {
			writeRuntimeError(w, err)
			return
		}
		if err := upsertChatAgentMember(r, appCore, caller, conversationID, member.AgentID, member.Version, displayName, "tool_agent", status, toolIDs, bindingStatus); err != nil {
			writeRuntimeError(w, err)
			return
		}
	}
	thread, _ = appCore.Conversations.GetThread(r.Context(), caller.TenantID, conversationID, conversationID)
	members, _ := appCore.Identity.ListGroupMembers(r.Context(), caller.TenantID, contracts.GroupID(conversationID))
	writeJSON(w, map[string]any{"conversation": chatConversationView(thread, members), "members": chatMemberViews(members)}, http.StatusCreated)
}

func handleChatConversationList(w http.ResponseWriter, r *http.Request, appCore *core.Core, caller auth.CallerIdentity) {
	limit := chatQueryInt(r, "limit", 50)
	offset := chatQueryInt(r, "offset", 0)
	threads, err := appCore.Conversations.ListThreads(r.Context(), caller.TenantID, contextconversation.KindGroup, limit, offset)
	if err != nil {
		writeRuntimeError(w, err)
		return
	}
	status := strings.ToLower(strings.TrimSpace(r.URL.Query().Get("status")))
	if status == "" {
		status = chatConversationStatusActive
	}
	out := make([]map[string]any, 0, len(threads))
	for _, thread := range threads {
		if thread.Provider != "" && thread.Provider != chatProvider {
			continue
		}
		if !chatConversationStatusMatches(thread, status) {
			continue
		}
		members, _ := appCore.Identity.ListGroupMembers(r.Context(), caller.TenantID, contracts.GroupID(thread.ConversationID))
		out = append(out, chatConversationView(thread, members))
	}
	writeJSON(w, map[string]any{"conversations": out}, http.StatusOK)
}

func handleChatConversationDetail(w http.ResponseWriter, r *http.Request, appCore *core.Core, caller auth.CallerIdentity, conversationID string) {
	thread, err := appCore.Conversations.GetThread(r.Context(), caller.TenantID, conversationID, conversationID)
	if err != nil {
		writeChatNotFoundOrError(w, err)
		return
	}
	members, _ := appCore.Identity.ListGroupMembers(r.Context(), caller.TenantID, contracts.GroupID(conversationID))
	writeJSON(w, map[string]any{"conversation": chatConversationView(thread, members), "members": chatMemberViews(members)}, http.StatusOK)
}

func handleChatConversationArchive(w http.ResponseWriter, r *http.Request, appCore *core.Core, caller auth.CallerIdentity, conversationID string) {
	thread, err := appCore.Conversations.GetThread(r.Context(), caller.TenantID, conversationID, conversationID)
	if err != nil {
		writeChatNotFoundOrError(w, err)
		return
	}
	now := time.Now().UTC()
	refs := cloneStringMap(thread.ExternalRefs)
	refs["status"] = chatConversationStatusArchived
	refs["archived_at"] = now.Format(time.RFC3339Nano)
	if caller.CallerID != "" {
		refs["archived_by"] = caller.CallerID
	}
	thread.ExternalRefs = refs
	thread.UpdatedAt = now
	if err := appCore.Conversations.UpsertThread(r.Context(), thread); err != nil {
		writeRuntimeError(w, err)
		return
	}
	members, _ := appCore.Identity.ListGroupMembers(r.Context(), caller.TenantID, contracts.GroupID(conversationID))
	writeJSON(w, map[string]any{"conversation": chatConversationView(thread, members), "archived": true}, http.StatusOK)
}

func handleChatMembers(w http.ResponseWriter, r *http.Request, appCore *core.Core, caller auth.CallerIdentity, conversationID string) {
	if _, err := appCore.Conversations.GetThread(r.Context(), caller.TenantID, conversationID, conversationID); err != nil {
		writeChatNotFoundOrError(w, err)
		return
	}
	members, err := appCore.Identity.ListGroupMembers(r.Context(), caller.TenantID, contracts.GroupID(conversationID))
	if err != nil {
		writeRuntimeError(w, err)
		return
	}
	writeJSON(w, map[string]any{"members": chatMemberViews(members)}, http.StatusOK)
}

func handleChatMemberAdd(w http.ResponseWriter, r *http.Request, appCore *core.Core, caller auth.CallerIdentity, conversationID string) {
	if _, err := appCore.Conversations.GetThread(r.Context(), caller.TenantID, conversationID, conversationID); err != nil {
		writeChatNotFoundOrError(w, err)
		return
	}
	var payload chatMemberAgentRequest
	if !decodeJSONPayload(w, r, &payload, "invalid chat member json") {
		return
	}
	if payload.AgentID == "" {
		writeError(w, contracts.NewRuntimeError(contracts.CodeDecisionSchemaError, "agent_id is required", nil), http.StatusBadRequest)
		return
	}
	displayName := firstNonEmpty(strings.TrimSpace(payload.DisplayName), strings.TrimSpace(payload.Name), nameForAgent(r, appCore, caller.TenantID, payload.AgentID, payload.Version))
	status := firstNonEmpty(strings.TrimSpace(payload.Status), contracts.MemberStatusActive)
	toolIDs, bindingStatus, err := syncChatAgentToolBinding(r, appCore, caller, payload.AgentID, payload.Version)
	if err != nil {
		writeRuntimeError(w, err)
		return
	}
	if err := upsertChatAgentMember(r, appCore, caller, conversationID, payload.AgentID, payload.Version, displayName, "tool_agent", status, toolIDs, bindingStatus); err != nil {
		writeRuntimeError(w, err)
		return
	}
	members, _ := appCore.Identity.ListGroupMembers(r.Context(), caller.TenantID, contracts.GroupID(conversationID))
	writeJSON(w, map[string]any{"members": chatMemberViews(members), "binding_status": bindingStatus, "tool_ids": toolIDs}, http.StatusOK)
}

func handleChatMemberRemove(w http.ResponseWriter, r *http.Request, appCore *core.Core, caller auth.CallerIdentity, conversationID string, memberID string) {
	if _, err := appCore.Conversations.GetThread(r.Context(), caller.TenantID, conversationID, conversationID); err != nil {
		writeChatNotFoundOrError(w, err)
		return
	}
	if memberID == "" {
		writeError(w, contracts.NewRuntimeError(contracts.CodeDecisionSchemaError, "member_id is required", nil), http.StatusBadRequest)
		return
	}
	members, err := appCore.Identity.ListGroupMembers(r.Context(), caller.TenantID, contracts.GroupID(conversationID))
	if err != nil {
		writeRuntimeError(w, err)
		return
	}
	var found *contracts.GroupMemberProfile
	for _, member := range members {
		if string(member.MemberID) == memberID {
			copy := member
			found = &copy
			break
		}
	}
	if found == nil {
		writeError(w, contracts.NewRuntimeError(contracts.CodeAgentNotFound, "member not found", nil), http.StatusNotFound)
		return
	}
	found.Status = contracts.MemberStatusLeft
	_, err = appCore.Identity.UpsertMember(r.Context(), *found)
	if err != nil {
		writeRuntimeError(w, err)
		return
	}
	members, _ = appCore.Identity.ListGroupMembers(r.Context(), caller.TenantID, contracts.GroupID(conversationID))
	writeJSON(w, map[string]any{"members": chatMemberViews(members), "binding_status": "pending_backend_unbinding"}, http.StatusOK)
}

func handleChatMessages(w http.ResponseWriter, r *http.Request, appCore *core.Core, caller auth.CallerIdentity, conversationID string) {
	if _, err := appCore.Conversations.GetThread(r.Context(), caller.TenantID, conversationID, conversationID); err != nil {
		writeChatNotFoundOrError(w, err)
		return
	}
	limit := chatQueryInt(r, "limit", 50)
	messages, err := appCore.Conversations.RecentMessages(r.Context(), caller.TenantID, conversationID, conversationID, limit)
	if err != nil {
		writeRuntimeError(w, err)
		return
	}
	writeJSON(w, map[string]any{"messages": chatMessageViews(chatVisibleMessages(messages), nil)}, http.StatusOK)
}

func handleChatMessageSend(w http.ResponseWriter, r *http.Request, appCore *core.Core, caller auth.CallerIdentity, conversationID string) {
	thread, err := appCore.Conversations.GetThread(r.Context(), caller.TenantID, conversationID, conversationID)
	if err != nil {
		writeChatNotFoundOrError(w, err)
		return
	}
	var payload sendChatMessageRequest
	if !decodeJSONPayload(w, r, &payload, "invalid chat message json") {
		return
	}
	text := strings.TrimSpace(payload.Text)
	if text == "" {
		writeError(w, contracts.NewRuntimeError(contracts.CodeDecisionSchemaError, "text is required", nil), http.StatusBadRequest)
		return
	}
	mainAgentID := contracts.AgentID(thread.ExternalRefs["main_agent_id"])
	if mainAgentID == "" {
		writeError(w, contracts.NewRuntimeError(contracts.CodeDecisionSchemaError, "chat conversation requires main_agent_id", nil), http.StatusBadRequest)
		return
	}
	messageID := strings.TrimSpace(payload.MessageID)
	if messageID == "" {
		messageID = idgen.New("msg")
	}
	now := time.Now().UTC()
	members, _ := appCore.Identity.ListGroupMembers(r.Context(), caller.TenantID, contracts.GroupID(conversationID))
	speakerName := chatCallerDisplayName(caller, payload, members)
	userMessage := contracts.ConversationMessage{
		MessageID:   messageID,
		SpeakerID:   caller.CallerID,
		SpeakerType: caller.CallerType,
		SpeakerName: speakerName,
		Text:        text,
		ThreadID:    conversationID,
		CreatedAt:   now,
		Metadata:    chatCallerMetadata(caller, speakerName),
	}
	if userMessage.SpeakerType == "" {
		userMessage.SpeakerType = "user"
	}
	if userMessage.SpeakerID == "" {
		userMessage.SpeakerID = string(caller.TenantID)
	}
	if err := appCore.Conversations.AppendMessage(r.Context(), conversationstore.MessageRecord{
		TenantID:       caller.TenantID,
		ConversationID: conversationID,
		ThreadID:       conversationID,
		Message:        userMessage,
		Metadata:       chatMessageMetadata("chat_user", caller, speakerName),
	}); err != nil {
		writeRuntimeError(w, err)
		return
	}
	thread.LastMessageAt = now
	thread.UpdatedAt = now
	if err := appCore.Conversations.UpsertThread(r.Context(), thread); err != nil {
		writeRuntimeError(w, err)
		return
	}
	recent, _ := appCore.Conversations.RecentMessages(r.Context(), caller.TenantID, conversationID, conversationID, 20)
	externalRefs := cloneStringMap(thread.ExternalRefs)
	if toolIDs := chatAllowedToolIDs(members); len(toolIDs) > 0 {
		externalRefs[chatAllowedToolIDsExternalRef] = strings.Join(toolIDs, ",")
	}
	envelope := contracts.AgentEnvelope{
		EnvelopeID: idgen.New("env"),
		TraceID:    contracts.TraceID(idgen.New("trace")),
		Target: contracts.AgentTarget{
			AgentID: mainAgentID,
			Version: contracts.AgentVersion(thread.ExternalRefs["main_agent_version"]),
		},
		Caller: contracts.AgentCaller{
			CallerID:    caller.CallerID,
			CallerType:  caller.CallerType,
			DisplayName: speakerName,
			TenantID:    caller.TenantID,
		},
		Command: "agent.run",
		Payload: map[string]any{"input": text},
		Context: contracts.RuntimeContext{
			TenantID: caller.TenantID,
			UserID:   contracts.UserID(caller.CallerID),
			Conversation: &contracts.RuntimeConversation{
				Provider:       chatProvider,
				Kind:           contextconversation.KindGroup,
				ConversationID: conversationID,
				ThreadID:       conversationID,
				ExternalRefs:   externalRefs,
				CurrentMessage: &contracts.RuntimeMessage{
					MessageID:   messageID,
					SpeakerID:   userMessage.SpeakerID,
					SpeakerType: userMessage.SpeakerType,
					SpeakerName: userMessage.SpeakerName,
					Mentions:    []string{string(mainAgentID)},
					Text:        text,
					ThreadID:    conversationID,
					CreatedAt:   now,
					Metadata:    chatCallerMetadata(caller, speakerName),
				},
				RecentMessages: recent,
				Participants:   chatParticipants(mainAgentID, members, chatCurrentUserParticipant(userMessage)),
			},
		},
		CreatedAt: now,
	}
	result, err := dispatchCommand(r, appCore, nil, envelope, caller)
	if err != nil {
		writeRuntimeError(w, err)
		return
	}
	runResult, ok := result.(kernel.RunResult)
	if !ok {
		writeJSON(w, map[string]any{"result": result}, http.StatusOK)
		return
	}
	replyMessage, appendErr := appendMainAgentReply(r, appCore, caller, conversationID, mainAgentID, envelope.TraceID, runResult)
	if appendErr != nil {
		writeRuntimeError(w, appendErr)
		return
	}
	messages := []contracts.ConversationMessage{userMessage}
	if replyMessage != nil {
		messages = append(messages, *replyMessage)
	}
	refs := map[string]chatRunRef{}
	if replyMessage != nil {
		refs[replyMessage.MessageID] = chatRunRef{RunID: runResult.RunID, TaskID: runResult.TaskID, TraceID: envelope.TraceID}
	}
	writeJSON(w, map[string]any{
		"conversation_id": conversationID,
		"run_id":          runResult.RunID,
		"task_id":         runResult.TaskID,
		"trace_id":        envelope.TraceID,
		"status":          runResult.Status,
		"messages":        chatMessageViews(messages, refs),
	}, http.StatusOK)
}

func appendMainAgentReply(r *http.Request, appCore *core.Core, caller auth.CallerIdentity, conversationID string, mainAgentID contracts.AgentID, traceID contracts.TraceID, result kernel.RunResult) (*contracts.ConversationMessage, error) {
	reply := result.Reply
	if reply == nil || strings.TrimSpace(reply.Text) == "" {
		return nil, nil
	}
	now := time.Now().UTC()
	thread, threadErr := appCore.Conversations.GetThread(r.Context(), caller.TenantID, conversationID, conversationID)
	if threadErr == nil && !thread.LastMessageAt.IsZero() && !now.After(thread.LastMessageAt) {
		now = thread.LastMessageAt.Add(time.Nanosecond)
	}
	message := contracts.ConversationMessage{
		MessageID:   idgen.New("msg"),
		SpeakerID:   string(mainAgentID),
		SpeakerType: "agent",
		SpeakerName: string(mainAgentID),
		Text:        strings.TrimSpace(reply.Text),
		ThreadID:    conversationID,
		CreatedAt:   now,
	}
	if err := appCore.Conversations.AppendMessage(r.Context(), conversationstore.MessageRecord{
		TenantID:       caller.TenantID,
		ConversationID: conversationID,
		ThreadID:       conversationID,
		Message:        message,
		Metadata: map[string]any{
			"run_id":   result.RunID,
			"task_id":  result.TaskID,
			"trace_id": traceID,
		},
	}); err != nil {
		return nil, err
	}
	if threadErr == nil {
		thread.LastMessageAt = now
		thread.UpdatedAt = now
		_ = appCore.Conversations.UpsertThread(r.Context(), thread)
	}
	return &message, nil
}

type chatRunRef struct {
	RunID   contracts.AgentRunID `json:"run_id,omitempty"`
	TaskID  contracts.TaskID     `json:"task_id,omitempty"`
	TraceID contracts.TraceID    `json:"trace_id,omitempty"`
}

func chatMessageViews(messages []contracts.ConversationMessage, refs map[string]chatRunRef) []map[string]any {
	out := make([]map[string]any, 0, len(messages))
	for _, message := range messages {
		row := map[string]any{
			"message_id":   message.MessageID,
			"speaker_id":   message.SpeakerID,
			"speaker_type": message.SpeakerType,
			"speaker_name": message.SpeakerName,
			"text":         message.Text,
			"thread_id":    message.ThreadID,
			"created_at":   message.CreatedAt,
		}
		if ref, ok := refs[message.MessageID]; ok {
			row["run_id"] = ref.RunID
			row["task_id"] = ref.TaskID
			row["trace_id"] = ref.TraceID
		} else {
			if runID := metadataString(message.Metadata, "run_id"); runID != "" {
				row["run_id"] = runID
			}
			if taskID := metadataString(message.Metadata, "task_id"); taskID != "" {
				row["task_id"] = taskID
			}
			if traceID := metadataString(message.Metadata, "trace_id"); traceID != "" {
				row["trace_id"] = traceID
			}
		}
		out = append(out, row)
	}
	return out
}

func chatVisibleMessages(messages []contracts.ConversationMessage) []contracts.ConversationMessage {
	out := make([]contracts.ConversationMessage, 0, len(messages))
	for _, message := range messages {
		if chatMessageIsInternal(message) {
			continue
		}
		out = append(out, message)
	}
	return out
}

func chatMessageIsInternal(message contracts.ConversationMessage) bool {
	speakerType := strings.ToLower(strings.TrimSpace(message.SpeakerType))
	switch speakerType {
	case "agent_tool", "tool", "tool_agent":
		return true
	}
	text := strings.TrimSpace(message.Text)
	return strings.HasPrefix(text, "Execute exported tool ")
}

func chatConversationView(thread conversationstore.Thread, members []contracts.GroupMemberProfile) map[string]any {
	mainAgentID := thread.ExternalRefs["main_agent_id"]
	return map[string]any{
		"conversation_id":    thread.ConversationID,
		"thread_id":          thread.ThreadID,
		"name":               thread.ExternalRefs["name"],
		"status":             chatConversationStatus(thread),
		"kind":               thread.Kind,
		"provider":           thread.Provider,
		"main_agent_id":      mainAgentID,
		"main_agent_version": thread.ExternalRefs["main_agent_version"],
		"member_count":       len(activeChatMembers(members)),
		"created_at":         thread.CreatedAt,
		"updated_at":         thread.UpdatedAt,
		"last_message_at":    thread.LastMessageAt,
	}
}

func chatConversationStatus(thread conversationstore.Thread) string {
	status := strings.ToLower(strings.TrimSpace(thread.ExternalRefs["status"]))
	switch status {
	case chatConversationStatusArchived:
		return chatConversationStatusArchived
	case chatConversationStatusActive, "":
		return chatConversationStatusActive
	default:
		return status
	}
}

func chatConversationStatusMatches(thread conversationstore.Thread, status string) bool {
	status = strings.ToLower(strings.TrimSpace(status))
	if status == "" || status == chatConversationStatusActive {
		return chatConversationStatus(thread) == chatConversationStatusActive
	}
	if status == "all" {
		return true
	}
	return chatConversationStatus(thread) == status
}

func chatMemberViews(members []contracts.GroupMemberProfile) []map[string]any {
	sort.SliceStable(members, func(i, j int) bool {
		leftRole := chatRoleRank(members[i])
		rightRole := chatRoleRank(members[j])
		if leftRole != rightRole {
			return leftRole < rightRole
		}
		return string(members[i].MemberID) < string(members[j].MemberID)
	})
	out := make([]map[string]any, 0, len(members))
	for _, member := range members {
		out = append(out, map[string]any{
			"member_id":     member.MemberID,
			"agent_id":      member.MemberID,
			"display_name":  member.DisplayName,
			"member_type":   member.MemberType,
			"roles":         member.Roles,
			"status":        member.Status,
			"agent_version": member.Metadata["agent_version"],
			"tool_binding":  member.Metadata["tool_binding"],
			"tool_ids":      metadataStringSlice(member.Metadata, "tool_ids"),
			"updated_at":    member.LastSeenAt,
		})
	}
	return out
}

func chatRoleRank(member contracts.GroupMemberProfile) int {
	for _, role := range member.Roles {
		if role == "main_agent" {
			return 0
		}
	}
	return 1
}

func activeChatMembers(members []contracts.GroupMemberProfile) []contracts.GroupMemberProfile {
	out := make([]contracts.GroupMemberProfile, 0, len(members))
	for _, member := range members {
		if member.Status == contracts.MemberStatusLeft {
			continue
		}
		out = append(out, member)
	}
	return out
}

func chatParticipants(mainAgentID contracts.AgentID, members []contracts.GroupMemberProfile, currentUser contracts.ConversationParticipant) []contracts.ConversationParticipant {
	out := make([]contracts.ConversationParticipant, 0, len(members)+1)
	if currentUser.ID != "" {
		out = append(out, currentUser)
	}
	for _, member := range members {
		if member.Status == contracts.MemberStatusLeft {
			continue
		}
		if member.MemberType == contracts.MemberTypeHuman {
			continue
		}
		role := "tool_agent"
		for _, current := range member.Roles {
			if current == "main_agent" {
				role = "origin-coordinator"
			}
		}
		if currentUser.ID != "" && string(member.MemberID) == currentUser.ID {
			continue
		}
		out = append(out, contracts.ConversationParticipant{
			ID:   string(member.MemberID),
			Type: "agent",
			Name: member.DisplayName,
			Role: role,
		})
	}
	if mainAgentID != "" {
		seen := false
		for _, participant := range out {
			if participant.ID == string(mainAgentID) {
				seen = true
				break
			}
		}
		if !seen {
			out = append(out, contracts.ConversationParticipant{ID: string(mainAgentID), Type: "agent", Role: "origin-coordinator"})
		}
	}
	return out
}

func chatCallerDisplayName(caller auth.CallerIdentity, payload sendChatMessageRequest, members []contracts.GroupMemberProfile) string {
	if value := firstNonEmpty(
		strings.TrimSpace(payload.SpeakerName),
		strings.TrimSpace(payload.DisplayName),
		strings.TrimSpace(payload.UserName),
		strings.TrimSpace(caller.DisplayName),
	); value != "" {
		return value
	}
	for _, member := range members {
		if member.Status == contracts.MemberStatusLeft || member.DisplayName == "" {
			continue
		}
		if member.MemberType != contracts.MemberTypeHuman && member.MemberType != "user" {
			continue
		}
		if strings.EqualFold(member.ExternalUserID, caller.CallerID) || strings.EqualFold(string(member.MemberID), caller.CallerID) {
			return strings.TrimSpace(member.DisplayName)
		}
	}
	return ""
}

func chatCurrentUserParticipant(message contracts.ConversationMessage) contracts.ConversationParticipant {
	if strings.TrimSpace(message.SpeakerID) == "" {
		return contracts.ConversationParticipant{}
	}
	return contracts.ConversationParticipant{
		ID:   message.SpeakerID,
		Type: firstNonEmpty(strings.TrimSpace(message.SpeakerType), "user"),
		Name: strings.TrimSpace(message.SpeakerName),
		Role: "current_user",
	}
}

func chatCallerMetadata(caller auth.CallerIdentity, displayName string) map[string]any {
	metadata := map[string]any{
		"caller_id":   caller.CallerID,
		"caller_type": caller.CallerType,
	}
	if displayName = strings.TrimSpace(displayName); displayName != "" {
		metadata["display_name"] = displayName
	}
	return metadata
}

func chatMessageMetadata(source string, caller auth.CallerIdentity, displayName string) map[string]any {
	metadata := chatCallerMetadata(caller, displayName)
	metadata["source"] = source
	return metadata
}

func upsertChatAgentMember(r *http.Request, appCore *core.Core, caller auth.CallerIdentity, conversationID string, agentID contracts.AgentID, version contracts.AgentVersion, displayName string, role string, status string, toolIDs []string, bindingStatus string) error {
	if displayName == "" {
		displayName = string(agentID)
	}
	if status == "" {
		status = contracts.MemberStatusActive
	}
	if bindingStatus == "" {
		bindingStatus = "pending_backend_binding"
	}
	_, err := appCore.Identity.UpsertMember(r.Context(), contracts.GroupMemberProfile{
		TenantID:    caller.TenantID,
		GroupID:     contracts.GroupID(conversationID),
		MemberID:    contracts.GroupMemberID(agentID),
		DisplayName: displayName,
		MemberType:  contracts.MemberTypeAgent,
		Roles:       []string{role},
		Status:      status,
		Metadata: map[string]any{
			"agent_id":      string(agentID),
			"agent_version": string(version),
			"tool_binding":  bindingStatus,
			"tool_ids":      toolIDs,
		},
	})
	return err
}

func syncChatAgentToolBinding(r *http.Request, appCore *core.Core, caller auth.CallerIdentity, agentID contracts.AgentID, version contracts.AgentVersion) ([]string, string, error) {
	provider, err := appCore.Agents.Load(r.Context(), caller.TenantID, agentID, version)
	if err != nil {
		return nil, "pending_backend_binding", nil
	}
	if provider.TenantID == "" {
		provider.TenantID = caller.TenantID
	}
	toolIDs := exportedChatToolIDs(provider.Exports.Tools)
	if len(toolIDs) == 0 {
		defaultTools := defaultChatAgentExportedTools(provider)
		if len(defaultTools) == 0 {
			return nil, "pending_backend_binding", nil
		}
		provider.Exports.Tools = append(provider.Exports.Tools, defaultTools...)
		if appCore.Packages != nil {
			projectionVersion := provider.Version
			if projectionVersion == "" {
				projectionVersion = version
			}
			if projectionVersion != "" {
				for _, tool := range defaultTools {
					if _, err := appCore.Packages.UpsertExportedToolProjection(r.Context(), provider.TenantID, provider.AgentID, projectionVersion, tool, caller.CallerID); err != nil {
						return nil, "", err
					}
				}
			}
		}
		toolIDs = exportedChatToolIDs(provider.Exports.Tools)
	}
	if err := syncAgentExportedTools(r.Context(), appCore, provider, caller.CallerID); err != nil {
		return nil, "", err
	}
	return toolIDs, "active", nil
}

func defaultChatAgentExportedTools(provider contracts.AgentDefinition) []contracts.AgentExportedTool {
	if !chatAgentLooksLikeMerchantLimit(provider) {
		return nil
	}
	version := strings.TrimSpace(string(provider.Version))
	if version == "" {
		version = "v1"
	}
	return []contracts.AgentExportedTool{{
		ToolID:      string(provider.AgentID) + ".run_merchant_limit_agent",
		GroupID:     "merchant-limit",
		Operation:   "run_merchant_limit_agent",
		Name:        "运行商家测额智能体",
		Description: "调用商家测额智能体处理请款单基础查询、融资额度分析、申请金额校验、授信、风控、质押店铺和回款查询。",
		WhenToUse: []string{
			"用户询问请款单、可融资金额、融资额度、申请金额是否覆盖、授信、风控、质押店铺或回款逾期时使用。",
		},
		InputSchema: map[string]any{
			"type": "object",
			"properties": map[string]any{
				"input":       map[string]any{"type": "string"},
				"rawInput":    map[string]any{"type": "string"},
				"userInput":   map[string]any{"type": "string"},
				"loanNo":      map[string]any{"type": "string"},
				"applyAmount": map[string]any{"type": "number"},
			},
		},
		OutputSchema: map[string]any{"type": "object"},
		RiskLevel:    contracts.RiskLow,
		Visibility:   contracts.ToolProtected,
		Status:       "enabled",
		Version:      version,
	}}
}

func chatAgentLooksLikeMerchantLimit(provider contracts.AgentDefinition) bool {
	values := []string{
		string(provider.AgentID),
		string(provider.SourceProviderID),
		provider.Name,
		provider.Description,
		provider.IdentityPrompt,
		provider.SystemPrompt,
		provider.DeveloperPrompt,
	}
	joined := strings.ToLower(strings.Join(values, "\n"))
	return strings.Contains(joined, "znt-merchant-limit") ||
		strings.Contains(joined, "merchant-limit") ||
		strings.Contains(joined, "商家测额") ||
		strings.Contains(joined, "测额") ||
		strings.Contains(joined, "请款单") ||
		strings.Contains(joined, "融资额度")
}

func exportedChatToolIDs(tools []contracts.AgentExportedTool) []string {
	toolIDs := make([]string, 0, len(tools))
	for _, tool := range tools {
		if strings.EqualFold(strings.TrimSpace(tool.Status), "disabled") {
			continue
		}
		toolIDs = appendUniqueChatString(toolIDs, tool.ToolID)
	}
	return toolIDs
}

func chatAllowedToolIDs(members []contracts.GroupMemberProfile) []string {
	toolIDs := []string{}
	for _, member := range members {
		if member.Status != "" && member.Status != contracts.MemberStatusActive {
			continue
		}
		if !chatMemberHasRole(member, "tool_agent") {
			continue
		}
		for _, toolID := range metadataStringSlice(member.Metadata, "tool_ids") {
			toolIDs = appendUniqueChatString(toolIDs, toolID)
		}
	}
	return toolIDs
}

func chatMemberHasRole(member contracts.GroupMemberProfile, role string) bool {
	for _, current := range member.Roles {
		if current == role {
			return true
		}
	}
	return false
}

func metadataStringSlice(metadata map[string]any, key string) []string {
	if len(metadata) == 0 {
		return nil
	}
	switch value := metadata[key].(type) {
	case []string:
		return append([]string(nil), value...)
	case []any:
		out := make([]string, 0, len(value))
		for _, item := range value {
			text, ok := item.(string)
			if !ok {
				continue
			}
			if text = strings.TrimSpace(text); text != "" {
				out = append(out, text)
			}
		}
		return out
	case string:
		if strings.TrimSpace(value) == "" {
			return nil
		}
		parts := strings.Split(value, ",")
		out := make([]string, 0, len(parts))
		for _, part := range parts {
			if text := strings.TrimSpace(part); text != "" {
				out = append(out, text)
			}
		}
		return out
	default:
		return nil
	}
}

func appendUniqueChatString(values []string, value string) []string {
	value = strings.TrimSpace(value)
	if value == "" {
		return values
	}
	for _, existing := range values {
		if existing == value {
			return values
		}
	}
	return append(values, value)
}

func nameForAgent(r *http.Request, appCore *core.Core, tenantID contracts.TenantID, agentID contracts.AgentID, version contracts.AgentVersion) string {
	definition, err := appCore.Agents.Load(r.Context(), tenantID, agentID, version)
	if err != nil || definition.Name == "" {
		return string(agentID)
	}
	return definition.Name
}

func firstAgentID(values ...contracts.AgentID) contracts.AgentID {
	for _, value := range values {
		if strings.TrimSpace(string(value)) != "" {
			return value
		}
	}
	return ""
}

func cloneStringMap(in map[string]string) map[string]string {
	out := map[string]string{}
	for key, value := range in {
		out[key] = value
	}
	return out
}

func chatQueryInt(r *http.Request, key string, fallback int) int {
	raw := strings.TrimSpace(r.URL.Query().Get(key))
	if raw == "" {
		return fallback
	}
	value, err := strconv.Atoi(raw)
	if err != nil {
		return fallback
	}
	return value
}

func metadataString(metadata map[string]any, key string) string {
	if metadata == nil {
		return ""
	}
	switch value := metadata[key].(type) {
	case string:
		return value
	case contracts.AgentRunID:
		return string(value)
	case contracts.TaskID:
		return string(value)
	case contracts.TraceID:
		return string(value)
	default:
		return ""
	}
}

func writeChatNotFoundOrError(w http.ResponseWriter, err error) {
	if errors.Is(err, storagerepo.ErrNotFound) {
		writeError(w, contracts.NewRuntimeError(contracts.CodeTaskCancelled, "chat conversation not found", nil), http.StatusNotFound)
		return
	}
	writeRuntimeError(w, err)
}
