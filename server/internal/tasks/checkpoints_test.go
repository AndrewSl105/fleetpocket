package tasks

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/andrewsl105/fleetpocket/server/internal/store"
)

func TestRestoreMode(t *testing.T) {
	c := &store.Checkpoint{SessionRef: "s/u"}
	if RestoreMode(&store.Task{Agent: "claude"}, c) != RestoreFork {
		t.Fatal("claude with a session_ref forks")
	}
	if RestoreMode(&store.Task{Agent: "codex"}, &store.Checkpoint{}) != RestoreSummary || RestoreMode(&store.Task{Agent: "claude"}, &store.Checkpoint{}) != RestoreSummary {
		t.Fatal("no session_ref: summary")
	}
}

func TestSummaryPromptIsDeterministicAndBounded(t *testing.T) {
	ev := func(typ, payload string) *store.Event {
		return &store.Event{Type: typ, Payload: json.RawMessage(payload)}
	}
	evs := []*store.Event{
		ev("assistant_text", `{"text":"I will add the table."}`),
		ev("tool_use", `{"summary":"Edit db/schema.sql"}`),
		ev("question", `{"text":"Which name?"}`),
		ev("answer", `{"text":"orders"}`),
		ev("message", `{"text":"also add an index"}`),
	}
	task := &store.Task{ID: 7, Prompt: "Add an orders table"}
	cp := &store.Checkpoint{RunID: 3, N: 2}
	got := SummaryPrompt(task, cp, evs, "then do it with a migration instead")
	for _, want := range []string{"task #7", "run 3, checkpoint 2", "Add an orders table", "- You said: I will add the table.", "- You ran: Edit db/schema.sql",
		"- You asked: Which name?", "- Owner answered: orders", "- Owner: also add an index", "Now: then do it with a migration instead"} {
		if !strings.Contains(got, want) {
			t.Fatalf("missing %q in:\n%s", want, got)
		}
	}
	if got != SummaryPrompt(task, cp, evs, "then do it with a migration instead") {
		t.Fatal("not deterministic")
	}
	var many []*store.Event
	for range 200 {
		many = append(many, ev("tool_use", `{"summary":"`+strings.Repeat("x", 1000)+`"}`))
	}
	long := SummaryPrompt(task, cp, many, "")
	if !strings.Contains(long, "(120 earlier steps left out)") || len(long) > 30000 || !strings.HasSuffix(long, "Now: Continue the task from here.") {
		t.Fatalf("bounds: %d", len(long))
	}
}
