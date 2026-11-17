// Package repository is the candidate-service persistence layer (PostgreSQL via GORM).
package repository

import (
	"context"
	"errors"
	"strings"
	"time"

	"github.com/google/uuid"
	"gorm.io/gorm"

	"github.com/usamatajemumba/smart-hire/services/candidate-service/internal/domain"
)

var (
	ErrNotFound     = errors.New("record not found")
	ErrStaleVersion = errors.New("version mismatch: record was modified concurrently")
	ErrDuplicate    = errors.New("duplicate record")
)

// Repository is everything candidate-service needs from storage.
type Repository interface {
	InTx(ctx context.Context, fn func(Repository) error) error

	CreateCandidate(ctx context.Context, c *domain.Candidate) error
	GetCandidate(ctx context.Context, id uuid.UUID) (*domain.Candidate, error)
	ListCandidates(ctx context.Context, f domain.CandidateFilter, limit, offset int) ([]domain.Candidate, int64, error)
	UpdateCandidate(ctx context.Context, id uuid.UUID, expectedVersion int, fields map[string]any) error
	SoftDeleteCandidate(ctx context.Context, id uuid.UUID) error

	ReplaceSkills(ctx context.Context, id uuid.UUID, skills []domain.Skill) error
	ReplaceExperiences(ctx context.Context, id uuid.UUID, exps []domain.WorkExperience) error
	ReplaceEducations(ctx context.Context, id uuid.UUID, edus []domain.Education) error

	CreateResume(ctx context.Context, r *domain.Resume) error
	LatestResume(ctx context.Context, candidateID uuid.UUID) (*domain.Resume, error)

	CreateAudit(ctx context.Context, a *domain.AuditLog) error
}

type gormRepo struct{ db *gorm.DB }

func New(db *gorm.DB) Repository { return &gormRepo{db: db} }

func (r *gormRepo) InTx(ctx context.Context, fn func(Repository) error) error {
	return r.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		return fn(&gormRepo{db: tx})
	})
}

func (r *gormRepo) CreateCandidate(ctx context.Context, c *domain.Candidate) error {
	return mapErr(r.db.WithContext(ctx).Create(c).Error)
}

func (r *gormRepo) GetCandidate(ctx context.Context, id uuid.UUID) (*domain.Candidate, error) {
	var c domain.Candidate
	err := r.db.WithContext(ctx).
		Preload("Skills", func(db *gorm.DB) *gorm.DB { return db.Order("years DESC, name") }).
		Preload("Experiences", func(db *gorm.DB) *gorm.DB { return db.Order("start_date DESC") }).
		Preload("Educations", func(db *gorm.DB) *gorm.DB { return db.Order("start_year DESC") }).
		First(&c, "id = ?", id).Error
	if err != nil {
		return nil, mapErr(err)
	}
	res, err := r.LatestResume(ctx, id)
	if err != nil && !errors.Is(err, ErrNotFound) {
		return nil, err
	}
	c.Resume = res
	return &c, nil
}

func (r *gormRepo) ListCandidates(ctx context.Context, f domain.CandidateFilter, limit, offset int) ([]domain.Candidate, int64, error) {
	q := r.db.WithContext(ctx).Model(&domain.Candidate{})
	if f.Location != "" {
		q = q.Where("lower(location) = lower(?)", f.Location)
	}
	if f.Query != "" {
		like := "%" + escapeLike(f.Query) + "%"
		q = q.Where("full_name ILIKE ? OR email ILIKE ? OR headline ILIKE ?", like, like, like)
	}
	if f.Skill != "" {
		// Semi-join: EXISTS stops at the first match and never duplicates rows.
		q = q.Where("EXISTS (SELECT 1 FROM candidate_skills s WHERE s.candidate_id = candidates.id AND lower(s.name) = lower(?))", f.Skill)
	}
	var total int64
	if err := q.Count(&total).Error; err != nil {
		return nil, 0, err
	}
	out := []domain.Candidate{}
	err := q.Preload("Skills").Order("created_at DESC, id DESC").Limit(limit).Offset(offset).Find(&out).Error
	return out, total, err
}

func (r *gormRepo) UpdateCandidate(ctx context.Context, id uuid.UUID, expectedVersion int, fields map[string]any) error {
	fields["version"] = gorm.Expr("version + 1")
	fields["updated_at"] = time.Now().UTC()
	res := r.db.WithContext(ctx).Model(&domain.Candidate{}).
		Where("id = ? AND version = ?", id, expectedVersion).Updates(fields)
	if res.Error != nil {
		return mapErr(res.Error)
	}
	if res.RowsAffected == 0 {
		var n int64
		if err := r.db.WithContext(ctx).Model(&domain.Candidate{}).Where("id = ?", id).Count(&n).Error; err != nil {
			return err
		}
		if n == 0 {
			return ErrNotFound
		}
		return ErrStaleVersion
	}
	return nil
}

func (r *gormRepo) SoftDeleteCandidate(ctx context.Context, id uuid.UUID) error {
	res := r.db.WithContext(ctx).Delete(&domain.Candidate{}, "id = ?", id)
	if res.Error != nil {
		return res.Error
	}
	if res.RowsAffected == 0 {
		return ErrNotFound
	}
	return nil
}

func (r *gormRepo) ReplaceSkills(ctx context.Context, id uuid.UUID, skills []domain.Skill) error {
	return replaceChildren(ctx, r, id, &domain.Skill{}, skills)
}

func (r *gormRepo) ReplaceExperiences(ctx context.Context, id uuid.UUID, exps []domain.WorkExperience) error {
	return replaceChildren(ctx, r, id, &domain.WorkExperience{}, exps)
}

func (r *gormRepo) ReplaceEducations(ctx context.Context, id uuid.UUID, edus []domain.Education) error {
	return replaceChildren(ctx, r, id, &domain.Education{}, edus)
}

// replaceChildren deletes all rows of one child table for the candidate,
// inserts the new set and bumps the candidate version. Callers run it in a tx.
func replaceChildren[T any](ctx context.Context, r *gormRepo, id uuid.UUID, model *T, rows []T) error {
	db := r.db.WithContext(ctx)
	if err := db.Where("candidate_id = ?", id).Delete(model).Error; err != nil {
		return err
	}
	if len(rows) > 0 {
		if err := db.Create(&rows).Error; err != nil {
			return mapErr(err)
		}
	}
	return db.Model(&domain.Candidate{}).Where("id = ?", id).
		Updates(map[string]any{"version": gorm.Expr("version + 1"), "updated_at": time.Now().UTC()}).Error
}

func (r *gormRepo) CreateResume(ctx context.Context, res *domain.Resume) error {
	return r.db.WithContext(ctx).Create(res).Error
}

func (r *gormRepo) LatestResume(ctx context.Context, candidateID uuid.UUID) (*domain.Resume, error) {
	var res domain.Resume
	err := r.db.WithContext(ctx).Where("candidate_id = ?", candidateID).
		Order("uploaded_at DESC").First(&res).Error
	if err != nil {
		return nil, mapErr(err)
	}
	return &res, nil
}

func (r *gormRepo) CreateAudit(ctx context.Context, a *domain.AuditLog) error {
	return r.db.WithContext(ctx).Create(a).Error
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
