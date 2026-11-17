// Package service holds candidate-service business logic.
package service

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"path/filepath"
	"strings"
	"time"

	"github.com/google/uuid"

	"github.com/usamatajemumba/smart-hire/pkg/apperr"
	"github.com/usamatajemumba/smart-hire/services/candidate-service/internal/domain"
	"github.com/usamatajemumba/smart-hire/services/candidate-service/internal/repository"
	"github.com/usamatajemumba/smart-hire/services/candidate-service/internal/storage"
)

// MaxResumeBytes caps resume uploads (5 MiB).
const MaxResumeBytes = 5 << 20

type Actor struct {
	ID        string
	RequestID string
}

type Service struct {
	repo  repository.Repository
	blobs storage.BlobStore
	now   func() time.Time
	newID func() uuid.UUID
}

func New(repo repository.Repository, blobs storage.BlobStore) *Service {
	return &Service{
		repo:  repo,
		blobs: blobs,
		now:   func() time.Time { return time.Now().UTC() },
		newID: func() uuid.UUID { return uuid.Must(uuid.NewV7()) },
	}
}

type RegisterInput struct {
	Email           string
	FullName        string
	Phone           string
	Location        string
	Headline        string
	Summary         string
	YearsExperience int
	Skills          []SkillInput
}

type UpdateInput struct {
	Version         int
	FullName        *string
	Phone           *string
	Location        *string
	Headline        *string
	Summary         *string
	YearsExperience *int
}

type SkillInput struct {
	Name  string
	Level string
	Years int
}

type ExperienceInput struct {
	Company     string
	Title       string
	StartDate   time.Time
	EndDate     *time.Time
	Description string
}

type EducationInput struct {
	Institution  string
	Degree       string
	FieldOfStudy string
	StartYear    int
	EndYear      *int
}

// Register creates a candidate profile. Email uniqueness is enforced by a
// unique index, not a SELECT-then-INSERT check, so two concurrent sign-ups
// with the same email cannot both succeed.
func (s *Service) Register(ctx context.Context, actor Actor, in RegisterInput) (*domain.Candidate, error) {
	now := s.now()
	c := &domain.Candidate{
		ID:              s.newID(),
		Email:           strings.ToLower(strings.TrimSpace(in.Email)),
		FullName:        strings.TrimSpace(in.FullName),
		Phone:           strings.TrimSpace(in.Phone),
		Location:        strings.TrimSpace(in.Location),
		Headline:        strings.TrimSpace(in.Headline),
		Summary:         strings.TrimSpace(in.Summary),
		YearsExperience: in.YearsExperience,
		Version:         1,
		CreatedAt:       now,
		UpdatedAt:       now,
	}
	skills, err := s.buildSkills(c.ID, in.Skills)
	if err != nil {
		return nil, err
	}
	c.Skills = skills

	err = s.repo.InTx(ctx, func(tx repository.Repository) error {
		if err := tx.CreateCandidate(ctx, c); err != nil {
			return err
		}
		return s.audit(ctx, tx, actor, c.ID, "candidate.registered", map[string]any{"email": c.Email})
	})
	if err != nil {
		return nil, translate(err, "candidate with this email")
	}
	c.Experiences, c.Educations = []domain.WorkExperience{}, []domain.Education{}
	return c, nil
}

func (s *Service) Get(ctx context.Context, id uuid.UUID) (*domain.Candidate, error) {
	c, err := s.repo.GetCandidate(ctx, id)
	if err != nil {
		return nil, translate(err, "candidate")
	}
	return c, nil
}

func (s *Service) List(ctx context.Context, f domain.CandidateFilter, limit, offset int) ([]domain.Candidate, int64, error) {
	out, total, err := s.repo.ListCandidates(ctx, f, limit, offset)
	if err != nil {
		return nil, 0, apperr.Internal(err)
	}
	return out, total, nil
}

// Update applies a partial profile update with optimistic locking.
func (s *Service) Update(ctx context.Context, actor Actor, id uuid.UUID, in UpdateInput) (*domain.Candidate, error) {
	fields := map[string]any{}
	for col, v := range map[string]*string{
		"full_name": in.FullName, "phone": in.Phone, "location": in.Location,
		"headline": in.Headline, "summary": in.Summary,
	} {
		if v != nil {
			fields[col] = strings.TrimSpace(*v)
		}
	}
	if in.YearsExperience != nil {
		fields["years_experience"] = *in.YearsExperience
	}
	if len(fields) == 0 {
		return nil, apperr.Validation("no fields to update")
	}
	if name, ok := fields["full_name"].(string); ok && name == "" {
		return nil, apperr.Validation("full_name cannot be empty")
	}
	changes := make(map[string]any, len(fields))
	for k, v := range fields {
		changes[k] = v
	}

	var out *domain.Candidate
	err := s.repo.InTx(ctx, func(tx repository.Repository) error {
		if err := tx.UpdateCandidate(ctx, id, in.Version, fields); err != nil {
			return err
		}
		if err := s.audit(ctx, tx, actor, id, "candidate.updated", changes); err != nil {
			return err
		}
		var err error
		out, err = tx.GetCandidate(ctx, id)
		return err
	})
	if err != nil {
		return nil, translate(err, "candidate")
	}
	return out, nil
}

func (s *Service) Delete(ctx context.Context, actor Actor, id uuid.UUID) error {
	err := s.repo.InTx(ctx, func(tx repository.Repository) error {
		if err := tx.SoftDeleteCandidate(ctx, id); err != nil {
			return err
		}
		return s.audit(ctx, tx, actor, id, "candidate.deleted", nil)
	})
	return translate(err, "candidate")
}

func (s *Service) SetSkills(ctx context.Context, actor Actor, id uuid.UUID, in []SkillInput) (*domain.Candidate, error) {
	skills, err := s.buildSkills(id, in)
	if err != nil {
		return nil, err
	}
	return s.replace(ctx, actor, id, "candidate.skills_replaced", len(skills),
		func(tx repository.Repository) error { return tx.ReplaceSkills(ctx, id, skills) })
}

func (s *Service) SetExperiences(ctx context.Context, actor Actor, id uuid.UUID, in []ExperienceInput) (*domain.Candidate, error) {
	out := make([]domain.WorkExperience, 0, len(in))
	for _, e := range in {
		if e.EndDate != nil && e.EndDate.Before(e.StartDate) {
			return nil, apperr.Validation(fmt.Sprintf("experience at %q: end_date before start_date", e.Company))
		}
		if e.StartDate.After(s.now()) {
			return nil, apperr.Validation(fmt.Sprintf("experience at %q: start_date is in the future", e.Company))
		}
		out = append(out, domain.WorkExperience{
			ID: s.newID(), CandidateID: id, Company: strings.TrimSpace(e.Company), Title: strings.TrimSpace(e.Title),
			StartDate: e.StartDate, EndDate: e.EndDate, Description: strings.TrimSpace(e.Description), CreatedAt: s.now(),
		})
	}
	return s.replace(ctx, actor, id, "candidate.experiences_replaced", len(out),
		func(tx repository.Repository) error { return tx.ReplaceExperiences(ctx, id, out) })
}

func (s *Service) SetEducations(ctx context.Context, actor Actor, id uuid.UUID, in []EducationInput) (*domain.Candidate, error) {
	out := make([]domain.Education, 0, len(in))
	for _, e := range in {
		if e.EndYear != nil && *e.EndYear < e.StartYear {
			return nil, apperr.Validation(fmt.Sprintf("education at %q: end_year before start_year", e.Institution))
		}
		out = append(out, domain.Education{
			ID: s.newID(), CandidateID: id, Institution: strings.TrimSpace(e.Institution), Degree: strings.TrimSpace(e.Degree),
			FieldOfStudy: strings.TrimSpace(e.FieldOfStudy), StartYear: e.StartYear, EndYear: e.EndYear, CreatedAt: s.now(),
		})
	}
	return s.replace(ctx, actor, id, "candidate.educations_replaced", len(out),
		func(tx repository.Repository) error { return tx.ReplaceEducations(ctx, id, out) })
}

func (s *Service) replace(ctx context.Context, actor Actor, id uuid.UUID, action string, count int, apply func(repository.Repository) error) (*domain.Candidate, error) {
	var out *domain.Candidate
	err := s.repo.InTx(ctx, func(tx repository.Repository) error {
		if _, err := tx.GetCandidate(ctx, id); err != nil { // 404 before touching children
			return err
		}
		if err := apply(tx); err != nil {
			return err
		}
		if err := s.audit(ctx, tx, actor, id, action, map[string]any{"count": count}); err != nil {
			return err
		}
		var err error
		out, err = tx.GetCandidate(ctx, id)
		return err
	})
	if err != nil {
		return nil, translate(err, "candidate")
	}
	return out, nil
}

// allowedResumeTypes maps a sniffed MIME type to accepted file extensions.
// We trust the bytes (magic numbers), not the client's Content-Type header.
// DOCX files are ZIP archives, so they sniff as application/zip.
var allowedResumeTypes = map[string][]string{
	"application/pdf": {".pdf"},
	"application/zip": {".docx"},
}

// UploadResume validates and stores a resume, then records its metadata.
// Order matters: file first, DB row second. If the DB insert fails we delete
// the file (compensating action), so no row ever points at a missing file.
func (s *Service) UploadResume(ctx context.Context, actor Actor, id uuid.UUID, fileName string, r io.Reader) (*domain.Resume, error) {
	if _, err := s.repo.GetCandidate(ctx, id); err != nil {
		return nil, translate(err, "candidate")
	}

	// Peek the first 512 bytes to detect the real type, then stitch them back.
	head := make([]byte, 512)
	n, err := io.ReadFull(r, head)
	if err != nil && !errors.Is(err, io.ErrUnexpectedEOF) && !errors.Is(err, io.EOF) {
		return nil, apperr.Validation("could not read upload")
	}
	if n == 0 {
		return nil, apperr.Validation("file is empty")
	}
	head = head[:n]
	contentType := http.DetectContentType(head)
	ext := strings.ToLower(filepath.Ext(fileName))
	if !contains(allowedResumeTypes[contentType], ext) {
		return nil, apperr.Validation("resume must be a PDF or DOCX file").
			WithDetails(map[string]any{"detected_type": contentType, "extension": ext})
	}
	if ext == ".docx" {
		contentType = "application/vnd.openxmlformats-officedocument.wordprocessingml.document"
	}

	resumeID := s.newID()
	key := fmt.Sprintf("candidates/%s/%s%s", id, resumeID, ext)
	// LimitReader(+1) lets us detect oversize files without buffering them.
	body := io.LimitReader(io.MultiReader(bytes.NewReader(head), r), MaxResumeBytes+1)
	size, err := s.blobs.Save(ctx, key, body)
	if err != nil {
		return nil, apperr.Internal(err)
	}
	if size > MaxResumeBytes {
		_ = s.blobs.Delete(ctx, key)
		return nil, apperr.Validation(fmt.Sprintf("resume exceeds %d MiB", MaxResumeBytes>>20))
	}

	res := &domain.Resume{
		ID: resumeID, CandidateID: id, FileName: filepath.Base(fileName),
		ContentType: contentType, SizeBytes: size, StorageKey: key, UploadedAt: s.now(),
	}
	err = s.repo.InTx(ctx, func(tx repository.Repository) error {
		if err := tx.CreateResume(ctx, res); err != nil {
			return err
		}
		return s.audit(ctx, tx, actor, id, "candidate.resume_uploaded",
			map[string]any{"resume_id": res.ID, "file_name": res.FileName, "size_bytes": size})
	})
	if err != nil {
		_ = s.blobs.Delete(ctx, key)
		return nil, translate(err, "resume")
	}
	return res, nil
}

// OpenResume returns the latest resume metadata and a reader for its bytes.
// The caller must close the reader.
func (s *Service) OpenResume(ctx context.Context, id uuid.UUID) (*domain.Resume, io.ReadCloser, error) {
	res, err := s.repo.LatestResume(ctx, id)
	if err != nil {
		return nil, nil, translate(err, "resume")
	}
	rc, err := s.blobs.Open(ctx, res.StorageKey)
	if errors.Is(err, storage.ErrNotFound) {
		return nil, nil, apperr.NotFound("resume file missing from storage")
	}
	if err != nil {
		return nil, nil, apperr.Internal(err)
	}
	return res, rc, nil
}

// ---------- helpers ----------

var skillLevels = map[string]bool{"beginner": true, "intermediate": true, "advanced": true, "expert": true}

func (s *Service) buildSkills(candidateID uuid.UUID, in []SkillInput) ([]domain.Skill, error) {
	out := make([]domain.Skill, 0, len(in))
	seen := map[string]bool{}
	for _, sk := range in {
		name := strings.TrimSpace(sk.Name)
		if name == "" {
			return nil, apperr.Validation("skill name cannot be empty")
		}
		if seen[strings.ToLower(name)] {
			return nil, apperr.Validation(fmt.Sprintf("duplicate skill %q", name))
		}
		seen[strings.ToLower(name)] = true
		level := sk.Level
		if level == "" {
			level = "intermediate"
		}
		if !skillLevels[level] {
			return nil, apperr.Validation(fmt.Sprintf("invalid level %q for skill %q", level, name))
		}
		out = append(out, domain.Skill{
			ID: s.newID(), CandidateID: candidateID, Name: name, Level: level, Years: sk.Years, CreatedAt: s.now(),
		})
	}
	return out, nil
}

func (s *Service) audit(ctx context.Context, tx repository.Repository, actor Actor, id uuid.UUID, action string, changes map[string]any) error {
	if changes == nil {
		changes = map[string]any{}
	}
	raw, err := json.Marshal(changes)
	if err != nil {
		return err
	}
	actorID := actor.ID
	if actorID == "" {
		actorID = "anonymous"
	}
	return tx.CreateAudit(ctx, &domain.AuditLog{
		ID: s.newID(), EntityType: "candidate", EntityID: id, Action: action,
		ActorID: actorID, Changes: raw, RequestID: actor.RequestID, CreatedAt: s.now(),
	})
}

func contains(list []string, v string) bool {
	for _, x := range list {
		if x == v {
			return true
		}
	}
	return false
}

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
	case errors.Is(err, repository.ErrDuplicate):
		return apperr.Conflict(entity + " already exists")
	default:
		return apperr.Internal(err)
	}
}
