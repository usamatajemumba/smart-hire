// Package service holds job-service business logic: lifecycle rules,
// validation that spans fields, transactions and the audit trail.
package service

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/google/uuid"

	"github.com/usamatajemumba/smart-hire/pkg/apperr"
	"github.com/usamatajemumba/smart-hire/services/job-service/internal/domain"
	"github.com/usamatajemumba/smart-hire/services/job-service/internal/repository"
)

// Actor identifies who performs an action. Week 1 has no auth, so it comes
// from the X-User-ID header; later it comes from a verified JWT.
type Actor struct {
	ID        string
	RequestID string
}

// Service implements job use cases.
type Service struct {
	repo  repository.Repository
	now   func() time.Time
	newID func() uuid.UUID
}

// New builds a Service. IDs are UUIDv7: time-ordered, so inserts land at the
// end of the B-tree index instead of random pages (better than v4 at scale).
func New(repo repository.Repository) *Service {
	return &Service{
		repo: repo,
		now:  func() time.Time { return time.Now().UTC() },
		newID: func() uuid.UUID {
			return uuid.Must(uuid.NewV7())
		},
	}
}

// CreateJobInput is a new job in draft state.
type CreateJobInput struct {
	RecruiterID    uuid.UUID
	Title          string
	Description    string
	Department     string
	Location       string
	EmploymentType string
	WorkMode       string
	ExperienceMin  *int
	ExperienceMax  *int
	SalaryMin      *int64
	SalaryMax      *int64
	Currency       *string
	Requirements   []RequirementInput
	Stages         []StageInput
}

// UpdateJobInput is a partial update; nil fields are left unchanged.
type UpdateJobInput struct {
	Version        int
	Title          *string
	Description    *string
	Department     *string
	Location       *string
	EmploymentType *string
	WorkMode       *string
	ExperienceMin  *int
	ExperienceMax  *int
	SalaryMin      *int64
	SalaryMax      *int64
	Currency       *string
}

type RequirementInput struct {
	Type        string
	Value       string
	IsMandatory bool
}

type StageInput struct {
	Name      string
	StageType string
	Position  int
}

// CreateJob stores a draft job (with optional requirements/stages) and its audit row atomically.
func (s *Service) CreateJob(ctx context.Context, actor Actor, in CreateJobInput) (*domain.Job, error) {
	if err := checkRanges(in.ExperienceMin, in.ExperienceMax, in.SalaryMin, in.SalaryMax); err != nil {
		return nil, err
	}
	reqs, err := s.buildRequirements(uuid.Nil, in.Requirements)
	if err != nil {
		return nil, err
	}
	stages, err := s.buildStages(uuid.Nil, in.Stages)
	if err != nil {
		return nil, err
	}

	now := s.now()
	job := &domain.Job{
		ID:             s.newID(),
		RecruiterID:    in.RecruiterID,
		Title:          strings.TrimSpace(in.Title),
		Description:    strings.TrimSpace(in.Description),
		Department:     strings.TrimSpace(in.Department),
		Location:       strings.TrimSpace(in.Location),
		EmploymentType: in.EmploymentType,
		WorkMode:       in.WorkMode,
		ExperienceMin:  in.ExperienceMin,
		ExperienceMax:  in.ExperienceMax,
		SalaryMin:      in.SalaryMin,
		SalaryMax:      in.SalaryMax,
		Currency:       upperPtr(in.Currency),
		Status:         domain.StatusDraft,
		Version:        1,
		CreatedAt:      now,
		UpdatedAt:      now,
	}
	for i := range reqs {
		reqs[i].JobID = job.ID
	}
	for i := range stages {
		stages[i].JobID = job.ID
	}
	job.Requirements, job.Stages = reqs, stages

	err = s.repo.InTx(ctx, func(tx repository.Repository) error {
		if err := tx.CreateJob(ctx, job); err != nil {
			return err
		}
		return s.audit(ctx, tx, actor, job.ID, "job.created", map[string]any{"title": job.Title, "status": job.Status})
	})
	if err != nil {
		return nil, translate(err, "job")
	}
	return job, nil
}

// GetJob returns a job with requirements and stages.
func (s *Service) GetJob(ctx context.Context, id uuid.UUID) (*domain.Job, error) {
	job, err := s.repo.GetJob(ctx, id)
	if err != nil {
		return nil, translate(err, "job")
	}
	return job, nil
}

// ListJobs returns a filtered page of jobs.
func (s *Service) ListJobs(ctx context.Context, f domain.JobFilter, limit, offset int) ([]domain.Job, int64, error) {
	jobs, total, err := s.repo.ListJobs(ctx, f, limit, offset)
	if err != nil {
		return nil, 0, apperr.Internal(err)
	}
	return jobs, total, nil
}

// UpdateJob applies a partial update guarded by optimistic locking.
func (s *Service) UpdateJob(ctx context.Context, actor Actor, id uuid.UUID, in UpdateJobInput) (*domain.Job, error) {
	var updated *domain.Job
	err := s.repo.InTx(ctx, func(tx repository.Repository) error {
		job, err := tx.GetJob(ctx, id)
		if err != nil {
			return err
		}
		if !job.Status.IsEditable() {
			return apperr.Rule(fmt.Sprintf("job in status %q cannot be edited", job.Status))
		}
		if job.Version != in.Version {
			return repository.ErrStaleVersion
		}

		fields := map[string]any{}
		setStr(fields, "title", in.Title)
		setStr(fields, "description", in.Description)
		setStr(fields, "department", in.Department)
		setStr(fields, "location", in.Location)
		setStr(fields, "employment_type", in.EmploymentType)
		setStr(fields, "work_mode", in.WorkMode)
		if in.ExperienceMin != nil {
			fields["experience_min"], job.ExperienceMin = *in.ExperienceMin, in.ExperienceMin
		}
		if in.ExperienceMax != nil {
			fields["experience_max"], job.ExperienceMax = *in.ExperienceMax, in.ExperienceMax
		}
		if in.SalaryMin != nil {
			fields["salary_min"], job.SalaryMin = *in.SalaryMin, in.SalaryMin
		}
		if in.SalaryMax != nil {
			fields["salary_max"], job.SalaryMax = *in.SalaryMax, in.SalaryMax
		}
		if in.Currency != nil {
			fields["currency"] = strings.ToUpper(*in.Currency)
		}
		if len(fields) == 0 {
			return apperr.Validation("no fields to update")
		}
		// Validate the merged result, not just the patch.
		if err := checkRanges(job.ExperienceMin, job.ExperienceMax, job.SalaryMin, job.SalaryMax); err != nil {
			return err
		}
		if title, ok := fields["title"].(string); ok && title == "" {
			return apperr.Validation("title cannot be empty")
		}
		if err := tx.UpdateJob(ctx, id, in.Version, fields); err != nil {
			return err
		}
		if err := s.audit(ctx, tx, actor, id, "job.updated", fields); err != nil {
			return err
		}
		updated, err = tx.GetJob(ctx, id)
		return err
	})
	if err != nil {
		return nil, translate(err, "job")
	}
	return updated, nil
}

// DeleteJob soft-deletes a job. Published jobs must be closed first so
// candidates never see a job vanish mid-application.
func (s *Service) DeleteJob(ctx context.Context, actor Actor, id uuid.UUID) error {
	err := s.repo.InTx(ctx, func(tx repository.Repository) error {
		job, err := tx.GetJob(ctx, id)
		if err != nil {
			return err
		}
		if job.Status == domain.StatusPublished {
			return apperr.Rule("published job must be closed before deletion")
		}
		if err := tx.SoftDeleteJob(ctx, id); err != nil {
			return err
		}
		return s.audit(ctx, tx, actor, id, "job.deleted", map[string]any{"status": job.Status})
	})
	return translate(err, "job")
}

// SetRequirements replaces the full requirement list (PUT semantics).
func (s *Service) SetRequirements(ctx context.Context, actor Actor, id uuid.UUID, in []RequirementInput) (*domain.Job, error) {
	reqs, err := s.buildRequirements(id, in)
	if err != nil {
		return nil, err
	}
	return s.mutateChildren(ctx, actor, id, "job.requirements_replaced", map[string]any{"count": len(reqs)},
		func(tx repository.Repository) error { return tx.ReplaceRequirements(ctx, id, reqs) })
}

// SetStages replaces the hiring pipeline (PUT semantics).
func (s *Service) SetStages(ctx context.Context, actor Actor, id uuid.UUID, in []StageInput) (*domain.Job, error) {
	stages, err := s.buildStages(id, in)
	if err != nil {
		return nil, err
	}
	names := make([]string, len(stages))
	for i, st := range stages {
		names[i] = st.Name
	}
	return s.mutateChildren(ctx, actor, id, "job.stages_replaced", map[string]any{"stages": names},
		func(tx repository.Repository) error { return tx.ReplaceStages(ctx, id, stages) })
}

func (s *Service) mutateChildren(ctx context.Context, actor Actor, id uuid.UUID, action string, changes map[string]any, apply func(repository.Repository) error) (*domain.Job, error) {
	var updated *domain.Job
	err := s.repo.InTx(ctx, func(tx repository.Repository) error {
		job, err := tx.GetJob(ctx, id)
		if err != nil {
			return err
		}
		if !job.Status.IsEditable() {
			return apperr.Rule(fmt.Sprintf("job in status %q cannot be edited", job.Status))
		}
		if err := apply(tx); err != nil {
			return err
		}
		if err := s.audit(ctx, tx, actor, id, action, changes); err != nil {
			return err
		}
		updated, err = tx.GetJob(ctx, id)
		return err
	})
	if err != nil {
		return nil, translate(err, "job")
	}
	return updated, nil
}

// PublishJob moves draft -> published after validating the whole aggregate.
//
// Week 1: synchronous. Week 2: this becomes "draft -> processing", and a
// Temporal workflow runs validation, search indexing, cache refresh and
// analytics init before marking the job published.
func (s *Service) PublishJob(ctx context.Context, actor Actor, id uuid.UUID) (*domain.Job, error) {
	return s.transition(ctx, actor, id, domain.StatusPublished, "job.published", func(job *domain.Job, now time.Time) (map[string]any, error) {
		if problems := job.PublishProblems(); len(problems) > 0 {
			return nil, apperr.Rule("job is not ready to publish").WithDetails(map[string]any{"problems": problems})
		}
		return map[string]any{"published_at": now}, nil
	})
}

// CloseJob stops a published job from accepting applications.
func (s *Service) CloseJob(ctx context.Context, actor Actor, id uuid.UUID) (*domain.Job, error) {
	return s.transition(ctx, actor, id, domain.StatusClosed, "job.closed", func(_ *domain.Job, now time.Time) (map[string]any, error) {
		return map[string]any{"closed_at": now}, nil
	})
}

// ArchiveJob hides a draft or closed job from normal listings.
func (s *Service) ArchiveJob(ctx context.Context, actor Actor, id uuid.UUID) (*domain.Job, error) {
	return s.transition(ctx, actor, id, domain.StatusArchived, "job.archived", nil)
}

type transitionCheck func(job *domain.Job, now time.Time) (map[string]any, error)

func (s *Service) transition(ctx context.Context, actor Actor, id uuid.UUID, to domain.JobStatus, action string, check transitionCheck) (*domain.Job, error) {
	var updated *domain.Job
	err := s.repo.InTx(ctx, func(tx repository.Repository) error {
		job, err := tx.GetJob(ctx, id)
		if err != nil {
			return err
		}
		if !domain.CanTransition(job.Status, to) {
			return apperr.Rule(fmt.Sprintf("cannot move job from %q to %q", job.Status, to)).
				WithDetails(map[string]any{"current_status": job.Status})
		}
		now := s.now()
		var extra map[string]any
		if check != nil {
			if extra, err = check(job, now); err != nil {
				return err
			}
		}
		if err := tx.TransitionStatus(ctx, id, job.Status, to, job.Version, extra); err != nil {
			return err
		}
		if err := s.audit(ctx, tx, actor, id, action, map[string]any{"from": job.Status, "to": to}); err != nil {
			return err
		}
		updated, err = tx.GetJob(ctx, id)
		return err
	})
	if err != nil {
		return nil, translate(err, "job")
	}
	return updated, nil
}

// ListAudit returns the audit trail for one job, newest first.
func (s *Service) ListAudit(ctx context.Context, id uuid.UUID, limit, offset int) ([]domain.AuditLog, int64, error) {
	if _, err := s.repo.GetJob(ctx, id); err != nil {
		return nil, 0, translate(err, "job")
	}
	logs, total, err := s.repo.ListAudit(ctx, id, limit, offset)
	if err != nil {
		return nil, 0, apperr.Internal(err)
	}
	return logs, total, nil
}

// ---------- helpers ----------

func (s *Service) audit(ctx context.Context, tx repository.Repository, actor Actor, entityID uuid.UUID, action string, changes map[string]any) error {
	raw, err := json.Marshal(changes)
	if err != nil {
		return err
	}
	actorID := actor.ID
	if actorID == "" {
		actorID = "anonymous"
	}
	return tx.CreateAudit(ctx, &domain.AuditLog{
		ID:         s.newID(),
		EntityType: "job",
		EntityID:   entityID,
		Action:     action,
		ActorID:    actorID,
		Changes:    raw,
		RequestID:  actor.RequestID,
		CreatedAt:  s.now(),
	})
}

func (s *Service) buildRequirements(jobID uuid.UUID, in []RequirementInput) ([]domain.Requirement, error) {
	out := make([]domain.Requirement, 0, len(in))
	seen := map[string]bool{}
	for _, r := range in {
		value := strings.TrimSpace(r.Value)
		if value == "" {
			return nil, apperr.Validation("requirement value cannot be empty")
		}
		key := r.Type + "|" + strings.ToLower(value)
		if seen[key] {
			return nil, apperr.Validation(fmt.Sprintf("duplicate requirement %s %q", r.Type, value))
		}
		seen[key] = true
		out = append(out, domain.Requirement{
			ID: s.newID(), JobID: jobID, Type: r.Type, Value: value, IsMandatory: r.IsMandatory, CreatedAt: s.now(),
		})
	}
	return out, nil
}

// buildStages validates the pipeline: unique names, positions 1..n with no gaps.
func (s *Service) buildStages(jobID uuid.UUID, in []StageInput) ([]domain.HiringStage, error) {
	out := make([]domain.HiringStage, 0, len(in))
	names := map[string]bool{}
	for _, st := range in {
		name := strings.TrimSpace(st.Name)
		if name == "" {
			return nil, apperr.Validation("stage name cannot be empty")
		}
		if names[strings.ToLower(name)] {
			return nil, apperr.Validation(fmt.Sprintf("duplicate stage name %q", name))
		}
		names[strings.ToLower(name)] = true
		out = append(out, domain.HiringStage{
			ID: s.newID(), JobID: jobID, Name: name, StageType: st.StageType, Position: st.Position, CreatedAt: s.now(),
		})
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Position < out[j].Position })
	for i, st := range out {
		if st.Position != i+1 {
			return nil, apperr.Validation("stage positions must be 1..n with no gaps or duplicates")
		}
	}
	return out, nil
}

func checkRanges(expMin, expMax *int, salMin, salMax *int64) error {
	if expMin != nil && expMax != nil && *expMax < *expMin {
		return apperr.Validation("experience_max must be >= experience_min")
	}
	if salMin != nil && salMax != nil && *salMax < *salMin {
		return apperr.Validation("salary_max must be >= salary_min")
	}
	return nil
}

func setStr(fields map[string]any, col string, v *string) {
	if v != nil {
		fields[col] = strings.TrimSpace(*v)
	}
}

func upperPtr(s *string) *string {
	if s == nil {
		return nil
	}
	u := strings.ToUpper(*s)
	return &u
}

// translate maps repository errors to client-facing apperr errors.
func translate(err error, entity string) error {
	if err == nil {
		return nil
	}
	var ae *apperr.Error
	switch {
	case errors.As(err, &ae):
		return ae
	case errors.Is(err, repository.ErrNotFound):
		return apperr.NotFound(entity + " not found")
	case errors.Is(err, repository.ErrStaleVersion):
		return apperr.Conflict(entity + " was modified by someone else; reload and retry with the latest version")
	case errors.Is(err, repository.ErrStaleState):
		return apperr.Conflict(entity + " changed concurrently; reload and retry")
	case errors.Is(err, repository.ErrDuplicate):
		return apperr.Conflict(entity + " already exists")
	default:
		return apperr.Internal(err)
	}
}
