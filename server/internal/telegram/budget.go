package telegram

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/mymmrac/telego"
	tu "github.com/mymmrac/telego/telegoutil"

	"github.com/andrewsl105/fleetpocket/server/internal/store"
)

// budgetPayload is a `budget` event (docs/API.md, event types).
type budgetPayload struct {
	Phase     string  `json:"phase"`
	Scope     string  `json:"scope"`
	ProjectID string  `json:"project_id"`
	SpentUSD  float64 `json:"spent_usd"`
	LimitUSD  float64 `json:"limit_usd"`
	Via       string  `json:"via"`
}

func (p budgetPayload) scopeName() string {
	if p.Scope == "project" {
		return "Project " + esc(cut(p.ProjectID, 60)) + "'s monthly budget"
	}
	return "The monthly budget"
}

// renderBudget is the notification for a `budget` event; "" for phases that
// send none. showButton tells whether "Start anyway" goes with it.
func renderBudget(t *store.Task, raw json.RawMessage) (text string, showButton bool) {
	var p budgetPayload
	if json.Unmarshal(raw, &p) != nil {
		return "", false
	}
	amounts := fmt.Sprintf("$%.2f of $%.2f", p.SpentUSD, p.LimitUSD)
	switch p.Phase {
	case "warning":
		return fmt.Sprintf("💸 <b>%s is at 80%%</b>\n%s spent this month (task #%d).", p.scopeName(), amounts, t.ID), false
	case "reached":
		return fmt.Sprintf("💸 <b>%s is used up</b>\n%s spent this month. Running tasks finish; new runs wait until next month, a higher cap or your OK.",
			p.scopeName(), amounts), false
	case "waiting":
		return fmt.Sprintf("⏸ <b>#%d · waiting: budget</b>\n%s\n%s is used up (%s).", t.ID, esc(cut(t.Title, 120)), p.scopeName(), amounts), true
	}
	return "", false
}

func (b *Bot) sendBudget(ctx context.Context, ev *store.Event) {
	t, err := b.Store.GetTask(ctx, ev.TaskID)
	if err != nil {
		return
	}
	text, button := renderBudget(t, ev.Payload)
	if text == "" {
		return
	}
	kb := b.taskButtons(ctx, t, b.targetPrivate(ctx, t))
	if button {
		row := tu.InlineKeyboardRow(tu.InlineKeyboardButton("▶️ Start anyway").WithCallbackData(b.callback(ctx, store.TelegramCallback{Action: "budget_override", TaskID: t.ID})))
		if kb == nil {
			kb = tu.InlineKeyboard(row)
		} else {
			kb.InlineKeyboard = append([][]telego.InlineKeyboardButton{row}, kb.InlineKeyboard...)
		}
	}
	_, _ = b.sendTask(ctx, t, "budget", text, kb, true)
}

// overrideBudget lets a task that waits for the budget start anyway.
func (b *Bot) overrideBudget(ctx context.Context, c store.TelegramCallback, answer func(string)) {
	if _, err := b.Tasks.OverrideBudget(ctx, c.TaskID, "telegram"); err != nil {
		answer(errText(err))
		return
	}
	answer("Starting despite the budget")
}
