package outputpolicy

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"strings"

	"znt/internal/contracts"
	decisionparser "znt/internal/decision/parser"
	decisionvalidator "znt/internal/decision/validator"
)

func ApplyPromptBundle(bundle contracts.PromptBundle, strategy contracts.OutputStrategy) contracts.PromptBundle {
	if len(strategy.OutputSchema) > 0 {
		bundle.OutputSchema = strategy.OutputSchema
	}
	if strings.TrimSpace(strategy.OutputMode) == "decision_json" {
		bundle.Constraints = append(bundle.Constraints, "output strategy: return exactly one CleanCore decision JSON object")
	}
	if strategy.StrictJSON {
		bundle.Constraints = append(bundle.Constraints, "output strategy strict_json: include required fields explicitly; do not rely on omitted defaults or tool name/id normalization")
	}
	return bundle
}

func ParseDecision(data []byte, strategy contracts.OutputStrategy) (contracts.Decision, error) {
	if !strategy.StrictJSON {
		return decisionparser.Parse(data)
	}
	var decision contracts.Decision
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&decision); err != nil {
		return contracts.Decision{}, contracts.NewRuntimeError(contracts.CodeDecisionSchemaError, fmt.Sprintf("parse strict decision json: %v", err), nil)
	}
	if err := decoder.Decode(&struct{}{}); err != io.EOF {
		return contracts.Decision{}, contracts.NewRuntimeError(contracts.CodeDecisionSchemaError, "parse strict decision json: multiple JSON values", nil)
	}
	return decision, nil
}

func ValidateDecision(strategy contracts.OutputStrategy, result decisionvalidator.ValidationResult) error {
	if !strategy.StrictJSON || len(result.Warnings) == 0 {
		return nil
	}
	return contracts.NewRuntimeError(contracts.CodeDecisionSchemaError, "output strategy strict_json rejected normalized decision", map[string]any{
		"warnings": result.Warnings,
	})
}
