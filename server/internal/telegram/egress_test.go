package telegram

import (
	"context"
	"strings"
	"testing"

	"github.com/mymmrac/telego"

	"github.com/andrewsl105/fleetpocket/server/internal/events"
	"github.com/andrewsl105/fleetpocket/server/internal/projects"
	"github.com/andrewsl105/fleetpocket/server/internal/store"
	"github.com/andrewsl105/fleetpocket/server/internal/tasks"
	"github.com/andrewsl105/fleetpocket/server/internal/testutil"
)

func TestEgressBlockedNotifiesAndAllows(t *testing.T) {
	st := testutil.DB(t)
	ctx := context.Background()
	api := &fakeAPI{}
	svc := &tasks.Service{Store: st, Hub: events.NewHub()}
	_, _ = (&projects.Service{Store: st}).Create(ctx, projects.CreateInput{ID: "demo"})
	_, _ = (&projects.Service{Store: st}).AddRepository(ctx, "demo", projects.RepoInput{FullName: "o/r", InstallationID: 1})
	v, err := svc.Create(ctx, tasks.CreateInput{ProjectID: "demo", Prompt: "work"})
	if err != nil {
		t.Fatal(err)
	}
	_ = st.SetProjectEgress(ctx, "demo", store.Egress{Mode: store.EgressAllowlist})
	b := &Bot{API: api, Store: st, Tasks: svc, Hub: svc.Hub, OwnerID: owner}
	b.init()
	ev, err := st.AddBackendEvent(ctx, v.ID, nil, "egress_blocked", map[string]any{"host": "example.com"})
	if err != nil {
		t.Fatal(err)
	}
	b.sendEgressBlocked(ctx, ev)
	sends, _, _ := api.snapshot()
	if len(sends) == 0 || !strings.Contains(sends[len(sends)-1].text, "blocked: example.com") {
		t.Fatalf("sends: %+v", sends)
	}
	kb := sends[len(sends)-1].markup.(*telego.InlineKeyboardMarkup)
	data := kb.InlineKeyboard[0][0].CallbackData
	b.HandleUpdate(ctx, telego.Update{CallbackQuery: &telego.CallbackQuery{
		ID: "cb", From: telego.User{ID: owner}, Data: data,
		Message: &telego.Message{MessageID: 9, Chat: telego.Chat{ID: owner, Type: "private"}},
	}})
	e, _ := st.ProjectEgress(ctx, "demo")
	if len(e.Hosts) != 1 || e.Hosts[0] != "example.com" {
		t.Fatalf("allowlist after the tap: %+v", e)
	}
}
