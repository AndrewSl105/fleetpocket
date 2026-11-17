package telegram

import (
	"context"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/mymmrac/telego"
	"github.com/pquerna/otp/totp"

	"github.com/andrewsl105/fleetpocket/server/internal/auth"
	"github.com/andrewsl105/fleetpocket/server/internal/config"
	"github.com/andrewsl105/fleetpocket/server/internal/events"
	"github.com/andrewsl105/fleetpocket/server/internal/projects"
	"github.com/andrewsl105/fleetpocket/server/internal/secretbox"
	"github.com/andrewsl105/fleetpocket/server/internal/store"
	"github.com/andrewsl105/fleetpocket/server/internal/tasks"
	"github.com/andrewsl105/fleetpocket/server/internal/team"
	"github.com/andrewsl105/fleetpocket/server/internal/testutil"
)

type ownerCreds struct{ h, s string }

func (o *ownerCreds) Owner() (string, string) { return o.h, o.s }
func (o *ownerCreds) SetOwner(h, s string)    { o.h, o.s = h, s }

func private(from int64, text string) telego.Update {
	return telego.Update{Message: &telego.Message{MessageID: 1, From: &telego.User{ID: from},
		Chat: telego.Chat{ID: from, Type: "private"}, Text: text}}
}

func lastText(api *fakeAPI, chat int64) string {
	s, _, _ := api.snapshot()
	for i := len(s) - 1; i >= 0; i-- {
		if s[i].chat == chat {
			return s[i].text
		}
	}
	return ""
}

// Team mode: /link links an account; unlinked users get the invite reply;
// viewers read but cannot create; members act on their own tasks only;
// questions escalate to admins after the timeout.
func TestBotTeamMode(t *testing.T) {
	st := testutil.DB(t)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	key, _ := secretbox.GenerateKey()
	box, _ := secretbox.New(key)
	hash, _ := auth.HashPassword("owner-password-1")
	secret, _, _ := auth.NewTOTP("o")
	ts := &team.Service{Store: st, Box: box, Cfg: &config.Config{TelegramAllowedUserID: owner, ClaudeOAuthToken: "x"}, Owner: &ownerCreds{hash, secret}}
	admin, err := ts.Enable(ctx, "ann@example.com", "Ann")
	if err != nil {
		t.Fatal(err)
	}
	if admin.TelegramUserID == nil || *admin.TelegramUserID != owner {
		t.Fatal("TELEGRAM_ALLOWED_USER_ID did not become user #1's link")
	}
	_ = ts.SetFallback(ctx, admin.ID, true)
	join := func(role, email string) *store.User {
		_, tok, _ := ts.CreateInvite(ctx, admin.ID, role, nil, 0)
		e, _ := ts.SignupStart(ctx, tok)
		c, _ := totp.GenerateCode(e.TOTPSecret, time.Now())
		u, err := ts.SignupFinish(ctx, team.SignupInput{Token: tok, EnrollmentID: e.ID, Name: role, Email: email, Password: "a-long-password", TOTP: c})
		if err != nil {
			t.Fatal(err)
		}
		return u
	}
	member := join("member", "m@example.com")
	viewer := join("viewer", "v@example.com")

	hub := events.NewHub()
	svc := &tasks.Service{Store: st, Hub: hub, TeamOn: ts.Enabled}
	name := "Demo"
	_, _ = (&projects.Service{Store: st}).Create(ctx, projects.CreateInput{ID: "demo", ProjectFields: store.ProjectFields{Name: &name}})
	_, _ = (&projects.Service{Store: st}).AddRepository(ctx, "demo", projects.RepoInput{FullName: "o/r", InstallationID: 1})
	api := &fakeAPI{}
	b := &Bot{API: api, Store: st, Tasks: svc, Hub: hub, OwnerID: owner, Team: ts, QuestionTimeout: time.Minute, EditInterval: 100 * time.Millisecond}
	b.Start(ctx)

	// Unlinked: the invite reply.
	b.HandleUpdate(ctx, private(500, "/tasks"))
	if !strings.Contains(lastText(api, 500), "Ask your admin for an invite") {
		t.Fatalf("unlinked reply: %q", lastText(api, 500))
	}
	// Link the member and the viewer.
	code, _, _ := ts.LinkCode(ctx, member.ID)
	b.HandleUpdate(ctx, private(500, "/link "+code))
	if !strings.Contains(lastText(api, 500), "Linked to") {
		t.Fatalf("link: %q", lastText(api, 500))
	}
	code, _, _ = ts.LinkCode(ctx, viewer.ID)
	b.HandleUpdate(ctx, private(600, "/link "+code))
	b.HandleUpdate(ctx, private(600, "/link 000000"))
	if !strings.Contains(lastText(api, 600), "invalid or expired") {
		t.Fatalf("bad code: %q", lastText(api, 600))
	}

	// The viewer reads but cannot create.
	b.HandleUpdate(ctx, private(600, "/new demo do it"))
	if !strings.Contains(lastText(api, 600), "viewer") {
		t.Fatalf("viewer create: %q", lastText(api, 600))
	}
	b.HandleUpdate(ctx, private(600, "/projects"))
	if !strings.Contains(lastText(api, 600), "demo") {
		t.Fatalf("viewer read: %q", lastText(api, 600))
	}

	// The member creates a task as themselves.
	b.HandleUpdate(ctx, private(500, "/new demo Add CSV export"))
	var tk *store.Task
	eventually(t, "member task", func() bool {
		list, _ := st.ListTasks(ctx, store.TaskFilter{Limit: 5})
		if len(list) == 1 {
			tk = list[0]
		}
		return tk != nil
	})
	if tk.CreatedByID == nil || *tk.CreatedByID != member.ID {
		t.Fatalf("task author: %+v", tk.CreatedByID)
	}
	// Its notices go to the member's chat.
	eventually(t, "status card in the member's chat", func() bool { return strings.Contains(lastText(api, 500), "#") })
	if to, _ := b.targetFor(ctx, tk); to.chatID != 500 {
		t.Fatalf("target: %+v", to)
	}
	// The viewer cannot cancel it; the member's own /cancel works.
	b.HandleUpdate(ctx, private(600, "/cancel "+strconv.FormatInt(tk.ID, 10)))
	if got, _ := st.GetTask(ctx, tk.ID); got.Status == tasks.Cancelled {
		t.Fatal("viewer cancelled a task")
	}

	// A question unanswered past the timeout goes to the admins too.
	run, _ := st.PendingRun(ctx, tk.ID)
	_ = st.MarkRunStarting(ctx, run.ID)
	_, _ = st.SetStatus(ctx, tk.ID, nil, tasks.WaitingInput, "", false)
	qid := tasks.QuestionID(run.ID, "q1")
	if _, err := st.CreateQuestion(ctx, store.Question{ID: qid, TaskID: tk.ID, RunID: run.ID, Text: "Which DB?", Options: []string{"pg", "sqlite"}}); err != nil {
		t.Fatal(err)
	}
	_, _ = st.Pool.Exec(ctx, `UPDATE questions SET created_at = now() - interval '2 minutes' WHERE id = $1`, qid)
	b.escalate(ctx)
	if !strings.Contains(lastText(api, owner), "Unanswered") || !strings.Contains(lastText(api, owner), "Which DB?") {
		t.Fatalf("escalation to admin: %q", lastText(api, owner))
	}
	before, _, _ := api.snapshot()
	b.escalate(ctx) // once only
	if after, _, _ := api.snapshot(); len(after) != len(before) {
		t.Fatal("escalated twice")
	}
	// Unlinking stops the bot from accepting the account.
	if err := ts.Unlink(ctx, member.ID); err != nil {
		t.Fatal(err)
	}
	b.HandleUpdate(ctx, private(500, "/tasks"))
	if !strings.Contains(lastText(api, 500), "Ask your admin for an invite") {
		t.Fatalf("after unlink: %q", lastText(api, 500))
	}
}
