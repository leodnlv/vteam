package output

import (
	"encoding/json"
)

type SchemaOptions struct {
	TaskContent   bool
	Memory        bool
	ThreadReplies bool
}

func stringSchema(minLength int) map[string]any {
	s := map[string]any{"type": "string"}
	if minLength > 0 {
		s["minLength"] = minLength
	}
	return s
}

func strictObject(properties map[string]any, required ...string) map[string]any {
	return map[string]any{"type": "object", "properties": properties, "required": required, "additionalProperties": false}
}

func stringArray(minItems int) map[string]any {
	s := map[string]any{"type": "array", "items": stringSchema(0)}
	if minItems > 0 {
		s["minItems"] = minItems
	}
	return s
}

// Schema is the JSON Schema passed to `claude --json-schema`. It mirrors validate and offers the same optional
// fields as the prompt's output instructions, so Claude Code rejects a final answer that Parse would reject.
func Schema(o SchemaOptions) string {
	content := strictObject(map[string]any{
		"type": map[string]any{"type": "string", "enum": []string{"generic"}},
		"body": stringSchema(0),
	}, "type", "body")
	if o.TaskContent {
		content = strictObject(map[string]any{
			"type": map[string]any{"type": "string", "enum": []string{"task"}},
			"body": strictObject(map[string]any{
				"title":        stringSchema(1),
				"severity":     map[string]any{"type": "string", "enum": []string{"critical", "high", "medium", "low"}},
				"description":  stringSchema(1),
				"suggestedFix": stringSchema(0),
				"files":        stringArray(1),
			}, "title", "severity", "description", "files"),
		}, "type", "body")
	}

	properties := map[string]any{
		"status":        map[string]any{"type": "string", "enum": []string{"completed", "partial", "blocked", "failed"}},
		"summary":       stringSchema(0),
		"content":       content,
		"filesChanged":  stringArray(0),
		"commitMessage": strictObject(map[string]any{"subject": stringSchema(1), "body": stringSchema(0)}, "subject", "body"),
		"blockerReason": stringSchema(0),
	}
	if o.Memory {
		properties["memoryUpdate"] = stringSchema(0)
	}
	if o.ThreadReplies {
		properties["threadReplies"] = map[string]any{
			"type":  "array",
			"items": strictObject(map[string]any{"thread": stringSchema(1), "body": stringSchema(1)}, "thread", "body"),
		}
	}

	data, err := json.Marshal(strictObject(properties, "status", "summary"))
	if err != nil {
		panic(err)
	}
	return string(data)
}
