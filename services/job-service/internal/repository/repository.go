// Package repository is the job-service persistence layer (PostgreSQL via GORM).
//
// The service layer depends on the Repository interface, not on GORM, so
// business rules can be unit-tested with an in-memory fake.
package repository

import (
	"context"
	"errors"
	"strings"
	"time"

	"github.com/google/uuid"
	"gorm.io/gorm"

	"github.com/usamatajemumba/smart-hire/services/job-service/internal/domain"
)

// Storage-level errors. The service layer translates them into apperr kinds.
var (
	ErrNotFound     = errors.New("record not found")
	ErrStaleVersion = errors.New("version mismatch: record was modified concurrently")
	ErrStaleState   = errors.New("status changed concurrently")
	ErrDuplicate    = errors.New("duplicate record")
)

// Repository is everything job-service needs from storage.
type Repository interface {
	// InTx runs fn inside one database transaction. If fn returns an error the
	// whole unit (job change + audit row) rolls back.
	InTx(ctx context.Context, fn func(Repository) error) error

	CreateJob(ctx context.Context, job *domain.Job) error
	GetJob(ctx context.Context, id uuid.UUID) (*domain.Job, error)
	ListJobs(ctx context.Context, f domain.JobFilter, limit, offset int) ([]domain.Job, int64, error)
	// UpdateJob applies fields only if the stored version equals expectedVersion,
	// then bumps version (optimistic locking).
	UpdateJob(ctx context.Context, id uuid.UUID, expectedVersion int, fields map[string]any) error
	// TransitionStatus moves from -> to atomically; fails with ErrStaleState if
	// the job is no longer in `from` or its version changed since it was read.
	TransitionStatus(ctx context.Context, id uuid.UUID, from, to domain.JobStatus, expectedVersion int, extra map[string]any) error
	SoftDeleteJob(ctx context.Context, id uuid.UUID) error
	ReplaceRequirements(ctx context.Context, jobID uuid.UUID, reqs []domain.Requirement) error
	ReplaceStages(ctx context.Context, jobID uuid.UUID, stages []domain.HiringStage) error

	CreateAudit(ctx context.Context, a *domain.AuditLog) error
	ListAudit(ctx context.Context, entityID uuid.UUID, limit, offset int) ([]domain.AuditLog, int64, error)
}

type gormRepo struct{ db *gorm.DB }

// New returns a GORM-backed Repository.
func New(db *gorm.DB) Repository { return &gormRepo{db: db} }

func (r *gormRepo) InTx(ctx context.Context, fn func(Repository) error) error {
	return r.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		return fn(&gormRepo{db: tx})
	})
}

func (r *gormRepo) CreateJob(ctx context.Context, job *domain.Job) error {
	// GORM also inserts the Requirements and Stages associations.
	return mapErr(r.db.WithContext(ctx).Create(job).Error)
}

func (r *gormRepo) GetJob(ctx context.Context, id uuid.UUID) (*domain.Job, error) {
	var job domain.Job
	err := r.withChildren(ctx).First(&job, "id = ?", id).Error
	if err != nil {
		return nil, mapErr(err)
	}
	return &job, nil
}

func (r *gormRepo) ListJobs(ctx context.Context, f domain.JobFilter, limit, offset int) ([]domain.Job, int64, error) {
	q := r.db.WithContext(ctx).Model(&domain.Job{})
	if f.Status != "" {
		q = q.Where("status = ?", f.Status)
	}
	if f.RecruiterID != nil {
		q = q.Where("recruiter_id = ?", *f.RecruiterID)
	}
	if f.Location != "" {
		q = q.Where("lower(location) = lower(?)", f.Location)
	}
	if f.Query != "" {
		like := "%" + escapeLike(f.Query) + "%"
		q = q.Where("title ILIKE ? OR description ILIKE ?", like, like)
	}

	var total int64
	if err := q.Count(&total).Error; err != nil {
		return nil, 0, err
	}
	jobs := []domain.Job{}
	err := q.Scopes(childrenPreload).
		Order("created_at DESC, id DESC"). // id breaks ties -> stable pages
		Limit(limit).Offset(offset).
		Find(&jobs).Error
	return jobs, total, err
}

func (r *gormRepo) UpdateJob(ctx context.Context, id uuid.UUID, expectedVersion int, fields map[string]any) error {
	fields["version"] = gorm.Expr("version + 1")
	fields["updated_at"] = time.Now().UTC()
	res := r.db.WithContext(ctx).Model(&domain.Job{}).
		Where("id = ? AND version = ?", id, expectedVersion).
		Updates(fields)
	if res.Error != nil {
		return mapErr(res.Error)
	}
	if res.RowsAffected == 0 {
		return r.missingOr(ctx, id, ErrStaleVersion)
	}
	return nil
}

func (r *gormRepo) TransitionStatus(ctx context.Context, id uuid.UUID, from, to domain.JobStatus, expectedVersion int, extra map[string]any) error {
	fields := map[string]any{
		"status":     to,
		"version":    gorm.Expr("version + 1"),
		"updated_at": time.Now().UTC(),
	}
	for k, v := range extra {
		fields[k] = v
	}
	// The WHERE clause is the concurrency guard: two recruiters clicking
	// "publish" at once -> exactly one UPDATE matches, the other gets 0 rows.
	res := r.db.WithContext(ctx).Model(&domain.Job{}).
		Where("id = ? AND status = ? AND version = ?", id, from, expectedVersion).
		Updates(fields)
	if res.Error != nil {
		return mapErr(res.Error)
	}
	if res.RowsAffected == 0 {
		return r.missingOr(ctx, id, ErrStaleState)
	}
	return nil
}

func (r *gormRepo) SoftDeleteJob(ctx context.Context, id uuid.UUID) error {
	res := r.db.WithContext(ctx).Delete(&domain.Job{}, "id = ?", id)
	if res.Error != nil {
		return res.Error
	}
	if res.RowsAffected == 0 {
		return ErrNotFound
	}
	return nil
}

func (r *gormRepo) ReplaceRequirements(ctx context.Context, jobID uuid.UUID, reqs []domain.Requirement) error {
	db := r.db.WithContext(ctx)
	if err := db.Where("job_id = ?", jobID).Delete(&domain.Requirement{}).Error; err != nil {
		return err
	}
	if len(reqs) > 0 {
		if err := db.Create(&reqs).Error; err != nil {
			return mapErr(err)
		}
	}
	return r.touch(ctx, jobID)
}

func (r *gormRepo) ReplaceStages(ctx context.Context, jobID uuid.UUID, stages []domain.HiringStage) error {
	db := r.db.WithContext(ctx)
	if err := db.Where("job_id = ?", jobID).Delete(&domain.HiringStage{}).Error; err != nil {
		return err
	}
	if len(stages) > 0 {
		if err := db.Create(&stages).Error; err != nil {
			return mapErr(err)
		}
	}
	return r.touch(ctx, jobID)
}

func (r *gormRepo) CreateAudit(ctx context.Context, a *domain.AuditLog) error {
	return r.db.WithContext(ctx).Create(a).Error
}

func (r *gormRepo) ListAudit(ctx context.Context, entityID uuid.UUID, limit, offset int) ([]domain.AuditLog, int64, error) {
	q := r.db.WithContext(ctx).Model(&domain.AuditLog{}).Where("entity_id = ?", entityID)
	var total int64
	if err := q.Count(&total).Error; err != nil {
		return nil, 0, err
	}
	logs := []domain.AuditLog{}
	err := q.Order("created_at DESC").Limit(limit).Offset(offset).Find(&logs).Error
	return logs, total, err
}

// touch bumps the job version so child changes invalidate stale reads of the
// aggregate (e.g. a concurrent publish that validated the old requirements).
func (r *gormRepo) touch(ctx context.Context, jobID uuid.UUID) error {
	return r.db.WithContext(ctx).Model(&domain.Job{}).Where("id = ?", jobID).
		Updates(map[string]any{"version": gorm.Expr("version + 1"), "updated_at": time.Now().UTC()}).Error
}

// missingOr distinguishes "row gone" from "row changed" after a 0-row update.
func (r *gormRepo) missingOr(ctx context.Context, id uuid.UUID, other error) error {
	var n int64
	if err := r.db.WithContext(ctx).Model(&domain.Job{}).Where("id = ?", id).Count(&n).Error; err != nil {
		return err
	}
	if n == 0 {
		return ErrNotFound
	}
	return other
}

func (r *gormRepo) withChildren(ctx context.Context) *gorm.DB {
	return r.db.WithContext(ctx).Scopes(childrenPreload)
}

func childrenPreload(db *gorm.DB) *gorm.DB {
	return db.
		Preload("Requirements", func(db *gorm.DB) *gorm.DB { return db.Order("created_at, value") }).
		Preload("Stages", func(db *gorm.DB) *gorm.DB { return db.Order("position") })
}

func mapErr(err error) error {
	switch {
	case err == nil:
		return nil
	case errors.Is(err, gorm.ErrRecordNotFound):
		return ErrNotFound
	case errors.Is(err, gorm.ErrDuplicatedKey):
		return ErrDuplicate
	default:
		return err
	}
}

func escapeLike(s string) string {
	return strings.NewReplacer(`\`, `\\`, `%`, `\%`, `_`, `\_`).Replace(s)
}
