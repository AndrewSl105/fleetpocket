package telegram

import (
	"encoding/json"
	"fmt"
	"strings"

	"github.com/andrewsl105/fleetpocket/server/internal/store"
)

// autoFixPayload is an `autofix` event (docs/API.md, event types).
type autoFixPayload struct {
	Phase       string   `json:"phase"`
	Attempt     int      `json:"attempt"`
	MaxAttempts int      `json:"max_attempts"`
	Fixes       int      `json:"fixes"`
	Reason      string   `json:"reason"`
	Reasons     []string `json:"reasons"`
	Signature   string   `json:"signature"`
	Checks      []struct {
		Name string `json:"name"`
		URL  string `json:"url"`
	} `json:"checks"`
}

// renderAutoFix is the notification for one automatic CI auto-fix step; ""
// for phases that send none.
func renderAutoFix(t *store.Task, raw json.RawMessage) string {
	var p autoFixPayload
	if json.Unmarshal(raw, &p) != nil {
		return ""
	}
	checks := func() string {
		var parts []string
		for _, c := range p.Checks {
			name := esc(cut(c.Name, 80))
			if c.URL != "" {
				name = fmt.Sprintf("<a href=\"%s\">%s</a>", esc(c.URL), name)
			}
			parts = append(parts, name)
		}
		return strings.Join(parts, ", ")
	}
	head := func(icon, text string) string { return fmt.Sprintf("%s <b>#%d · %s</b>", icon, t.ID, text) }
	switch p.Phase {
	case "started":
		s := head("🔧", fmt.Sprintf("CI auto-fix attempt %d/%d", p.Attempt, p.MaxAttempts))
		if c := checks(); c != "" {
			s += "\nFailing: " + c
		}
		if p.Signature != "" {
			s += "\nSignature <code>" + esc(p.Signature) + "</code>"
		}
		return s
	case "pushed":
		return head("⬆️", fmt.Sprintf("Auto-fix %d/%d pushed", p.Attempt, p.MaxAttempts)) + "\nWaiting for CI."
	case "green":
		return head("✅", fmt.Sprintf("CI green after %d %s", p.Fixes, plural(p.Fixes, "fix", "fixes")))
	case "needs_you":
		s := head("🙋", "CI auto-fix needs you") + "\n" + esc(cut(p.Reason, 300))
		if c := checks(); c != "" {
			s += "\nLogs: " + c
		}
		return s
	case "held":
		return head("🙋", "The fix changes CI or tests") + "\n" + esc(cut("- "+strings.Join(p.Reasons, "\n- "), 600)) +
			"\nAnswer the question to push or discard it."
	case "discarded":
		return head("🗑", "Auto-fix discarded")
	case "cancelled":
		return head("⏹", "CI auto-fix cancelled") + "\n" + esc(cut(p.Reason, 200))
	}
	return ""
}

func plural(n int, one, many string) string {
	if n == 1 {
		return one
	}
	return many
}
