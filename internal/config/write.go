package config

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
)

// SetAgentCron writes cron into the agent's agent.json, creating the file when the agent has none.
func SetAgentCron(cwd, name, expr string) error {
	dir := filepath.Join(AgentsDir(cwd), name)
	if !fileExists(filepath.Join(dir, "AGENT.md")) {
		return fmt.Errorf("agent %q not found", name)
	}
	return setJSONKey(filepath.Join(dir, "agent.json"), []string{"cron"}, expr, `{"$schema": "../../agent.schema.json"}`)
}

// SetMergeEnsurerCron writes mergeEnsurer.cron into vteam.config.json.
func SetMergeEnsurerCron(cwd, expr string) error {
	if !fileExists(ConfigPath(cwd)) {
		return errors.New("vteam.config.json not found. Run 'vteam init' first")
	}
	return setJSONKey(ConfigPath(cwd), []string{"mergeEnsurer", "cron"}, expr, "")
}

// setJSONKey sets the key at path in a JSON object file, keeping the order of the other keys,
// and rewrites the file with two-space indentation. A missing file starts from empty.
func setJSONKey(path string, keys []string, value any, empty string) error {
	data, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) && empty != "" {
		data, err = []byte(empty), nil
	}
	if err != nil {
		return err
	}
	encoded, err := json.Marshal(value)
	if err != nil {
		return err
	}
	updated, err := setMember(data, keys, encoded)
	if err != nil {
		return fmt.Errorf("cannot update %s: %w", filepath.Base(path), err)
	}
	var out bytes.Buffer
	if err := json.Indent(&out, updated, "", "  "); err != nil {
		return err
	}
	out.WriteByte('\n')
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, out.Bytes(), 0o644); err != nil {
		return err
	}
	return os.Rename(tmp, path)
}

type member struct {
	key   string
	value json.RawMessage
}

func setMember(object []byte, keys []string, value json.RawMessage) ([]byte, error) {
	members, err := decodeMembers(object)
	if err != nil {
		return nil, err
	}
	idx := -1
	for i, m := range members {
		if m.key == keys[0] {
			idx = i
		}
	}
	if len(keys) > 1 {
		inner := json.RawMessage("{}")
		if idx >= 0 {
			inner = members[idx].value
		}
		if value, err = setMember(inner, keys[1:], value); err != nil {
			return nil, err
		}
	}
	if idx >= 0 {
		members[idx].value = value
	} else {
		members = append(members, member{key: keys[0], value: value})
	}
	return encodeMembers(members)
}

func decodeMembers(object []byte) ([]member, error) {
	dec := json.NewDecoder(bytes.NewReader(object))
	if tok, err := dec.Token(); err != nil || tok != json.Delim('{') {
		return nil, errors.New("expected a JSON object")
	}
	var members []member
	for dec.More() {
		tok, err := dec.Token()
		if err != nil {
			return nil, err
		}
		key, _ := tok.(string)
		var value json.RawMessage
		if err := dec.Decode(&value); err != nil {
			return nil, err
		}
		members = append(members, member{key: key, value: value})
	}
	return members, nil
}

func encodeMembers(members []member) ([]byte, error) {
	var buf bytes.Buffer
	buf.WriteByte('{')
	for i, m := range members {
		if i > 0 {
			buf.WriteByte(',')
		}
		key, err := json.Marshal(m.key)
		if err != nil {
			return nil, err
		}
		buf.Write(key)
		buf.WriteByte(':')
		buf.Write(m.value)
	}
	buf.WriteByte('}')
	return buf.Bytes(), nil
}
