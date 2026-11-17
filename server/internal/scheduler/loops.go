package scheduler

import (
	"context"
	"encoding/json"
	"errors"

	"github.com/andrewsl105/fleetpocket/server/internal/loops"
	"github.com/andrewsl105/fleetpocket/server/internal/runnermgr"
	"github.com/andrewsl105/fleetpocket/server/internal/store"
	"github.com/andrewsl105/fleetpocket/server/internal/tasks"
)

// EventLoop is the backend event of a loop: phase "iteration" when an
// iteration is queued (iteration 1 when the task is created), "stopped"
// when the loop ended.
const EventLoop = "loop"

func loopRunning(t *store.Task) bool {
	return t.Loop != nil && deref(t.LoopState, "") == store.LoopRunning
}

// applyLoop makes a run of a running loop an iteration: it records the
// iteration on the run and gives the runner the check command. A retry
// starts the loop over.
func (s *Scheduler) applyLoop(ctx context.Context, t *store.Task, run *store.Run, cmd *runnermgr.RunCommand) error {
	if t.Loop == nil {
		return nil
	}
	if run.Kind == tasks.RunRetry {
		nt, err := s.Store.RestartLoop(ctx, t.ID)
		if err != nil {
			return err
		}
		t.LoopState, t.LoopIteration, t.LoopIdle = nt.LoopState, nt.LoopIteration, nt.LoopIdle
		s.addEvent(ctx, t.ID, &run.ID, EventLoop, map[string]any{"phase": "iteration", "iteration": 1,
			"max_iterations": t.Loop.MaxIterations, "check_command": t.Loop.CheckCommand, "run_id": run.ID})
	}
	if !loopRunning(t) {
		return nil
	}
	if run.LoopIteration == nil {
		n := max(t.LoopIteration, 1)
		if err := s.Store.SetRunLoopIteration(ctx, run.ID, n); err != nil {
			return err
		}
		run.LoopIteration = &n
	}
	cmd.Check = &runnermgr.CheckConfig{Command: t.Loop.CheckCommand, TimeoutSec: t.Loop.TimeoutSec}
	return nil
}

// checkRaw rewrites a runner `check` event for the timeline: the output
// trimmed like CI logs, plus the iteration it belongs to.
func checkRaw(t *store.Task, ev runnermgr.Event) json.RawMessage {
	var m map[string]any
	if err := json.Unmarshal(ev.Raw, &m); err != nil {
		return ev.Raw
	}
	m["output"] = loops.TrimOutput(ev.Output)
	if t.Loop != nil {
		m["iteration"], m["max_iterations"] = max(t.LoopIteration, 1), t.Loop.MaxIterations
	}
	b, err := json.Marshal(m)
	if err != nil {
		return ev.Raw
	}
	return b
}

// runCheck is the check result of the run: from the stream, else the
// stored event (a restart between the check and the exit).
func (s *Scheduler) runCheck(ctx context.Context, runID int64, out runOutcome) *loops.Check {
	if out.check != nil && out.check.ExitCode != nil {
		return &loops.Check{Command: out.check.Command, ExitCode: *out.check.ExitCode, Output: loops.TrimOutput(out.check.Output), TimedOut: out.check.TimedOut}
	}
	ev, err := s.Store.RunCheck(ctx, runID)
	if err != nil {
		return nil
	}
	var p struct {
		Command  string `json:"command"`
		ExitCode *int   `json:"exit_code"`
		Output   string `json:"output"`
		TimedOut bool   `json:"timed_out"`
	}
	if json.Unmarshal(ev.Payload, &p) != nil || p.ExitCode == nil {
		return nil
	}
	return &loops.Check{Command: p.Command, ExitCode: *p.ExitCode, Output: p.Output, TimedOut: p.TimedOut}
}

// loopStep runs after an iteration's run ended done or at a limit. It
// returns true when it queued the next iteration (the commits stay in the
// workspace, nothing is pushed); false when there is no running loop or the
// loop just ended, and the run is finished as usual (push and PR).
func (s *Scheduler) loopStep(ctx context.Context, t *store.Task, run *store.Run, code int, limit string, out runOutcome) bool {
	if !loopRunning(t) {
		return false
	}
	iteration := max(t.LoopIteration, 1)
	if run.LoopIteration != nil {
		iteration = *run.LoopIteration
	}
	spent, err := s.Store.LoopSpent(ctx, t.ID)
	if err != nil {
		s.log(t, run.ID).Error("loop spent", "err", err)
	}
	budget := deref(t.Loop.BudgetUSD, 0)
	check := s.runCheck(ctx, run.ID, out)
	d := loops.Decide(loops.State{
		Iteration: iteration, MaxIterations: t.Loop.MaxIterations, Idle: t.LoopIdle, Changed: out.committed,
		Check: check, LimitHit: code == runnermgr.ExitLimitExceeded, Limit: limit, SpentUSD: spent, BudgetUSD: budget,
	})
	payload := map[string]any{"iteration": iteration, "max_iterations": t.Loop.MaxIterations, "reason": d.Reason, "spent_usd": round2(spent)}
	if !d.Next {
		s.endLoop(ctx, t, d.State, payload)
		return false
	}
	var runBudget *float64
	if budget > 0 {
		left := budget - spent
		if per := s.perRunBudget(ctx, t); per > 0 && per < left {
			left = per
		}
		runBudget = &left
	}
	payload["exit_code"], payload["timed_out"] = check.ExitCode, check.TimedOut
	nt, ev, err := s.Store.StartLoopIteration(ctx, store.LoopNext{
		TaskID: t.ID, From: []string{tasks.Running, tasks.WaitingInput}, Iteration: iteration + 1, Idle: d.Idle,
		Input: loops.Input(iteration+1, t.Loop.MaxIterations, *check), BudgetUSD: runBudget, Payload: payload,
	})
	if err != nil {
		if !errors.Is(err, store.ErrConflict) {
			s.log(t, run.ID).Error("next loop iteration", "err", err)
		}
		return false
	}
	s.log(t, run.ID).Info("loop: next iteration", "iteration", iteration+1, "reason", d.Reason)
	s.Hub.PublishEvent(ev)
	s.Hub.PublishTask(nt)
	return true
}

// perRunBudget is the budget a run of the task gets without a loop cap.
func (s *Scheduler) perRunBudget(ctx context.Context, t *store.Task) float64 {
	if t.RunBudgetUSD != nil {
		return *t.RunBudgetUSD
	}
	if p, err := s.Store.GetProject(ctx, t.ProjectID); err == nil {
		return deref(p.MaxBudgetUSD, s.Cfg.DefaultMaxBudgetUSD)
	}
	return s.Cfg.DefaultMaxBudgetUSD
}

// endLoop records why a running loop ended (a `loop` event, phase stopped).
func (s *Scheduler) endLoop(ctx context.Context, t *store.Task, state string, payload map[string]any) {
	if !loopRunning(t) {
		return
	}
	nt, ev, err := s.Store.EndLoop(ctx, t.ID, state, payload)
	if err != nil {
		if !errors.Is(err, store.ErrConflict) {
			s.log(t, 0).Error("end loop", "err", err)
		}
		return
	}
	t.LoopState = nt.LoopState
	s.log(t, 0).Info("loop ended", "state", state, "reason", payload["reason"])
	if s.Hooks.LoopEnded != nil {
		s.Hooks.LoopEnded(t.ProjectID, state)
	}
	s.Hub.PublishEvent(ev)
	s.Hub.PublishTask(nt)
}
