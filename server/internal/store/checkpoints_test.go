package store_test

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/andrewsl105/fleetpocket/server/internal/store"
	"github.com/andrewsl105/fleetpocket/server/internal/testutil"
)

func TestCheckpointSettingsDefaultOff(t *testing.T) {
	st := testutil.DB(t)
	ctx := context.Background()
	c, err := st.CheckpointSettings(ctx)
	if err != nil || c.Enabled {
		t.Fatalf("default: %+v %v", c, err)
	}
	if err := st.SetCheckpointSettings(ctx, store.CheckpointSettings{Enabled: true}); err != nil {
		t.Fatal(err)
	}
	if c, _ := st.CheckpointSettings(ctx); !c.Enabled {
		t.Fatal("not saved")
	}
}

func TestCheckpointsAndRestoreRun(t *testing.T) {
	st := testutil.DB(t)
	ctx := context.Background()
	task := newTask(t, st)
	run, _ := st.PendingRun(ctx, task.ID)
	_ = st.MarkRunStarting(ctx, run.ID)
	raw := json.RawMessage(`{"type":"checkpoint","n":1,"ref_sha":"abc","turn":2,"session_ref":"s/u","files_changed":1,"reason":"turn","ts":"2026-11-17T10:00:00Z","seq":5}`)
	ev, err := st.AddRunnerEvent(ctx, task.ID, run.ID, 5, "checkpoint", raw)
	if err != nil {
		t.Fatal(err)
	}
	cp, err := st.AddCheckpoint(ctx, store.Checkpoint{TaskID: task.ID, RunID: run.ID, EventID: &ev.ID, N: 1, RefSHA: "abc", Turn: 2, SessionRef: "s/u", FilesChanged: 1})
	if err != nil || cp.ID == 0 || cp.Reason != "turn" {
		t.Fatalf("%+v %v", cp, err)
	}
	// A replay keeps the row.
	again, err := st.AddCheckpoint(ctx, store.Checkpoint{TaskID: task.ID, RunID: run.ID, N: 1, RefSHA: "abc"})
	if err != nil || again.ID != cp.ID || again.EventID == nil {
		t.Fatalf("replay: %+v %v", again, err)
	}
	list, _ := st.ListCheckpoints(ctx, task.ID)
	if len(list) != 1 || list[0].SessionRef != "s/u" {
		t.Fatalf("%+v", list)
	}
	if ok, _ := st.HasRestore(ctx, task.ID); ok {
		t.Fatal("no restore yet")
	}
	if _, err := st.SetStatus(ctx, task.ID, nil, "pr_open", "", false); err != nil {
		t.Fatal(err)
	}
	nt, rev, err := st.RequeueRestore(ctx, task.ID, []string{"pr_open"}, cp, "do it differently", map[string]any{"checkpoint_id": cp.ID})
	if err != nil || nt.Status != "queued" || rev.Type != "checkpoint_restored" {
		t.Fatalf("%+v %+v %v", nt, rev, err)
	}
	pending, _ := st.PendingRun(ctx, task.ID)
	if pending.Kind != "restore" || pending.RestoreCheckpointID == nil || *pending.RestoreCheckpointID != cp.ID || pending.Input != "do it differently" {
		t.Fatalf("%+v", pending)
	}
	if ok, _ := st.HasRestore(ctx, task.ID); !ok {
		t.Fatal("restore not seen")
	}
	// Only from the given states.
	if _, _, err := st.RequeueRestore(ctx, task.ID, []string{"pr_open"}, cp, "x", map[string]any{}); err == nil {
		t.Fatal("restore from queued must conflict")
	}
}
