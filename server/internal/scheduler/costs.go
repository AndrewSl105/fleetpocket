package scheduler

import (
	"context"
	"log/slog"
	"strconv"
	"time"

	"github.com/andrewsl105/fleetpocket/server/internal/costs"
	"github.com/andrewsl105/fleetpocket/server/internal/runnermgr"
	"github.com/andrewsl105/fleetpocket/server/internal/store"
	"github.com/andrewsl105/fleetpocket/server/internal/tasks"
)

// usageBase is what a run's usage events are measured against: what earlier
// runs of the same Claude session recorded (Claude reports cumulative
// session totals), and the run's model once `started` named it.
type usageBase struct {
	runID  int64
	cost   float64
	tokens costs.Tokens
	model  string
}

func (s *Scheduler) usageBase(ctx context.Context, t *store.Task, run *store.Run) *usageBase {
	b := &usageBase{runID: run.ID, model: deref(run.Model, "")}
	if (run.Kind == tasks.RunResume || run.Kind == tasks.RunFollowup || run.Kind == tasks.RunLoop) && costs.ReportsCost(t.Agent) {
		b.cost, b.tokens, _ = s.Store.SessionBaseline(ctx, t.ID, run.ID)
	}
	return b
}

func (s *Scheduler) now() time.Time {
	if s.Now != nil {
		return s.Now()
	}
	return time.Now()
}

func minus(a, b int64) int64 {
	if a < b {
		return 0
	}
	return a - b
}

// runUsage turns a `usage` or `done` event into the run's own usage. ok is
// false when the event changes nothing (`done` of an agent that reports no
// cost: its tokens came with the last `usage`).
func (s *Scheduler) runUsage(ctx context.Context, t *store.Task, ev runnermgr.Event, b *usageBase) (store.RunUsage, bool) {
	var u store.RunUsage
	if ev.Type == runnermgr.EvUsage {
		turns := ev.Turns
		u.Turns = &turns
		u.TokensKnown = true
		u.Tokens = costs.Tokens{Input: ev.InputTokens, Output: ev.OutputTokens, CacheRead: ev.CacheReadTokens, CacheWrite: ev.CacheWriteTokens}
	}
	if costs.ReportsCost(t.Agent) {
		u.Source = costs.SourceReported
		u.CostUSD = ev.CostUSDTotal - b.cost
		if u.CostUSD < 0 {
			u.CostUSD = 0
		}
		u.Tokens = costs.Tokens{
			Input: minus(u.Tokens.Input, b.tokens.Input), Output: minus(u.Tokens.Output, b.tokens.Output),
			CacheRead: minus(u.Tokens.CacheRead, b.tokens.CacheRead), CacheWrite: minus(u.Tokens.CacheWrite, b.tokens.CacheWrite),
		}
		return u, true
	}
	if ev.Type != runnermgr.EvUsage {
		return u, false
	}
	// Codex and Cursor: tokens of this run only, priced from the table.
	if b.model == "" {
		if r, err := s.Store.GetRun(ctx, b.runID); err == nil {
			b.model = deref(r.Model, "")
		}
	}
	settings, err := s.Store.CostSettings(ctx)
	if err != nil {
		slog.Error("cost settings", "err", err)
	}
	u.Source = costs.SourceUnknown
	if p, ok := settings.Prices().Find(t.Agent, b.model); ok {
		u.Source = costs.SourceEstimated
		u.CostUSD = p.Estimate(u.Tokens)
	}
	return u, true
}

// recordUsage stores a run's usage, publishes the task and checks the
// monthly budgets it counts against.
func (s *Scheduler) recordUsage(ctx context.Context, t *store.Task, runID int64, ev runnermgr.Event, b *usageBase) {
	u, ok := s.runUsage(ctx, t, ev, b)
	if !ok {
		return
	}
	nt, err := s.Store.SetRunUsage(ctx, runID, u)
	if err != nil {
		s.log(t, runID).Error("set usage", "err", err)
		return
	}
	if ev.Type == runnermgr.EvUsage {
		s.Hub.PublishTask(nt)
	}
	s.checkBudgetAlerts(ctx, nt)
}

// budgetGate is the global cap and the month ClaimNext checks against.
func (s *Scheduler) budgetGate(ctx context.Context) store.BudgetGate {
	g := store.BudgetGate{MonthStart: costs.MonthStart(s.now())}
	if cs, err := s.Store.CostSettings(ctx); err == nil {
		g.GlobalUSD = cs.GlobalCap()
	} else {
		slog.Error("cost settings", "err", err)
	}
	return g
}

// markBudgetWaits labels queued tasks that a monthly cap holds ("waiting:
// budget", a `budget` event that Telegram turns into a notification with a
// "Start anyway" button) and unlabels the ones it no longer holds.
func (s *Scheduler) markBudgetWaits(ctx context.Context, gate store.BudgetGate) {
	var spent map[string]float64
	var total float64
	spentFor := func(scope string, t *store.Task) (float64, float64) {
		if spent == nil {
			total, spent, _ = s.Store.MonthSpend(ctx, gate.MonthStart)
		}
		if scope == costs.ScopeGlobal {
			return total, gate.GlobalUSD
		}
		if scope == costs.ScopeSchedule && t.ScheduleID != nil {
			if sc, err := s.Store.GetSchedule(ctx, *t.ScheduleID, gate.MonthStart); err == nil {
				return sc.MonthSpendUSD, deref(sc.MonthlyBudgetUSD, 0)
			}
		}
		p, err := s.Store.GetProject(ctx, t.ProjectID)
		if err != nil {
			return spent[t.ProjectID], 0
		}
		return spent[t.ProjectID], p.Budget.Cap()
	}
	changes, err := s.Store.MarkBudgetWaits(ctx, gate, spentFor)
	if err != nil {
		if ctx.Err() == nil {
			slog.Error("budget waits", "err", err)
		}
		return
	}
	for _, c := range changes {
		if c.Event != nil {
			s.log(c.Task, 0).Info("task waits for the monthly budget", "scope", *c.Task.BudgetWait)
			if s.Hooks.BudgetHeld != nil {
				s.Hooks.BudgetHeld(c.Task.ProjectID, *c.Task.BudgetWait)
			}
			s.Hub.PublishEvent(c.Event)
		}
		s.Hub.PublishTask(c.Task)
	}
}

// checkBudgetAlerts sends the 80% and 100% notices of the global and the
// task's project monthly caps, once per scope and month, as `budget` events
// on the task whose run crossed the line. A running run is never stopped
// for a monthly cap; the next runs wait (markBudgetWaits).
func (s *Scheduler) checkBudgetAlerts(ctx context.Context, t *store.Task) {
	cs, err := s.Store.CostSettings(ctx)
	if err != nil {
		return
	}
	p, err := s.Store.GetProject(ctx, t.ProjectID)
	if err != nil {
		return
	}
	month := costs.MonthStart(s.now())
	var sched *store.Schedule
	if t.ScheduleID != nil {
		if sc, err := s.Store.GetSchedule(ctx, *t.ScheduleID, month); err == nil && deref(sc.MonthlyBudgetUSD, 0) > 0 {
			sched = sc
		}
	}
	if cs.GlobalCap() <= 0 && p.Budget.Cap() <= 0 && sched == nil {
		return
	}
	total, by, err := s.Store.MonthSpend(ctx, month)
	if err != nil {
		return
	}
	type scope struct {
		kind, id     string
		spent, limit float64
	}
	scopes := []scope{
		{costs.ScopeGlobal, "", total, cs.GlobalCap()},
		{costs.ScopeProject, t.ProjectID, by[t.ProjectID], p.Budget.Cap()},
	}
	if sched != nil {
		scopes = append(scopes, scope{costs.ScopeSchedule, strconv.FormatInt(sched.ID, 10), sched.MonthSpendUSD, *sched.MonthlyBudgetUSD})
	}
	for _, sc := range scopes {
		level := costs.Level(sc.spent, sc.limit)
		if level == 0 {
			continue
		}
		// Both levels are recorded, so a jump past 100% sends one notice.
		fresh := 0
		for _, l := range []int{costs.LevelWarning, costs.LevelReached} {
			if l > level {
				break
			}
			if ok, err := s.Store.AddBudgetAlert(ctx, sc.kind, sc.id, month, l); err == nil && ok {
				fresh = l
			}
		}
		if fresh == 0 {
			continue
		}
		phase := "warning"
		if fresh == costs.LevelReached {
			phase = "reached"
		}
		payload := map[string]any{"phase": phase, "scope": sc.kind, "level": fresh,
			"spent_usd": round2(sc.spent), "limit_usd": sc.limit}
		if sc.kind == costs.ScopeProject {
			payload["project_id"] = sc.id
		}
		if sc.kind == costs.ScopeSchedule {
			payload["schedule_id"], payload["schedule"] = sched.ID, sched.Name
		}
		s.addEvent(ctx, t.ID, nil, "budget", payload)
		if s.Hooks.BudgetAlert != nil {
			s.Hooks.BudgetAlert(sc.kind, fresh)
		}
	}
}

func round2(v float64) float64 { return float64(int64(v*100+0.5)) / 100 }
