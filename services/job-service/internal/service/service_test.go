package service

import (
	"context"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/usamatajemumba/smart-hire/pkg/apperr"
	"github.com/usamatajemumba/smart-hire/services/job-service/internal/domain"
	"github.com/usamatajemumba/smart-hire/services/job-service/internal/repository"
)

// fakeRepo is an in-memory Repository. It is NOT transactional (InTx just
// calls fn), which is enough to test business rules; SQL behaviour is covered
// by the end-to-end checks against real Postgres.
type fakeRepo struct {
	mu    sync.Mutex
	jobs  map[uuid.UUID]*domain.Job
	audit []domain.AuditLog
}

func newFakeRepo() *fakeRepo { return &fakeRepo{jobs: map[uuid.UUID]*domain.Job{}} }

func (f *fakeRepo) InTx(_ context.Context, fn func(repository.Repository) error) error { return fn(f) }

func (f *fakeRepo) CreateJob(_ context.Context, j *domain.Job) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	cp := *j
	f.jobs[j.ID] = &cp
	return nil
}

func (f *fakeRepo) GetJob(_ context.Context, id uuid.UUID) (*domain.Job, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	j, ok := f.jobs[id]
	if !ok {
		return nil, repository.ErrNotFound
	}
	cp := *j
	return &cp, nil
}

func (f *fakeRepo) ListJobs(_ context.Context, fl domain.JobFilter, _, _ int) ([]domain.Job, int64, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	var out []domain.Job
	for _, j := range f.jobs {
		if fl.Status == "" || j.Status == fl.Status {
			out = append(out, *j)
		}
	}
	return out, int64(len(out)), nil
}

func (f *fakeRepo) UpdateJob(_ context.Context, id uuid.UUID, v int, fields map[string]any) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	j, ok := f.jobs[id]
	if !ok {
		return repository.ErrNotFound
	}
	if j.Version != v {
		return repository.ErrStaleVersion
	}
	if t, ok := fields["title"].(string); ok {
		j.Title = t
	}
	j.Version++
	return nil
}

func (f *fakeRepo) TransitionStatus(_ context.Context, id uuid.UUID, from, to domain.JobStatus, v int, _ map[string]any) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	j, ok := f.jobs[id]
	if !ok {
		return repository.ErrNotFound
	}
	if j.Status != from || j.Version != v {
		return repository.ErrStaleState
	}
	j.Status = to
	j.Version++
	return nil
}

func (f *fakeRepo) SoftDeleteJob(_ context.Context, id uuid.UUID) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	delete(f.jobs, id)
	return nil
}

func (f *fakeRepo) ReplaceRequirements(_ context.Context, id uuid.UUID, r []domain.Requirement) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.jobs[id].Requirements = r
	f.jobs[id].Version++
	return nil
}

func (f *fakeRepo) ReplaceStages(_ context.Context, id uuid.UUID, s []domain.HiringStage) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.jobs[id].Stages = s
	f.jobs[id].Version++
	return nil
}

func (f *fakeRepo) CreateAudit(_ context.Context, a *domain.AuditLog) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.audit = append(f.audit, *a)
	return nil
}

func (f *fakeRepo) ListAudit(_ context.Context, id uuid.UUID, _, _ int) ([]domain.AuditLog, int64, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	var out []domain.AuditLog
	for _, a := range f.audit {
		if a.EntityID == id {
			out = append(out, a)
		}
	}
	return out, int64(len(out)), nil
}

// ---------- tests ----------

var (
	ctx   = context.Background()
	alice = Actor{ID: "recruiter-alice", RequestID: "req-1"}
)

func newSvc() (*Service, *fakeRepo) {
	repo := newFakeRepo()
	s := New(repo)
	s.now = func() time.Time { return time.Date(2026, 11, 17, 10, 0, 0, 0, time.UTC) }
	return s, repo
}

func validInput() CreateJobInput {
	return CreateJobInput{
		RecruiterID:    uuid.New(),
		Title:          "Senior Go Engineer",
		Description:    "Build scalable recruitment workflows with Go, Postgres and Kafka.",
		Location:       "Islamabad",
		EmploymentType: "full_time",
		WorkMode:       "hybrid",
		Requirements:   []RequirementInput{{Type: "skill", Value: "Go", IsMandatory: true}},
		Stages: []StageInput{
			{Name: "Screening", StageType: "screening", Position: 1},
			{Name: "Tech Interview", StageType: "interview", Position: 2},
		},
	}
}

func kind(t *testing.T, err error) apperr.Kind {
	t.Helper()
	if err == nil {
		t.Fatal("expected error, got nil")
	}
	return apperr.KindOf(err)
}

func TestCreateJob_StartsAsDraftAndAudits(t *testing.T) {
	s, repo := newSvc()
	job, err := s.CreateJob(ctx, alice, validInput())
	if err != nil {
		t.Fatal(err)
	}
	if job.Status != domain.StatusDraft || job.Version != 1 {
		t.Fatalf("got status=%s version=%d, want draft/1", job.Status, job.Version)
	}
	if len(repo.audit) != 1 || repo.audit[0].Action != "job.created" || repo.audit[0].ActorID != alice.ID {
		t.Fatalf("unexpected audit: %+v", repo.audit)
	}
}

func TestCreateJob_Validation(t *testing.T) {
	tests := []struct {
		name   string
		mutate func(*CreateJobInput)
	}{
		{"salary range inverted", func(in *CreateJobInput) {
			lo, hi := int64(200), int64(100)
			in.SalaryMin, in.SalaryMax = &lo, &hi
		}},
		{"experience range inverted", func(in *CreateJobInput) {
			lo, hi := 5, 2
			in.ExperienceMin, in.ExperienceMax = &lo, &hi
		}},
		{"stage position gap", func(in *CreateJobInput) { in.Stages[1].Position = 3 }},
		{"duplicate stage name", func(in *CreateJobInput) { in.Stages[1].Name = "screening" }},
		{"duplicate requirement", func(in *CreateJobInput) {
			in.Requirements = append(in.Requirements, RequirementInput{Type: "skill", Value: "go"})
		}},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			s, _ := newSvc()
			in := validInput()
			tc.mutate(&in)
			_, err := s.CreateJob(ctx, alice, in)
			if k := kind(t, err); k != apperr.KindValidation {
				t.Fatalf("kind = %s, want VALIDATION_ERROR", k)
			}
		})
	}
}

func TestPublish_RequiresCompleteJob(t *testing.T) {
	s, _ := newSvc()
	in := validInput()
	in.Stages = nil
	in.Description = "too short"
	job, _ := s.CreateJob(ctx, alice, in)

	_, err := s.PublishJob(ctx, alice, job.ID)
	if k := kind(t, err); k != apperr.KindRule {
		t.Fatalf("kind = %s, want BUSINESS_RULE_FAILED", k)
	}
	problems := err.(*apperr.Error).Details["problems"].([]string)
	if len(problems) != 2 {
		t.Fatalf("want 2 problems (description, stages), got %v", problems)
	}
}

func TestLifecycle_HappyPathAndIllegalTransitions(t *testing.T) {
	s, _ := newSvc()
	job, _ := s.CreateJob(ctx, alice, validInput())

	pub, err := s.PublishJob(ctx, alice, job.ID)
	if err != nil || pub.Status != domain.StatusPublished {
		t.Fatalf("publish: status=%v err=%v", pub, err)
	}
	if _, err := s.PublishJob(ctx, alice, job.ID); kind(t, err) != apperr.KindRule {
		t.Fatal("publishing twice must be rejected")
	}
	if err := s.DeleteJob(ctx, alice, job.ID); kind(t, err) != apperr.KindRule {
		t.Fatal("deleting a published job must be rejected")
	}
	if _, err := s.CloseJob(ctx, alice, job.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := s.UpdateJob(ctx, alice, job.ID, UpdateJobInput{Version: 3, Title: strPtr("New")}); kind(t, err) != apperr.KindRule {
		t.Fatal("closed job must not be editable")
	}
	if _, err := s.ArchiveJob(ctx, alice, job.ID); err != nil {
		t.Fatal(err)
	}
}

func TestUpdate_OptimisticLocking(t *testing.T) {
	s, _ := newSvc()
	job, _ := s.CreateJob(ctx, alice, validInput())

	if _, err := s.UpdateJob(ctx, alice, job.ID, UpdateJobInput{Version: 1, Title: strPtr("Staff Go Engineer")}); err != nil {
		t.Fatal(err)
	}
	// Second writer still holds version 1 -> conflict, not a silent overwrite.
	_, err := s.UpdateJob(ctx, alice, job.ID, UpdateJobInput{Version: 1, Title: strPtr("Lost update")})
	if k := kind(t, err); k != apperr.KindConflict {
		t.Fatalf("kind = %s, want CONFLICT", k)
	}
}

func TestGetJob_NotFound(t *testing.T) {
	s, _ := newSvc()
	if _, err := s.GetJob(ctx, uuid.New()); kind(t, err) != apperr.KindNotFound {
		t.Fatal("want NOT_FOUND")
	}
}

func strPtr(s string) *string { return &s }
