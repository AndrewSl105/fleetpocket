package telegram

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/mymmrac/telego"

	"github.com/andrewsl105/fleetpocket/server/internal/events"
	"github.com/andrewsl105/fleetpocket/server/internal/projects"
	"github.com/andrewsl105/fleetpocket/server/internal/store"
	"github.com/andrewsl105/fleetpocket/server/internal/tasks"
	"github.com/andrewsl105/fleetpocket/server/internal/testutil"
)

func TestRenderBudget(t *testing.T) {
	task := &store.Task{ID: 7, Title: "Fix it", ProjectID: "demo"}
	text, button := renderBudget(task, []byte(`{"phase":"warning","scope":"project","project_id":"demo","spent_usd":8,"limit_usd":10}`))
	if !strings.Contains(text, "Project demo's monthly budget is at 80%") || !strings.Contains(text, "$8.00 of $10.00") || button {
		t.Fatalf("warning: %q %v", text, button)
	}
	text, _ = renderBudget(task, []byte(`{"phase":"reached","scope":"global","spent_usd":10.5,"limit_usd":10}`))
	if !strings.Contains(text, "The monthly budget is used up") || !strings.Contains(text, "Running tasks finish") {
		t.Fatalf("reached: %q", text)
	}
	if text, _ := renderBudget(task, []byte(`{"phase":"override","via":"web"}`)); text != "" {
		t.Fatalf("override sends nothing: %q", text)
	}
}

func TestBudgetWaitingStartsAnyway(t *testing.T) {
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
	b := &Bot{API: api, Store: st, Tasks: svc, Hub: svc.Hub, OwnerID: owner}
	b.init()
	ev, err := st.AddBackendEvent(ctx, v.ID, nil, "budget", map[string]any{"phase": "waiting", "scope": "global", "spent_usd": 12, "limit_usd": 10})
	if err != nil {
		t.Fatal(err)
	}
	b.sendBudget(ctx, ev)
	sends, _, _ := api.snapshot()
	if len(sends) == 0 || !strings.Contains(sends[len(sends)-1].text, "waiting: budget") {
		t.Fatalf("sends: %+v", sends)
	}
	kb := sends[len(sends)-1].markup.(*telego.InlineKeyboardMarkup)
	if kb.InlineKeyboard[0][0].Text != "▶️ Start anyway" {
		t.Fatalf("first button: %+v", kb.InlineKeyboard[0][0])
	}
	b.HandleUpdate(ctx, telego.Update{CallbackQuery: &telego.CallbackQuery{
		ID: "cb", From: telego.User{ID: owner}, Data: kb.InlineKeyboard[0][0].CallbackData,
		Message: &telego.Message{MessageID: 9, Chat: telego.Chat{ID: owner, Type: "private"}},
	}})
	got, _ := st.GetTask(ctx, v.ID)
	if got.BudgetOverrideAt == nil {
		t.Fatal("the tap did not override the budget")
	}
	evs, _ := st.LastEvents(ctx, v.ID, []string{"budget"}, 1)
	if len(evs) != 1 || !strings.Contains(string(evs[0].Payload), `"via": "telegram"`) && !strings.Contains(string(evs[0].Payload), `"via":"telegram"`) {
		t.Fatalf("override event: %+v", evs)
	}
}

func TestCardCostMarkers(t *testing.T) {
	now := time.Now()
	base := store.Task{ID: 1, Title: "t", ProjectID: "p", Status: "completed", CreatedAt: now}
	unknown := base
	unknown.CostUnknown = true
	if got := renderCard(cardData{Task: &unknown, Now: now}); !strings.Contains(got, "Completed · $? ·") {
		t.Fatalf("unknown: %q", got)
	}
	mixed := base
	mixed.CostUSDTotal, mixed.CostEstimated, mixed.CostUnknown = 0.84, true, true
	if got := renderCard(cardData{Task: &mixed, Now: now}); !strings.Contains(got, "≈$0.84+?") {
		t.Fatalf("mixed: %q", got)
	}
}
