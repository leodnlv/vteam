package config

import _ "embed"

// AgentSchemaJSON is the JSON Schema for agent.json, written to vteam/agent.schema.json by vteam init.
// It is for editor autocomplete only and cannot express the cross-field rules that ParseAgentConfig enforces.
//
//go:embed agent.schema.json
var AgentSchemaJSON []byte
