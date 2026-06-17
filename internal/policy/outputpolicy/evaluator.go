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
		bundle.AssemblySteps = append(bundle.AssemblySteps, promptAssemblyStep(
			"output-schema",
			"加入输出结构要求",
			"output_schema",
			"输出策略",
			"运行策略 > 输出协议",
			"system",
			"output_schema",
			"输出策略要求模型返回指定结构",
			renderJSON(strategy.OutputSchema),
		))
	}
	if strings.TrimSpace(strategy.OutputMode) == "decision_json" {
		constraint := "output strategy: return exactly one CleanCore decision JSON object"
		bundle.Constraints = append(bundle.Constraints, constraint)
		bundle.AssemblySteps = append(bundle.AssemblySteps, promptAssemblyStep(
			"output-mode-decision-json",
			"加入决策 JSON 输出要求",
			"output_mode",
			"输出策略",
			"运行策略 > 输出协议",
			"system",
			"constraints",
			"模型必须返回可解析的运行决策",
			constraint,
		))
	}
	if strategy.StrictJSON {
		constraint := "output strategy strict_json: include required fields explicitly; do not rely on omitted defaults or tool name/id normalization"
		bundle.Constraints = append(bundle.Constraints, constraint)
		bundle.AssemblySteps = append(bundle.AssemblySteps, promptAssemblyStep(
			"output-strict-json",
			"加入严格 JSON 约束",
			"output_strict_json",
			"输出策略",
			"运行策略 > 输出协议",
			"system",
			"constraints",
			"严格模式要求字段显式完整",
			constraint,
		))
	}
	return bundle
}

func promptAssemblyStep(stepID string, title string, sourceType string, sourceLabel string, editTarget string, role string, section string, reason string, content string) contracts.PromptAssemblyStep {
	content = strings.TrimSpace(content)
	return contracts.PromptAssemblyStep{
		StepID:         stepID,
		Title:          title,
		SourceType:     sourceType,
		SourceLabel:    sourceLabel,
		EditTarget:     editTarget,
		MessageRole:    role,
		PromptSection:  section,
		Reason:         reason,
		Content:        content,
		TokensEstimate: estimateTokens(content),
		Included:       content != "",
	}
}

func estimateTokens(value string) int {
	value = strings.TrimSpace(value)
	if value == "" {
		return 0
	}
	words := len(strings.Fields(value))
	runes := len([]rune(value))
	charEstimate := (runes + 3) / 4
	if words > charEstimate {
		return words
	}
	return charEstimate
}

func renderJSON(value any) string {
	data, err := json.MarshalIndent(value, "", "  ")
	if err != nil {
		return fmt.Sprintf("%v", value)
	}
	return string(data)
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
