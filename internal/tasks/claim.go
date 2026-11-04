package tasks

import (
	"context"
	"os"
	"strconv"

	"github.com/leodnlv/vteam/internal/model"
)

var ClaimKeys = []string{model.MetaClaimedBy, model.MetaClaimedPID, model.MetaClaimedHost}

func ClaimMetadata(runID string, pid int, host string) map[string]string {
	return map[string]string{
		model.MetaClaimedBy:   runID,
		model.MetaClaimedPID:  strconv.Itoa(pid),
		model.MetaClaimedHost: host,
	}
}

func Hostname() string {
	host, _ := os.Hostname()
	return host
}

func ClaimedPID(t model.Task) int {
	pid, _ := strconv.Atoi(t.Metadata[model.MetaClaimedPID])
	return pid
}

type OrphanSweep struct {
	Released    []model.Task
	ForeignHost []model.Task
	Unclaimed   []model.Task
}

// ReleaseOrphaned returns active tasks claimed on this host by a dead process to todo.
func ReleaseOrphaned(ctx context.Context, m Manager, isAlive func(int) bool, host string) (OrphanSweep, error) {
	var sweep OrphanSweep
	idx, err := m.GetIndex(ctx)
	if err != nil {
		return sweep, err
	}
	for _, t := range idx.ByStatus[model.StatusActive] {
		pid := ClaimedPID(t)
		switch {
		case pid == 0:
			sweep.Unclaimed = append(sweep.Unclaimed, t)
		case t.Metadata[model.MetaClaimedHost] != host:
			sweep.ForeignHost = append(sweep.ForeignHost, t)
		case !isAlive(pid):
			if err := m.Update(ctx, t.Ref, Update{Status: model.StatusTodo, RemoveMetadata: ClaimKeys}); err != nil {
				return sweep, err
			}
			sweep.Released = append(sweep.Released, t)
		}
	}
	return sweep, nil
}
