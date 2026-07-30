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
		if outcome == OutcomeVerifiedSuccess {
			ep.Verification = []VerificationRecord{{Type: VerificationTests, Command: "go test", Result: "ok", Success: true}}
		}
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

func TestNormalizeVerificationRecords(t *testing.T) {
	input := []VerificationRecord{
		{Type: VerificationTests, Command: " go test ./... ", Result: " PASS ", Success: true},
		{Type: VerificationTests, Command: "go test ./...", Result: "PASS", Success: true},
	}
	got, err := NormalizeVerificationRecords(input)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 1 || got[0].Command != "go test ./..." {
		t.Fatalf("unexpected normalized verification: %#v", got)
	}
}

func TestValidateOutcomeTransition(t *testing.T) {
	valid := VerificationRecord{Type: VerificationTests, Command: "go test", Result: "ok", Success: true}
	if err := ValidateOutcomeTransition(&Episode{Outcome: OutcomeUnverifiedSuccess}, &Episode{Outcome: OutcomeVerifiedSuccess, Verification: []VerificationRecord{valid}}); err != nil {
		t.Fatal(err)
	}
	if err := ValidateOutcomeTransition(&Episode{Outcome: OutcomeUnverifiedSuccess}, &Episode{Outcome: OutcomeVerifiedSuccess}); err == nil {
		t.Fatal("expected promotion without evidence to fail")
	}
	if err := ValidateOutcomeTransition(&Episode{Outcome: OutcomeVerifiedSuccess, Verification: []VerificationRecord{valid}}, &Episode{Outcome: OutcomeVerifiedSuccess}); err == nil {
		t.Fatal("expected removal of final evidence to fail")
	}
}
