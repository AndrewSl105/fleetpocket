package api_test

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"mime/multipart"
	"net/http"
	"net/http/cookiejar"
	"net/http/httptest"
	"net/textproto"
	"strings"
	"testing"
	"time"

	"github.com/pquerna/otp/totp"

	"github.com/andrewsl105/fleetpocket/server/internal/api"
	"github.com/andrewsl105/fleetpocket/server/internal/attachments"
	"github.com/andrewsl105/fleetpocket/server/internal/auth"
	"github.com/andrewsl105/fleetpocket/server/internal/config"
	"github.com/andrewsl105/fleetpocket/server/internal/events"
	"github.com/andrewsl105/fleetpocket/server/internal/ideas"
	"github.com/andrewsl105/fleetpocket/server/internal/projects"
	"github.com/andrewsl105/fleetpocket/server/internal/store"
	"github.com/andrewsl105/fleetpocket/server/internal/tasks"
	"github.com/andrewsl105/fleetpocket/server/internal/testutil"
)

// fakeSTT answers every recording with text, or fails with err.
type fakeSTT struct {
	text  string
	err   error
	mimes []string
}

func (f *fakeSTT) Transcribe(_ context.Context, _, mime string, _ []byte) (string, error) {
	f.mimes = append(f.mimes, mime)
	return f.text, f.err
}

func setupIdeas(t *testing.T) (*client, *store.Store, *ideas.Service, *fakeSTT) {
	t.Helper()
	st := testutil.DB(t)
	hash, _ := auth.HashPassword("pw-for-tests")
	secret, _, _ := auth.NewTOTP("t")
	hub := events.NewHub()
	files := &attachments.Files{Dir: t.TempDir()}
	stt := &fakeSTT{text: "Rewrite the onboarding flow. It is too long and people drop off."}
	svc := &ideas.Service{Store: st, Files: files, Hub: hub, STT: stt, NoPreviews: true,
		Now: func() time.Time { return time.Date(2026, 11, 17, 9, 30, 0, 0, time.Local) }}
	srv := &api.Server{
		Cfg: &config.Config{ClaudeOAuthToken: "t", DefaultAgent: "claude"}, Store: st, Hub: hub, Files: files,
		Tasks:    &tasks.Service{Store: st, Hub: hub, Files: files},
		Projects: &projects.Service{Store: st},
		Ideas:    svc,
		Auth:     &auth.Manager{Store: st, PasswordHash: hash, TOTPSecret: secret, SessionSecret: []byte("s"), CookieInsecure: true},
	}
	ts := httptest.NewServer(srv.Handler())
	t.Cleanup(ts.Close)
	jar, _ := cookiejar.New(nil)
	c := &client{t: t, base: ts.URL, http: &http.Client{Jar: jar}}
	otp, _ := totp.GenerateCode(secret, time.Now())
	if code, body := c.do("POST", "/api/v1/auth/login", map[string]string{"password": "pw-for-tests", "totp": otp}); code != 200 {
		t.Fatalf("login: %d %v", code, body)
	}
	c.do("POST", "/api/v1/projects", map[string]any{"id": "demo", "name": "Demo"})
	c.do("POST", "/api/v1/projects/demo/repositories", map[string]any{"full_name": "o/r", "installation_id": 1, "base_branch": "main"})
	return c, st, svc, stt
}

// voice uploads a recording with its own part Content-Type.
func (c *client) voice(path, mime string, data []byte, fields map[string]string) (int, map[string]any) {
	c.t.Helper()
	var b bytes.Buffer
	mw := multipart.NewWriter(&b)
	for k, v := range fields {
		_ = mw.WriteField(k, v)
	}
	h := textproto.MIMEHeader{}
	h.Set("Content-Disposition", `form-data; name="audio"; filename="voice.webm"`)
	h.Set("Content-Type", mime)
	w, _ := mw.CreatePart(h)
	_, _ = w.Write(data)
	_ = mw.Close()
	req, _ := http.NewRequest("POST", c.base+path, &b)
	req.Header.Set("Content-Type", mw.FormDataContentType())
	req.Header.Set("X-Requested-With", "fleetpocket")
	resp, err := c.http.Do(req)
	if err != nil {
		c.t.Fatal(err)
	}
	defer resp.Body.Close()
	var out map[string]any
	_ = json.NewDecoder(resp.Body).Decode(&out)
	return resp.StatusCode, out
}

func idOf(m map[string]any) int64 { return int64(m["id"].(float64)) }

func TestIdeasOffByDefault(t *testing.T) {
	c, _, _, _ := setupIdeas(t)
	if _, me := c.do("GET", "/api/v1/me", nil); me["ideas_enabled"] != false {
		t.Fatalf("me: %v", me)
	}
	if code, body := c.do("GET", "/api/v1/ideas", nil); code != 409 || !strings.HasPrefix(errCode(body), "ideas_disabled") {
		t.Fatalf("list while off: %d %v", code, body)
	}
	if code, body := c.do("POST", "/api/v1/tasks", map[string]any{"project_id": "demo", "prompt": "x", "idea_id": 1}); code != 409 {
		t.Fatalf("task from idea while off: %d %v", code, body)
	}
	code, s := c.do("GET", "/api/v1/settings/ideas", nil)
	if code != 200 || s["enabled"] != false || len(s["columns"].([]any)) != 5 {
		t.Fatalf("settings: %d %v", code, s)
	}
}

func TestIdeasAPI(t *testing.T) {
	c, st, svc, stt := setupIdeas(t)
	if code, s := c.do("PUT", "/api/v1/settings/ideas", map[string]any{"enabled": true}); code != 200 || s["enabled"] != true || len(s["columns"].([]any)) != 5 {
		t.Fatalf("enable: %d %v", code, s)
	}
	if _, me := c.do("GET", "/api/v1/me", nil); me["ideas_enabled"] != true {
		t.Fatalf("me: %v", me)
	}

	// Text: the title comes from the first words when none is given.
	code, a := c.do("POST", "/api/v1/ideas", map[string]any{"body": "Dark mode for the canvas. Users asked twice.", "tags": []string{"UI"}, "project_id": "demo"})
	if code != 201 || a["title"] != "Dark mode for the canvas" || a["column"] != "inbox" || a["tags"].([]any)[0] != "ui" || a["project_id"] != "demo" {
		t.Fatalf("create: %d %v", code, a)
	}
	if code, body := c.do("POST", "/api/v1/ideas", map[string]any{"column": "nope", "title": "x"}); code != 400 {
		t.Fatalf("unknown column: %d %v", code, body)
	}
	if code, body := c.do("POST", "/api/v1/ideas", map[string]any{}); code != 400 {
		t.Fatalf("empty idea: %d %v", code, body)
	}

	// Files and links (multipart); images get thumbnails.
	code, b := c.multipart("/api/v1/ideas", part{field: "title", data: []byte("Screenshot of the bug")},
		part{field: "tags", data: []byte("bug, ui")}, part{field: "links", data: []byte("https://example.com/issue/1\n")},
		part{field: "files[]", name: "shot.png", data: pngBytes(800, 600)})
	if code != 201 || len(b["attachments"].([]any)) != 2 {
		t.Fatalf("multipart create: %d %v", code, b)
	}
	atts := b["attachments"].([]any)
	img, link := atts[0].(map[string]any), atts[1].(map[string]any)
	if img["kind"] != "file" || img["has_thumb"] != true || link["kind"] != "link" || link["url"] != "https://example.com/issue/1" {
		t.Fatalf("attachments: %v", atts)
	}
	resp, body := c.get(fmt.Sprintf("/api/v1/ideas/%d/attachments/%d?thumb=1", idOf(b), idOf(img)))
	if resp.StatusCode != 200 || resp.Header.Get("Content-Type") != "image/jpeg" || len(body) == 0 {
		t.Fatalf("thumb: %d %s", resp.StatusCode, resp.Header.Get("Content-Type"))
	}
	if code, body := c.do("POST", fmt.Sprintf("/api/v1/ideas/%d/links", idOf(b)), map[string]any{"url": "file:///etc/passwd"}); code != 400 {
		t.Fatalf("file link: %d %v", code, body)
	}

	// Voice: transcribed with the recorder's declared type; title from the first sentence.
	code, v := c.voice("/api/v1/ideas/voice", "audio/webm;codecs=opus", []byte("OggS fake"), map[string]string{"duration_ms": "4200"})
	if code != 201 || v["title"] != "Rewrite the onboarding flow" || !strings.HasPrefix(v["body"].(string), "Rewrite the onboarding flow.") {
		t.Fatalf("voice: %d %v", code, v)
	}
	note := v["voice_notes"].([]any)[0].(map[string]any)
	if note["duration_ms"] != 4200.0 || note["mime"] != "audio/webm" || note["transcript"] == nil || stt.mimes[0] != "audio/webm;codecs=opus" {
		t.Fatalf("voice note: %v %v", note, stt.mimes)
	}
	resp, _ = c.get(fmt.Sprintf("/api/v1/ideas/%d/attachments/%d", idOf(v), idOf(note)))
	if resp.StatusCode != 200 || resp.Header.Get("Content-Type") != "audio/webm" {
		t.Fatalf("voice download: %d %s", resp.StatusCode, resp.Header.Get("Content-Type"))
	}
	if code, body := c.voice("/api/v1/ideas/voice", "text/html", []byte("<script>"), nil); code != 400 {
		t.Fatalf("html as voice: %d %v", code, body)
	}
	// STT failing (or off): saved without a transcript, titled by time.
	stt.err = errors.New("down")
	code, v2 := c.voice("/api/v1/ideas/voice", "audio/mp4", []byte("ftyp fake"), nil)
	if code != 201 || v2["title"] != "Voice note Nov 17 09:30" || v2["body"] != "" || v2["voice_notes"].([]any)[0].(map[string]any)["transcript"] != nil {
		t.Fatalf("voice without STT: %d %v", code, v2)
	}
	stt.err = nil
	svc.STT = nil
	code, v3 := c.voice(fmt.Sprintf("/api/v1/ideas/%d/voice", idOf(a)), "audio/ogg", []byte("OggS"), nil)
	if code != 200 || len(v3["voice_notes"].([]any)) != 1 || v3["body"] != "Dark mode for the canvas. Users asked twice." {
		t.Fatalf("add voice without STT: %d %v", code, v3)
	}

	// Ordering: newest on top; move after a card; into another column.
	_, list := c.do("GET", "/api/v1/ideas", nil)
	var inbox []int64
	for _, it := range list["items"].([]any) {
		if m := it.(map[string]any); m["column"] == "inbox" {
			inbox = append(inbox, idOf(m))
		}
	}
	if len(inbox) != 4 || inbox[0] != idOf(v2) || inbox[3] != idOf(a) {
		t.Fatalf("inbox order: %v", inbox)
	}
	if code, m := c.do("POST", fmt.Sprintf("/api/v1/ideas/%d/move", idOf(v2)), map[string]any{"column": "inbox", "after_id": idOf(a)}); code != 200 || m["column"] != "inbox" {
		t.Fatalf("move: %d %v", code, m)
	}
	if code, m := c.do("POST", fmt.Sprintf("/api/v1/ideas/%d/move", idOf(b)), map[string]any{"column": "shaping"}); code != 200 || m["column"] != "shaping" {
		t.Fatalf("move to shaping: %d %v", code, m)
	}
	if code, _ := c.do("POST", fmt.Sprintf("/api/v1/ideas/%d/move", idOf(b)), map[string]any{"column": "nope"}); code != 400 {
		t.Fatalf("move to unknown column: %d", code)
	}
	if _, f := c.do("GET", "/api/v1/ideas?tag=bug", nil); len(f["items"].([]any)) != 1 {
		t.Fatalf("tag filter: %v", f)
	}
	if _, f := c.do("GET", "/api/v1/ideas?project=demo&q=canvas", nil); len(f["items"].([]any)) != 1 {
		t.Fatalf("project + search: %v", f)
	}
	if code, u := c.do("PATCH", fmt.Sprintf("/api/v1/ideas/%d", idOf(a)), map[string]any{"title": "Dark canvas", "project_id": ""}); code != 200 || u["title"] != "Dark canvas" || u["project_id"] != nil {
		t.Fatalf("patch: %d %v", code, u)
	}

	// Columns: rename, add, remove an empty added one; not the defaults.
	cols := []map[string]any{{"id": "inbox", "name": "Dump"}, {"id": "shaping", "name": "Shaping"}, {"id": "ready", "name": "Ready"},
		{"id": "in_progress", "name": "Doing"}, {"id": "done", "name": "Done"}, {"name": "Someday"}}
	code, s := c.do("PUT", "/api/v1/settings/ideas", map[string]any{"enabled": true, "columns": cols})
	if code != 200 || len(s["columns"].([]any)) != 6 {
		t.Fatalf("add column: %d %v", code, s)
	}
	someday := s["columns"].([]any)[5].(map[string]any)["id"].(string)
	if code, body := c.do("PUT", "/api/v1/settings/ideas", map[string]any{"enabled": true, "columns": cols[1:5]}); code != 400 {
		t.Fatalf("remove a default column: %d %v", code, body)
	}
	c.do("POST", fmt.Sprintf("/api/v1/ideas/%d/move", idOf(b)), map[string]any{"column": someday})
	if code, body := c.do("PUT", "/api/v1/settings/ideas", map[string]any{"enabled": true, "columns": cols[:5]}); code != 409 {
		t.Fatalf("remove a column with cards: %d %v", code, body)
	}
	many := append([]map[string]any{}, cols[:5]...)
	for i := 0; i < 4; i++ {
		many = append(many, map[string]any{"name": fmt.Sprint("x", i)})
	}
	if code, _ := c.do("PUT", "/api/v1/settings/ideas", map[string]any{"enabled": true, "columns": many}); code != 400 {
		t.Fatalf("9 columns: %d", code)
	}

	// Start a task from an idea: linked, moved to In progress, an event on the task.
	if code, _ := c.do("POST", "/api/v1/tasks", map[string]any{"project_id": "demo", "prompt": "x", "idea_id": 99999}); code != 400 {
		t.Fatalf("unknown idea: %d", code)
	}
	if n, _ := st.ListTasks(context.Background(), store.TaskFilter{Limit: 10}); len(n) != 0 {
		t.Fatalf("a task was created for an unknown idea: %d", len(n))
	}
	code, task := c.do("POST", "/api/v1/tasks", map[string]any{"project_id": "demo", "prompt": "Dark mode for the canvas", "idea_id": idOf(a)})
	if code != 201 {
		t.Fatalf("task from idea: %d %v", code, task)
	}
	_, got := c.do("GET", fmt.Sprintf("/api/v1/ideas/%d", idOf(a)), nil)
	if got["column"] != "in_progress" || got["linked_task_ids"].([]any)[0] != task["id"] || got["auto_note"] == nil ||
		!strings.Contains(got["auto_note"].(string), "Doing") {
		t.Fatalf("idea after task: %v", got)
	}
	evs := c.eventsOf(idOf(task), "idea")
	if len(evs) != 1 || evs[0]["payload"].(map[string]any)["action"] != "started" {
		t.Fatalf("idea event: %v", evs)
	}
	// Multipart task creation takes idea_id too.
	code, mt := c.multipart("/api/v1/tasks", part{field: "project_id", data: []byte("demo")}, part{field: "prompt", data: []byte("with a file")},
		part{field: "idea_id", data: []byte(fmt.Sprint(idOf(b)))}, part{field: "files[]", name: "shot.png", data: pngBytes(10, 10)})
	if code != 201 {
		t.Fatalf("multipart task from idea: %d %v", code, mt)
	}

	// Archive, then delete for good (only archived ones).
	path := fmt.Sprintf("/api/v1/ideas/%d", idOf(v))
	if code, _ := c.do("DELETE", path, nil); code != 409 {
		t.Fatalf("delete live idea: %d", code)
	}
	if code, ar := c.do("POST", path+"/archive", nil); code != 200 || ar["archived_at"] == nil {
		t.Fatalf("archive: %d %v", code, ar)
	}
	if _, l := c.do("GET", "/api/v1/ideas?archived=true", nil); len(l["items"].([]any)) != 1 {
		t.Fatalf("archive list: %v", l)
	}
	if code, _ := c.do("DELETE", path, nil); code != 204 {
		t.Fatalf("delete archived: %d", code)
	}
	if code, _ := c.do("GET", path, nil); code != 404 {
		t.Fatalf("deleted idea: %d", code)
	}
}
