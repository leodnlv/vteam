package config

import (
	"encoding/json"
	"slices"
)

func sortedKeys(m map[string]any) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	slices.Sort(keys)
	return keys
}

// decodeChecked re-decodes a document that already passed validation into its typed form.
func decodeChecked(doc any, v any) error {
	data, err := json.Marshal(doc)
	if err != nil {
		return err
	}
	return json.Unmarshal(data, v)
}
