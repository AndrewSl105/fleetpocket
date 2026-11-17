package telegram

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/mymmrac/telego"
	tu "github.com/mymmrac/telego/telegoutil"

	"github.com/andrewsl105/fleetpocket/server/internal/egress"
	"github.com/andrewsl105/fleetpocket/server/internal/store"
)

// renderEgressBlocked is the notification for an `egress_blocked` event.
func renderEgressBlocked(t *store.Task, raw json.RawMessage) (string, string) {
	var p struct {
		Host string `json:"host"`
	}
	if json.Unmarshal(raw, &p) != nil || p.Host == "" {
		return "", ""
	}
	return fmt.Sprintf("🚧 <b>#%d · blocked: %s</b>\nNot on project %s's egress allowlist. Allowing it takes effect on the next run.",
		t.ID, esc(cut(p.Host, 120)), esc(t.ProjectID)), p.Host
}

func (b *Bot) sendEgressBlocked(ctx context.Context, ev *store.Event) {
	t, err := b.Store.GetTask(ctx, ev.TaskID)
	if err != nil {
		return
	}
	text, host := renderEgressBlocked(t, ev.Payload)
	if text == "" {
		return
	}
	kb := tu.InlineKeyboard(tu.InlineKeyboardRow(
		tu.InlineKeyboardButton("✅ Allow for this project").WithCallbackData(b.callback(ctx, store.TelegramCallback{Action: "egress_allow", TaskID: t.ID, Value: host})),
	))
	_, _ = b.sendTask(ctx, t, "egress_blocked", text, kb, true)
}

// allowEgress adds the host to the task's project allowlist.
func (b *Bot) allowEgress(ctx context.Context, cq *telego.CallbackQuery, c store.TelegramCallback, answer func(string)) {
	t, err := b.Store.GetTask(ctx, c.TaskID)
	if err != nil {
		answer("Task not found")
		return
	}
	host, err := egress.NormalizeHost(c.Value)
	if err != nil {
		answer(errText(err))
		return
	}
	if _, err := b.Store.AddEgressHost(ctx, t.ProjectID, host); err != nil {
		answer(errText(err))
		return
	}
	answer("Allowed " + cut(host, 100) + " for " + cut(t.ProjectID, 50) + " (next run)")
	if m := cq.Message; m != nil {
		_, _ = b.API.EditMessageReplyMarkup(ctx, &telego.EditMessageReplyMarkupParams{
			ChatID: tu.ID(m.GetChat().ID), MessageID: m.GetMessageID(), ReplyMarkup: &telego.InlineKeyboardMarkup{InlineKeyboard: [][]telego.InlineKeyboardButton{}},
		})
	}
}
