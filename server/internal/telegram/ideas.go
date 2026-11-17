package telegram

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/mymmrac/telego"
	tu "github.com/mymmrac/telego/telegoutil"

	"github.com/andrewsl105/fleetpocket/server/internal/ideas"
	"github.com/andrewsl105/fleetpocket/server/internal/store"
)

const ideasOff = "The Ideas board is off. Turn it on in the app under Settings → Ideas board."

// ideasSettings returns the board settings, or "" plus a sentence for the
// owner when the board cannot take ideas.
func (b *Bot) ideasSettings(ctx context.Context) (store.IdeaSettings, string) {
	if b.Ideas == nil {
		return store.IdeaSettings{}, "The Ideas board is not available."
	}
	cfg, err := b.Ideas.Settings(ctx)
	if err != nil {
		return cfg, "❌ " + esc(errText(err))
	}
	if !cfg.Enabled {
		return cfg, ideasOff
	}
	return cfg, ""
}

// newIdea answers /idea: the command's text, or (as a caption) its message's
// voice note and media, or (as a reply) the message it answers. A bare
// /idea asks for the idea with a ForceReply prompt.
func (b *Bot) newIdea(ctx context.Context, m *telego.Message, args string) error {
	cfg, off := b.ideasSettings(ctx)
	if off != "" {
		_, err := b.reply(ctx, m, off, nil)
		return err
	}
	src, text := m, args
	media := append(messageMedia(m), albumMedia(ctx)...)
	if r := m.ReplyToMessage; r != nil && m.Voice == nil && m.Audio == nil && len(media) == 0 {
		// /idea in reply to a message saves that message (plus any words after /idea).
		src, media = r, messageMedia(r)
		quoted := strings.TrimSpace(r.Text)
		if quoted == "" {
			quoted = strings.TrimSpace(r.Caption)
		}
		text = strings.TrimSpace(strings.Join([]string{args, quoted}, "\n\n"))
	}
	if text == "" && len(media) == 0 && src.Voice == nil && src.Audio == nil {
		id, err := b.send(ctx, target{m.Chat.ID, int64(m.MessageThreadID)}, "💡 Send the idea: text, a voice message, photos or files.",
			&telego.ForceReply{ForceReply: true, InputFieldPlaceholder: "Your idea"}, m.MessageID)
		if err != nil {
			return err
		}
		return b.Store.AddTelegramReplyPrompt(ctx, m.Chat.ID, int64(id), store.TelegramReplyPrompt{Kind: "idea"})
	}
	return b.saveIdea(ctx, cfg, m, src, text, media)
}

// saveIdea creates an Inbox card from text, src's voice note (transcribed
// when STT is on, kept either way) and media; reply goes to m.
func (b *Bot) saveIdea(ctx context.Context, cfg store.IdeaSettings, m, src *telego.Message, text string, media []store.TelegramDraftMedia) error {
	images, err := b.downloadDraftImages(ctx, media)
	if err != nil {
		return err
	}
	in := ideas.Input{Body: text, Source: "telegram"}
	if p := b.topicProject(ctx, m); p != nil {
		in.ProjectID = &p.ID
	}
	var idea *store.Idea
	if src.Voice != nil || src.Audio != nil {
		v := ideas.Voice{Mime: "audio/ogg", Name: "voice.ogg"}
		fileID := ""
		if src.Voice != nil {
			fileID, v.DurationMS = src.Voice.FileID, src.Voice.Duration*1000
			if src.Voice.MimeType != "" {
				v.Mime = src.Voice.MimeType
			}
		} else {
			fileID, v.DurationMS, v.Name = src.Audio.FileID, src.Audio.Duration*1000, src.Audio.FileName
			if src.Audio.MimeType != "" {
				v.Mime = src.Audio.MimeType
			}
		}
		if v.Data, err = b.download(ctx, fileID); err != nil {
			return err
		}
		idea, err = b.Ideas.CreateVoiceWith(ctx, cfg, in, v, images)
	} else {
		idea, err = b.Ideas.CreateWith(ctx, cfg, in, images)
	}
	if err != nil {
		return err
	}
	_, err = b.reply(ctx, m, ideaSaved(cfg, idea), b.boardKeyboard(m))
	return err
}

func ideaSaved(cfg store.IdeaSettings, idea *store.Idea) string {
	s := fmt.Sprintf("💡 Saved to <b>%s</b>: %s", esc(cfg.ColumnName(idea.Column)), esc(cut(idea.Title, 120)))
	if len(idea.VoiceNotes) > 0 && idea.VoiceNotes[0].Transcript == nil {
		s += "\n<i>Voice note kept without a transcript (speech-to-text is off or failed).</i>"
	}
	return s
}

// boardKeyboard is an "Open board" button, when the app URL is known.
func (b *Bot) boardKeyboard(m *telego.Message) *telego.InlineKeyboardMarkup {
	btn, ok := b.appButton("💡 Open board", "/ideas", m.Chat.Type == "private")
	if !ok {
		return nil
	}
	return tu.InlineKeyboard(tu.InlineKeyboardRow(btn))
}

// ideaFromDraft saves a task draft (its text, a voice message's transcript,
// its media) as an Inbox card instead of a task.
func (b *Bot) ideaFromDraft(ctx context.Context, d *store.TelegramDraft, answer func(string)) {
	cfg, off := b.ideasSettings(ctx)
	if off != "" {
		answer(cut(off, 190))
		return
	}
	if strings.TrimSpace(d.Text) == "" && len(d.Media) == 0 {
		answer("Nothing to save yet")
		return
	}
	images, err := b.downloadDraftImages(ctx, d.Media)
	if err != nil {
		answer(cut(errText(err), 190))
		return
	}
	in := ideas.Input{Body: d.Text, Source: "telegram"}
	if d.ProjectID != "" {
		in.ProjectID = &d.ProjectID
	}
	idea, err := b.Ideas.CreateWith(ctx, cfg, in, images)
	if err != nil {
		answer(cut(errText(err), 190))
		return
	}
	if d.CardMessageID != 0 {
		b.edit(ctx, d.ChatID, int(d.CardMessageID), ideaSaved(cfg, idea), nil)
	}
	_ = b.Store.DeleteTelegramDraft(ctx, d.ID)
	answer("Saved as an idea")
}

// ideaButtonShown: the draft card offers "💡 Idea" only while the board is on.
func (b *Bot) ideaButtonShown(ctx context.Context) bool {
	return b.Ideas != nil && b.Ideas.Enabled(ctx)
}

// renderIdeaEvent is the silent notice for an `idea` event ("" for none).
func renderIdeaEvent(t *store.Task, raw json.RawMessage) string {
	var p struct {
		Title      string `json:"title"`
		Action     string `json:"action"`
		ColumnName string `json:"column_name"`
		Moved      bool   `json:"moved"`
		Reason     string `json:"reason"`
	}
	if json.Unmarshal(raw, &p) != nil {
		return ""
	}
	title := esc(cut(p.Title, 100))
	switch p.Action {
	case "started":
		return fmt.Sprintf("💡 <b>#%d</b> started from the idea “%s” → %s", t.ID, title, esc(p.ColumnName))
	case "done":
		if !p.Moved {
			return ""
		}
		return fmt.Sprintf("💡 Idea “%s” moved to <b>%s</b> (%s)", title, esc(p.ColumnName), esc(p.Reason))
	case "failed":
		return fmt.Sprintf("✕ Idea “%s”: task #%d failed", title, t.ID)
	}
	return ""
}
