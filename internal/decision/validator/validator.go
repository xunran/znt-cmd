package validator

import (
	"fmt"

	"znt/internal/contracts"
)

type Validator struct{}

type ValidationResult struct {
	Decision contracts.Decision `json:"decision"`
	Warnings []string           `json:"warnings,omitempty"`
}

func New() Validator {
	return Validator{}
}

func (Validator) Validate(decision contracts.Decision, candidateTools []contracts.ToolCard) error {
	_, err := (Validator{}).Normalize(decision, candidateTools)
	return err
}

func (Validator) Normalize(decision contracts.Decision, candidateTools []contracts.ToolCard) (ValidationResult, error) {
	if err := decision.Type.Validate(); err != nil {
		return ValidationResult{}, contracts.NewRuntimeError(contracts.CodeDecisionSchemaError, err.Error(), nil)
	}
	if decision.Confidence < 0 || decision.Confidence > 1 {
		return ValidationResult{}, schemaError("decision confidence must be between 0 and 1")
	}
	warnings := make([]string, 0)
	switch decision.Type {
	case contracts.DecisionTypeReply:
		if decision.Reply == nil || decision.Reply.Text == "" {
			return ValidationResult{}, schemaError("reply decision requires reply.text")
		}
		if decision.Reply.Kind == "" {
			decision.Reply.Kind = contracts.ReplyAnswer
			warnings = append(warnings, "reply.kind defaulted to answer")
		}
		if !validReplyKind(decision.Reply.Kind) {
			return ValidationResult{}, schemaError(fmt.Sprintf("unknown reply kind %q", decision.Reply.Kind))
		}
	case contracts.DecisionTypeAskClarification:
		if decision.Ask == nil || decision.Ask.Question == "" {
			return ValidationResult{}, schemaError("ask_clarification decision requires ask.question")
		}
	case contracts.DecisionTypeToolCall:
		if len(decision.ToolCalls) == 0 {
			return ValidationResult{}, schemaError("tool_call decision requires at least one tool call")
		}
		allowed := map[string]struct{}{}
		byName := map[string]contracts.ToolCard{}
		byID := map[string]contracts.ToolCard{}
		for _, tool := range candidateTools {
			allowed[tool.ToolID] = struct{}{}
			allowed[tool.Name] = struct{}{}
			byID[tool.ToolID] = tool
			byName[tool.Name] = tool
		}
		for i, call := range decision.ToolCalls {
			if call.ToolID == "" && call.Name == "" {
				return ValidationResult{}, schemaError("tool_call requires tool_id or name")
			}
			if call.ToolID == "" {
				if tool, ok := byName[call.Name]; ok {
					call.ToolID = tool.ToolID
					warnings = append(warnings, fmt.Sprintf("tool call %q normalized name to tool_id", call.Name))
				}
			} else if _, ok := byID[call.ToolID]; !ok {
				if tool, ok := byName[call.ToolID]; ok {
					call.ToolID = tool.ToolID
					if call.Name == "" {
						call.Name = tool.Name
					}
					warnings = append(warnings, fmt.Sprintf("tool call %q normalized tool name used as tool_id", tool.Name))
				}
			}
			if call.Name == "" {
				if tool, ok := byID[call.ToolID]; ok {
					call.Name = tool.Name
					warnings = append(warnings, fmt.Sprintf("tool call %q normalized tool_id to name", call.ToolID))
				}
			}
			if _, ok := allowed[call.ToolID]; !ok {
				if _, ok := allowed[call.Name]; !ok {
					return ValidationResult{}, contracts.NewRuntimeError(contracts.CodeToolNotFound, fmt.Sprintf("tool call %q is not in candidate set", call.Name), nil)
				}
			}
			decision.ToolCalls[i] = call
		}
	case contracts.DecisionTypeUnsupported:
		if decision.Reason == "" {
			return ValidationResult{}, schemaError("unsupported decision requires reason")
		}
	case contracts.DecisionTypeError:
		if decision.Error == nil || (decision.Error.Code == "" && decision.Error.Message == "") {
			return ValidationResult{}, schemaError("error decision requires error code or message")
		}
	}
	return ValidationResult{Decision: decision, Warnings: warnings}, nil
}

func schemaError(message string) error {
	return contracts.NewRuntimeError(contracts.CodeDecisionSchemaError, message, nil)
}

func validReplyKind(kind contracts.ReplyKind) bool {
	switch kind {
	case contracts.ReplyAnswer, contracts.ReplyRefusal, contracts.ReplyPolicyNotice, contracts.ReplyClarificationMessage, contracts.ReplyStatusUpdate:
		return true
	default:
		return false
	}
}
