package tools

import (
	"fmt"
	"strconv"
)

// Validate checks that every required parameter is present and has a compatible type.
// Supported type values: "string", "int", "bool".
func Validate(t Tool, params map[string]any) error {
	for _, p := range t.Params {
		v, ok := params[p.Name]
		if !ok {
			if p.Required {
				return fmt.Errorf("missing required parameter %q", p.Name)
			}
			continue
		}
		if err := checkType(p.Name, p.Type, v); err != nil {
			return err
		}
	}
	return nil
}

// checkType returns an error when v cannot be interpreted as the declared type.
// String representations are accepted for "int" and "bool" to handle JSON numbers
// decoded into float64 or stringified inputs from tool calls.
func checkType(name, typ string, v any) error {
	switch typ {
	case "string":
		if _, ok := v.(string); !ok {
			return fmt.Errorf("parameter %q must be a string", name)
		}
	case "int":
		switch v.(type) {
		case int, int8, int16, int32, int64,
			uint, uint8, uint16, uint32, uint64,
			float32, float64:
			// numeric types are fine
		case string:
			if _, err := strconv.ParseInt(v.(string), 10, 64); err != nil {
				return fmt.Errorf("parameter %q must be an integer", name)
			}
		default:
			return fmt.Errorf("parameter %q must be an integer", name)
		}
	case "bool":
		switch v := v.(type) {
		case bool:
			// ok
		case string:
			if _, err := strconv.ParseBool(v); err != nil {
				return fmt.Errorf("parameter %q must be a boolean", name)
			}
		case int, int8, int16, int32, int64,
			uint, uint8, uint16, uint32, uint64,
			float32, float64:
			// 0 and 1 are accepted as false/true
		default:
			return fmt.Errorf("parameter %q must be a boolean", name)
		}
	}
	return nil
}
