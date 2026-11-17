package costs

import (
	"testing"
	"time"
)

func TestEstimate(t *testing.T) {
	p := Price{Agent: "codex", Model: "m", InputUSD: 2, CachedInputUSD: f(0.2), OutputUSD: 12}
	// 1M input of which 400k cached: 600k × 2 + 400k × 0.2 = 1.28; 100k out × 12 = 1.2.
	got := p.Estimate(Tokens{Input: 1_000_000, CacheRead: 400_000, Output: 100_000})
	if got != 2.48 {
		t.Fatalf("estimate = %v, want 2.48", got)
	}
	// Cache rates default to the input rate.
	q := Price{InputUSD: 1, OutputUSD: 1}
	if got := q.Estimate(Tokens{Input: 500_000, CacheRead: 200_000, CacheWrite: 100_000, Output: 500_000}); got != 1 {
		t.Fatalf("estimate = %v, want 1", got)
	}
	// Inconsistent counts never go negative.
	if got := q.Estimate(Tokens{Input: 10, CacheRead: 100}); got < 0 {
		t.Fatalf("negative estimate %v", got)
	}
}

func TestFind(t *testing.T) {
	tbl := PriceTable{Prices: []Price{
		{Agent: "codex", Model: "*", InputUSD: 1},
		{Agent: "codex", Model: "gpt-x", InputUSD: 2},
		{Agent: "cursor", Model: "composer", InputUSD: 3},
	}}
	if p, ok := tbl.Find("codex", "GPT-X"); !ok || p.InputUSD != 2 {
		t.Fatalf("exact: %+v %v", p, ok)
	}
	if p, ok := tbl.Find("codex", "default"); !ok || p.InputUSD != 1 {
		t.Fatalf("wildcard: %+v %v", p, ok)
	}
	if _, ok := tbl.Find("cursor", "auto"); ok {
		t.Fatal("cursor auto has no price")
	}
	if _, ok := DefaultPrices.Find("codex", "default"); ok {
		t.Fatal("the default table prices no Codex default model")
	}
	if _, ok := DefaultPrices.Find("codex", "gpt-5.5"); !ok {
		t.Fatal("gpt-5.5 is in the default table")
	}
}

func TestValidate(t *testing.T) {
	if err := DefaultPrices.Validate(); err != nil {
		t.Fatal(err)
	}
	bad := []PriceTable{
		{Updated: "17.11.2026"},
		{Prices: []Price{{Agent: "gpt", Model: "x"}}},
		{Prices: []Price{{Agent: "codex", Model: " "}}},
		{Prices: []Price{{Agent: "codex", Model: "x", InputUSD: -1}}},
		{Prices: []Price{{Agent: "codex", Model: "x"}, {Agent: "codex", Model: "X"}}},
	}
	for i, tbl := range bad {
		if tbl.Validate() == nil {
			t.Errorf("table %d: no error", i)
		}
	}
}

func TestLevelAndMonth(t *testing.T) {
	cases := []struct {
		spent, limit float64
		want         int
	}{{10, 0, 0}, {79.99, 100, 0}, {80, 100, 80}, {99, 100, 80}, {100, 100, 100}, {150, 100, 100}}
	for _, c := range cases {
		if got := Level(c.spent, c.limit); got != c.want {
			t.Errorf("Level(%v, %v) = %d, want %d", c.spent, c.limit, got, c.want)
		}
	}
	loc := time.FixedZone("x", 3*3600)
	now := time.Date(2026, 11, 17, 1, 2, 3, 0, loc)
	if m := MonthStart(now); !m.Equal(time.Date(2026, 11, 1, 0, 0, 0, 0, loc)) {
		t.Fatalf("MonthStart = %v", m)
	}
}

func TestFormat(t *testing.T) {
	cases := []struct {
		usd          float64
		est, unknown bool
		want         string
	}{
		{0.84, false, false, "$0.84"},
		{0.84, true, false, "≈$0.84"},
		{0, false, true, "$?"},
		{0.84, false, true, "$0.84+?"},
		{0.84, true, true, "≈$0.84+?"},
		{0, false, false, "$0.00"},
	}
	for _, c := range cases {
		if got := Format(c.usd, c.est, c.unknown); got != c.want {
			t.Errorf("Format(%v, %v, %v) = %q, want %q", c.usd, c.est, c.unknown, got, c.want)
		}
	}
}
