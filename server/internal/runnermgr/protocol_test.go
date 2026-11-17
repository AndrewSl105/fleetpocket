package runnermgr

import (
	"encoding/json"
	"os"
	"strings"
	"testing"
)

func TestParseEvent(t *testing.T) {
	ok := []string{
		`{"type":"ready","runner_version":"0.1.0","ts":"2026-10-14T15:08:42.564Z","seq":1}`,
		`{"type":"question","question_id":"q1","text":"Which?","options":["a","b"],"ts":"2026-10-14T15:08:42.573Z","seq":8}`,
		`{"type":"done","session_id":"s","summary":"x","no_changes":false,"cost_usd_total":0.01,"ts":"2026-10-14T15:08:42.614Z","seq":13}`,
		`{"type":"tool_result","tool":"Read","ok":false,"ts":"2026-10-14T15:08:42.614Z","seq":4}`,
	}
	for _, l := range ok {
		ev, err := ParseEvent([]byte(l))
		if err != nil {
			t.Fatalf("%s: %v", l, err)
		}
		if string(ev.Raw) != l {
			t.Fatalf("raw not kept")
		}
	}
	ev, _ := ParseEvent([]byte(ok[1]))
	if ev.QuestionID != "q1" || len(ev.Options) != 2 {
		t.Fatalf("bad question: %+v", ev)
	}
	ev, _ = ParseEvent([]byte(ok[3]))
	if ev.OK == nil || *ev.OK {
		t.Fatalf("ok=false must be preserved")
	}

	bad := map[string]string{
		`not json`: "invalid",
		`{"type":"boom","ts":"2026-10-14T15:08:42.564Z","seq":1}`:                "unknown",
		`{"type":"ready","ts":"2026-10-14T15:08:42.564Z"}`:                       "seq",
		`{"type":"ready","seq":3}`:                                               "ts",
		`{"type":"question","ts":"2026-10-14T15:08:42.564Z","seq":2}`:            "question",
		`{"type":"error","message":"x","ts":"2026-10-14T15:08:42.564Z","seq":2}`: "code",
		`{"type":"checkpoint","n":1,"ts":"2026-10-14T15:08:42.564Z","seq":2}`:    "checkpoint",
	}
	for l, want := range bad {
		if _, err := ParseEvent([]byte(l)); err == nil || !strings.Contains(err.Error(), want) {
			t.Fatalf("%s: got %v, want %q", l, err, want)
		}
	}
}

func TestParseCheckpointEvent(t *testing.T) {
	l := `{"type":"checkpoint","n":2,"ref_sha":"abc","turn":5,"session_ref":"s/u","files_changed":3,"reason":"turn","ts":"2026-10-14T15:08:42.564Z","seq":9}`
	ev, err := ParseEvent([]byte(l))
	if err != nil {
		t.Fatal(err)
	}
	if ev.N != 2 || ev.RefSHA != "abc" || ev.Turn != 5 || ev.SessionRef != "s/u" || ev.FilesChanged != 3 || ev.Reason != "turn" {
		t.Fatalf("%+v", ev)
	}
	if got := CheckpointRef(12, 2); got != "refs/jm/checkpoints/12/2" {
		t.Fatal(got)
	}
}

func TestRunCommandCheckpointFields(t *testing.T) {
	b, _ := EncodeCommand(NewRun(RunCommand{TaskID: "1", Prompt: "p", Checkpoints: &CheckpointsConfig{RunID: "4"}, ResumeSessionRef: "s/u"}))
	if !strings.Contains(string(b), `"checkpoints":{"run_id":"4"}`) || !strings.Contains(string(b), `"resume_session_ref":"s/u"`) {
		t.Fatalf("%s", b)
	}
	// Off: neither field on the wire.
	b, _ = EncodeCommand(NewRun(RunCommand{TaskID: "1", Prompt: "p"}))
	if strings.Contains(string(b), "checkpoints") || strings.Contains(string(b), "resume_session_ref") {
		t.Fatalf("%s", b)
	}
}

func TestEncodeCommand(t *testing.T) {
	b, err := EncodeCommand(NewAnswer("q1", "yes"))
	if err != nil || string(b) != `{"type":"answer","question_id":"q1","text":"yes"}`+"\n" {
		t.Fatalf("%q %v", b, err)
	}
	b, _ = EncodeCommand(NewRun(RunCommand{TaskID: "1", Prompt: "p"}))
	if !strings.HasPrefix(string(b), `{"type":"run","task_id":"1"`) {
		t.Fatalf("%s", b)
	}
}

func TestRunCommandSkillsAndOutputRoundTrip(t *testing.T) {
	in := NewRun(RunCommand{TaskID: "1", Prompt: "p", Output: OutputArtifacts,
		Skills: []Skill{{Slug: "db-schema", Files: []SkillFile{{Path: "SKILL.md", Content: "---\nname: x\n---\n"}}}}})
	b, err := EncodeCommand(in)
	if err != nil {
		t.Fatal(err)
	}
	var out RunCommand
	if err := json.Unmarshal(b, &out); err != nil {
		t.Fatal(err)
	}
	if out.Output != OutputArtifacts || len(out.Skills) != 1 || out.Skills[0].Files[0].Path != "SKILL.md" {
		t.Fatalf("%+v", out)
	}
	// Today's tasks: neither field on the wire.
	b, _ = EncodeCommand(NewRun(RunCommand{TaskID: "1", Prompt: "p"}))
	if strings.Contains(string(b), "skills") || strings.Contains(string(b), "output") {
		t.Fatalf("%s", b)
	}
}

// Every persisted runner event type has a typed schema in the API contract.
func TestOpenAPIHasEveryRunnerEvent(t *testing.T) {
	spec, err := os.ReadFile("../../../docs/openapi.yaml") // the repository root docs/
	if err != nil {
		t.Fatal(err)
	}
	for typ := range knownEvents {
		if typ == EvHeartbeat {
			continue // never persisted
		}
		if !strings.Contains(string(spec), "\n          "+typ+": '#/components/schemas/") {
			t.Errorf("docs/openapi.yaml: Event discriminator has no mapping for %q", typ)
		}
	}
}

func TestParseCheckEventAndCommand(t *testing.T) {
	ev, err := ParseEvent([]byte(`{"type":"check","command":"npm test","exit_code":0,"output":"ok","duration_ms":12,"timed_out":false,"ts":"2026-11-17T10:00:00.000Z","seq":9}`))
	if err != nil || ev.ExitCode == nil || *ev.ExitCode != 0 || ev.Command != "npm test" || ev.DurationMS != 12 {
		t.Fatalf("check: %+v %v", ev, err)
	}
	if _, err := ParseEvent([]byte(`{"type":"check","command":"x","ts":"2026-11-17T10:00:00.000Z","seq":9}`)); err == nil {
		t.Fatal("check without exit_code accepted")
	}
	b, _ := json.Marshal(NewRun(RunCommand{Check: &CheckConfig{Command: "go test ./...", TimeoutSec: 300}, Commit: CommitInfo{AllowEmpty: true}}))
	if !strings.Contains(string(b), `"check":{"command":"go test ./...","timeout_sec":300}`) || !strings.Contains(string(b), `"allow_empty":true`) {
		t.Fatalf("%s", b)
	}
	b, _ = json.Marshal(NewRun(RunCommand{}))
	if strings.Contains(string(b), "check") || strings.Contains(string(b), "allow_empty") {
		t.Fatalf("today's command changed: %s", b)
	}
}
