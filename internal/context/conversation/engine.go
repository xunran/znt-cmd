package conversation

import (
	"context"
	"encoding/json"
	"sort"
	"strings"
	"time"

	"znt/internal/contracts"
)

const (
	KindDirect = "direct"
	KindGroup  = "group"
	KindThread = "thread"

	ActionEnterMainAgent = "enter_main_agent"
	ActionNoOp           = "no_op"
	ActionAskIfAddressed = "ask_if_addressed"
	ActionRetrieve       = "retrieve_context"

	PhasePreAddressing = "pre_addressing"
	PhasePreDecision   = "pre_decision"
)

type AddressingJudge interface {
	JudgeAddressing(ctx context.Context, conversation contracts.ConversationContext, definition contracts.AgentDefinition) (contracts.AddressingAssessment, error)
}

type SufficiencyJudge interface {
	JudgeSufficiency(ctx context.Context, conversation contracts.ConversationContext, phase string) (contracts.ContextSufficiencyAssessment, error)
}

type Retriever interface {
	Retrieve(ctx context.Context, queries []contracts.ContextRetrievalQuery, input RetrievalInput) ([]contracts.RetrievedContext, error)
}

type RetrievalInput struct {
	Conversation contracts.ConversationContext
	TaskEvents   []contracts.TaskEvent
	Memory       []contracts.MemorySummary
	Artifacts    []contracts.ArtifactRef
	ToolResults  []contracts.ToolResultSummary
	Now          time.Time
}

type HeuristicAddressingJudge struct{}

func (HeuristicAddressingJudge) JudgeAddressing(_ context.Context, conversation contracts.ConversationContext, definition contracts.AgentDefinition) (contracts.AddressingAssessment, error) {
	current := conversation.CurrentMessage
	agentID := string(definition.AgentID)
	agentName := strings.TrimSpace(definition.Name)
	signals := make([]string, 0)
	addresseeIDs := make([]string, 0)
	if current.SpeakerType == "agent" || current.SpeakerID == agentID {
		return contracts.AddressingAssessment{
			AddressedToAgent: false,
			Confidence:       0.99,
			Reason:           "当前消息来自智能体自身，避免自触发。",
			Signals:          []string{"self_message"},
			DecisionSource:   "rule",
			SuggestedAction:  ActionNoOp,
		}, nil
	}
	if conversation.Kind == KindDirect || conversation.Kind == "" {
		return contracts.AddressingAssessment{
			AddressedToAgent: true,
			Confidence:       0.95,
			Reason:           "私聊或未标记群聊时，默认当前用户在对原智能体说话。",
			Signals:          []string{"direct_conversation"},
			AddresseeIDs:     []string{agentID},
			DecisionSource:   "rule",
			SuggestedAction:  ActionEnterMainAgent,
		}, nil
	}
	if containsString(current.Mentions, agentID) || (agentName != "" && strings.Contains(current.Text, agentName)) || strings.Contains(current.Text, "原智能体") || strings.Contains(current.Text, "协调智能体") {
		return contracts.AddressingAssessment{
			AddressedToAgent: true,
			Confidence:       0.96,
			Reason:           "当前消息明确称呼或提到原智能体。",
			Signals:          []string{"explicit_agent_mention"},
			AddresseeIDs:     []string{agentID},
			DecisionSource:   "rule",
			SuggestedAction:  ActionEnterMainAgent,
		}, nil
	}
	if current.ReplyToMessageID != "" {
		if msg, ok := FindMessageByID(conversation.RecentMessages, current.ReplyToMessageID); ok {
			if msg.SpeakerType == "agent" || msg.SpeakerID == agentID {
				return contracts.AddressingAssessment{
					AddressedToAgent: true,
					Confidence:       0.95,
					Reason:           "当前消息 reply_to 指向原智能体消息。",
					Signals:          []string{"reply_to_agent_message"},
					AddresseeIDs:     []string{agentID},
					DecisionSource:   "rule",
					SuggestedAction:  ActionEnterMainAgent,
				}, nil
			}
			if msg.SpeakerID != "" && msg.SpeakerID != current.SpeakerID {
				return contracts.AddressingAssessment{
					AddressedToAgent: false,
					Confidence:       0.90,
					Reason:           "当前消息 reply_to 指向其他用户消息，且没有原智能体相关意图。",
					Signals:          []string{"reply_to_human_message"},
					AddresseeIDs:     []string{msg.SpeakerID},
					DecisionSource:   "rule",
					SuggestedAction:  ActionNoOp,
				}, nil
			}
		}
		signals = append(signals, "reply_to_unknown_message")
	}
	if last, ok := LastRecentMessage(conversation.RecentMessages); ok {
		if last.SpeakerType == "agent" || last.SpeakerID == agentID {
			if IsShortFollowup(current.Text) || HasSecondPersonReference(current.Text) || HasContinuationReference(current.Text) {
				return contracts.AddressingAssessment{
					AddressedToAgent: true,
					Confidence:       0.84,
					Reason:           "当前消息延续原智能体上一轮发言。",
					Signals:          []string{"previous_speaker_is_agent", "followup_reference"},
					AddresseeIDs:     []string{agentID},
					DecisionSource:   "heuristic",
					SuggestedAction:  ActionEnterMainAgent,
				}, nil
			}
		}
		if last.SpeakerType == "user" && last.SpeakerID != "" && last.SpeakerID != current.SpeakerID && HasSecondPersonReference(current.Text) {
			return contracts.AddressingAssessment{
				AddressedToAgent: false,
				Confidence:       0.82,
				Reason:           "上一轮是其他用户发言，当前二人称短句更可能是人对人接续。",
				Signals:          []string{"previous_speaker_is_human", "second_person_reference"},
				AddresseeIDs:     []string{last.SpeakerID},
				DecisionSource:   "heuristic",
				SuggestedAction:  ActionNoOp,
			}, nil
		}
	}
	if HasHistoricalReference(current.Text) {
		signals = append(signals, "historical_reference")
		return contracts.AddressingAssessment{
			AddressedToAgent: false,
			Confidence:       0.52,
			Reason:           "当前消息依赖更早上下文，当前窗口不足以稳定判断接话对象。",
			Signals:          signals,
			AddresseeIDs:     addresseeIDs,
			DecisionSource:   "heuristic",
			SuggestedAction:  ActionRetrieve,
		}, nil
	}
	if conversation.Kind == KindGroup || conversation.Kind == KindThread {
		return contracts.AddressingAssessment{
			AddressedToAgent: false,
			Confidence:       0.76,
			Reason:           "群聊消息没有明确指向原智能体。",
			Signals:          append(signals, "group_without_agent_signal"),
			AddresseeIDs:     addresseeIDs,
			DecisionSource:   "heuristic",
			SuggestedAction:  ActionNoOp,
		}, nil
	}
	return contracts.AddressingAssessment{
		AddressedToAgent: true,
		Confidence:       0.60,
		Reason:           "当前会话没有足够群聊信号，按可接话处理。",
		Signals:          signals,
		AddresseeIDs:     []string{agentID},
		DecisionSource:   "heuristic",
		SuggestedAction:  ActionEnterMainAgent,
	}, nil
}

type HeuristicSufficiencyJudge struct{}

func (HeuristicSufficiencyJudge) JudgeSufficiency(_ context.Context, conversation contracts.ConversationContext, phase string) (contracts.ContextSufficiencyAssessment, error) {
	text := conversation.CurrentMessage.Text
	if conversation.Addressing != nil && conversation.Addressing.SuggestedAction == ActionNoOp && conversation.Addressing.Confidence >= 0.85 {
		return contracts.ContextSufficiencyAssessment{
			Phase:           phase,
			Sufficient:      true,
			Confidence:      0.90,
			Reason:          "已高置信判断不是对原智能体说话，无需召回旧上下文。",
			RetrievalNeeded: false,
			SuggestedAction: ActionNoOp,
		}, nil
	}
	if HasHistoricalReference(text) && !ConversationHasHistoricalAnchor(conversation) {
		query := BuildRetrievalQuery(conversation, text)
		return contracts.ContextSufficiencyAssessment{
			Phase:      phase,
			Sufficient: false,
			Confidence: 0.78,
			Reason:     "当前消息包含历史指代，但最近消息中没有足够锚点。",
			MissingFacts: []string{
				"当前消息中历史指代的具体对象",
				"用户希望延续的旧讨论内容",
			},
			RetrievalNeeded: true,
			Queries:         []contracts.ContextRetrievalQuery{query},
			SuggestedAction: ActionRetrieve,
		}, nil
	}
	if phase == PhasePreDecision && IsVagueActionRequest(text) {
		return contracts.ContextSufficiencyAssessment{
			Phase:      phase,
			Sufficient: false,
			Confidence: 0.83,
			Reason:     "当前请求缺少目标、对象或期望动作。",
			MissingFacts: []string{
				"要协调的具体事项",
				"期望原智能体执行的下一步动作",
			},
			RetrievalNeeded: false,
			SuggestedAction: "ask_clarification",
		}, nil
	}
	return contracts.ContextSufficiencyAssessment{
		Phase:           phase,
		Sufficient:      true,
		Confidence:      0.82,
		Reason:          "当前窗口足够进行下一步判断。",
		RetrievalNeeded: false,
		SuggestedAction: "continue",
	}, nil
}

type BasicRetriever struct{}

func (BasicRetriever) Retrieve(_ context.Context, queries []contracts.ContextRetrievalQuery, input RetrievalInput) ([]contracts.RetrievedContext, error) {
	limit := 0
	if len(queries) > 0 && queries[0].MaxResults > 0 {
		limit = queries[0].MaxResults
	}
	queryText := ""
	for _, query := range queries {
		queryText += " " + query.Query
	}
	scope := BuildRetrievalScope(queries)
	now := input.Now
	if now.IsZero() {
		now = input.Conversation.CurrentMessage.CreatedAt
	}
	if now.IsZero() {
		now = time.Now().UTC()
	}
	candidates := make([]contracts.RetrievedContext, 0)
	for _, message := range input.Conversation.RecentMessages {
		if message.Text == "" || !scope.SourceAllowed("conversation_history") || !scope.SpeakerAllowed(message.SpeakerID) || !scope.ThreadAllowed(message.ThreadID) {
			continue
		}
		score := RelevanceScore(queryText, message.Text)
		if score <= 0 && !SameThread(input.Conversation.CurrentMessage.ThreadID, message.ThreadID) {
			continue
		}
		candidates = append(candidates, contracts.RetrievedContext{
			SourceType:   "conversation_history",
			SourceID:     FirstNonEmpty(message.MessageID, message.ExternalMessageID),
			SpeakerID:    message.SpeakerID,
			SpeakerName:  message.SpeakerName,
			CreatedAt:    message.CreatedAt,
			Summary:      SummarizeText(message.Text, 220),
			Snippet:      message.Text,
			Relevance:    MaxFloat(score, 0.55),
			RecencyScore: RecencyScore(now, message.CreatedAt),
			TrustLevel:   "untrusted_user_text",
			Visibility:   "conversation",
		})
	}
	for _, memory := range input.Memory {
		if !scope.SourceAllowed("memory") {
			continue
		}
		text := strings.TrimSpace(memory.Summary)
		if text == "" {
			continue
		}
		score := RelevanceScore(queryText, text)
		if score <= 0 && !HasHistoricalReference(input.Conversation.CurrentMessage.Text) {
			continue
		}
		candidates = append(candidates, contracts.RetrievedContext{
			SourceType: "memory",
			SourceID:   string(memory.MemoryID),
			Summary:    SummarizeText(text, 220),
			Snippet:    text,
			Relevance:  MaxFloat(score, 0.50),
			TrustLevel: "system_record",
			Visibility: "tenant_agent_user",
		})
	}
	for _, event := range input.TaskEvents {
		if isCurrentInputEvent(event, input.Conversation.CurrentMessage.Text) || !scope.SourceAllowed("task_event") || !scope.SpeakerAllowed(event.ActorID) || !scope.ThreadAllowed(TaskEventThreadID(event)) {
			continue
		}
		text := TaskEventText(event)
		if text == "" {
			continue
		}
		score := RelevanceScore(queryText, text)
		if score <= 0 && !HasHistoricalReference(input.Conversation.CurrentMessage.Text) {
			continue
		}
		candidates = append(candidates, contracts.RetrievedContext{
			SourceType:   "task_event",
			SourceID:     string(event.EventID),
			SpeakerID:    event.ActorID,
			CreatedAt:    event.CreatedAt,
			Summary:      SummarizeText(text, 220),
			Snippet:      text,
			Relevance:    MaxFloat(score, 0.48),
			RecencyScore: RecencyScore(now, event.CreatedAt),
			TrustLevel:   "untrusted_user_text",
			Visibility:   "task",
		})
	}
	for _, artifact := range input.Artifacts {
		if !scope.SourceAllowed("artifact") {
			continue
		}
		text := strings.TrimSpace(strings.Join([]string{string(artifact.ArtifactID), artifact.Type, artifact.Summary}, " "))
		if text == "" {
			continue
		}
		score := RelevanceScore(queryText, text)
		if score <= 0 && !HasArtifactReference(input.Conversation.CurrentMessage.Text) {
			continue
		}
		candidates = append(candidates, contracts.RetrievedContext{
			SourceType: "artifact",
			SourceID:   string(artifact.ArtifactID),
			Summary:    SummarizeText(text, 220),
			Snippet:    artifact.Summary,
			Relevance:  MaxFloat(score, 0.46),
			TrustLevel: "tool_result",
			Visibility: "artifact_ref",
		})
	}
	for _, tool := range input.ToolResults {
		if !scope.SourceAllowed("tool_result") {
			continue
		}
		text := strings.TrimSpace(strings.Join([]string{string(tool.ToolCallID), string(tool.Status), tool.Summary}, " "))
		if text == "" {
			continue
		}
		score := RelevanceScore(queryText, text)
		if score <= 0 && !strings.Contains(input.Conversation.CurrentMessage.Text, "工具") && !strings.Contains(strings.ToLower(input.Conversation.CurrentMessage.Text), "tool") {
			continue
		}
		candidates = append(candidates, contracts.RetrievedContext{
			SourceType: "tool_result",
			SourceID:   string(tool.ToolCallID),
			Summary:    SummarizeText(text, 220),
			Snippet:    tool.Summary,
			Relevance:  MaxFloat(score, 0.46),
			TrustLevel: "tool_result",
			Visibility: "run",
		})
	}
	sort.SliceStable(candidates, func(i, j int) bool {
		left := candidates[i].Relevance + candidates[i].RecencyScore
		right := candidates[j].Relevance + candidates[j].RecencyScore
		if left == right {
			return candidates[i].CreatedAt.After(candidates[j].CreatedAt)
		}
		return left > right
	})
	capacity := len(candidates)
	if limit > 0 {
		capacity = MinInt(len(candidates), limit)
	}
	out := make([]contracts.RetrievedContext, 0, capacity)
	seen := map[string]struct{}{}
	for _, candidate := range candidates {
		key := candidate.SourceType + ":" + candidate.SourceID
		if candidate.SourceID == "" {
			key = candidate.SourceType + ":" + candidate.Summary
		}
		if _, ok := seen[key]; ok {
			continue
		}
		seen[key] = struct{}{}
		out = append(out, candidate)
		if limit > 0 && len(out) >= limit {
			break
		}
	}
	return out, nil
}

func BuildRetrievalQuery(conversation contracts.ConversationContext, text string) contracts.ContextRetrievalQuery {
	return BuildRetrievalQueryWithLimit(conversation, text, 0)
}

func BuildRetrievalQueryWithLimit(conversation contracts.ConversationContext, text string, maxResults int) contracts.ContextRetrievalQuery {
	query := strings.TrimSpace(text)
	if query == "" {
		query = "当前消息相关的上一轮上下文"
	}
	return contracts.ContextRetrievalQuery{
		Query:      query,
		Sources:    []string{"conversation_history", "memory", "task_event", "artifact", "tool_result"},
		ThreadID:   conversation.CurrentMessage.ThreadID,
		TimeHint:   "recent",
		MaxResults: maxResults,
	}
}

type RetrievalScope struct {
	Sources    map[string]struct{}
	SpeakerIDs map[string]struct{}
	ThreadIDs  map[string]struct{}
}

func BuildRetrievalScope(queries []contracts.ContextRetrievalQuery) RetrievalScope {
	scope := RetrievalScope{
		Sources:    map[string]struct{}{},
		SpeakerIDs: map[string]struct{}{},
		ThreadIDs:  map[string]struct{}{},
	}
	for _, query := range queries {
		for _, source := range query.Sources {
			source = strings.TrimSpace(source)
			if source != "" {
				scope.Sources[source] = struct{}{}
			}
		}
		for _, speakerID := range query.SpeakerIDs {
			speakerID = strings.TrimSpace(speakerID)
			if speakerID != "" {
				scope.SpeakerIDs[speakerID] = struct{}{}
			}
		}
		threadID := strings.TrimSpace(query.ThreadID)
		if threadID != "" {
			scope.ThreadIDs[threadID] = struct{}{}
		}
	}
	return scope
}

func (s RetrievalScope) SourceAllowed(source string) bool {
	if len(s.Sources) == 0 {
		return true
	}
	_, ok := s.Sources[source]
	return ok
}

func (s RetrievalScope) SpeakerAllowed(speakerID string) bool {
	if len(s.SpeakerIDs) == 0 || strings.TrimSpace(speakerID) == "" {
		return true
	}
	_, ok := s.SpeakerIDs[speakerID]
	return ok
}

func (s RetrievalScope) ThreadAllowed(threadID string) bool {
	if len(s.ThreadIDs) == 0 || strings.TrimSpace(threadID) == "" {
		return true
	}
	_, ok := s.ThreadIDs[threadID]
	return ok
}

func TaskEventText(event contracts.TaskEvent) string {
	parts := []string{event.Type}
	if len(event.Payload) > 0 {
		data, err := json.Marshal(event.Payload)
		if err == nil {
			parts = append(parts, string(data))
		}
	}
	return strings.TrimSpace(strings.Join(parts, " "))
}

func TaskEventThreadID(event contracts.TaskEvent) string {
	for _, key := range []string{"thread_id", "external_thread_id"} {
		value, ok := event.Payload[key].(string)
		if ok && strings.TrimSpace(value) != "" {
			return strings.TrimSpace(value)
		}
	}
	return ""
}

func isCurrentInputEvent(event contracts.TaskEvent, currentText string) bool {
	if event.Type != "conversation.input" {
		return false
	}
	input, ok := event.Payload["input"].(string)
	if !ok {
		return false
	}
	return strings.TrimSpace(input) != "" && strings.TrimSpace(input) == strings.TrimSpace(currentText)
}

func RelevanceScore(query string, text string) float64 {
	query = strings.TrimSpace(strings.ToLower(query))
	text = strings.TrimSpace(strings.ToLower(text))
	if query != "" && text != "" {
		if strings.Contains(text, query) || strings.Contains(query, text) {
			return 1
		}
	}
	queryTerms := Tokenize(query)
	textTerms := Tokenize(text)
	if len(queryTerms) == 0 || len(textTerms) == 0 {
		return 0
	}
	textSet := map[string]struct{}{}
	for _, term := range textTerms {
		textSet[term] = struct{}{}
	}
	matches := 0
	for _, term := range queryTerms {
		if term != "" && strings.Contains(text, term) {
			matches++
			continue
		}
		if normalized := TrimQuestionSuffix(term); normalized != "" && strings.Contains(text, normalized) {
			matches++
			continue
		}
		if _, ok := textSet[term]; ok {
			matches++
		}
	}
	if matches == 0 {
		return 0
	}
	return float64(matches) / float64(len(queryTerms))
}

func TrimQuestionSuffix(value string) string {
	value = strings.TrimSpace(value)
	for _, suffix := range []string{"呢", "吗", "嘛", "吧", "么", "啊", "呀"} {
		value = strings.TrimSuffix(value, suffix)
	}
	return strings.TrimSpace(value)
}

func Tokenize(value string) []string {
	value = strings.ToLower(strings.TrimSpace(value))
	separators := func(r rune) bool {
		switch r {
		case ' ', '\t', '\n', '\r', ',', '.', ';', ':', '，', '。', '；', '：', '？', '?', '！', '!', '、', '(', ')', '（', '）', '[', ']', '【', '】', '"', '\'', '“', '”':
			return true
		default:
			return false
		}
	}
	fields := strings.FieldsFunc(value, separators)
	out := make([]string, 0, len(fields))
	for _, field := range fields {
		field = strings.TrimSpace(field)
		if field == "" {
			continue
		}
		out = append(out, field)
	}
	return out
}

func HasHistoricalReference(text string) bool {
	needles := []string{"刚才", "之前", "上面", "前面", "继续", "第二个", "第2个", "那个", "这个", "按之前", "上次", "刚刚", "前一个", "上一条", "老信息", "旧上下文"}
	for _, needle := range needles {
		if strings.Contains(text, needle) {
			return true
		}
	}
	return false
}

func HasArtifactReference(text string) bool {
	needles := []string{"文档", "文件", "artifact", "方案", "报告"}
	for _, needle := range needles {
		if strings.Contains(strings.ToLower(text), strings.ToLower(needle)) {
			return true
		}
	}
	return false
}

func HasSecondPersonReference(text string) bool {
	return strings.Contains(text, "你") || strings.Contains(strings.ToLower(text), "you")
}

func HasContinuationReference(text string) bool {
	needles := []string{"继续", "安排", "处理", "推进", "说下", "讲下", "解释", "第二个", "这个", "那个"}
	for _, needle := range needles {
		if strings.Contains(text, needle) {
			return true
		}
	}
	return false
}

func IsShortFollowup(text string) bool {
	trimmed := strings.TrimSpace(text)
	return len([]rune(trimmed)) > 0 && len([]rune(trimmed)) <= 20
}

func IsVagueActionRequest(text string) bool {
	trimmed := strings.TrimSpace(text)
	if trimmed == "" {
		return false
	}
	vagueActions := []string{"帮我安排一下", "帮我推进一下", "处理一下这个事", "继续处理", "安排一下", "推进一下"}
	for _, action := range vagueActions {
		if strings.Contains(trimmed, action) {
			return true
		}
	}
	return false
}

func ConversationHasHistoricalAnchor(conversation contracts.ConversationContext) bool {
	if len(conversation.Retrieved) > 0 {
		return true
	}
	if len(conversation.RecentMessages) == 0 {
		return false
	}
	current := conversation.CurrentMessage.Text
	if strings.Contains(current, "第二个") || strings.Contains(current, "第2个") {
		for _, message := range conversation.RecentMessages {
			if strings.Contains(message.Text, "第二") || strings.Contains(message.Text, "2.") || strings.Contains(message.Text, "二") {
				return true
			}
		}
	}
	return len(conversation.RecentMessages) >= 3 && !IsShortFollowup(current)
}

func FindMessageByID(messages []contracts.ConversationMessage, id string) (contracts.ConversationMessage, bool) {
	for _, message := range messages {
		if message.MessageID == id || message.ExternalMessageID == id {
			return message, true
		}
	}
	return contracts.ConversationMessage{}, false
}

func LastRecentMessage(messages []contracts.ConversationMessage) (contracts.ConversationMessage, bool) {
	if len(messages) == 0 {
		return contracts.ConversationMessage{}, false
	}
	return messages[len(messages)-1], true
}

func FirstNonEmpty(values ...string) string {
	for _, value := range values {
		if strings.TrimSpace(value) != "" {
			return value
		}
	}
	return ""
}

func SameThread(a, b string) bool {
	return a != "" && b != "" && a == b
}

func RecencyScore(now time.Time, createdAt time.Time) float64 {
	if now.IsZero() || createdAt.IsZero() {
		return 0
	}
	delta := now.Sub(createdAt)
	if delta < 0 {
		delta = -delta
	}
	switch {
	case delta <= time.Hour:
		return 0.30
	case delta <= 24*time.Hour:
		return 0.20
	case delta <= 7*24*time.Hour:
		return 0.10
	default:
		return 0.02
	}
}

func SummarizeText(text string, limit int) string {
	text = strings.TrimSpace(text)
	if limit <= 0 || len([]rune(text)) <= limit {
		return text
	}
	runes := []rune(text)
	return string(runes[:limit]) + "...[truncated]"
}

func MaxFloat(a, b float64) float64 {
	if a > b {
		return a
	}
	return b
}

func MinInt(a, b int) int {
	if a < b {
		return a
	}
	return b
}

func containsString(values []string, target string) bool {
	for _, value := range values {
		if value == target {
			return true
		}
	}
	return false
}
