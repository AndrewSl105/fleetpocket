package team

import (
	"errors"
	"reflect"
	"testing"
)

func TestResolve(t *testing.T) {
	env := map[string]string{"CLAUDE_CODE_OAUTH_TOKEN": "server"}
	pk := &PersonalKey{Kind: "api_key", Value: "mine"}
	sub := &PersonalKey{Kind: "oauth_token", Value: "mysub"}
	cases := []struct {
		name string
		in   ResolveInput
		env  map[string]string
		src  string
		err  error
	}{
		{"single-owner: env keys, unchanged", ResolveInput{Agent: "claude", EnvKeys: env, Personal: pk}, nil, SourceEnv, nil},
		{"node task: the computer's own logins", ResolveInput{TeamOn: true, OnNode: true, Agent: "claude"}, nil, SourceNode, nil},
		{"node task, single-owner", ResolveInput{OnNode: true, Agent: "claude", EnvKeys: env}, nil, SourceNode, nil},
		{"personal API key", ResolveInput{TeamOn: true, Agent: "claude", Personal: pk, EnvKeys: env}, map[string]string{"ANTHROPIC_API_KEY": "mine"}, SourcePersonal, nil},
		{"personal subscription token", ResolveInput{TeamOn: true, Agent: "claude", Personal: sub}, map[string]string{"CLAUDE_CODE_OAUTH_TOKEN": "mysub"}, SourcePersonal, nil},
		{"personal beats fallback", ResolveInput{TeamOn: true, Fallback: true, Agent: "claude", Personal: pk, EnvKeys: env}, map[string]string{"ANTHROPIC_API_KEY": "mine"}, SourcePersonal, nil},
		{"codex personal", ResolveInput{TeamOn: true, Agent: "codex", Personal: pk}, map[string]string{"OPENAI_API_KEY": "mine"}, SourcePersonal, nil},
		{"cursor personal", ResolveInput{TeamOn: true, Agent: "cursor", Personal: pk}, map[string]string{"CURSOR_API_KEY": "mine"}, SourcePersonal, nil},
		{"no key, fallback on: env key", ResolveInput{TeamOn: true, Fallback: true, Agent: "claude", EnvKeys: env}, env, SourceFallback, nil},
		{"no key, fallback off: needs_key", ResolveInput{TeamOn: true, Agent: "claude", EnvKeys: env}, nil, "", ErrNeedsKey},
		{"no key, fallback on but no env key: needs_key", ResolveInput{TeamOn: true, Fallback: true, Agent: "codex", EnvKeys: map[string]string{}}, nil, "", ErrNeedsKey},
		{"fake runner, fallback on", ResolveInput{TeamOn: true, Fallback: true, Agent: "claude", Fake: true}, map[string]string{}, SourceFallback, nil},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			env, src, err := Resolve(c.in)
			if !errors.Is(err, c.err) || src != c.src || !reflect.DeepEqual(env, c.env) {
				t.Fatalf("got %v %q %v, want %v %q %v", env, src, err, c.env, c.src, c.err)
			}
		})
	}
}

func TestCanActOn(t *testing.T) {
	me, other := int64(1), int64(2)
	if !CanActOn("admin", me, &other) || !CanActOn("member", me, &me) || CanActOn("member", me, &other) ||
		CanActOn("member", me, nil) || CanActOn("viewer", me, &me) {
		t.Fatal("role matrix")
	}
}
