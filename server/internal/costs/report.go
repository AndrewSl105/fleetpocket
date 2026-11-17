package costs

import (
	"sort"
	"time"
)

// RunCost is one started run, as the cost report sees it.
type RunCost struct {
	StartedAt    time.Time
	ProjectID    string
	Agent        string
	CostUSD      float64
	Source       string // "" for runs from before sources were recorded: reported
	Auth         string
	AutoFix      bool
	InputTokens  int64
	OutputTokens int64
}

// Sum adds up runs. CostUSD includes EstimatedUSD; AutoFixUSD and
// APIEquivalentUSD are shares of CostUSD; UnknownRuns had tokens but no
// price.
type Sum struct {
	CostUSD          float64 `json:"cost_usd"`
	EstimatedUSD     float64 `json:"estimated_usd"`
	AutoFixUSD       float64 `json:"autofix_usd"`
	APIEquivalentUSD float64 `json:"api_equivalent_usd"`
	Runs             int     `json:"runs"`
	UnknownRuns      int     `json:"unknown_runs"`
	InputTokens      int64   `json:"input_tokens"`
	OutputTokens     int64   `json:"output_tokens"`
}

func (s *Sum) add(r RunCost) {
	s.CostUSD += r.CostUSD
	s.Runs++
	s.InputTokens += r.InputTokens
	s.OutputTokens += r.OutputTokens
	switch r.Source {
	case SourceEstimated:
		s.EstimatedUSD += r.CostUSD
	case SourceUnknown:
		s.UnknownRuns++
	}
	if r.AutoFix {
		s.AutoFixUSD += r.CostUSD
	}
	if r.Auth == AuthSubscription && r.Source != SourceEstimated {
		s.APIEquivalentUSD += r.CostUSD
	}
}

func (s *Sum) round() {
	for _, v := range []*float64{&s.CostUSD, &s.EstimatedUSD, &s.AutoFixUSD, &s.APIEquivalentUSD} {
		*v = roundUSD(*v)
	}
}

func roundUSD(v float64) float64 {
	if v < 0 {
		return -roundUSD(-v)
	}
	return float64(int64(v*1e4+0.5)) / 1e4
}

// KeyedSum is a Sum for one project or agent.
type KeyedSum struct {
	Key string `json:"key"`
	Sum
}

// PeriodSum is a Sum for one day, week (starting Monday) or month.
type PeriodSum struct {
	Start string `json:"start"` // YYYY-MM-DD
	Sum
}

// Row is the finest breakdown: one day, project and agent (the CSV export).
type Row struct {
	Day     string `json:"day"`
	Project string `json:"project_id"`
	Agent   string `json:"agent"`
	Sum
}

// Report is the cost report of [From, To).
type Report struct {
	Period    string      `json:"period"`
	Total     Sum         `json:"total"`
	ByProject []KeyedSum  `json:"by_project"`
	ByAgent   []KeyedSum  `json:"by_agent"`
	ByPeriod  []PeriodSum `json:"by_period"`
	Rows      []Row       `json:"rows"`
}

// Periods are the bucket sizes of Report.ByPeriod.
var Periods = []string{"day", "week", "month"}

// PeriodStart is the start of t's day, week (Monday) or month in loc.
func PeriodStart(t time.Time, period string, loc *time.Location) time.Time {
	t = t.In(loc)
	d := time.Date(t.Year(), t.Month(), t.Day(), 0, 0, 0, 0, loc)
	switch period {
	case "week":
		return d.AddDate(0, 0, -((int(d.Weekday()) + 6) % 7))
	case "month":
		return MonthStart(d)
	}
	return d
}

func nextPeriod(t time.Time, period string) time.Time {
	switch period {
	case "week":
		return t.AddDate(0, 0, 7)
	case "month":
		return t.AddDate(0, 1, 0)
	}
	return t.AddDate(0, 0, 1)
}

// Aggregate builds the report of runs started in [from, to), bucketed in
// loc. Every period from from's to the last one before to is listed, empty
// ones included; projects and agents are sorted by cost, highest first.
func Aggregate(runs []RunCost, from, to time.Time, period string, loc *time.Location) Report {
	rep := Report{Period: period, ByProject: []KeyedSum{}, ByAgent: []KeyedSum{}, ByPeriod: []PeriodSum{}, Rows: []Row{}}
	idx := map[string]int{}
	for p := PeriodStart(from, period, loc); p.Before(to); p = nextPeriod(p, period) {
		idx[p.Format("2006-01-02")] = len(rep.ByPeriod)
		rep.ByPeriod = append(rep.ByPeriod, PeriodSum{Start: p.Format("2006-01-02")})
	}
	projects, agents, rows := map[string]*Sum{}, map[string]*Sum{}, map[[3]string]*Sum{}
	get := func(m map[string]*Sum, k string) *Sum {
		if m[k] == nil {
			m[k] = &Sum{}
		}
		return m[k]
	}
	for _, r := range runs {
		if r.StartedAt.Before(from) || !r.StartedAt.Before(to) {
			continue
		}
		rep.Total.add(r)
		get(projects, r.ProjectID).add(r)
		get(agents, r.Agent).add(r)
		if i, ok := idx[PeriodStart(r.StartedAt, period, loc).Format("2006-01-02")]; ok {
			rep.ByPeriod[i].add(r)
		}
		k := [3]string{PeriodStart(r.StartedAt, "day", loc).Format("2006-01-02"), r.ProjectID, r.Agent}
		if rows[k] == nil {
			rows[k] = &Sum{}
		}
		rows[k].add(r)
	}
	rep.Total.round()
	keyed := func(m map[string]*Sum) []KeyedSum {
		out := []KeyedSum{}
		for k, s := range m {
			s.round()
			out = append(out, KeyedSum{Key: k, Sum: *s})
		}
		sort.Slice(out, func(i, j int) bool {
			if out[i].CostUSD != out[j].CostUSD {
				return out[i].CostUSD > out[j].CostUSD
			}
			return out[i].Key < out[j].Key
		})
		return out
	}
	rep.ByProject, rep.ByAgent = keyed(projects), keyed(agents)
	for i := range rep.ByPeriod {
		rep.ByPeriod[i].round()
	}
	for k, s := range rows {
		s.round()
		rep.Rows = append(rep.Rows, Row{Day: k[0], Project: k[1], Agent: k[2], Sum: *s})
	}
	sort.Slice(rep.Rows, func(i, j int) bool {
		a, b := rep.Rows[i], rep.Rows[j]
		if a.Day != b.Day {
			return a.Day < b.Day
		}
		if a.Project != b.Project {
			return a.Project < b.Project
		}
		return a.Agent < b.Agent
	})
	return rep
}
