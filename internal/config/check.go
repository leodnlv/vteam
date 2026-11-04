package config

import (
	"encoding/json"
	"fmt"
	"math"
	"strings"

	"github.com/leodnlv/vteam/internal/cron"
)

// Issue is one validation failure, worded like the zod messages the TypeScript version printed.
type Issue struct {
	Path    []string
	Message string
}

func (i Issue) String() string {
	return strings.Join(i.Path, ".") + ": " + i.Message
}

type checker struct {
	issues []Issue
}

func (c *checker) add(path []string, format string, args ...any) {
	c.issues = append(c.issues, Issue{Path: append([]string(nil), path...), Message: fmt.Sprintf(format, args...)})
}

func at(path []string, key string) []string {
	return append(append([]string(nil), path...), key)
}

func typeName(v any) string {
	switch v.(type) {
	case nil:
		return "null"
	case string:
		return "string"
	case float64:
		return "number"
	case bool:
		return "boolean"
	case []any:
		return "array"
	case map[string]any:
		return "object"
	}
	return "unknown"
}

func (c *checker) expect(path []string, v any, want string) bool {
	if got := typeName(v); got != want {
		c.add(path, "Invalid input: expected %s, received %s", want, got)
		return false
	}
	return true
}

func (c *checker) str(path []string, v any, minLen int) {
	if c.expect(path, v, "string") && len(v.(string)) < minLen {
		c.add(path, "Too small: expected string to have >=%d characters", minLen)
	}
}

func (c *checker) boolean(path []string, v any) { c.expect(path, v, "boolean") }

func (c *checker) strings(path []string, v any) {
	if !c.expect(path, v, "array") {
		return
	}
	for i, item := range v.([]any) {
		c.str(at(path, fmt.Sprint(i)), item, 0)
	}
}

func (c *checker) enum(path []string, v any, options ...string) {
	s, _ := v.(string)
	for _, o := range options {
		if s == o {
			return
		}
	}
	quoted := make([]string, len(options))
	for i, o := range options {
		quoted[i] = fmt.Sprintf("%q", o)
	}
	if len(options) == 1 {
		c.add(path, "Invalid input: expected %s", quoted[0])
		return
	}
	c.add(path, "Invalid option: expected one of %s", strings.Join(quoted, "|"))
}

func (c *checker) cronExpr(path []string, v any) {
	if c.expect(path, v, "string") && !cron.Valid(v.(string)) {
		c.add(path, "Invalid cron expression (expected 5 fields: minute hour day month weekday)")
	}
}

func (c *checker) nonNegativeInt(path []string, v any) {
	if !c.expect(path, v, "number") {
		return
	}
	n := v.(float64)
	if n != math.Trunc(n) {
		c.add(path, "Invalid input: expected int, received number")
	} else if n < 0 {
		c.add(path, "Too small: expected number to be >=0")
	}
}

type rule struct {
	key   string
	check func(c *checker, path []string, v any)
	// requiredType is the type named in the error when the key is missing; empty means optional.
	requiredType string
}

// object checks the keys of v in rule order. Unknown keys are reported last when strict.
func (c *checker) object(path []string, v any, strict bool, rules []rule) bool {
	if !c.expect(path, v, "object") {
		return false
	}
	obj := v.(map[string]any)
	known := map[string]bool{}
	for _, r := range rules {
		known[r.key] = true
		value, ok := obj[r.key]
		if !ok {
			if r.requiredType != "" {
				c.add(at(path, r.key), "Invalid input: expected %s, received undefined", r.requiredType)
			}
			continue
		}
		r.check(c, at(path, r.key), value)
	}
	if strict {
		for _, key := range sortedKeys(obj) {
			if !known[key] {
				c.add(path, "Unrecognized key: %q", key)
			}
		}
	}
	return true
}

func decodeDocument(data []byte) (any, error) {
	var doc any
	err := json.Unmarshal(data, &doc)
	return doc, err
}
