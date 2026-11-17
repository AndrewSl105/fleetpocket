package api_test

import (
	"fmt"
	"testing"
	"time"

	"github.com/pquerna/otp/totp"
)

func TestSchedulesAPI(t *testing.T) {
	c, st, secret := setup(t)
	otp, _ := totp.GenerateCode(secret, time.Now())
	if code, _ := c.do("POST", "/api/v1/auth/login", map[string]string{"password": "pw-for-tests", "totp": otp}); code != 200 {
		t.Fatal("login")
	}
	c.do("POST", "/api/v1/projects", map[string]any{"id": "demo", "name": "Demo"})
	c.do("POST", "/api/v1/projects/demo/repositories", map[string]any{"full_name": "o/r", "installation_id": 1, "is_default": true})

	if code, body := c.do("GET", "/api/v1/schedules", nil); code != 200 || len(body["items"].([]any)) != 0 {
		t.Fatalf("empty list: %d %v", code, body)
	}
	if code, body := c.do("POST", "/api/v1/schedules", map[string]any{"name": "x", "project_id": "demo", "prompt": "p", "cron": "nope"}); code != 400 {
		t.Fatalf("bad cron: %d %v", code, body)
	}
	code, sc := c.do("POST", "/api/v1/schedules", map[string]any{
		"name": "Weekday lint", "project_id": "demo", "prompt": "Fix lint warnings", "cron": "0 9 * * 1-5", "timezone": "America/New_York",
		"skills": []string{"go"}, "mcp_servers": []string{"linear"}, "monthly_budget_usd": 5, "run_budget_usd": 1,
		"concurrency": "queue", "pr_only_if_changes": false,
		"loop": map[string]any{"check_command": "make lint", "max_iterations": 2},
	})
	if code != 201 || sc["next_run_at"] == nil || sc["concurrency"] != "queue" || sc["pr_only_if_changes"] != false || sc["busy"] != false {
		t.Fatalf("create: %d %v", code, sc)
	}
	id := int64(sc["id"].(float64))
	path := fmt.Sprintf("/api/v1/schedules/%d", id)

	code, f := c.do("POST", path+"/run", nil)
	if code != 201 || f["outcome"] != "started" || f["trigger"] != "manual" || f["task_id"] == nil {
		t.Fatalf("run: %d %v", code, f)
	}
	_, task := c.do("GET", fmt.Sprintf("/api/v1/tasks/%d", int64(f["task_id"].(float64))), nil)
	if task["source"] != "schedule" || task["schedule_id"] != float64(id) || task["loop"] == nil || task["loop_state"] != "running" ||
		task["run_budget_usd"] != 1.0 || task["mcp_servers"].([]any)[0] != "linear" {
		t.Fatalf("task: %v", task)
	}
	if code, body := c.do("GET", path+"/firings", nil); code != 200 || len(body["items"].([]any)) != 1 {
		t.Fatalf("firings: %d %v", code, body)
	}
	if code, body := c.do("POST", path+"/disable", nil); code != 200 || body["enabled"] != false || body["next_run_at"] != nil {
		t.Fatalf("disable: %d %v", code, body)
	}
	if code, body := c.do("POST", path+"/enable", nil); code != 200 || body["enabled"] != true || body["next_run_at"] == nil {
		t.Fatalf("enable: %d %v", code, body)
	}
	code, dup := c.do("POST", path+"/duplicate", nil)
	if code != 201 || dup["name"] != "Weekday lint (copy)" || dup["enabled"] != false {
		t.Fatalf("duplicate: %d %v", code, dup)
	}
	code, upd := c.do("PUT", path, map[string]any{"name": "Daily lint", "project_id": "demo", "prompt": "Fix lint", "cron": "@daily", "timezone": "UTC"})
	if code != 200 || upd["name"] != "Daily lint" || upd["loop"] != nil || upd["concurrency"] != "skip" {
		t.Fatalf("update: %d %v", code, upd)
	}
	_, list := c.do("GET", "/api/v1/schedules", nil)
	if items := list["items"].([]any); len(items) != 2 || items[0].(map[string]any)["busy"] != true {
		t.Fatalf("list: %v", list)
	}
	if code, _ := c.do("DELETE", path, nil); code != 200 {
		t.Fatalf("delete: %d", code)
	}
	if code, _ := c.do("GET", path, nil); code != 404 {
		t.Fatalf("deleted: %d", code)
	}
	// A loop on a plain task.
	code, lt := c.do("POST", "/api/v1/tasks", map[string]any{"project_id": "demo", "prompt": "make tests pass", "loop": map[string]any{"check_command": "npm test"}})
	if code != 201 || lt["loop"].(map[string]any)["max_iterations"] != 3.0 || lt["loop_iteration"] != 1.0 {
		t.Fatalf("loop task: %d %v", code, lt)
	}
	if code, _ := c.do("POST", "/api/v1/tasks", map[string]any{"project_id": "demo", "prompt": "x", "loop": map[string]any{"check_command": "x", "max_iterations": 11}}); code != 400 {
		t.Fatalf("max_iterations 11: %d", code)
	}
	_ = st
}
