// Package tasks holds the task model, the state machine and the task service
// shared by the REST API and the Telegram bot.
package tasks

const (
	Queued       = "queued"
	Preparing    = "preparing"
	Running      = "running"
	WaitingInput = "waiting_input"
	Paused       = "paused"
	Pushing      = "pushing"
	PROpen       = "pr_open"
	// BranchReady: a local repository received the task branch
	// (jm/<id>-<slug>); it mirrors pr_open.
	BranchReady = "branch_ready"
	Completed   = "completed"
	Failed      = "failed"
	Cancelled   = "cancelled"
)

var transitions = map[string][]string{
	Queued:       {Preparing, Cancelled, Failed},
	Preparing:    {Running, Failed, Cancelled, Queued},
	Running:      {WaitingInput, Paused, Pushing, PROpen, BranchReady, Completed, Failed, Cancelled},
	WaitingInput: {Running, Paused, Pushing, PROpen, BranchReady, Completed, Failed, Cancelled},
	Paused:       {Queued, Cancelled, Failed},
	Pushing:      {PROpen, BranchReady, Completed, Failed, Cancelled},
	// pr_open -> pushing: the owner approves a held CI auto-fix.
	PROpen: {Queued, Pushing, Completed, Failed, Cancelled},
	// branch_ready -> pushing: "Push & open PR"; -> completed: merged or
	// discarded; -> queued: a follow-up.
	BranchReady: {Queued, Pushing, Completed, Failed, Cancelled},
	Completed:   {Queued},
	Failed:      {Queued},
	Cancelled:   {Queued},
}

// CanTransition reports whether the state machine allows from → to.
func CanTransition(from, to string) bool {
	for _, s := range transitions[from] {
		if s == to {
			return true
		}
	}
	return false
}

// From lists the states that may move to `to`.
func From(to string) []string {
	var out []string
	for from, tos := range transitions {
		for _, s := range tos {
			if s == to {
				out = append(out, from)
			}
		}
	}
	return out
}

func IsTerminal(s string) bool { return s == Completed || s == Failed || s == Cancelled }

// IsActive states hold a scheduler slot.
func IsActive(s string) bool {
	return s == Preparing || s == Running || s == WaitingInput || s == Pushing
}

// Live states have a runner container attached.
func IsLive(s string) bool { return s == Running || s == WaitingInput }

func Valid(s string) bool { _, ok := transitions[s]; return ok }

// Run kinds.
const (
	RunInitial  = "initial"
	RunResume   = "resume"
	RunFollowup = "followup"
	RunRetry    = "retry"
)

// MessageAction decides what POST /message does in a state (section 7).
type MessageAction int

const (
	MessageReject MessageAction = iota
	// MessageQueue keeps the text until the active run ends, then starts it
	// as the next run. A run handles one prompt, so nothing is injected.
	MessageQueue
	MessageNewRun
)

// QueueStates accept a message as queued: a run is pending or active.
var QueueStates = []string{Queued, Preparing, Running, WaitingInput, Pushing}

func MessageActionFor(status string) MessageAction {
	switch status {
	case Queued, Preparing, Running, WaitingInput, Pushing:
		return MessageQueue
	case Paused, PROpen, BranchReady, Completed:
		return MessageNewRun
	}
	return MessageReject
}
