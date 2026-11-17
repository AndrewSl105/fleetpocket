// Package schedules fires recurring tasks (docs/SPEC-server.md
// "Schedules"): it validates schedules, computes their next firing from a
// cron expression in an IANA zone, and on each firing creates an ordinary
// task tagged with the schedule, so questions, CI auto-fix, costs,
// checkpoints and loops work as for any task.
package schedules

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"slices"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/andrewsl105/fleetpocket/server/internal/costs"
	"github.com/andrewsl105/fleetpocket/server/internal/cron"
	"github.com/andrewsl105/fleetpocket/server/internal/events"
	"github.com/andrewsl105/fleetpocket/server/internal/loops"
	"github.com/andrewsl105/fleetpocket/server/internal/store"
	"github.com/andrewsl105/fleetpocket/server/internal/tasks"
)

// TickInterval is how often due schedules are checked.
const TickInterval = 20 * time.Second

// EventSchedule is the backend event on a task a schedule started.
const EventSchedule = "schedule"

// Notifier hears about every firing (the Telegram bot).
type Notifier interface {
	ScheduleFired(ctx context.Context, sc *store.Schedule, f *store.ScheduleFiring)
}

type Service struct {
	Store *store.Store
	Tasks *tasks.Service
	Hub   *events.Hub
	// Notify may be nil.
	Notify Notifier
	// OnFiring counts firings by outcome (metrics); may be nil.
	OnFiring func(outcome string)
	// Now is the clock; nil: time.Now (tests set it).
	Now func() time.Time
}

func (s *Service) now() time.Time {
	if s.Now != nil {
		return s.Now()
	}
	return time.Now()
}

func (s *Service) month() time.Time { return costs.MonthStart(s.now()) }

// Input is a schedule as the API and the web send it.
type Input struct {
	Name             string            `json:"name"`
	ProjectID        string            `json:"project_id"`
	RepositoryID     *int64            `json:"repository_id"`
	Prompt           string            `json:"prompt"`
	Agent            *string           `json:"agent"`
	Skills           []string          `json:"skills"`
	MCPServers       []string          `json:"mcp_servers"`
	Cron             string            `json:"cron"`
	Timezone         string            `json:"timezone"`
	RunBudgetUSD     *float64          `json:"run_budget_usd"`
	MonthlyBudgetUSD *float64          `json:"monthly_budget_usd"`
	Concurrency      string            `json:"concurrency"`
	PROnlyIfChanges  *bool             `json:"pr_only_if_changes"`
	EndsAt           *time.Time        `json:"ends_at"`
	Loop             *store.LoopConfig `json:"loop"`
	Enabled          *bool             `json:"enabled"`
}

func invalid(format string, a ...any) error {
	return fmt.Errorf("%w: %s", tasks.ErrInvalid, fmt.Sprintf(format, a...))
}

func positive(v *float64) *float64 {
	if v == nil || *v <= 0 {
		return nil
	}
	return v
}

// fields validates in and computes the next firing.
func (s *Service) fields(ctx context.Context, in Input) (store.ScheduleFields, error) {
	f := store.ScheduleFields{
		Name: strings.TrimSpace(in.Name), ProjectID: in.ProjectID, RepositoryID: in.RepositoryID, Prompt: strings.TrimSpace(in.Prompt),
		Agent: in.Agent, Skills: in.Skills, MCPServers: in.MCPServers, Cron: strings.TrimSpace(in.Cron), Timezone: in.Timezone,
		RunBudgetUSD: positive(in.RunBudgetUSD), MonthlyBudgetUSD: positive(in.MonthlyBudgetUSD), Concurrency: in.Concurrency,
		PROnlyIfChanges: in.PROnlyIfChanges == nil || *in.PROnlyIfChanges, EndsAt: in.EndsAt, Enabled: in.Enabled == nil || *in.Enabled,
	}
	switch {
	case f.Name == "":
		return f, invalid("name is required")
	case utf8.RuneCountInString(f.Name) > 80:
		return f, invalid("name is longer than 80 characters")
	case f.Prompt == "":
		return f, invalid("prompt is required")
	}
	if f.Concurrency == "" {
		f.Concurrency = store.ConcurrencySkip
	}
	if f.Concurrency != store.ConcurrencySkip && f.Concurrency != store.ConcurrencyQueue {
		return f, invalid("concurrency must be skip or queue")
	}
	if f.Agent != nil && *f.Agent == "" {
		f.Agent = nil
	}
	if f.Agent != nil && !slices.Contains([]string{"claude", "codex", "cursor"}, *f.Agent) {
		return f, invalid("agent must be claude, codex or cursor")
	}
	sched, err := cron.Parse(f.Cron)
	if err != nil {
		return f, invalid("%s", err.Error())
	}
	f.Cron = sched.String()
	if f.Timezone == "" {
		f.Timezone = "UTC"
	}
	loc, err := cron.LoadLocation(f.Timezone)
	if err != nil {
		return f, invalid("%s", err.Error())
	}
	if f.Loop, err = loops.Normalize(in.Loop); err != nil {
		return f, invalid("%s", err.Error())
	}
	p, err := s.Store.GetProject(ctx, f.ProjectID)
	if err != nil {
		if errors.Is(err, store.ErrNotFound) {
			return f, invalid("project %q does not exist", f.ProjectID)
		}
		return f, err
	}
	if f.RepositoryID != nil {
		found := false
		for _, r := range p.Repositories {
			found = found || r.ID == *f.RepositoryID
		}
		if !found {
			return f, invalid("repository %d does not belong to project %q", *f.RepositoryID, p.ID)
		}
	}
	if f.Enabled {
		f.NextRunAt = next(sched, loc, s.now(), f.EndsAt)
	}
	return f, nil
}

// next is the first firing after `after`, nil after the end date.
func next(sched *cron.Schedule, loc *time.Location, after time.Time, ends *time.Time) *time.Time {
	n := sched.Next(after, loc)
	if n.IsZero() || (ends != nil && n.After(*ends)) {
		return nil
	}
	return &n
}

func (s *Service) List(ctx context.Context) ([]*store.Schedule, error) {
	return s.Store.ListSchedules(ctx, s.month())
}

func (s *Service) Get(ctx context.Context, id int64) (*store.Schedule, error) {
	return s.Store.GetSchedule(ctx, id, s.month())
}

func (s *Service) Create(ctx context.Context, in Input) (*store.Schedule, error) {
	f, err := s.fields(ctx, in)
	if err != nil {
		return nil, err
	}
	id, err := s.Store.CreateSchedule(ctx, f)
	if err != nil {
		return nil, err
	}
	return s.Get(ctx, id)
}

func (s *Service) Update(ctx context.Context, id int64, in Input) (*store.Schedule, error) {
	if _, err := s.Get(ctx, id); err != nil {
		return nil, err
	}
	f, err := s.fields(ctx, in)
	if err != nil {
		return nil, err
	}
	if err := s.Store.UpdateSchedule(ctx, id, f); err != nil {
		return nil, err
	}
	return s.Get(ctx, id)
}

func (s *Service) Delete(ctx context.Context, id int64) error { return s.Store.DeleteSchedule(ctx, id) }

// SetEnabled pauses or resumes a schedule. Resuming fires next at the first
// time after now (no catch-up for the paused period).
func (s *Service) SetEnabled(ctx context.Context, id int64, enabled bool) (*store.Schedule, error) {
	sc, err := s.Get(ctx, id)
	if err != nil {
		return nil, err
	}
	var n *time.Time
	if enabled {
		sched, loc, err := parse(sc)
		if err != nil {
			return nil, invalid("%s", err.Error())
		}
		n = next(sched, loc, s.now(), sc.EndsAt)
	}
	if err := s.Store.SetScheduleState(ctx, id, enabled, n); err != nil {
		return nil, err
	}
	return s.Get(ctx, id)
}

// Duplicate copies a schedule as "<name> (copy)", disabled so it does not
// fire before the owner has edited it.
func (s *Service) Duplicate(ctx context.Context, id int64) (*store.Schedule, error) {
	sc, err := s.Get(ctx, id)
	if err != nil {
		return nil, err
	}
	name := sc.Name + " (copy)"
	if utf8.RuneCountInString(name) > 80 {
		name = string([]rune(name)[:80])
	}
	off := false
	return s.Create(ctx, Input{Name: name, ProjectID: sc.ProjectID, RepositoryID: sc.RepositoryID, Prompt: sc.Prompt, Agent: sc.Agent,
		Skills: sc.Skills, MCPServers: sc.MCPServers, Cron: sc.Cron, Timezone: sc.Timezone, RunBudgetUSD: sc.RunBudgetUSD,
		MonthlyBudgetUSD: sc.MonthlyBudgetUSD, Concurrency: sc.Concurrency, PROnlyIfChanges: &sc.PROnlyIfChanges, EndsAt: sc.EndsAt,
		Loop: sc.Loop, Enabled: &off})
}

func (s *Service) History(ctx context.Context, id int64, limit int) ([]*store.ScheduleFiring, error) {
	if _, err := s.Get(ctx, id); err != nil {
		return nil, err
	}
	return s.Store.ScheduleFirings(ctx, id, limit)
}

// RunNow fires a schedule now (also a paused one), with its concurrency
// policy; via names who asked (web, telegram).
func (s *Service) RunNow(ctx context.Context, id int64, via string) (*store.ScheduleFiring, error) {
	sc, err := s.Get(ctx, id)
	if err != nil {
		return nil, err
	}
	return s.fire(ctx, sc, s.now(), store.TriggerManual, via)
}

func parse(sc *store.Schedule) (*cron.Schedule, *time.Location, error) {
	sched, err := cron.Parse(sc.Cron)
	if err != nil {
		return nil, nil, err
	}
	loc, err := cron.LoadLocation(sc.Timezone)
	return sched, loc, err
}

// Run checks for due schedules and waiting firings until ctx ends.
func (s *Service) Run(ctx context.Context) {
	t := time.NewTicker(TickInterval)
	defer t.Stop()
	for {
		s.Tick(ctx)
		select {
		case <-ctx.Done():
			return
		case <-t.C:
		}
	}
}

// Tick starts queued firings whose previous task finished, then fires due
// schedules. A firing more than one interval late (the server was down) is
// recorded as missed; at most one late firing runs per schedule.
func (s *Service) Tick(ctx context.Context) {
	s.startQueued(ctx)
	now := s.now()
	due, err := s.Store.DueSchedules(ctx, now, s.month())
	if err != nil {
		if ctx.Err() == nil {
			slog.Error("schedules: due", "err", err)
		}
		return
	}
	for _, sc := range due {
		at := *sc.NextRunAt
		sched, loc, err := parse(sc)
		if err != nil {
			slog.Error("schedules: invalid schedule", "schedule_id", sc.ID, "err", err)
			_, _ = s.Store.AdvanceSchedule(ctx, sc.ID, at, nil)
			continue
		}
		fire, n := sched.Due(at, now, loc)
		nextAt := &n
		if n.IsZero() || (sc.EndsAt != nil && n.After(*sc.EndsAt)) {
			nextAt = nil
		}
		if ok, err := s.Store.AdvanceSchedule(ctx, sc.ID, at, nextAt); err != nil || !ok {
			continue // another tick took it
		}
		if !fire {
			reason := "missed: the server was not running for more than one interval"
			s.record(ctx, sc, store.ScheduleFiring{ScheduleID: sc.ID, ScheduledFor: at, Trigger: store.TriggerCron, Outcome: store.FiringMissed, Reason: &reason})
			continue
		}
		if _, err := s.fire(ctx, sc, at, store.TriggerCron, ""); err != nil {
			slog.Error("schedules: fire", "schedule_id", sc.ID, "err", err)
		}
	}
}

// fire applies the concurrency policy and starts a task (or queues or
// skips the firing).
func (s *Service) fire(ctx context.Context, sc *store.Schedule, at time.Time, trigger, via string) (*store.ScheduleFiring, error) {
	busy, err := s.Store.ScheduleBusy(ctx, sc.ID)
	if err != nil {
		return nil, err
	}
	f := store.ScheduleFiring{ScheduleID: sc.ID, ScheduledFor: at, Trigger: trigger}
	if busy {
		reason := "the previous task is still running"
		f.Outcome, f.Reason = store.FiringSkipped, &reason
		if sc.Concurrency == store.ConcurrencyQueue {
			waiting, err := s.Store.HasQueuedFiring(ctx, sc.ID)
			if err != nil {
				return nil, err
			}
			if waiting {
				reason = "a firing already waits for the previous task"
			} else {
				reason = "waits for the previous task"
				f.Outcome = store.FiringQueued
			}
		}
		return s.record(ctx, sc, f), nil
	}
	task, err := s.startTask(ctx, sc, trigger, at, via)
	if err != nil {
		reason := err.Error()
		f.Outcome, f.Reason = store.FiringFailed, &reason
		return s.record(ctx, sc, f), nil
	}
	f.Outcome, f.TaskID = store.FiringStarted, &task.ID
	return s.record(ctx, sc, f), nil
}

func (s *Service) record(ctx context.Context, sc *store.Schedule, f store.ScheduleFiring) *store.ScheduleFiring {
	out, err := s.Store.AddFiring(ctx, f)
	if err != nil {
		slog.Error("schedules: record firing", "schedule_id", sc.ID, "err", err)
		out = &f
	}
	s.firing(ctx, sc, out)
	return out
}

func (s *Service) firing(ctx context.Context, sc *store.Schedule, f *store.ScheduleFiring) {
	slog.Info("schedule fired", "schedule_id", sc.ID, "trigger", f.Trigger, "outcome", f.Outcome, "task_id", f.TaskID)
	if s.OnFiring != nil {
		s.OnFiring(f.Outcome)
	}
	if s.Notify != nil {
		s.Notify.ScheduleFired(ctx, sc, f)
	}
}

// startQueued starts each waiting firing whose schedule has no running task.
func (s *Service) startQueued(ctx context.Context) {
	queued, err := s.Store.QueuedFirings(ctx)
	if err != nil {
		if ctx.Err() == nil {
			slog.Error("schedules: queued firings", "err", err)
		}
		return
	}
	for _, f := range queued {
		busy, err := s.Store.ScheduleBusy(ctx, f.ScheduleID)
		if err != nil || busy {
			continue
		}
		sc, err := s.Get(ctx, f.ScheduleID)
		if err != nil {
			continue
		}
		// A timed firing that waited while the owner paused the schedule
		// does not start; a Run now the owner asked for still does.
		if !sc.Enabled && f.Trigger == store.TriggerCron {
			reason := "the schedule was paused while this firing waited"
			_ = s.Store.FiringSkipped(ctx, f.ID, reason)
			f.Outcome, f.Reason = store.FiringSkipped, &reason
			s.firing(ctx, sc, f)
			continue
		}
		task, err := s.startTask(ctx, sc, f.Trigger, f.ScheduledFor, "")
		if err != nil {
			_ = s.Store.FiringFailed(ctx, f.ID, err.Error())
			reason := err.Error()
			f.Outcome, f.Reason = store.FiringFailed, &reason
		} else {
			_ = s.Store.FiringStartedTask(ctx, f.ID, task.ID)
			f.Outcome, f.TaskID = store.FiringStarted, &task.ID
		}
		s.firing(ctx, sc, f)
	}
}

// startTask creates the firing's task and records who started it.
func (s *Service) startTask(ctx context.Context, sc *store.Schedule, trigger string, at time.Time, via string) (*tasks.View, error) {
	in := tasks.CreateInput{ProjectID: sc.ProjectID, Title: sc.Name, Prompt: sc.Prompt, SkillsAdd: sc.Skills, Source: "schedule",
		ScheduleID: &sc.ID, MCPServers: sc.MCPServers, RunBudgetUSD: sc.RunBudgetUSD, PRAlways: !sc.PROnlyIfChanges, Loop: sc.Loop}
	if sc.RepositoryID != nil {
		in.RepositoryID = *sc.RepositoryID
	}
	if sc.Agent != nil {
		in.Agent = *sc.Agent
	}
	v, err := s.Tasks.Create(ctx, in)
	if err != nil {
		return nil, err
	}
	payload := map[string]any{"schedule_id": sc.ID, "name": sc.Name, "trigger": trigger, "scheduled_for": at.UTC().Format(time.RFC3339)}
	if via != "" {
		payload["via"] = via
	}
	if ev, err := s.Store.AddBackendEvent(ctx, v.ID, nil, EventSchedule, payload); err == nil && s.Hub != nil {
		s.Hub.PublishEvent(ev)
	}
	return v, nil
}
