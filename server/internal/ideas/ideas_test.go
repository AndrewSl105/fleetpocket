package ideas

import (
	"bytes"
	"context"
	"encoding/json"
	"image"
	"image/png"
	"net/http"
	"net/http/httptest"
	"net/netip"
	"net/url"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/andrewsl105/fleetpocket/server/internal/events"
	"github.com/andrewsl105/fleetpocket/server/internal/store"
	"github.com/andrewsl105/fleetpocket/server/internal/tasks"
	"github.com/andrewsl105/fleetpocket/server/internal/testutil"
)

func TestTitleFromText(t *testing.T) {
	for in, want := range map[string]string{
		"Rewrite the onboarding flow. It is too long.":                  "Rewrite the onboarding flow",
		"so I was thinking we could add a dark mode to the canvas view": "so I was thinking we could add a…",
		"Version 1.2 is broken":                                         "Version 1.2 is broken",
		"first line\nsecond line":                                       "first line",
		"Really?! yes":                                                  "Really?",
		"   ":                                                           "",
		"one, two, three, four, five, six, seven, eight, nine":          "one, two, three, four, five, six, seven, eight…",
	} {
		if got := TitleFromText(in); got != want {
			t.Errorf("%q: got %q, want %q", in, got, want)
		}
	}
}

func TestTaskOutcome(t *testing.T) {
	for _, c := range []struct {
		status, reason string
		want           Outcome
	}{
		{"completed", "PR merged", OutcomeDone},
		{"completed", "branch merged into main", OutcomeDone},
		{"completed", "PR closed without merging", OutcomeNone},
		{"completed", "branch discarded", OutcomeNone},
		{"completed", "committed locally", OutcomeNone},
		{"failed", "boom", OutcomeFailed},
		{"cancelled", "", OutcomeNone},
		{"pr_open", "", OutcomeNone},
	} {
		if got := TaskOutcome(c.status, c.reason); got != c.want {
			t.Errorf("%s %q: %v", c.status, c.reason, got)
		}
	}
}

func TestAutoMoveOnMergeAndFailure(t *testing.T) {
	st := testutil.DB(t)
	ctx := context.Background()
	hub := events.NewHub()
	svc := &Service{Store: st, Hub: hub, NoPreviews: true}
	if _, err := st.CreateProject(ctx, "p", store.ProjectFields{}); err != nil {
		t.Fatal(err)
	}
	repo, err := st.AddRepository(ctx, store.Repository{ProjectID: "p", FullName: "o/r", BaseBranch: "main", InstallationID: 1})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := svc.SetSettings(ctx, store.IdeaSettings{Enabled: true}); err != nil {
		t.Fatal(err)
	}
	newTask := func() *store.Task {
		task, err := st.CreateTask(ctx, store.Task{ProjectID: "p", RepositoryID: repo.ID, Title: "t", Prompt: "p", Source: "api"}, nil)
		if err != nil {
			t.Fatal(err)
		}
		return task
	}
	idea, err := svc.Create(ctx, Input{Title: "Ship it"}, nil)
	if err != nil {
		t.Fatal(err)
	}
	task := newTask()
	if err := svc.LinkTask(ctx, idea.ID, task); err != nil {
		t.Fatal(err)
	}
	if got, _ := st.GetIdea(ctx, idea.ID); got.Column != store.IdeaInProgress {
		t.Fatalf("after link: %s", got.Column)
	}

	// A PR closed without merging leaves the card where it is.
	closed, _ := st.SetStatus(ctx, task.ID, nil, "completed", "PR closed without merging", true)
	svc.OnTaskStatus(ctx, closed)
	if got, _ := st.GetIdea(ctx, idea.ID); got.Column != store.IdeaInProgress {
		t.Fatalf("closed PR moved the card: %s", got.Column)
	}
	merged, _ := st.SetStatus(ctx, task.ID, nil, "completed", "PR merged", true)
	svc.OnTaskStatus(ctx, merged)
	got, _ := st.GetIdea(ctx, idea.ID)
	if got.Column != store.IdeaDone || got.AutoNote == nil || !strings.Contains(*got.AutoNote, "PR merged") {
		t.Fatalf("after merge: %+v", got)
	}
	evs, _ := st.LastEvents(ctx, task.ID, []string{EventIdea}, 5)
	if len(evs) != 2 {
		t.Fatalf("idea events: %d", len(evs))
	}
	var p map[string]any
	_ = json.Unmarshal(evs[1].Payload, &p) // oldest first
	if p["action"] != "done" || p["column"] != "done" || p["moved"] != true {
		t.Fatalf("done event: %v", p)
	}

	// A local branch merged counts as done too; a failure stamps ✕ and says so.
	idea2, _ := svc.Create(ctx, Input{Title: "Local"}, nil)
	t2 := newTask()
	_ = svc.LinkTask(ctx, idea2.ID, t2)
	failed, _ := st.SetStatus(ctx, t2.ID, nil, "failed", "boom", true)
	svc.OnTaskStatus(ctx, failed)
	if got, _ := st.GetIdea(ctx, idea2.ID); !got.Failed || got.Column != store.IdeaInProgress {
		t.Fatalf("after failure: %+v", got)
	}
	evs, _ = st.LastEvents(ctx, t2.ID, []string{EventIdea}, 1)
	_ = json.Unmarshal(evs[0].Payload, &p)
	if p["action"] != "failed" {
		t.Fatalf("failed event: %v", p)
	}
	branch, _ := st.SetStatus(ctx, t2.ID, nil, "completed", "branch merged into main", true)
	svc.OnTaskStatus(ctx, branch)
	if got, _ := st.GetIdea(ctx, idea2.ID); got.Column != store.IdeaDone || got.Failed {
		t.Fatalf("after branch merge: %+v", got)
	}

	// With the board off, nothing moves.
	_, _ = svc.SetSettings(ctx, store.IdeaSettings{Enabled: false})
	idea3, _ := st.CreateIdea(ctx, store.IdeaFields{Title: "x"}, nil, nil)
	t3 := newTask()
	_ = st.LinkIdeaTask(ctx, idea3.ID, t3.ID)
	m3, _ := st.SetStatus(ctx, t3.ID, nil, "completed", "PR merged", true)
	svc.OnTaskStatus(ctx, m3)
	if got, _ := st.GetIdea(ctx, idea3.ID); got.Column != store.IdeaInbox {
		t.Fatalf("moved while off: %s", got.Column)
	}
}

func TestDialCheckRefusesPrivateAddresses(t *testing.T) {
	for _, addr := range []string{"127.0.0.1:80", "10.1.2.3:443", "192.168.1.1:80", "172.16.0.1:443", "169.254.169.254:80",
		"[::1]:443", "[fd00::1]:443", "[::ffff:127.0.0.1]:80", "0.0.0.0:80", "100.64.0.1:80", "[64:ff9b::a00:1]:443", "[fe80::1]:80"} {
		if err := dialCheck("tcp", addr, nil); err == nil {
			t.Errorf("%s allowed", addr)
		}
	}
	if err := dialCheck("tcp", "93.184.215.14:443", nil); err != nil {
		t.Errorf("public address refused: %v", err)
	}
	if err := dialCheck("tcp", "93.184.215.14:8080", nil); err == nil {
		t.Error("port 8080 allowed")
	}
	// The real client never reaches a loopback server, on its own or through a redirect.
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { _, _ = w.Write([]byte("<title>secret</title>")) }))
	defer srv.Close()
	defaultPortsOnly = false
	defer func() { defaultPortsOnly = true }()
	if _, _, err := FetchPreview(context.Background(), SafeClient(), srv.URL); err == nil {
		t.Fatal("fetched a loopback URL")
	}
	for _, raw := range []string{"file:///etc/passwd", "ftp://x/y", "javascript:alert(1)", "http://user:pw@example.com/"} {
		u, _ := url.Parse(raw)
		if checkURL(u) == nil {
			t.Errorf("%s allowed", raw)
		}
	}
	if !PublicAddr(netip.MustParseAddr("8.8.8.8")) || PublicAddr(netip.MustParseAddr("::")) {
		t.Fatal("PublicAddr")
	}
}

func TestFetchPreview(t *testing.T) {
	var img bytes.Buffer
	_ = png.Encode(&img, image.NewRGBA(image.Rect(0, 0, 40, 20)))
	mux := http.NewServeMux()
	mux.HandleFunc("/page", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		_, _ = w.Write([]byte(`<html><head><title>Fallback</title>
			<meta property="og:title" content="Fix &amp; ship">
			<meta content="/img.png" property="og:image"></head></html>`))
	})
	mux.HandleFunc("/img.png", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "image/png")
		_, _ = w.Write(img.Bytes())
	})
	mux.HandleFunc("/redirect", func(w http.ResponseWriter, r *http.Request) { http.Redirect(w, r, "/page", http.StatusFound) })
	mux.HandleFunc("/json", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{}`))
	})
	srv := httptest.NewServer(mux)
	defer srv.Close()
	defaultPortsOnly = false
	defer func() { defaultPortsOnly = true }()
	// The test server is on loopback: allow it here, keep every other rule.
	c := safeClient(func(string, string, syscall.RawConn) error { return nil })
	p, data, err := FetchPreview(context.Background(), c, srv.URL+"/redirect")
	if err != nil || p.Title != "Fix & ship" || p.ImageURL != srv.URL+"/img.png" || len(data) == 0 {
		t.Fatalf("preview: %+v %d %v", p, len(data), err)
	}
	if _, _, err := FetchPreview(context.Background(), c, srv.URL+"/json"); err == nil {
		t.Fatal("JSON taken for a page")
	}
	p = parseHead(`<title>  Plain
		title </title>`, &url.URL{Scheme: "https", Host: "x"})
	if p.Title != "Plain title" || p.ImageURL != "" {
		t.Fatalf("plain: %+v", p)
	}
}

// The production path: Watch hears status changes on the hub as the task
// service (webhook, merge button, scheduler) publishes them.
func TestWatchMovesCardsFromHubStatusChanges(t *testing.T) {
	st := testutil.DB(t)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	hub := events.NewHub()
	taskSvc := &tasks.Service{Store: st, Hub: hub}
	svc := &Service{Store: st, Hub: hub, NoPreviews: true}
	if _, err := st.CreateProject(ctx, "p", store.ProjectFields{}); err != nil {
		t.Fatal(err)
	}
	if _, err := st.AddRepository(ctx, store.Repository{ProjectID: "p", FullName: "o/r", BaseBranch: "main", InstallationID: 1, IsDefault: true}); err != nil {
		t.Fatal(err)
	}
	if _, err := svc.SetSettings(ctx, store.IdeaSettings{Enabled: true}); err != nil {
		t.Fatal(err)
	}
	go svc.Watch(ctx)
	time.Sleep(50 * time.Millisecond) // let Watch subscribe

	start := func(title string) (*store.Idea, int64) {
		idea, err := svc.Create(ctx, Input{Title: title}, nil)
		if err != nil {
			t.Fatal(err)
		}
		v, err := taskSvc.Create(ctx, tasks.CreateInput{ProjectID: "p", Prompt: title, Source: "web"})
		if err != nil {
			t.Fatal(err)
		}
		if err := svc.LinkTask(ctx, idea.ID, v.Task); err != nil {
			t.Fatal(err)
		}
		return idea, v.ID
	}
	eventually := func(what string, cond func(*store.Idea) bool, id int64) {
		t.Helper()
		deadline := time.Now().Add(5 * time.Second)
		for {
			got, err := st.GetIdea(ctx, id)
			if err == nil && cond(got) {
				return
			}
			if time.Now().After(deadline) {
				t.Fatalf("timed out waiting for %s: %+v", what, got)
			}
			time.Sleep(20 * time.Millisecond)
		}
	}

	merged, mergedTask := start("Merge me")
	if _, err := st.SetStatus(ctx, mergedTask, nil, tasks.PROpen, "", false); err != nil {
		t.Fatal(err)
	}
	if _, err := taskSvc.SetStatus(ctx, mergedTask, tasks.Completed, "PR merged"); err != nil {
		t.Fatal(err)
	}
	eventually("Done after the PR merged", func(i *store.Idea) bool { return i.Column == store.IdeaDone }, merged.ID)

	failed, failedTask := start("Fail me")
	if _, err := taskSvc.SetStatus(ctx, failedTask, tasks.Failed, "boom"); err != nil {
		t.Fatal(err)
	}
	eventually("✕ after the failure", func(i *store.Idea) bool { return i.Failed && i.Column == store.IdeaInProgress }, failed.ID)
	eventuallyEvents := func() int {
		evs, _ := st.LastEvents(ctx, failedTask, []string{EventIdea}, 5)
		return len(evs)
	}
	deadline := time.Now().Add(5 * time.Second)
	for eventuallyEvents() != 2 { // started, failed
		if time.Now().After(deadline) {
			t.Fatalf("idea events on the failed task: %d", eventuallyEvents())
		}
		time.Sleep(20 * time.Millisecond)
	}
}
