package tasks

import (
	"regexp"
	"strings"

	"github.com/leodnlv/vteam/internal/config"
)

var labelSeparators = regexp.MustCompile(`[^a-z0-9]+`)

// ToAvenLabel mirrors aven's label normalization, so the labels vteam writes match the ones aven stores.
func ToAvenLabel(name string) string {
	return strings.Trim(labelSeparators.ReplaceAllString(strings.ToLower(name), "-"), "-")
}

func AgentLabel(cfg config.Tasks, agent string) string {
	return ToAvenLabel(cfg.Label + "-" + agent)
}

func LabelsOnCreate(cfg config.Tasks, agent string) []string {
	return []string{AgentLabel(cfg, agent)}
}

func LabelsOnDone(cfg config.Tasks, agent string) []string {
	return []string{AgentLabel(cfg, agent)}
}
