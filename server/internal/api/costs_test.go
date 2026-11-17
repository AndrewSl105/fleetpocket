package api_test

import (
	"strconv"
	"testing"
	"time"

	"github.com/pquerna/otp/totp"

	"github.com/andrewsl105/fleetpocket/server/internal/costs"
	"github.com/andrewsl105/fleetpocket/server/internal/store"
)

func TestCostsAPI(t *testing.T) {
	c, st, secret := setup(t)
	otp, _ := totp.GenerateCode(secret, time.Now())
	if code, _ := c.do("POST", "/api/v1/auth/login", map[string]string{"password": "pw-for-tests", "totp": otp}); code != 200 {
		t.Fatal("login")
	}
	// No caps by default; the built-in price table.
	code, body := c.do("GET", "/api/v1/settings/costs", nil)
	if code != 200 || body["monthly_budget_usd"] != nil || body["price_table_is_default"] != true {
		t.Fatalf("default settings: %d %v", code, body)
	}
	if body["price_table"].(map[string]any)["updated"] != costs.DefaultPrices.Updated {
		t.Fatalf("price table: %v", body["price_table"])
	}
	if code, _ := c.do("PUT", "/api/v1/settings/costs", map[string]any{"price_table": map[string]any{"prices": []any{map[string]any{"agent": "gpt", "model": "x"}}}}); code != 400 {
		t.Fatalf("bad agent accepted: %d", code)
	}
	code, body = c.do("PUT", "/api/v1/settings/costs", map[string]any{"monthly_budget_usd": 50,
		"price_table": map[string]any{"updated": "2026-11-17", "prices": []any{map[string]any{"agent": "cursor", "model": "*", "input_usd": 1, "output_usd": 2}}}})
	if code != 200 || body["monthly_budget_usd"] != 50.0 || body["price_table_is_default"] != false {
		t.Fatalf("put: %d %v", code, body)
	}

	// A project with a cap and one run of cost.
	c.do("POST", "/api/v1/projects", map[string]any{"id": "demo", "name": "Demo"})
	c.do("POST", "/api/v1/projects/demo/repositories", map[string]any{"full_name": "o/r", "installation_id": 1, "is_default": true})
	if code, p := c.do("PATCH", "/api/v1/projects/demo", map[string]any{"budget": map[string]any{"monthly_usd": -1}}); code != 400 {
		t.Fatalf("negative cap: %d %v", code, p)
	}
	code, p := c.do("PATCH", "/api/v1/projects/demo", map[string]any{"budget": map[string]any{"monthly_usd": 20}})
	if code != 200 || p["budget"].(map[string]any)["monthly_usd"] != 20.0 {
		t.Fatalf("cap: %d %v", code, p)
	}
	code, task := c.do("POST", "/api/v1/tasks", map[string]any{"project_id": "demo", "prompt": "work"})
	if code != 201 {
		t.Fatalf("task: %d %v", code, task)
	}
	id := int64(task["id"].(float64))
	run, _ := st.PendingRun(t.Context(), id)
	_ = st.MarkRunStarting(t.Context(), run.ID)
	_ = st.SetRunAgent(t.Context(), run.ID, "claude", "claude-x", costs.AuthSubscription)
	if _, err := st.SetRunUsage(t.Context(), run.ID, store.RunUsage{CostUSD: 1.5, Source: costs.SourceReported, TokensKnown: true, Tokens: costs.Tokens{Input: 10, Output: 5}}); err != nil {
		t.Fatal(err)
	}

	_, list := c.do("GET", "/api/v1/projects", nil)
	if got := list["items"].([]any)[0].(map[string]any)["month_cost_usd"]; got != 1.5 {
		t.Fatalf("month cost on the project card: %v", got)
	}
	code, rep := c.do("GET", "/api/v1/costs", nil)
	if code != 200 || rep["total"].(map[string]any)["cost_usd"] != 1.5 || rep["total"].(map[string]any)["api_equivalent_usd"] != 1.5 {
		t.Fatalf("report: %d %v", code, rep)
	}
	if b := rep["budget"].(map[string]any); b["monthly_usd"] != 50.0 || b["month_cost_usd"] != 1.5 {
		t.Fatalf("budget: %v", b)
	}
	if ps := rep["projects"].([]any); len(ps) != 1 || ps[0].(map[string]any)["monthly_usd"] != 20.0 {
		t.Fatalf("projects: %v", ps)
	}
	if rows := rep["rows"].([]any); len(rows) != 1 || rows[0].(map[string]any)["agent"] != "claude" {
		t.Fatalf("rows: %v", rows)
	}
	if code, _ := c.do("GET", "/api/v1/costs?period=year", nil); code != 400 {
		t.Fatalf("bad period: %d", code)
	}
	if code, _ := c.do("GET", "/api/v1/costs?from=2026-11-20&to=2026-11-01", nil); code != 400 {
		t.Fatalf("reversed range: %d", code)
	}
	_, tk := c.do("GET", "/api/v1/tasks/"+itoa(id), nil)
	if tk["cost_api_equivalent"] != true || tk["cost_estimated"] != false || tk["budget_wait"] != nil {
		t.Fatalf("task: %v", tk)
	}

	// Override: a queued task can start anyway; anything else is a conflict.
	code, ov := c.do("POST", "/api/v1/tasks/"+itoa(id)+"/budget-override", nil)
	if code != 200 || ov["budget_override_at"] == nil {
		t.Fatalf("override: %d %v", code, ov)
	}
	_, _ = st.SetStatus(t.Context(), id, nil, "completed", "", true)
	if code, _ := c.do("POST", "/api/v1/tasks/"+itoa(id)+"/budget-override", nil); code != 409 {
		t.Fatalf("override of a completed task: %d", code)
	}
}

func itoa(n int64) string { return strconv.FormatInt(n, 10) }
