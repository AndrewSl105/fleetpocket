package telegram

import (
	"context"
	"encoding/json"
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/mymmrac/telego"
	tu "github.com/mymmrac/telego/telegoutil"

	"github.com/andrewsl105/fleetpocket/server/internal/store"
)

// Schedules is what /schedules needs (internal/schedules.Service).
type Schedules interface {
	List(ctx context.Context) ([]*store.Schedule, error)
	RunNow(ctx context.Context, id int64, via string) (*store.ScheduleFiring, error)
	SetEnabled(ctx context.Context, id int64, enabled bool) (*store.Schedule, error)
}

// listSchedules answers /schedules: one line per schedule with its next
// run, and Run now / Pause (or Resume) buttons.
func (b *Bot) listSchedules(ctx context.Context, m *telego.Message) {
	if b.Schedules == nil {
		_, _ = b.reply(ctx, m, "Schedules are not available.", nil)
		return
	}
	list, err := b.Schedules.List(ctx)
	if err != nil {
		_, _ = b.reply(ctx, m, "❌ "+esc(errText(err)), nil)
		return
	}
	if len(list) == 0 {
		_, _ = b.reply(ctx, m, "No schedules yet. Create one in the app (Schedules).", nil)
		return
	}
	var sb strings.Builder
	sb.WriteString("<b>Schedules</b>\n")
	var rows [][]telego.InlineKeyboardButton
	for i, sc := range list {
		if i == 20 {
			fmt.Fprintf(&sb, "\n… and %d more in the app.", len(list)-i)
			break
		}
		sb.WriteString("\n" + scheduleLine(sc))
		id := strconv.FormatInt(sc.ID, 10)
		toggle := tu.InlineKeyboardButton("⏸ Pause " + cut(sc.Name, 20)).WithCallbackData(b.callback(ctx, store.TelegramCallback{Action: "schedule_pause", Value: id}))
		if !sc.Enabled {
			toggle = tu.InlineKeyboardButton("▶️ Resume " + cut(sc.Name, 20)).WithCallbackData(b.callback(ctx, store.TelegramCallback{Action: "schedule_resume", Value: id}))
		}
		rows = append(rows, tu.InlineKeyboardRow(
			tu.InlineKeyboardButton("🚀 Run "+cut(sc.Name, 20)).WithCallbackData(b.callback(ctx, store.TelegramCallback{Action: "schedule_run", Value: id})),
			toggle,
		))
	}
	_, _ = b.reply(ctx, m, sb.String(), tu.InlineKeyboard(rows...))
}

// scheduleLine is "<b>name</b> · project · next run (in the schedule's zone) · last result".
func scheduleLine(sc *store.Schedule) string {
	next := "paused"
	if sc.Enabled {
		next = "no next run"
		if sc.NextRunAt != nil {
			at := *sc.NextRunAt
			if loc, err := time.LoadLocation(sc.Timezone); err == nil {
				at = at.In(loc)
			}
			next = "next " + at.Format("Mon Jan 2 15:04") + " " + esc(sc.Timezone)
		}
	}
	s := fmt.Sprintf("<b>%s</b> · %s · %s", esc(cut(sc.Name, 60)), esc(sc.ProjectID), next)
	if f := sc.LastFiring; f != nil {
		s += " · last: " + firingResult(f)
	}
	return s
}

func firingResult(f *store.ScheduleFiring) string {
	if f.TaskID != nil {
		st := "started"
		if f.TaskStatus != nil {
			st = strings.ReplaceAll(*f.TaskStatus, "_", " ")
		}
		return fmt.Sprintf("#%d %s", *f.TaskID, st)
	}
	return f.Outcome
}

func (b *Bot) scheduleCallback(ctx context.Context, c store.TelegramCallback, answer func(string)) {
	id, err := strconv.ParseInt(c.Value, 10, 64)
	if err != nil || b.Schedules == nil {
		answer("Schedule not found")
		return
	}
	switch c.Action {
	case "schedule_run":
		f, err := b.Schedules.RunNow(ctx, id, "telegram")
		if err != nil {
			answer(errText(err))
			return
		}
		switch {
		case f.TaskID != nil:
			answer(fmt.Sprintf("Started task #%d", *f.TaskID))
		case f.Reason != nil:
			answer(cut(f.Outcome+": "+*f.Reason, 190))
		default:
			answer(f.Outcome)
		}
	case "schedule_pause", "schedule_resume":
		sc, err := b.Schedules.SetEnabled(ctx, id, c.Action == "schedule_resume")
		if err != nil {
			answer(errText(err))
			return
		}
		if sc.Enabled {
			answer("Resumed " + cut(sc.Name, 100))
		} else {
			answer("Paused " + cut(sc.Name, 100))
		}
	}
}

// ScheduleFired notifies every firing (schedules.Notifier): a started task
// as a task message with its buttons; a skipped, queued, missed or failed
// firing as a plain notice to the owner.
func (b *Bot) ScheduleFired(ctx context.Context, sc *store.Schedule, f *store.ScheduleFiring) {
	how := "⏰ Schedule"
	if f.Trigger == store.TriggerManual {
		how = "▶️ Run now"
	}
	name := esc(cut(sc.Name, 80))
	if f.TaskID != nil {
		t, err := b.Store.GetTask(ctx, *f.TaskID)
		if err != nil {
			return
		}
		text := fmt.Sprintf("%s <b>%s</b> started #%d\n%s", how, name, t.ID, esc(cut(t.Prompt, 200)))
		_, _ = b.sendTask(ctx, t, "schedule", text, b.taskButtons(ctx, t, b.targetPrivate(ctx, t)), false)
		return
	}
	reason := ""
	if f.Reason != nil {
		reason = "\n" + esc(cut(*f.Reason, 200))
	}
	icon := map[string]string{store.FiringSkipped: "⏭", store.FiringQueued: "⏳", store.FiringMissed: "💤", store.FiringFailed: "❌"}[f.Outcome]
	text := fmt.Sprintf("%s <b>%s</b> · %s%s", icon, name, f.Outcome, reason)
	to := target{b.OwnerID, 0}
	if chat, thread, err := b.Store.ProjectTelegram(ctx, sc.ProjectID); err == nil && chat != 0 {
		to = target{chat, thread}
	}
	_, _ = b.send(ctx, to, text, nil, 0)
}

// loopPayload is a `loop` event (docs/API.md, event types).
type loopPayload struct {
	Phase         string `json:"phase"`
	Iteration     int    `json:"iteration"`
	MaxIterations int    `json:"max_iterations"`
	State         string `json:"state"`
	Reason        string `json:"reason"`
	ExitCode      *int   `json:"exit_code"`
}

// renderLoop is the notification of an automatic loop step: the next
// iteration after a failed check, and the end of the loop. "" for the
// first iteration (the task itself was just created).
func renderLoop(t *store.Task, raw json.RawMessage) string {
	var p loopPayload
	if json.Unmarshal(raw, &p) != nil {
		return ""
	}
	head := func(icon, text string) string { return fmt.Sprintf("%s <b>#%d · %s</b>", icon, t.ID, text) }
	switch {
	case p.Phase == "iteration" && p.Iteration > 1:
		return head("🔁", fmt.Sprintf("Loop iteration %d/%d", p.Iteration, p.MaxIterations)) + "\n" + esc(cut(p.Reason, 200))
	case p.Phase == "stopped" && p.State == store.LoopPassed:
		return head("✅", fmt.Sprintf("Check passed (%d/%d)", p.Iteration, p.MaxIterations))
	case p.Phase == "stopped":
		return head("🛑", "Loop stopped") + "\n" + esc(cut(p.Reason, 300))
	}
	return ""
}
