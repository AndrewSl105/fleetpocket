package costs

import (
	"testing"
	"time"
)

func TestAggregate(t *testing.T) {
	loc := time.UTC
	day := func(d, h int) time.Time { return time.Date(2026, 11, d, h, 0, 0, 0, loc) }
	runs := []RunCost{
		{StartedAt: day(2, 10), ProjectID: "a", Agent: "claude", CostUSD: 1.25, Source: SourceReported, Auth: AuthSubscription, InputTokens: 100, OutputTokens: 10},
		{StartedAt: day(2, 11), ProjectID: "a", Agent: "claude", CostUSD: 0.5, Source: SourceReported, AutoFix: true},
		{StartedAt: day(3, 9), ProjectID: "b", Agent: "codex", CostUSD: 2, Source: SourceEstimated, InputTokens: 1000},
		{StartedAt: day(9, 9), ProjectID: "b", Agent: "cursor", Source: SourceUnknown, InputTokens: 5},
		{StartedAt: day(20, 0), ProjectID: "a", Agent: "claude", CostUSD: 99}, // outside [from, to)
	}
	from, to := day(1, 0), day(10, 0)
	rep := Aggregate(runs, from, to, "day", loc)

	tot := rep.Total
	if tot.CostUSD != 3.75 || tot.EstimatedUSD != 2 || tot.AutoFixUSD != 0.5 || tot.APIEquivalentUSD != 1.25 ||
		tot.Runs != 4 || tot.UnknownRuns != 1 || tot.InputTokens != 1105 || tot.OutputTokens != 10 {
		t.Fatalf("total = %+v", tot)
	}
	if len(rep.ByProject) != 2 || rep.ByProject[0].Key != "b" || rep.ByProject[0].CostUSD != 2 || rep.ByProject[1].CostUSD != 1.75 {
		t.Fatalf("by project = %+v", rep.ByProject)
	}
	if len(rep.ByAgent) != 3 || rep.ByAgent[0].Key != "codex" || rep.ByAgent[2].Key != "cursor" {
		t.Fatalf("by agent = %+v", rep.ByAgent)
	}
	if len(rep.ByPeriod) != 9 || rep.ByPeriod[0].Start != "2026-11-01" || rep.ByPeriod[1].CostUSD != 1.75 ||
		rep.ByPeriod[2].CostUSD != 2 || rep.ByPeriod[0].Runs != 0 {
		t.Fatalf("by day = %+v", rep.ByPeriod)
	}
	if len(rep.Rows) != 3 || rep.Rows[0].Day != "2026-11-02" || rep.Rows[0].Runs != 2 || rep.Rows[2].Agent != "cursor" {
		t.Fatalf("rows = %+v", rep.Rows)
	}

	// Weeks start on Monday (2026-11-02); months on the 1st.
	wk := Aggregate(runs, from, to, "week", loc)
	if len(wk.ByPeriod) != 3 || wk.ByPeriod[0].Start != "2026-10-26" || wk.ByPeriod[1].CostUSD != 3.75 || wk.ByPeriod[2].Runs != 1 {
		t.Fatalf("by week = %+v", wk.ByPeriod)
	}
	mo := Aggregate(runs, from, to, "month", loc)
	if len(mo.ByPeriod) != 1 || mo.ByPeriod[0].CostUSD != 3.75 {
		t.Fatalf("by month = %+v", mo.ByPeriod)
	}
}

func TestPeriodStartInLocation(t *testing.T) {
	kyiv := time.FixedZone("EET", 2*3600)
	// 23:30 UTC on the 31st is already the 1st in UTC+2.
	ts := time.Date(2026, 10, 31, 23, 30, 0, 0, time.UTC)
	if got := PeriodStart(ts, "month", kyiv).Format("2006-01-02"); got != "2026-11-01" {
		t.Fatalf("month in UTC+2 = %s", got)
	}
}
