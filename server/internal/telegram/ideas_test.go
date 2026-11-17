package telegram

import (
	"context"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/mymmrac/telego"

	"github.com/andrewsl105/fleetpocket/server/internal/events"
	"github.com/andrewsl105/fleetpocket/server/internal/ideas"
	"github.com/andrewsl105/fleetpocket/server/internal/projects"
	"github.com/andrewsl105/fleetpocket/server/internal/store"
	"github.com/andrewsl105/fleetpocket/server/internal/tasks"
	"github.com/andrewsl105/fleetpocket/server/internal/testutil"
)

func TestIdeaCommand(t *testing.T) {
	st := testutil.DB(t)
	ctx := context.Background()
	api := &fakeAPI{}
	hub := events.NewHub()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { _, _ = io.WriteString(w, "OggS audio") }))
	defer srv.Close()
	var sttErr error
	svc := &ideas.Service{Store: st, Hub: hub, NoPreviews: true, STT: transcriberFunc(func(_ context.Context, _, mime string, _ []byte) (string, error) {
		if mime != "audio/ogg" {
			t.Fatalf("STT mime %q", mime)
		}
		return "Add a CSV export to the costs page. Weekly.", sttErr
	})}
	b := &Bot{API: api, Store: st, Tasks: &tasks.Service{Store: st, Hub: hub}, Hub: hub, OwnerID: owner, Ideas: svc}
	b.init()
	private := func(id int, text string) *telego.Message {
		return &telego.Message{MessageID: id, From: &telego.User{ID: owner}, Chat: telego.Chat{ID: owner, Type: "private"}, Text: text}
	}
	last := func() string {
		sends, _, _ := api.snapshot()
		return sends[len(sends)-1].text
	}
	board := func() []*store.Idea {
		list, err := st.ListIdeas(ctx, store.IdeaFilter{})
		if err != nil {
			t.Fatal(err)
		}
		return list
	}

	// Off by default: the bot says so and nothing is saved.
	b.onMessage(ctx, private(1, "/idea dark mode for the canvas"))
	if last() != ideasOff || len(board()) != 0 {
		t.Fatalf("while off: %q %d", last(), len(board()))
	}
	if _, err := svc.SetSettings(ctx, store.IdeaSettings{Enabled: true}); err != nil {
		t.Fatal(err)
	}

	// Text.
	b.onMessage(ctx, private(2, "/idea Dark mode for the canvas. Asked twice."))
	list := board()
	if len(list) != 1 || list[0].Title != "Dark mode for the canvas" || list[0].Column != store.IdeaInbox || list[0].Source != "telegram" ||
		!strings.Contains(last(), "Saved to <b>Inbox</b>") {
		t.Fatalf("text idea: %+v %q", list, last())
	}

	// A voice message with the caption /idea: kept and transcribed.
	voice := private(3, "")
	voice.Caption, voice.Voice = "/idea", &telego.Voice{FileID: srv.URL, MimeType: "audio/ogg", Duration: 7}
	b.onMessage(ctx, voice)
	list = board()
	if len(list) != 2 || list[0].Title != "Add a CSV export to the costs page" || len(list[0].VoiceNotes) != 1 ||
		*list[0].VoiceNotes[0].DurationMS != 7000 || list[0].VoiceNotes[0].Transcript == nil {
		t.Fatalf("voice idea: %+v", list[0])
	}

	// /idea in reply to a voice message, with STT failing: kept without a transcript.
	sttErr = errors.New("down")
	reply := private(4, "/idea")
	reply.ReplyToMessage = &telego.Message{MessageID: 3, Chat: voice.Chat, Voice: &telego.Voice{FileID: srv.URL, Duration: 2}}
	b.onMessage(ctx, reply)
	list = board()
	if len(list) != 3 || !strings.HasPrefix(list[0].Title, "Voice note ") || list[0].VoiceNotes[0].Transcript != nil ||
		!strings.Contains(last(), "without a transcript") {
		t.Fatalf("reply to voice: %+v %q", list[0], last())
	}

	// A bare /idea asks; the reply becomes the card.
	b.onMessage(ctx, private(5, "/idea"))
	sends, _, _ := api.snapshot()
	prompt := sends[len(sends)-1]
	if _, ok := prompt.markup.(*telego.ForceReply); !ok {
		t.Fatalf("prompt: %+v", prompt)
	}
	answer := private(6, "Keyboard shortcuts for the board")
	answer.ReplyToMessage = &telego.Message{MessageID: prompt.msgID, Chat: answer.Chat}
	b.onMessage(ctx, answer)
	if list = board(); len(list) != 4 || list[0].Title != "Keyboard shortcuts for the board" {
		t.Fatalf("prompt reply: %+v", list[0])
	}

	// Voice-to-task is unchanged: a plain voice message still makes a task draft,
	// now with a "Save as an idea" button that saves it as a card instead.
	sttErr = nil
	b.STT, b.STTEnabled = svc.STT, true
	plain := private(7, "")
	plain.Voice = &telego.Voice{FileID: srv.URL, MimeType: "audio/ogg"}
	b.onMessage(ctx, plain)
	sends, _, _ = api.snapshot()
	card := sends[len(sends)-1]
	if !strings.Contains(card.text, "Create a task?") {
		t.Fatalf("draft card: %q", card.text)
	}
	var ideaBtn string
	for _, row := range card.markup.(*telego.InlineKeyboardMarkup).InlineKeyboard {
		for _, btn := range row {
			if strings.Contains(btn.Text, "idea") {
				ideaBtn = btn.CallbackData
			}
		}
	}
	if ideaBtn == "" {
		t.Fatalf("no idea button: %+v", card.markup)
	}
	b.HandleUpdate(ctx, telego.Update{CallbackQuery: &telego.CallbackQuery{ID: "cb", From: telego.User{ID: owner}, Data: ideaBtn,
		Message: &telego.Message{MessageID: card.msgID, Chat: telego.Chat{ID: owner, Type: "private"}}}})
	if list = board(); len(list) != 5 || list[0].Title != "Add a CSV export to the costs page" {
		t.Fatalf("draft as idea: %+v", list[0])
	}
	var drafts int
	_ = st.Pool.QueryRow(ctx, `SELECT count(*) FROM telegram_drafts`).Scan(&drafts)
	if drafts != 0 {
		t.Fatalf("draft kept: %d", drafts)
	}
}

func TestIdeaEventNotices(t *testing.T) {
	st := testutil.DB(t)
	ctx := context.Background()
	api := &fakeAPI{}
	hub := events.NewHub()
	svc := &tasks.Service{Store: st, Hub: hub}
	_, _ = (&projects.Service{Store: st}).Create(ctx, projects.CreateInput{ID: "demo"})
	_, _ = (&projects.Service{Store: st}).AddRepository(ctx, "demo", projects.RepoInput{FullName: "o/r", InstallationID: 1})
	b := &Bot{API: api, Store: st, Tasks: svc, Hub: hub, OwnerID: owner}
	b.init()
	v, err := svc.Create(ctx, tasks.CreateInput{ProjectID: "demo", Prompt: "x", Source: "web"})
	if err != nil {
		t.Fatal(err)
	}
	ev, _ := st.AddBackendEvent(ctx, v.ID, nil, ideas.EventIdea, map[string]any{"idea_id": 1, "title": "Dark mode", "action": "done",
		"column": "done", "column_name": "Done", "moved": true, "reason": "task #1, PR merged"})
	b.onEvent(ctx, ev)
	sends, _, _ := api.snapshot()
	n := sends[len(sends)-1]
	if !strings.Contains(n.text, "moved to <b>Done</b>") || !n.silent {
		t.Fatalf("notice: %+v", n)
	}
	if s := renderIdeaEvent(v.Task, []byte(`{"title":"x","action":"failed"}`)); !strings.Contains(s, "failed") {
		t.Fatal(s)
	}
}
