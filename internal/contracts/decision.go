package contracts

type Decision struct {
	DecisionID DecisionID   `json:"decision_id"`
	Type       DecisionType `json:"type"`

	Reason     string  `json:"reason,omitempty"`
	Confidence float64 `json:"confidence,omitempty"`

	Reply     *DecisionReply        `json:"reply,omitempty"`
	ToolCalls []ToolCall            `json:"tool_calls,omitempty"`
	Ask       *ClarificationRequest `json:"ask,omitempty"`
	Error     *DecisionError        `json:"error,omitempty"`
}

type DecisionReply struct {
	Kind        ReplyKind `json:"kind"`
	Text        string    `json:"text"`
	ContentType string    `json:"content_type,omitempty"`
}

type ClarificationRequest struct {
	Question       string   `json:"question"`
	RequiredFields []string `json:"required_fields,omitempty"`
}

type DecisionError struct {
	Code    ErrorCode `json:"code,omitempty"`
	Message string    `json:"message,omitempty"`
}
