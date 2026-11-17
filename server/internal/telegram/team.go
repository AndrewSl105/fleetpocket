package telegram

import (
	"context"
	"fmt"
	"log/slog"
	"strconv"
	"strings"
	"time"

	"github.com/mymmrac/telego"

	"github.com/andrewsl105/fleetpocket/server/internal/store"
	"github.com/andrewsl105/fleetpocket/server/internal/team"
)

// Team mode (docs/SPEC-server.md "Telegram per user"): the bot accepts
// messages from linked users only, every command acts as that user with
// their role, and notices go to the task creator's chat. With team mode off
// the single OwnerID is the only user, as before.

type tgUserKey struct{}

func withTGUser(ctx context.Context, u *store.User) context.Context {
	return context.WithValue(ctx, tgUserKey{}, u)
}

// tgUser is the linked user of the update being handled (nil: team off).
func tgUser(ctx context.Context) *store.User {
	u, _ := ctx.Value(tgUserKey{}).(*store.User)
	return u
}

// actorID is the user tasks and ideas created from this update belong to.
func actorID(ctx context.Context) *int64 {
	if u := tgUser(ctx); u != nil {
		id := u.ID
		return &id
	}
	return nil
}

const notLinked = "This bot belongs to a team. Ask your admin for an invite, then link this account in Settings → Telegram (/link 123456)."

func (b *Bot) teamOn(ctx context.Context) bool { return b.Team != nil && b.Team.Enabled(ctx) }

// admit resolves who sent an update. It handles /link itself and refuses
// unlinked accounts; ok=false means the update is done.
func (b *Bot) admit(ctx context.Context, from *telego.User, m *telego.Message, cq *telego.CallbackQuery) (context.Context, bool) {
	if !b.teamOn(ctx) {
		if from == nil || from.ID != b.OwnerID {
			slog.Info("telegram: ignored update from non-owner", "from", userID(from))
			return ctx, false
		}
		return ctx, true
	}
	if from == nil {
		return ctx, false
	}
	if m != nil && m.Chat.Type == "private" {
		if code, ok := strings.CutPrefix(strings.TrimSpace(m.Text), "/link"); ok {
			u, err := b.Team.Link(ctx, strings.TrimSpace(code), from.ID)
			if err != nil {
				_, _ = b.reply(ctx, m, "❌ That code is invalid or expired. Get a new one in Settings → Telegram.", nil)
			} else {
				_, _ = b.reply(ctx, m, fmt.Sprintf("✅ Linked to <b>%s</b> (%s).", esc(u.Name), esc(u.Role)), nil)
			}
			return ctx, false
		}
	}
	u, err := b.Team.TelegramUser(ctx, from.ID)
	if err != nil {
		switch {
		case m != nil && m.Chat.Type == "private":
			_, _ = b.reply(ctx, m, notLinked, nil)
		case cq != nil:
			b.fail("answer_callback", b.API.AnswerCallbackQuery(ctx, &telego.AnswerCallbackQueryParams{CallbackQueryID: cq.ID, Text: "Ask your admin for an invite"}))
		}
		return ctx, false
	}
	return withTGUser(ctx, u), true
}

// readOnlyCommands are what a viewer may send.
var readOnlyCommands = map[string]bool{"start": true, "help": true, "tasks": true, "task": true, "projects": true, "schedules": true, "app": true}

// mayMessage reports whether the sender may act with this message; it
// replies with the reason when not.
func (b *Bot) mayMessage(ctx context.Context, m *telego.Message) bool {
	u := tgUser(ctx)
	if u == nil || team.CanWrite(u.Role) {
		return true
	}
	cmd := ""
	if strings.HasPrefix(m.Text, "/") {
		cmd = strings.TrimPrefix(strings.Fields(m.Text)[0], "/")
		cmd, _, _ = strings.Cut(cmd, "@")
	}
	if readOnlyCommands[cmd] {
		return true
	}
	_, _ = b.reply(ctx, m, "👀 You are a viewer: you can read tasks and get notices, not create or change them.", nil)
	return false
}

// mayTask reports whether the sender may act on a task (own, or admin).
func (b *Bot) mayTask(ctx context.Context, taskID int64) error {
	u := tgUser(ctx)
	if u == nil {
		return nil
	}
	t, err := b.Store.GetTask(ctx, taskID)
	if err != nil {
		return err
	}
	if !team.CanActOn(u.Role, u.ID, t.CreatedByID) {
		return fmt.Errorf("%w: only its creator or an admin can do this", store.ErrConflict)
	}
	if u.Role == store.RoleAdmin && (t.CreatedByID == nil || *t.CreatedByID != u.ID) {
		b.Team.Audit(ctx, &u.ID, "task.admin_action", "task", fmt.Sprint(taskID), map[string]any{"via": "telegram"})
	}
	return nil
}

// mayCallback gates a button press by the sender's role.
func (b *Bot) mayCallback(ctx context.Context, c store.TelegramCallback) string {
	u := tgUser(ctx)
	if u == nil {
		return ""
	}
	if !team.CanWrite(u.Role) {
		return "Viewers cannot change anything"
	}
	switch c.Action {
	case "egress_allow", "budget_override":
		if u.Role != store.RoleAdmin {
			return "Admins only"
		}
	case "schedule_run", "schedule_pause", "schedule_resume":
		if u.Role != store.RoleAdmin {
			id, _ := strconv.ParseInt(c.Value, 10, 64)
			if by, err := b.Store.AuthorOf(ctx, "schedules", id); err != nil || by == nil || *by != u.ID {
				return "Only its creator or an admin can do this"
			}
		}
	}
	if c.TaskID != 0 {
		if err := b.mayTask(ctx, c.TaskID); err != nil {
			return "Only its creator or an admin can do this"
		}
	}
	return ""
}

// creatorChat is the private chat of the task creator's linked Telegram
// account (team mode), or OwnerID.
func (b *Bot) creatorChat(ctx context.Context, by *int64) int64 {
	if by != nil && b.teamOn(ctx) {
		if u, err := b.Store.GetUser(ctx, *by); err == nil && u.TelegramUserID != nil && u.DisabledAt == nil {
			return *u.TelegramUserID
		}
		if b.teamOn(ctx) {
			return 0 // not linked: nothing to send
		}
	}
	return b.OwnerID
}

// private reports whether chatID is a person's private chat with the bot.
func (b *Bot) private(ctx context.Context, chatID int64) bool {
	if chatID == b.OwnerID {
		return true
	}
	if !b.teamOn(ctx) || chatID <= 0 {
		return false
	}
	_, err := b.Store.UserByTelegram(ctx, chatID)
	return err == nil
}

// adminChats are the linked admins' chats except skip's.
func (b *Bot) adminChats(ctx context.Context, skip int64) []int64 {
	if !b.teamOn(ctx) {
		return nil
	}
	users, err := b.Store.ListUsers(ctx)
	if err != nil {
		return nil
	}
	var out []int64
	for _, u := range users {
		if u.Role == store.RoleAdmin && u.Active() && u.TelegramUserID != nil && *u.TelegramUserID != skip {
			out = append(out, *u.TelegramUserID)
		}
	}
	return out
}

// escalate sends questions unanswered for QuestionTimeout to the admins
// too (team mode), once per question.
func (b *Bot) escalate(ctx context.Context) {
	if !b.teamOn(ctx) || b.QuestionTimeout <= 0 {
		return
	}
	qs, err := b.Store.QuestionsToEscalate(ctx, time.Now().Add(-b.QuestionTimeout))
	if err != nil {
		return
	}
	for _, q := range qs {
		t, err := b.Store.GetTask(ctx, q.TaskID)
		if err != nil {
			continue
		}
		if err := b.Store.MarkEscalated(ctx, q.ID); err != nil {
			continue
		}
		who := "its creator"
		if t.CreatedBy != nil {
			who = t.CreatedBy.Name
		}
		text := fmt.Sprintf("⏰ <b>Unanswered for %s</b> (asked %s)\n%s", b.QuestionTimeout.Round(time.Minute), esc(who), renderQuestion(t, q))
		kb := b.questionKeyboard(ctx, q)
		for _, chat := range b.adminChats(ctx, b.creatorChat(ctx, t.CreatedByID)) {
			_, _ = b.sendMode(ctx, target{chat, 0}, text, kb, 0, true)
		}
	}
}

// escalateLoop runs escalate every minute.
func (b *Bot) escalateLoop(ctx context.Context) {
	t := time.NewTicker(time.Minute)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
			b.escalate(ctx)
		}
	}
}

// alsoToAdmins sends a person-budget notice to the admins as well.
func (b *Bot) alsoToAdmins(ctx context.Context, t *store.Task, text string) {
	for _, chat := range b.adminChats(ctx, b.creatorChat(ctx, t.CreatedByID)) {
		_, _ = b.sendMode(ctx, target{chat, 0}, text, nil, 0, true)
	}
}
