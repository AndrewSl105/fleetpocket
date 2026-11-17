package scheduler

import (
	"context"
	"errors"
	"fmt"
	"strconv"

	"github.com/andrewsl105/fleetpocket/server/internal/hosts"
	"github.com/andrewsl105/fleetpocket/server/internal/runnermgr"
	"github.com/andrewsl105/fleetpocket/server/internal/store"
	"github.com/andrewsl105/fleetpocket/server/internal/tasks"
	"github.com/andrewsl105/fleetpocket/server/internal/workspace"
)

// applyCheckpoints turns checkpoints on for the run when the owner enabled
// them, and for a restore run resets the workspace to its checkpoint and
// tells the agent where to resume (SPEC-server, "Checkpoints").
func (s *Scheduler) applyCheckpoints(ctx context.Context, h hosts.Host, t *store.Task, run *store.Run, cmd *runnermgr.RunCommand) error {
	settings, err := s.Store.CheckpointSettings(ctx)
	if err != nil {
		return err
	}
	if settings.Enabled {
		cmd.Checkpoints = &runnermgr.CheckpointsConfig{RunID: strconv.FormatInt(run.ID, 10)}
	}
	if run.Kind != tasks.RunRestore {
		return nil
	}
	if run.RestoreCheckpointID == nil {
		return errors.New("the checkpoint of this restore no longer exists")
	}
	cp, err := s.Store.GetCheckpoint(ctx, t.ID, *run.RestoreCheckpointID)
	if err != nil {
		return err
	}
	if err := h.RestoreCheckpoint(ctx, t.ID, runnermgr.CheckpointRef(cp.RunID, cp.N), cp.RefSHA); err != nil {
		if errors.Is(err, workspace.ErrCheckpointGone) {
			return fmt.Errorf("checkpoint %d of run %d is no longer in the workspace", cp.N, cp.RunID)
		}
		return err
	}
	cmd.ResumeSessionID = ""
	if tasks.RestoreMode(t, cp) == tasks.RestoreFork {
		cmd.ResumeSessionRef = cp.SessionRef
	}
	return nil
}

// recordCheckpoint stores a runner checkpoint (also when replayed from the
// event log: then without its timeline row, unless already stored).
func (s *Scheduler) recordCheckpoint(ctx context.Context, t *store.Task, runID int64, ev runnermgr.Event, stored *store.Event) {
	c := store.Checkpoint{TaskID: t.ID, RunID: runID, N: ev.N, RefSHA: ev.RefSHA, Turn: ev.Turn,
		SessionRef: ev.SessionRef, FilesChanged: ev.FilesChanged, Reason: ev.Reason}
	if stored != nil {
		c.EventID = &stored.ID
	}
	if _, err := s.Store.AddCheckpoint(ctx, c); err != nil {
		s.log(t, runID).Error("store checkpoint", "err", err)
	}
}

// needsForce: after a restore the branch may have been rewritten; then the
// push needs --force-with-lease (the publisher refuses branches Jungle
// Monkey did not create).
func (s *Scheduler) needsForce(ctx context.Context, h hosts.Host, t *store.Task, branch string) (bool, error) {
	if branch == "" || s.Publisher == nil {
		return false, nil
	}
	restored, err := s.Store.HasRestore(ctx, t.ID)
	if err != nil || !restored {
		return false, err
	}
	return h.Diverged(ctx, t.ID, branch)
}
