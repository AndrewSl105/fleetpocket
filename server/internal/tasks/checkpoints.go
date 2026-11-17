package tasks

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/andrewsl105/fleetpocket/server/internal/store"
)

// RunRestore is a run that starts from a checkpoint (SPEC-server, "Checkpoints").
const RunRestore = "restore"

// Restore modes: how the agent's conversation continues.
const (
	// RestoreFork resumes the session at the checkpoint as a new fork (Claude).
	RestoreFork = "fork"
	// RestoreSummary starts a new session seeded with a summary of the
	// timeline up to the checkpoint (Codex, Cursor, or no session_ref).
	RestoreSummary = "summary"
)

// ErrCheckpointGone is returned when the checkpoint commit is no longer in
// the workspace (workspace.ErrCheckpointGone, mapped by CheckpointDiff).
var ErrCheckpointGone = errors.New("checkpoint is no longer available")

// CheckpointDiffFunc returns `git diff <checkpoint> HEAD` from the task's
// host (what a restore discards); a missing checkpoint wraps ErrCheckpointGone.
type CheckpointDiffFunc func(ctx context.Context, t *store.Task, ref, sha string, limit int) (string, bool, error)

// CheckpointView is a checkpoint with whether it can be restored now.
type CheckpointView struct {
	*store.Checkpoint
	Ref       string `json:"ref"`
	Mode      string `json:"mode"`
	Available bool   `json:"available"`
	// Reason says why it cannot be restored ("" when available).
	Unavailable string `json:"unavailable_reason,omitempty"`
}

// Discarded counts what a restore drops: everything after the checkpoint.
type Discarded struct {
	Runs     int `json:"runs"`
	Steps    int `json:"steps"`
	Messages int `json:"messages"`
	Commits  int `json:"commits"`
}

// CheckpointPreview is what the restore dialog shows.
type CheckpointPreview struct {
	CheckpointView
	Discarded Discarded `json:"discarded"`
	// Diff is `git diff <checkpoint> HEAD`: the file changes a restore discards.
	Diff      string `json:"diff"`
	Truncated bool   `json:"truncated"`
}

const maxCheckpointDiff = 512 << 10

func checkpointRef(c *store.Checkpoint) string {
	return fmt.Sprintf("refs/jm/checkpoints/%d/%d", c.RunID, c.N)
}

// RestoreMode is how a restore from c continues the conversation.
func RestoreMode(t *store.Task, c *store.Checkpoint) string {
	if (t.Agent == "" || t.Agent == "claude") && c.SessionRef != "" {
		return RestoreFork
	}
	return RestoreSummary
}

// restoreBlock says why the task cannot be restored now ("" when it can).
func (s *Service) restoreBlock(t *store.Task) string {
	if t.CleanedAt != nil {
		return fmt.Sprintf("Checkpoints were removed with the workspace after %s (WORKSPACE_RETENTION).", retentionText(s.WorkspaceRetention))
	}
	switch t.Status {
	case Queued, Preparing, Running, WaitingInput, Pushing:
		return "The task is busy; restore once the current run has ended."
	case Completed:
		if t.PRNumber != nil {
			return "The pull request is closed; start a new task instead."
		}
	}
	return ""
}

func retentionText(d time.Duration) string {
	if d <= 0 {
		return "the retention period"
	}
	if d%(24*time.Hour) == 0 {
		return fmt.Sprintf("%d days", int(d/(24*time.Hour)))
	}
	return d.String()
}

func (s *Service) checkpointView(t *store.Task, c *store.Checkpoint) CheckpointView {
	reason := s.restoreBlock(t)
	return CheckpointView{Checkpoint: c, Ref: checkpointRef(c), Mode: RestoreMode(t, c), Available: reason == "", Unavailable: reason}
}

// Checkpoints lists a task's checkpoints, oldest first.
func (s *Service) Checkpoints(ctx context.Context, taskID int64) ([]CheckpointView, error) {
	t, err := s.Store.GetTask(ctx, taskID)
	if err != nil {
		return nil, err
	}
	list, err := s.Store.ListCheckpoints(ctx, taskID)
	if err != nil {
		return nil, err
	}
	out := make([]CheckpointView, 0, len(list))
	for _, c := range list {
		out = append(out, s.checkpointView(t, c))
	}
	return out, nil
}

// PreviewCheckpoint is what restoring a checkpoint would discard. A
// checkpoint whose commit is gone comes back unavailable with a reason.
func (s *Service) PreviewCheckpoint(ctx context.Context, taskID, checkpointID int64) (*CheckpointPreview, error) {
	t, err := s.Store.GetTask(ctx, taskID)
	if err != nil {
		return nil, err
	}
	c, err := s.Store.GetCheckpoint(ctx, taskID, checkpointID)
	if err != nil {
		return nil, err
	}
	p := &CheckpointPreview{CheckpointView: s.checkpointView(t, c)}
	if c.EventID != nil {
		counts, err := s.Store.EventsAfter(ctx, taskID, *c.EventID)
		if err != nil {
			return nil, err
		}
		p.Discarded = Discarded{Runs: counts["run_started"], Steps: counts["tool_use"], Messages: counts["message"] + counts["answer"], Commits: counts["committed"]}
	}
	if t.CleanedAt != nil || s.CheckpointDiff == nil {
		return p, nil
	}
	p.Diff, p.Truncated, err = s.CheckpointDiff(ctx, t, p.Ref, c.RefSHA, maxCheckpointDiff)
	if errors.Is(err, ErrCheckpointGone) {
		p.Available, p.Unavailable = false, "This checkpoint is no longer in the workspace (it was cleaned up or re-cloned)."
		return p, nil
	}
	if err != nil {
		return nil, err
	}
	return p, nil
}

// Restore queues a run that resets the workspace to the checkpoint and
// continues from there with instruction (may be empty). via names who asked
// (web, telegram) for the timeline.
func (s *Service) Restore(ctx context.Context, taskID, checkpointID int64, instruction, via string) (*View, error) {
	instruction = strings.TrimSpace(instruction)
	if utf8.RuneCountInString(instruction) > 20000 {
		return nil, fmt.Errorf("%w: instruction is too long", ErrInvalid)
	}
	t, err := s.Store.GetTask(ctx, taskID)
	if err != nil {
		return nil, err
	}
	c, err := s.Store.GetCheckpoint(ctx, taskID, checkpointID)
	if err != nil {
		return nil, err
	}
	if reason := s.restoreBlock(t); reason != "" {
		return nil, fmt.Errorf("%w: %s", ErrConflict, reason)
	}
	mode := RestoreMode(t, c)
	var prompt string
	if mode == RestoreFork {
		prompt = ForkPrompt(c, instruction)
	} else {
		var evs []*store.Event
		if c.EventID != nil {
			if evs, err = s.Store.EventsThrough(ctx, taskID, *c.EventID, SummaryEventTypes); err != nil {
				return nil, err
			}
		}
		prompt = SummaryPrompt(t, c, evs, instruction)
	}
	payload := map[string]any{
		"checkpoint_id": c.ID, "n": c.N, "run_id": c.RunID, "ref_sha": c.RefSHA, "mode": mode,
		"instruction": instruction, "via": via,
	}
	if c.EventID != nil {
		payload["checkpoint_event_id"] = *c.EventID
	}
	nt, ev, err := s.Store.RequeueRestore(ctx, taskID, From(Queued), c, prompt, payload)
	if err != nil {
		return nil, err
	}
	s.publishTask(nt)
	s.publishEvent(ev)
	if s.AutoFix != nil {
		s.AutoFix.Cancel(ctx, taskID, "the owner restored a checkpoint")
	}
	s.wake()
	return s.Get(ctx, taskID)
}

const defaultRestoreInstruction = "Continue the task from here."

// ForkPrompt is the first message of a forked Claude session.
func ForkPrompt(c *store.Checkpoint, instruction string) string {
	if instruction == "" {
		instruction = defaultRestoreInstruction
	}
	return fmt.Sprintf("The owner rolled this task back to an earlier checkpoint (run %d, checkpoint %d). "+
		"The files in /workspace are exactly as they were at this point of our conversation; everything after it was discarded.\n\n%s",
		c.RunID, c.N, instruction)
}

// SummaryEventTypes are the timeline events a restore summary is built from.
var SummaryEventTypes = []string{"message", "answer", "assistant_text", "tool_use", "question", "done", "checkpoint_restored"}

const (
	summaryMaxLines = 80
	summaryLineMax  = 300
)

// SummaryPrompt seeds a new session (agents that cannot resume at a point)
// with the task and its timeline up to the checkpoint. Deterministic.
func SummaryPrompt(t *store.Task, c *store.Checkpoint, evs []*store.Event, instruction string) string {
	if instruction == "" {
		instruction = defaultRestoreInstruction
	}
	var lines []string
	for _, e := range evs {
		var p struct {
			Text    string `json:"text"`
			Summary string `json:"summary"`
		}
		_ = json.Unmarshal(e.Payload, &p)
		var l string
		switch e.Type {
		case "message":
			l = "Owner: " + p.Text
		case "answer":
			l = "Owner answered: " + p.Text
		case "question":
			l = "You asked: " + p.Text
		case "assistant_text":
			l = "You said: " + p.Text
		case "tool_use":
			l = "You ran: " + p.Summary
		case "done":
			l = "Run finished: " + p.Summary
		case "checkpoint_restored":
			l = "(The owner rolled back to an earlier checkpoint here.)"
		}
		if l = oneLine(l); l != "" {
			lines = append(lines, "- "+l)
		}
	}
	omitted := 0
	if len(lines) > summaryMaxLines {
		omitted = len(lines) - summaryMaxLines
		lines = lines[omitted:]
	}
	var b strings.Builder
	fmt.Fprintf(&b, "You are continuing task #%d in a new session after the owner rolled it back to an earlier checkpoint (run %d, checkpoint %d). ", t.ID, c.RunID, c.N)
	b.WriteString("The files in /workspace are exactly as they were at that checkpoint; everything after it was discarded. ")
	b.WriteString("Your earlier conversation is not available, so here is a summary of it.\n\n")
	b.WriteString("Task:\n")
	b.WriteString(t.Prompt)
	b.WriteString("\n\nWhat happened up to the checkpoint:\n")
	if omitted > 0 {
		fmt.Fprintf(&b, "- (%d earlier steps left out)\n", omitted)
	}
	if len(lines) == 0 {
		b.WriteString("- (no recorded steps)\n")
	}
	for _, l := range lines {
		b.WriteString(l)
		b.WriteString("\n")
	}
	b.WriteString("\nNow: ")
	b.WriteString(instruction)
	return b.String()
}

func oneLine(s string) string {
	s = strings.Join(strings.Fields(s), " ")
	if utf8.RuneCountInString(s) > summaryLineMax {
		r := []rune(s)
		s = string(r[:summaryLineMax-1]) + "…"
	}
	if strings.HasSuffix(s, ":") {
		return ""
	}
	return s
}
