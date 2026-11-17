// Package costs holds the cost rules that need no database: the price table
// used to estimate runs of agents that report tokens but no cost, the
// calendar month that monthly budgets cover, and budget thresholds
// (docs/SPEC-server.md section "Costs and budgets").
package costs

import (
	"fmt"
	"math"
	"strings"
	"time"
)

// Where a run's cost_usd came from.
const (
	SourceReported  = "reported"  // the agent reported it (Claude)
	SourceEstimated = "estimated" // tokens × the price table
	SourceUnknown   = "unknown"   // tokens only, no price for the model
)

// How the agent was authenticated (runs.auth).
const (
	AuthAPIKey       = "api_key"
	AuthSubscription = "subscription" // Claude OAuth token: cost is API-equivalent
)

// Price is what one model costs, in USD per million tokens. CachedInputUSD
// and CacheWriteUSD default to InputUSD when nil. Model "*" matches any
// model of the agent that has no row of its own.
type Price struct {
	Agent          string   `json:"agent"`
	Model          string   `json:"model"`
	InputUSD       float64  `json:"input_usd"`
	CachedInputUSD *float64 `json:"cached_input_usd"`
	CacheWriteUSD  *float64 `json:"cache_write_usd"`
	OutputUSD      float64  `json:"output_usd"`
}

// PriceTable is the owner-editable table; Updated is the date its numbers
// were last checked (YYYY-MM-DD).
type PriceTable struct {
	Updated string  `json:"updated"`
	Prices  []Price `json:"prices"`
}

func f(v float64) *float64 { return &v }

// DefaultPrices is the prefilled table: OpenAI's standard-tier API prices
// for the models Codex runs (developers.openai.com/api/docs/pricing,
// ≤272K-token context), checked on Updated. Codex's own default model
// ("default") and Cursor's "auto" route to different models, so they have
// no row: their runs stay "unknown" unless the owner adds one.
var DefaultPrices = PriceTable{
	Updated: "2026-11-17",
	Prices: []Price{
		{Agent: "codex", Model: "gpt-5.6-sol", InputUSD: 4, CachedInputUSD: f(0.4), OutputUSD: 20},
		{Agent: "codex", Model: "gpt-5.6-terra", InputUSD: 2, CachedInputUSD: f(0.2), OutputUSD: 12},
		{Agent: "codex", Model: "gpt-5.6-luna", InputUSD: 0.2, CachedInputUSD: f(0.02), OutputUSD: 1.2},
		{Agent: "codex", Model: "gpt-5.5", InputUSD: 5, CachedInputUSD: f(0.5), OutputUSD: 30},
		{Agent: "codex", Model: "gpt-5.4", InputUSD: 2.5, CachedInputUSD: f(0.25), OutputUSD: 15},
		{Agent: "codex", Model: "gpt-5.3-codex", InputUSD: 1.75, CachedInputUSD: f(0.175), OutputUSD: 14},
	},
}

// Find returns the price of an agent's model: its own row, else the
// agent's "*" row.
func (t PriceTable) Find(agent, model string) (Price, bool) {
	var wild *Price
	for i, p := range t.Prices {
		if p.Agent != agent {
			continue
		}
		if strings.EqualFold(p.Model, model) {
			return p, true
		}
		if p.Model == "*" && wild == nil {
			wild = &t.Prices[i]
		}
	}
	if wild != nil {
		return *wild, true
	}
	return Price{}, false
}

// Validate checks an owner-edited table.
func (t PriceTable) Validate() error {
	if t.Updated != "" {
		if _, err := time.Parse("2006-01-02", t.Updated); err != nil {
			return fmt.Errorf("updated: want YYYY-MM-DD")
		}
	}
	seen := map[string]bool{}
	for i, p := range t.Prices {
		switch p.Agent {
		case "claude", "codex", "cursor":
		default:
			return fmt.Errorf("prices[%d].agent: want claude, codex or cursor", i)
		}
		if strings.TrimSpace(p.Model) == "" {
			return fmt.Errorf("prices[%d].model: required (\"*\" for any)", i)
		}
		bad := func(v float64) bool { return v < 0 || math.IsNaN(v) || math.IsInf(v, 0) }
		if bad(p.InputUSD) || bad(p.OutputUSD) || p.CachedInputUSD != nil && bad(*p.CachedInputUSD) || p.CacheWriteUSD != nil && bad(*p.CacheWriteUSD) {
			return fmt.Errorf("prices[%d]: prices must be numbers ≥ 0", i)
		}
		key := p.Agent + "\x00" + strings.ToLower(p.Model)
		if seen[key] {
			return fmt.Errorf("prices[%d]: %s %s is listed twice", i, p.Agent, p.Model)
		}
		seen[key] = true
	}
	return nil
}

// Tokens a run used. Input includes CacheRead and CacheWrite.
type Tokens struct {
	Input, Output, CacheRead, CacheWrite int64
}

// Estimate is the cost of tokens at price p.
func (p Price) Estimate(t Tokens) float64 {
	cached, write := p.InputUSD, p.InputUSD
	if p.CachedInputUSD != nil {
		cached = *p.CachedInputUSD
	}
	if p.CacheWriteUSD != nil {
		write = *p.CacheWriteUSD
	}
	plain := t.Input - t.CacheRead - t.CacheWrite
	if plain < 0 {
		plain = 0
	}
	usd := (float64(plain)*p.InputUSD + float64(t.CacheRead)*cached + float64(t.CacheWrite)*write + float64(t.Output)*p.OutputUSD) / 1e6
	return math.Round(usd*1e4) / 1e4 // runs.cost_usd is numeric(10,4)
}

// ReportsCost tells whether an agent reports its own cost (Claude); the
// others report tokens only (docs/PROTOCOL.md, "Agents").
func ReportsCost(agent string) bool { return agent == "" || agent == "claude" }

// MonthStart is the first instant of now's calendar month in now's location
// (the server's local time, as for GET /stats). Monthly budgets, alerts and
// the month-to-date numbers all use it.
func MonthStart(now time.Time) time.Time {
	return time.Date(now.Year(), now.Month(), 1, 0, 0, 0, 0, now.Location())
}

// Budget scopes. A schedule scope (PR 5) adds a kind here; the gate checks
// every scope of a task.
const (
	ScopeGlobal  = "global"
	ScopeProject = "project"
)

// Alert levels, in percent of a cap.
const (
	LevelWarning = 80
	LevelReached = 100
)

// Level is the highest alert level spent has reached against limit (0:
// none, or no cap).
func Level(spent, limit float64) int {
	switch {
	case limit <= 0:
		return 0
	case spent >= limit:
		return LevelReached
	case spent >= limit*LevelWarning/100:
		return LevelWarning
	}
	return 0
}

// Format shows a cost as $0.84, or ≈$0.84 when part of it is estimated.
func Format(usd float64, estimated bool) string {
	s := fmt.Sprintf("$%.2f", usd)
	if estimated {
		return "≈" + s
	}
	return s
}
