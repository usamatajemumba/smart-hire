package domain

import "testing"

func TestCanTransition(t *testing.T) {
	tests := []struct {
		from, to JobStatus
		want     bool
	}{
		{StatusDraft, StatusPublished, true},
		{StatusDraft, StatusArchived, true},
		{StatusDraft, StatusClosed, false},
		{StatusPublished, StatusClosed, true},
		{StatusPublished, StatusDraft, false},
		{StatusClosed, StatusArchived, true},
		{StatusClosed, StatusPublished, false},
		{StatusArchived, StatusDraft, false},
	}
	for _, tc := range tests {
		if got := CanTransition(tc.from, tc.to); got != tc.want {
			t.Errorf("CanTransition(%s -> %s) = %v, want %v", tc.from, tc.to, got, tc.want)
		}
	}
}

func TestPublishProblems_RemoteNeedsNoLocation(t *testing.T) {
	j := &Job{
		Title:        "Go Engineer",
		Description:  "A sufficiently long description of the role.",
		WorkMode:     "remote",
		Requirements: []Requirement{{Type: "skill", Value: "Go"}},
		Stages:       []HiringStage{{Name: "Screen", Position: 1}},
	}
	if p := j.PublishProblems(); len(p) != 0 {
		t.Fatalf("expected publishable, got %v", p)
	}
	j.WorkMode = "onsite"
	if p := j.PublishProblems(); len(p) != 1 {
		t.Fatalf("onsite without location should fail once, got %v", p)
	}
}
