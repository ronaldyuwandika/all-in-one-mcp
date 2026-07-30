package models

import (
	"strings"
	"testing"
)

func TestNormalizeFailedApproaches(t *testing.T) {
	input := []FailedApproach{
		{Approach: " retry ", FailureMode: " timeout ", RootCause: " no deadline ", Lesson: " set one "},
		{Approach: "retry", FailureMode: "timeout", RootCause: "no deadline", Lesson: "set one"},
	}
	got, err := NormalizeFailedApproaches(input)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 1 || got[0].Approach != "retry" {
		t.Fatalf("unexpected normalized approaches: %#v", got)
	}
	for _, outcome := range []EpisodeOutcome{OutcomeVerifiedSuccess, OutcomeUnverifiedSuccess, OutcomePartialSuccess, OutcomeFailure, OutcomeAbandoned} {
		ep := Episode{Problem: "p", Outcome: outcome, FailedApproaches: input}
		if err := ep.Validate(); err != nil {
			t.Fatalf("outcome %s rejected: %v", outcome, err)
		}
	}
}

func TestNormalizeFailedApproachesValidation(t *testing.T) {
	valid := FailedApproach{Approach: "a", FailureMode: "f", RootCause: "r", Lesson: "l"}
	tooMany := make([]FailedApproach, 21)
	for i := range tooMany {
		tooMany[i] = valid
		tooMany[i].Approach += string(rune('a' + i))
	}
	cases := [][]FailedApproach{
		{{Approach: " ", FailureMode: "f", RootCause: "r", Lesson: "l"}},
		{{Approach: strings.Repeat("x", 2001), FailureMode: "f", RootCause: "r", Lesson: "l"}},
		{{Approach: string([]byte{0xff}), FailureMode: "f", RootCause: "r", Lesson: "l"}},
		tooMany,
	}
	for i, tc := range cases {
		if _, err := NormalizeFailedApproaches(tc); err == nil {
			t.Fatalf("case %d unexpectedly valid", i)
		}
	}
}
