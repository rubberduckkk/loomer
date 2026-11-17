package core

import (
	"bytes"
	"encoding/json"
	"fmt"
	"math"
	"time"
)

// JSONSchemaValidator implements the JSON Schema keywords used by Loomer's
// initial contracts. It intentionally rejects unsupported validation
// keywords instead of silently treating them as valid.
type JSONSchemaValidator struct{}

func (JSONSchemaValidator) Validate(schema, value json.RawMessage) error {
	if !json.Valid(schema) {
		return fmt.Errorf("schema is not valid JSON")
	}
	if !json.Valid(value) {
		return fmt.Errorf("value is not valid JSON")
	}
	var schemaValue, data any
	if err := json.Unmarshal(schema, &schemaValue); err != nil {
		return fmt.Errorf("decode schema: %w", err)
	}
	if err := json.Unmarshal(value, &data); err != nil {
		return fmt.Errorf("decode value: %w", err)
	}
	return validateSchemaNode(schemaValue, data, "$")
}

func validateSchemaNode(schema, value any, path string) error {
	if schemaMap, ok := schema.(map[string]any); ok {
		for key := range schemaMap {
			switch key {
			case "$schema", "$id", "title", "description", "type", "properties", "required", "additionalProperties", "items", "minItems", "maxItems", "minLength", "maxLength", "minimum", "maximum", "enum", "uniqueItems", "format":
			default:
				return fmt.Errorf("%s: unsupported schema keyword %q", path, key)
			}
		}
		if rawType, exists := schemaMap["type"]; exists && !matchesType(rawType, value) {
			return fmt.Errorf("%s: expected type %v", path, rawType)
		}
		if enum, exists := schemaMap["enum"]; exists {
			values, ok := enum.([]any)
			if !ok {
				return fmt.Errorf("%s: enum must be an array", path)
			}
			found := false
			for _, candidate := range values {
				if jsonEqual(candidate, value) {
					found = true
					break
				}
			}
			if !found {
				return fmt.Errorf("%s: value is not in enum", path)
			}
		}
		if object, ok := value.(map[string]any); ok {
			if required, exists := schemaMap["required"]; exists {
				keys, ok := required.([]any)
				if !ok {
					return fmt.Errorf("%s: required must be an array", path)
				}
				for _, entry := range keys {
					key, ok := entry.(string)
					if !ok {
						return fmt.Errorf("%s: required entries must be strings", path)
					}
					if _, exists := object[key]; !exists {
						return fmt.Errorf("%s: required property %q is missing", path, key)
					}
				}
			}
			properties, _ := schemaMap["properties"].(map[string]any)
			for key, child := range object {
				propertySchema, exists := properties[key]
				if !exists {
					if additional, configured := schemaMap["additionalProperties"]; configured {
						if allowed, isBool := additional.(bool); isBool {
							if !allowed {
								return fmt.Errorf("%s: additional property %q is not allowed", path, key)
							}
						} else if err := validateSchemaNode(additional, child, path+"."+key); err != nil {
							return err
						}
					}
					continue
				}
				if err := validateSchemaNode(propertySchema, child, path+"."+key); err != nil {
					return err
				}
			}
		}
		if array, ok := value.([]any); ok {
			if itemSchema, exists := schemaMap["items"]; exists {
				for index, item := range array {
					if err := validateSchemaNode(itemSchema, item, fmt.Sprintf("%s[%d]", path, index)); err != nil {
						return err
					}
				}
			}
			if minimum, exists, err := schemaNumber(schemaMap, "minItems", path); err != nil {
				return err
			} else if exists && float64(len(array)) < minimum {
				return fmt.Errorf("%s: array has fewer than %v items", path, minimum)
			}
			if maximum, exists, err := schemaNumber(schemaMap, "maxItems", path); err != nil {
				return err
			} else if exists && float64(len(array)) > maximum {
				return fmt.Errorf("%s: array has more than %v items", path, maximum)
			}
			if unique, _ := schemaMap["uniqueItems"].(bool); unique {
				for i := range array {
					for j := 0; j < i; j++ {
						if jsonEqual(array[i], array[j]) {
							return fmt.Errorf("%s: array items at indexes %d and %d are duplicates", path, j, i)
						}
					}
				}
			}
		}
		if text, ok := value.(string); ok {
			if minimum, exists, err := schemaNumber(schemaMap, "minLength", path); err != nil {
				return err
			} else if exists && float64(len([]rune(text))) < minimum {
				return fmt.Errorf("%s: string is shorter than %v characters", path, minimum)
			}
			if maximum, exists, err := schemaNumber(schemaMap, "maxLength", path); err != nil {
				return err
			} else if exists && float64(len([]rune(text))) > maximum {
				return fmt.Errorf("%s: string is longer than %v characters", path, maximum)
			}
			if format, _ := schemaMap["format"].(string); format == "date-time" {
				if _, err := time.Parse(time.RFC3339, text); err != nil {
					return fmt.Errorf("%s: expected RFC3339 date-time", path)
				}
			}
		}
		if number, ok := value.(float64); ok {
			if minimum, exists, err := schemaNumber(schemaMap, "minimum", path); err != nil {
				return err
			} else if exists && number < minimum {
				return fmt.Errorf("%s: number is below minimum %v", path, minimum)
			}
			if maximum, exists, err := schemaNumber(schemaMap, "maximum", path); err != nil {
				return err
			} else if exists && number > maximum {
				return fmt.Errorf("%s: number is above maximum %v", path, maximum)
			}
		}
		return nil
	}
	return fmt.Errorf("%s: schema node must be an object", path)
}

func matchesType(rawType, value any) bool {
	if types, ok := rawType.([]any); ok {
		for _, candidate := range types {
			if matchesType(candidate, value) {
				return true
			}
		}
		return false
	}
	typeName, ok := rawType.(string)
	if !ok {
		return false
	}
	switch typeName {
	case "object":
		_, ok := value.(map[string]any)
		return ok
	case "array":
		_, ok := value.([]any)
		return ok
	case "string":
		_, ok := value.(string)
		return ok
	case "number":
		_, ok := value.(float64)
		return ok
	case "integer":
		number, ok := value.(float64)
		return ok && math.Trunc(number) == number
	case "boolean":
		_, ok := value.(bool)
		return ok
	case "null":
		return value == nil
	default:
		return false
	}
}

func jsonEqual(left, right any) bool {
	leftJSON, leftErr := json.Marshal(left)
	rightJSON, rightErr := json.Marshal(right)
	return leftErr == nil && rightErr == nil && bytes.Equal(leftJSON, rightJSON)
}

func schemaNumber(schema map[string]any, key, path string) (float64, bool, error) {
	value, exists := schema[key]
	if !exists {
		return 0, false, nil
	}
	number, ok := value.(float64)
	if !ok || math.IsNaN(number) || math.IsInf(number, 0) {
		return 0, true, fmt.Errorf("%s: schema keyword %q must be a finite number", path, key)
	}
	if (key == "minItems" || key == "maxItems" || key == "minLength" || key == "maxLength") && (number < 0 || math.Trunc(number) != number) {
		return 0, true, fmt.Errorf("%s: schema keyword %q must be a non-negative integer", path, key)
	}
	return number, true, nil
}
