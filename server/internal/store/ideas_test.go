package store_test

import (
	"errors"
	"testing"

	"github.com/andrewsl105/fleetpocket/server/internal/store"
)

func ideaIDs(t *testing.T, f *costFixture, column string) []int64 {
	t.Helper()
	list, err := f.st.ListIdeas(f.ctx, store.IdeaFilter{})
	if err != nil {
		t.Fatal(err)
	}
	var out []int64
	for _, i := range list {
		if i.Column == column {
			out = append(out, i.ID)
		}
	}
	return out
}

func sameIDs(a, b []int64) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

func TestIdeaSettingsDefault(t *testing.T) {
	f := newCostFixture(t)
	c, err := f.st.IdeaSettings(f.ctx)
	if err != nil || c.Enabled || len(c.Columns) != 5 || c.Columns[3].ID != store.IdeaInProgress || c.ColumnName("done") != "Done" {
		t.Fatalf("default: %+v %v", c, err)
	}
	c.Enabled = true
	c.Columns[0].Name = "Dump"
	if err := f.st.SetIdeaSettings(f.ctx, c); err != nil {
		t.Fatal(err)
	}
	if c, _ := f.st.IdeaSettings(f.ctx); !c.Enabled || c.Columns[0].Name != "Dump" {
		t.Fatalf("saved: %+v", c)
	}
}

func TestIdeasCRUDAndOrdering(t *testing.T) {
	f := newCostFixture(t, "p")
	ctx, st := f.ctx, f.st
	p := "p"
	a, err := st.CreateIdea(ctx, store.IdeaFields{Title: "A", Body: "first", ProjectID: &p, Tags: []string{" UI", "#ui", "perf", ""}}, nil, nil)
	if err != nil || a.Column != store.IdeaInbox || len(a.Tags) != 2 || a.Tags[0] != "ui" || *a.ProjectID != "p" {
		t.Fatalf("create: %+v %v", a, err)
	}
	b, _ := st.CreateIdea(ctx, store.IdeaFields{Title: "B"}, nil, nil)
	c, _ := st.CreateIdea(ctx, store.IdeaFields{Title: "C"}, nil, nil)
	// New cards go on top.
	if got := ideaIDs(t, f, store.IdeaInbox); !sameIDs(got, []int64{c.ID, b.ID, a.ID}) {
		t.Fatalf("order: %v", got)
	}
	nope := "nope"
	if _, err := st.CreateIdea(ctx, store.IdeaFields{Title: "x", ProjectID: &nope}, nil, nil); !errors.Is(err, store.ErrNotFound) {
		t.Fatalf("unknown project: %v", err)
	}

	// Move C after A (to the bottom), then A to the top.
	if _, err := st.MoveIdea(ctx, c.ID, store.IdeaInbox, &a.ID); err != nil {
		t.Fatal(err)
	}
	if got := ideaIDs(t, f, store.IdeaInbox); !sameIDs(got, []int64{b.ID, a.ID, c.ID}) {
		t.Fatalf("after move: %v", got)
	}
	if _, err := st.MoveIdea(ctx, a.ID, store.IdeaInbox, nil); err != nil {
		t.Fatal(err)
	}
	if got := ideaIDs(t, f, store.IdeaInbox); !sameIDs(got, []int64{a.ID, b.ID, c.ID}) {
		t.Fatalf("to top: %v", got)
	}
	// Between two cards: the midpoint.
	if _, err := st.MoveIdea(ctx, c.ID, store.IdeaInbox, &a.ID); err != nil {
		t.Fatal(err)
	}
	if got := ideaIDs(t, f, store.IdeaInbox); !sameIDs(got, []int64{a.ID, c.ID, b.ID}) {
		t.Fatalf("between: %v", got)
	}
	// Repeated halving between the same neighbours eventually renumbers the
	// column, and the order stays right.
	x, y := c.ID, b.ID
	for i := 0; i < 80; i++ {
		if _, err := st.MoveIdea(ctx, y, store.IdeaInbox, &a.ID); err != nil {
			t.Fatalf("halving %d: %v", i, err)
		}
		x, y = y, x
	}
	got := ideaIDs(t, f, store.IdeaInbox)
	if len(got) != 3 || got[0] != a.ID {
		t.Fatalf("after halving: %v", got)
	}
	list, _ := st.ListIdeas(ctx, store.IdeaFilter{})
	for i := 1; i < len(list); i++ {
		if list[i].Column == list[i-1].Column && list[i].Position <= list[i-1].Position {
			t.Fatalf("positions not increasing: %v %v", list[i-1].Position, list[i].Position)
		}
	}

	// To another column; after_id must be a card of that column.
	if _, err := st.MoveIdea(ctx, b.ID, store.IdeaReady, &a.ID); !errors.Is(err, store.ErrNotFound) {
		t.Fatalf("after in other column: %v", err)
	}
	if m, err := st.MoveIdea(ctx, b.ID, store.IdeaReady, nil); err != nil || m.Column != store.IdeaReady {
		t.Fatalf("to ready: %+v %v", m, err)
	}
	if _, err := st.MoveIdea(ctx, b.ID, store.IdeaReady, &b.ID); !errors.Is(err, store.ErrConflict) {
		t.Fatalf("after itself: %v", err)
	}

	// Update, filter, search.
	title, body, empty := "A2", "uses **markdown**", ""
	tags := []string{"Perf"}
	u, err := st.UpdateIdea(ctx, a.ID, store.IdeaPatch{Title: &title, Body: &body, Tags: &tags, ProjectID: &empty})
	if err != nil || u.Title != "A2" || u.ProjectID != nil || len(u.Tags) != 1 || u.Tags[0] != "perf" {
		t.Fatalf("update: %+v %v", u, err)
	}
	if l, _ := st.ListIdeas(ctx, store.IdeaFilter{Tag: "PERF"}); len(l) != 1 || l[0].ID != a.ID {
		t.Fatalf("tag filter: %d", len(l))
	}
	if l, _ := st.ListIdeas(ctx, store.IdeaFilter{Query: "MARKDOWN"}); len(l) != 1 || l[0].ID != a.ID {
		t.Fatalf("search: %d", len(l))
	}
	if l, _ := st.ListIdeas(ctx, store.IdeaFilter{Query: "100%"}); len(l) != 0 {
		t.Fatalf("search escapes: %d", len(l))
	}

	// Attachments and transcripts are searched too.
	tr := "rewrite the onboarding flow"
	dur := 4200
	if _, err := st.AddIdeaAttachments(ctx, b.ID, []store.NewIdeaAttachment{{Kind: store.IdeaVoice, Name: "voice.webm", Mime: "audio/webm", Size: 10, DurationMS: &dur, Transcript: &tr}}, nil); err != nil {
		t.Fatal(err)
	}
	if l, _ := st.ListIdeas(ctx, store.IdeaFilter{Query: "onboarding"}); len(l) != 1 || l[0].ID != b.ID || len(l[0].VoiceNotes) != 1 || *l[0].VoiceNotes[0].DurationMS != 4200 {
		t.Fatalf("voice search: %+v", l)
	}

	// Archive: off the board, into the archive; delete only when archived.
	if err := st.DeleteIdea(ctx, a.ID); !errors.Is(err, store.ErrIdeaNotArchived) {
		t.Fatalf("delete live: %v", err)
	}
	if ar, err := st.SetIdeaArchived(ctx, a.ID, true); err != nil || ar.ArchivedAt == nil {
		t.Fatalf("archive: %+v %v", ar, err)
	}
	if _, err := st.MoveIdea(ctx, a.ID, store.IdeaReady, nil); !errors.Is(err, store.ErrConflict) {
		t.Fatalf("move archived: %v", err)
	}
	if l, _ := st.ListIdeas(ctx, store.IdeaFilter{Archived: true}); len(l) != 1 || l[0].ID != a.ID {
		t.Fatalf("archive list: %d", len(l))
	}
	if got := ideaIDs(t, f, store.IdeaInbox); sameIDs(got, []int64{a.ID}) || len(got) != 1 {
		t.Fatalf("board after archive: %v", got)
	}
	if un, err := st.SetIdeaArchived(ctx, a.ID, false); err != nil || un.ArchivedAt != nil {
		t.Fatalf("unarchive: %+v %v", un, err)
	}
	_, _ = st.SetIdeaArchived(ctx, a.ID, true)
	if err := st.DeleteIdea(ctx, a.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := st.GetIdea(ctx, a.ID); !errors.Is(err, store.ErrNotFound) {
		t.Fatalf("deleted: %v", err)
	}
}

func TestIdeaLinksAndAutoMove(t *testing.T) {
	f := newCostFixture(t, "p")
	ctx, st := f.ctx, f.st
	i, _ := st.CreateIdea(ctx, store.IdeaFields{Title: "I"}, nil, nil)
	task := f.task("p")
	if err := st.LinkIdeaTask(ctx, i.ID, task.ID); err != nil {
		t.Fatal(err)
	}
	if err := st.LinkIdeaTask(ctx, i.ID, task.ID); err != nil {
		t.Fatalf("link twice: %v", err)
	}
	if err := st.LinkIdeaTask(ctx, 9999, task.ID); !errors.Is(err, store.ErrNotFound) {
		t.Fatalf("unknown idea: %v", err)
	}
	if ids, _ := st.IdeaIDsForTask(ctx, task.ID); len(ids) != 1 || ids[0] != i.ID {
		t.Fatalf("ideas for task: %v", ids)
	}
	got, moved, err := st.MoveIdeaAuto(ctx, i.ID, store.IdeaInProgress, "Moved to In progress: task #1 started")
	if err != nil || !moved || got.Column != store.IdeaInProgress || got.AutoNote == nil || len(got.LinkedTasks) != 1 || got.LinkedTasks[0].Status != "queued" || got.Failed {
		t.Fatalf("auto move: %+v %v %v", got, moved, err)
	}
	if _, moved, _ := st.MoveIdeaAuto(ctx, i.ID, store.IdeaInProgress, "again"); moved {
		t.Fatal("moved into the same column")
	}
	if _, err := st.SetStatus(ctx, task.ID, []string{"queued"}, "failed", "boom", true); err != nil {
		t.Fatal(err)
	}
	if got, _ := st.GetIdea(ctx, i.ID); !got.Failed {
		t.Fatalf("failed stamp: %+v", got)
	}
	// The owner's move clears the note.
	if got, _ := st.MoveIdea(ctx, i.ID, store.IdeaReady, nil); got.AutoNote != nil {
		t.Fatalf("note kept: %+v", got)
	}
}
