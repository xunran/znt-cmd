package parser

import (
	"encoding/json"
	"fmt"

	"znt/internal/contracts"
)

func Parse(data []byte) (contracts.Decision, error) {
	var decision contracts.Decision
	if err := json.Unmarshal(data, &decision); err != nil {
		return contracts.Decision{}, contracts.NewRuntimeError(contracts.CodeDecisionSchemaError, fmt.Sprintf("parse decision json: %v", err), nil)
	}
	return decision, nil
}
