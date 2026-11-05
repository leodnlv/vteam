package output

import (
	"encoding/json"
	"reflect"
	"slices"
	"strings"
	"testing"

	"github.com/leodnlv/vteam/internal/model"
)

func parseSchema(t *testing.T, o SchemaOptions) map[string]any {
	t.Helper()
	var s map[string]any
	if err := json.Unmarshal([]byte(Schema(o)), &s); err != nil {
		t.Fatalf("schema is not valid JSON: %v", err)
	}
	return s
}

func props(s map[string]any) map[string]any {
	return s["properties"].(map[string]any)
}

func TestSchemaRequiresStatusAndSummaryOnly(t *testing.T) {
	s := parseSchema(t, SchemaOptions{})
	if got := s["required"]; !reflect.DeepEqual(got, []any{"status", "summary"}) {
		t.Errorf("required = %v", got)
	}
	if s["additionalProperties"] != false {
		t.Error("unknown top-level fields should be rejected")
	}
}

func TestSchemaContentMatchesOutputMode(t *testing.T) {
	generic := props(props(parseSchema(t, SchemaOptions{}))["content"].(map[string]any))
	if generic["body"].(map[string]any)["type"] != "string" || !reflect.DeepEqual(generic["type"].(map[string]any)["enum"], []any{"generic"}) {
		t.Errorf("generic content = %v", generic)
	}
	task := props(props(parseSchema(t, SchemaOptions{TaskContent: true}))["content"].(map[string]any))
	body := task["body"].(map[string]any)
	if !reflect.DeepEqual(task["type"].(map[string]any)["enum"], []any{"task"}) || !reflect.DeepEqual(body["required"], []any{"title", "severity", "description", "files"}) {
		t.Errorf("task content = %v", task)
	}
}

func TestSchemaOptionalFieldsFollowTheAgent(t *testing.T) {
	bare := props(parseSchema(t, SchemaOptions{}))
	if _, ok := bare["memoryUpdate"]; ok {
		t.Error("memoryUpdate offered to an agent without memory")
	}
	if _, ok := bare["threadReplies"]; ok {
		t.Error("threadReplies offered without review threads")
	}
	full := props(parseSchema(t, SchemaOptions{Memory: true, ThreadReplies: true}))
	for _, key := range []string{"memoryUpdate", "threadReplies"} {
		if _, ok := full[key]; !ok {
			t.Errorf("%s missing", key)
		}
	}
}

func TestSchemaFieldsExistOnAgentOutput(t *testing.T) {
	var fields []string
	typ := reflect.TypeFor[model.AgentOutput]()
	for i := range typ.NumField() {
		name, _, _ := strings.Cut(typ.Field(i).Tag.Get("json"), ",")
		fields = append(fields, name)
	}
	for key := range props(parseSchema(t, SchemaOptions{Memory: true, ThreadReplies: true})) {
		if !slices.Contains(fields, key) {
			t.Errorf("schema property %q is not an AgentOutput field", key)
		}
	}
}
