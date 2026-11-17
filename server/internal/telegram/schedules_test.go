package telegram

import (
	"context"
	"strings"
	"testing"

	"github.com/mymmrac/telego"

	"github.com/andrewsl105/fleetpocket/server/internal/events"
	"github.com/andrewsl105/fleetpocket/server/internal/projects"
	"github.com/andrewsl105/fleetpocket/server/internal/schedules"
	"github.com/andrewsl105/fleetpocket/server/internal/store"
	"github.com/andrewsl105/fleetpocket/server/internal/tasks"
	"github.com/andrewsl105/fleetpocket/server/internal/testutil"
)

func TestSchedulesCommandRunAndPause(t *testing.T) {
	st := testutil.DB(t)
	ctx := context.Background()
	api := &fakeAPI{}
	svc := &tasks.Service{Store: st, Hub: events.NewHub()}
	_, _ = (&projects.Service{Store: st}).Create(ctx, projects.CreateInput{ID: "demo"})
	_, _ = (&projects.Service{Store: st}).AddRepository(ctx, "demo", projects.RepoInput{FullName: "o/r", InstallationID: 1})
	scheds := &schedules.Service{Store: st, Tasks: svc}
	b := &Bot{API: api, Store: st, Tasks: svc, Hub: svc.Hub, OwnerID: owner, Schedules: scheds}
	scheds.Notify = b
	b.init()
	sc, err := scheds.Create(ctx, schedules.Input{Name: "Nightly deps", ProjectID: "demo", Prompt: "Update dependencies", Cron: "0 3 * * *", Timezone: "Europe/Kyiv"})
	if err != nil {
		t.Fatal(err)
	}
	b.HandleUpdate(ctx, msg(owner, 0, "/schedules"))
	sends, _, _ := api.snapshot()
	list := sends[len(sends)-1]
	if !strings.Contains(list.text, "<b>Nightly deps</b> · demo · next ") || !strings.Contains(list.text, "Europe/Kyiv") {
		t.Fatalf("list: %s", list.text)
	}
	kb := list.markup.(*telego.InlineKeyboardMarkup)
	tap := func(data string) {
		b.HandleUpdate(ctx, telego.Update{CallbackQuery: &telego.CallbackQuery{
			ID: "cb", From: telego.User{ID: owner}, Data: data,
			Message: &telego.Message{MessageID: 9, Chat: telego.Chat{ID: owner, Type: "private"}},
		}})
	}
	if !strings.HasPrefix(kb.InlineKeyboard[0][0].Text, "🚀 Run") || !strings.HasPrefix(kb.InlineKeyboard[0][1].Text, "⏸ Pause") {
		t.Fatalf("buttons: %+v", kb.InlineKeyboard[0])
	}
	tap(kb.InlineKeyboard[0][0].CallbackData)
	h, _ := st.ScheduleFirings(ctx, sc.ID, 5)
	if len(h) != 1 || h[0].TaskID == nil || h[0].Trigger != store.TriggerManual {
		t.Fatalf("run now: %+v", h)
	}
	sends, _, callbacks := api.snapshot()
	if !strings.HasPrefix(callbacks[len(callbacks)-1], "Started task #") {
		t.Fatalf("callback answer: %v", callbacks)
	}
	found := false
	for _, s := range sends {
		found = found || strings.Contains(s.text, "▶️ Run now <b>Nightly deps</b> started #")
	}
	if !found {
		t.Fatalf("no started notice: %+v", sends)
	}
	tap(kb.InlineKeyboard[0][1].CallbackData)
	if got, _ := scheds.Get(ctx, sc.ID); got.Enabled {
		t.Fatal("not paused")
	}
	b.HandleUpdate(ctx, msg(owner, 0, "/schedules"))
	sends, _, _ = api.snapshot()
	if !strings.Contains(sends[len(sends)-1].text, "paused") {
		t.Fatalf("paused list: %s", sends[len(sends)-1].text)
	}
	// A second run while the first task is queued: skipped, with a notice.
	tap(kb.InlineKeyboard[0][0].CallbackData)
	sends, _, _ = api.snapshot()
	if !strings.Contains(sends[len(sends)-1].text, "⏭ <b>Nightly deps</b> · skipped") {
		t.Fatalf("skip notice: %s", sends[len(sends)-1].text)
	}
}

func TestRenderLoop(t *testing.T) {
	tk := &store.Task{ID: 7}
	if s := renderLoop(tk, []byte(`{"phase":"iteration","iteration":1,"max_iterations":3}`)); s != "" {
		t.Fatalf("first iteration: %s", s)
	}
	if s := renderLoop(tk, []byte(`{"phase":"iteration","iteration":2,"max_iterations":3,"reason":"check failed (exit code 1); iteration 2/3"}`)); !strings.Contains(s, "Loop iteration 2/3") {
		t.Fatal(s)
	}
	if s := renderLoop(tk, []byte(`{"phase":"stopped","state":"passed","iteration":2,"max_iterations":3}`)); !strings.Contains(s, "Check passed (2/3)") {
		t.Fatal(s)
	}
	if s := renderLoop(tk, []byte(`{"phase":"stopped","state":"no_changes","reason":"2 iterations in a row changed no files"}`)); !strings.Contains(s, "Loop stopped") {
		t.Fatal(s)
	}
}
