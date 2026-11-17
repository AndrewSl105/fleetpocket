package api_test

import (
	"encoding/json"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/pquerna/otp/totp"

	"github.com/andrewsl105/fleetpocket/server/internal/store"
)

func TestCheckpointsAPI(t *testing.T) {
	c, st, secret := setup(t)
	ctx := t.Context()
	otp, _ := totp.GenerateCode(secret, time.Now())
	if code, _ := c.do("POST", "/api/v1/auth/login", map[string]string{"password": "pw-for-tests", "totp": otp}); code != 200 {
		t.Fatal("login")
	}
	// Off by default.
	if code, body := c.do("GET", "/api/v1/settings/checkpoints", nil); code != 200 || body["enabled"] != false {
		t.Fatalf("default: %d %v", code, body)
	}
	if code, body := c.do("PUT", "/api/v1/settings/checkpoints", map[string]any{"enabled": true}); code != 200 || body["enabled"] != true {
		t.Fatalf("put: %d %v", code, body)
	}

	c.do("POST", "/api/v1/projects", map[string]any{"id": "demo", "name": "Demo"})
	c.do("POST", "/api/v1/projects/demo/repositories", map[string]any{"full_name": "o/r", "installation_id": 1, "is_default": true})
	_, task := c.do("POST", "/api/v1/tasks", map[string]any{"project_id": "demo", "prompt": "add a table"})
	id := int64(task["id"].(float64))
	run, _ := st.PendingRun(ctx, id)
	_ = st.MarkRunStarting(ctx, run.ID)
	_, _ = st.SetStatus(ctx, id, nil, "running", "", false)
	raw := json.RawMessage(`{"type":"checkpoint","n":1,"ref_sha":"abc","turn":2,"session_ref":"s/u","files_changed":1,"reason":"turn","ts":"2026-11-17T10:00:00Z","seq":5}`)
	ev, _ := st.AddRunnerEvent(ctx, id, run.ID, 5, "checkpoint", raw)
	cp, _ := st.AddCheckpoint(ctx, store.Checkpoint{TaskID: id, RunID: run.ID, EventID: &ev.ID, N: 1, RefSHA: "abc", Turn: 2, SessionRef: "s/u", FilesChanged: 1})
	_, _ = st.AddRunnerEvent(ctx, id, run.ID, 6, "tool_use", json.RawMessage(`{"type":"tool_use","tool":"Edit","summary":"Edit a.go","ts":"2026-11-17T10:00:01Z","seq":6}`))

	code, body := c.do("GET", fmt.Sprintf("/api/v1/tasks/%d/checkpoints", id), nil)
	items := body["items"].([]any)
	if code != 200 || len(items) != 1 {
		t.Fatalf("list: %d %v", code, body)
	}
	item := items[0].(map[string]any)
	if item["available"] != false || item["reason"] != "turn" || !strings.Contains(item["unavailable_reason"].(string), "busy") || item["mode"] != "fork" || item["ref"] != fmt.Sprintf("refs/jm/checkpoints/%d/1", run.ID) {
		t.Fatalf("running task: %v", item)
	}
	if _, ok := item["session_ref"]; ok {
		t.Fatal("session_ref is internal")
	}
	path := fmt.Sprintf("/api/v1/tasks/%d/checkpoints/%d", id, cp.ID)
	if code, body := c.do("POST", path+"/restore", map[string]any{"instruction": "x"}); code != 409 {
		t.Fatalf("restore while running: %d %v", code, body)
	}

	_, _ = st.SetStatus(ctx, id, nil, "pr_open", "", false)
	code, prev := c.do("GET", path, nil)
	if code != 200 || prev["available"] != true || prev["discarded"].(map[string]any)["steps"] != 1.0 {
		t.Fatalf("preview: %d %v", code, prev)
	}
	code, view := c.do("POST", path+"/restore", map[string]any{"instruction": "use a migration instead"})
	if code != 200 || view["status"] != "queued" {
		t.Fatalf("restore: %d %v", code, view)
	}
	pending, _ := st.PendingRun(ctx, id)
	if pending.Kind != "restore" || !strings.Contains(pending.Input, "use a migration instead") || !strings.Contains(pending.Input, "rolled this task back") {
		t.Fatalf("pending: %+v", pending)
	}
	evs, _ := st.LastEvents(ctx, id, []string{"checkpoint_restored"}, 1)
	var p map[string]any
	_ = json.Unmarshal(evs[0].Payload, &p)
	if p["mode"] != "fork" || p["checkpoint_event_id"] != float64(ev.ID) || p["instruction"] != "use a migration instead" {
		t.Fatalf("event: %v", p)
	}

	// After WORKSPACE_RETENTION the checkpoints are gone: restore is disabled with a reason.
	_, _ = st.SetStatus(ctx, id, nil, "pr_open", "", false)
	_ = st.MarkCleaned(ctx, id)
	_, body = c.do("GET", fmt.Sprintf("/api/v1/tasks/%d/checkpoints", id), nil)
	item = body["items"].([]any)[0].(map[string]any)
	if item["available"] != false || !strings.Contains(item["unavailable_reason"].(string), "WORKSPACE_RETENTION") {
		t.Fatalf("cleaned: %v", item)
	}
	if code, body := c.do("POST", path+"/restore", map[string]any{}); code != 409 || !strings.Contains(fmt.Sprint(body), "WORKSPACE_RETENTION") {
		t.Fatalf("restore after retention: %d %v", code, body)
	}
}
