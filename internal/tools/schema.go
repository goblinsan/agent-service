package tools

// JSONSchema converts a Tool's parameter list into a JSON Schema object suitable
// for advertising via the OpenAI-compatible "tools" array on a chat request.
func JSONSchema(t Tool) map[string]any {
	properties := map[string]any{}
	var required []string
	for _, p := range t.Params {
		jsType := "string"
		switch p.Type {
		case "int", "integer":
			jsType = "integer"
		case "bool", "boolean":
			jsType = "boolean"
		case "number", "float":
			jsType = "number"
		}
		properties[p.Name] = map[string]any{
			"type":        jsType,
			"description": p.Description,
		}
		if p.Required {
			required = append(required, p.Name)
		}
	}
	schema := map[string]any{
		"type":       "object",
		"properties": properties,
	}
	if len(required) > 0 {
		schema["required"] = required
	}
	return schema
}
