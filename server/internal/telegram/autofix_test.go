package telegram

import (
	"strings"
	"testing"

	"github.com/andrewsl105/fleetpocket/server/internal/store"
)

func TestRenderAutoFix(t *testing.T) {
	task := &store.Task{ID: 7}
	cases := map[string]string{
		`{"phase":"started","attempt":1,"max_attempts":2,"signature":"ab12","checks":[{"name":"test","url":"https://x/1"}]}`: `CI auto-fix attempt 1/2`,
		`{"phase":"green","fixes":1}`: "CI green after 1 fix",
		`{"phase":"needs_you","reason":"the same failure repeated after a fix","checks":[{"name":"lint","url":"https://x/2"}]}`: `Logs: <a href="https://x/2">lint</a>`,
		`{"phase":"cancelled","reason":"the PR was closed"}`:                                                                    "the PR was closed",
	}
	for in, want := range cases {
		if got := renderAutoFix(task, []byte(in)); !strings.Contains(got, want) || !strings.Contains(got, "#7") {
			t.Errorf("%s: %q lacks %q", in, got, want)
		}
	}
	if renderAutoFix(task, []byte(`{"phase":"unknown"}`)) != "" {
		t.Error("unknown phase notified")
	}
}
