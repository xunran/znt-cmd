package jsonschema

import "fmt"

func ValidateObject(input map[string]any, schema map[string]any) error {
	if len(schema) == 0 {
		return nil
	}
	if schemaType, _ := schema["type"].(string); schemaType != "" && schemaType != "object" {
		return fmt.Errorf("unsupported schema type %q", schemaType)
	}
	required := stringList(schema["required"])
	if err := ValidateRequired(input, required); err != nil {
		return err
	}
	properties, _ := schema["properties"].(map[string]any)
	for name, rawProperty := range properties {
		value, exists := input[name]
		if !exists {
			continue
		}
		property, _ := rawProperty.(map[string]any)
		expectedType, _ := property["type"].(string)
		if expectedType == "" {
			continue
		}
		if !matchesType(value, expectedType) {
			return fmt.Errorf("field %q must be %s", name, expectedType)
		}
	}
	return nil
}

func ValidateRequired(input map[string]any, required []string) error {
	for _, key := range required {
		if _, ok := input[key]; !ok {
			return fmt.Errorf("missing required field %q", key)
		}
	}
	return nil
}

func stringList(value any) []string {
	switch typed := value.(type) {
	case []string:
		return typed
	case []any:
		out := make([]string, 0, len(typed))
		for _, item := range typed {
			if str, ok := item.(string); ok {
				out = append(out, str)
			}
		}
		return out
	default:
		return nil
	}
}

func matchesType(value any, expected string) bool {
	switch expected {
	case "string":
		_, ok := value.(string)
		return ok
	case "object":
		_, ok := value.(map[string]any)
		return ok
	case "array":
		_, ok := value.([]any)
		return ok
	case "boolean":
		_, ok := value.(bool)
		return ok
	case "number":
		switch value.(type) {
		case float64, float32, int, int64, int32:
			return true
		default:
			return false
		}
	case "integer":
		switch value.(type) {
		case int, int64, int32:
			return true
		default:
			return false
		}
	default:
		return true
	}
}
