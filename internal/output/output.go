// Package output parses and validates the AgentOutput JSON that Claude returns as its final message.
package output

import (
	"encoding/json"
	"errors"
	"fmt"
	"regexp"
	"strings"

	"github.com/leodnlv/vteam/internal/model"
)

var fence = regexp.MustCompile("^\\s*```(?:json)?\\s*\\n([\\s\\S]*?)\\n\\s*```\\s*$")

func stripFences(text string) string {
	if m := fence.FindStringSubmatch(text); m != nil {
		return strings.TrimSpace(m[1])
	}
	return strings.TrimSpace(text)
}

// findJSONObject returns the first span from a '{' to the last '}' that is valid JSON.
// Prose before the JSON may itself contain braces, so every '{' is tried in turn.
func findJSONObject(text string) (string, bool) {
	for i := 0; i < len(text); i++ {
		if text[i] != '{' {
			continue
		}
		candidate := text[i:]
		last := strings.LastIndexByte(candidate, '}')
		if last == -1 {
			return "", false
		}
		if slice := candidate[:last+1]; json.Valid([]byte(slice)) {
			return slice, true
		}
	}
	return "", false
}

func extractJSON(text string) string {
	stripped := stripFences(text)
	if obj, ok := findJSONObject(stripped); ok {
		return obj
	}
	if obj, ok := findJSONObject(text); ok {
		return obj
	}
	return stripped
}

func Parse(resultText string) (model.AgentOutput, error) {
	var out model.AgentOutput
	raw := []byte(extractJSON(resultText))
	var doc any
	if err := json.Unmarshal(raw, &doc); err != nil {
		return out, fmt.Errorf("agent output is not valid JSON: %w", err)
	}
	if err := validate(doc); err != nil {
		return out, fmt.Errorf("invalid agent output: %w", err)
	}
	if err := json.Unmarshal(raw, &out); err != nil {
		return out, fmt.Errorf("invalid agent output: %w", err)
	}
	return out, nil
}

type object map[string]any

func asObject(v any, path string) (object, error) {
	m, ok := v.(map[string]any)
	if !ok {
		return nil, fmt.Errorf("%s: expected object", path)
	}
	return m, nil
}

// field returns the value at key, rejecting explicit nulls: optional fields may be omitted but not null.
func (o object) field(key, path string, required bool) (any, bool, error) {
	v, ok := o[key]
	if !ok {
		if required {
			return nil, false, fmt.Errorf("%s: required", path)
		}
		return nil, false, nil
	}
	if v == nil {
		return nil, false, fmt.Errorf("%s: must not be null", path)
	}
	return v, true, nil
}

func (o object) str(key, path string, required bool, minLen int) error {
	v, ok, err := o.field(key, path, required)
	if err != nil || !ok {
		return err
	}
	s, isStr := v.(string)
	if !isStr {
		return fmt.Errorf("%s: expected string", path)
	}
	if len(s) < minLen {
		return fmt.Errorf("%s: must not be empty", path)
	}
	return nil
}

func (o object) enum(key, path string, allowed ...string) error {
	v, _, err := o.field(key, path, true)
	if err != nil {
		return err
	}
	s, _ := v.(string)
	for _, a := range allowed {
		if s == a {
			return nil
		}
	}
	return fmt.Errorf("%s: expected one of %s", path, strings.Join(allowed, "|"))
}

func (o object) strings(key, path string, minItems int) error {
	v, ok, err := o.field(key, path, false)
	if err != nil || !ok {
		if err == nil && minItems > 0 {
			return fmt.Errorf("%s: required", path)
		}
		return err
	}
	items, isArr := v.([]any)
	if !isArr {
		return fmt.Errorf("%s: expected array", path)
	}
	if len(items) < minItems {
		return fmt.Errorf("%s: expected at least %d item(s)", path, minItems)
	}
	for i, item := range items {
		if _, isStr := item.(string); !isStr {
			return fmt.Errorf("%s[%d]: expected string", path, i)
		}
	}
	return nil
}

func validate(doc any) error {
	root, err := asObject(doc, "(root)")
	if err != nil {
		return err
	}
	return errors.Join(
		root.enum("status", "status", "completed", "partial", "blocked", "failed"),
		root.str("summary", "summary", true, 0),
		validateContent(root),
		root.strings("filesChanged", "filesChanged", 0),
		validateCommitMessage(root),
		root.str("blockerReason", "blockerReason", false, 0),
		root.str("memoryUpdate", "memoryUpdate", false, 0),
		validateThreadReplies(root),
	)
}

func validateContent(root object) error {
	v, ok, err := root.field("content", "content", false)
	if err != nil || !ok {
		return err
	}
	content, err := asObject(v, "content")
	if err != nil {
		return err
	}
	switch content["type"] {
	case "generic":
		return content.str("body", "content.body", true, 0)
	case "task":
		v, _, err := content.field("body", "content.body", true)
		if err != nil {
			return err
		}
		body, err := asObject(v, "content.body")
		if err != nil {
			return err
		}
		return errors.Join(
			body.str("title", "content.body.title", true, 1),
			body.enum("severity", "content.body.severity", "critical", "high", "medium", "low"),
			body.str("description", "content.body.description", true, 1),
			body.str("suggestedFix", "content.body.suggestedFix", false, 0),
			body.strings("files", "content.body.files", 1),
		)
	default:
		return errors.New(`content.type: expected "task" or "generic"`)
	}
}

func validateCommitMessage(root object) error {
	v, ok, err := root.field("commitMessage", "commitMessage", false)
	if err != nil || !ok {
		return err
	}
	msg, err := asObject(v, "commitMessage")
	if err != nil {
		return err
	}
	return errors.Join(
		msg.str("subject", "commitMessage.subject", true, 1),
		msg.str("body", "commitMessage.body", true, 0),
	)
}

func validateThreadReplies(root object) error {
	v, ok, err := root.field("threadReplies", "threadReplies", false)
	if err != nil || !ok {
		return err
	}
	items, isArr := v.([]any)
	if !isArr {
		return errors.New("threadReplies: expected array")
	}
	var errs []error
	for i, item := range items {
		path := fmt.Sprintf("threadReplies[%d]", i)
		reply, err := asObject(item, path)
		if err != nil {
			errs = append(errs, err)
			continue
		}
		errs = append(errs,
			reply.str("thread", path+".thread", true, 1),
			reply.str("body", path+".body", true, 1),
		)
	}
	return errors.Join(errs...)
}
