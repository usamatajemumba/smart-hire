package service

import (
	"bytes"
	"context"
	"io/fs"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/usamatajemumba/smart-hire/pkg/apperr"
	"github.com/usamatajemumba/smart-hire/services/candidate-service/internal/domain"
	"github.com/usamatajemumba/smart-hire/services/candidate-service/internal/repository"
	"github.com/usamatajemumba/smart-hire/services/candidate-service/internal/storage"
)

// fakeRepo implements only what these tests exercise; the embedded nil
// interface makes any other call panic loudly instead of silently passing.
type fakeRepo struct {
	repository.Repository
	candidates map[uuid.UUID]*domain.Candidate
	emails     map[string]bool
	resumes    []domain.Resume
	failResume bool
}

func newFakeRepo() *fakeRepo {
	return &fakeRepo{candidates: map[uuid.UUID]*domain.Candidate{}, emails: map[string]bool{}}
}

func (f *fakeRepo) InTx(_ context.Context, fn func(repository.Repository) error) error { return fn(f) }

func (f *fakeRepo) CreateCandidate(_ context.Context, c *domain.Candidate) error {
	if f.emails[c.Email] {
		return repository.ErrDuplicate
	}
	f.emails[c.Email] = true
	f.candidates[c.ID] = c
	return nil
}

func (f *fakeRepo) GetCandidate(_ context.Context, id uuid.UUID) (*domain.Candidate, error) {
	c, ok := f.candidates[id]
	if !ok {
		return nil, repository.ErrNotFound
	}
	return c, nil
}

func (f *fakeRepo) CreateResume(_ context.Context, r *domain.Resume) error {
	if f.failResume {
		return repository.ErrDuplicate
	}
	f.resumes = append(f.resumes, *r)
	return nil
}

func (f *fakeRepo) CreateAudit(context.Context, *domain.AuditLog) error { return nil }

var ctx = context.Background()

// newSvc returns the service, its fake repo and the blob storage root dir.
func newSvc(t *testing.T) (*Service, *fakeRepo, string) {
	repo := newFakeRepo()
	dir := t.TempDir()
	blobs, err := storage.NewLocal(dir)
	if err != nil {
		t.Fatal(err)
	}
	s := New(repo, blobs)
	s.now = func() time.Time { return time.Date(2026, 11, 17, 0, 0, 0, 0, time.UTC) }
	return s, repo, dir
}

// countFiles counts regular files under dir.
func countFiles(t *testing.T, dir string) int {
	t.Helper()
	n := 0
	err := filepath.WalkDir(dir, func(_ string, d fs.DirEntry, err error) error {
		if err == nil && !d.IsDir() {
			n++
		}
		return err
	})
	if err != nil {
		t.Fatal(err)
	}
	return n
}

func register(t *testing.T, s *Service) *domain.Candidate {
	t.Helper()
	c, err := s.Register(ctx, Actor{}, RegisterInput{Email: "Ada@Example.com", FullName: "Ada Lovelace"})
	if err != nil {
		t.Fatal(err)
	}
	return c
}

func TestRegister_NormalisesEmailAndRejectsDuplicates(t *testing.T) {
	s, _, _ := newSvc(t)
	c := register(t, s)
	if c.Email != "ada@example.com" {
		t.Fatalf("email not normalised: %q", c.Email)
	}
	_, err := s.Register(ctx, Actor{}, RegisterInput{Email: "ADA@example.com ", FullName: "Imposter"})
	if apperr.KindOf(err) != apperr.KindConflict {
		t.Fatalf("want CONFLICT, got %v", err)
	}
}

func TestRegister_DuplicateSkill(t *testing.T) {
	s, _, _ := newSvc(t)
	_, err := s.Register(ctx, Actor{}, RegisterInput{
		Email: "x@y.com", FullName: "X Y",
		Skills: []SkillInput{{Name: "Go"}, {Name: "go"}},
	})
	if apperr.KindOf(err) != apperr.KindValidation {
		t.Fatalf("want VALIDATION_ERROR, got %v", err)
	}
}

func TestUploadResume(t *testing.T) {
	pdf := "%PDF-1.7\n" + strings.Repeat("x", 100)

	t.Run("accepts real PDF", func(t *testing.T) {
		s, repo, _ := newSvc(t)
		c := register(t, s)
		res, err := s.UploadResume(ctx, Actor{}, c.ID, "cv.pdf", strings.NewReader(pdf))
		if err != nil {
			t.Fatal(err)
		}
		if res.ContentType != "application/pdf" || res.SizeBytes != int64(len(pdf)) || len(repo.resumes) != 1 {
			t.Fatalf("unexpected resume: %+v", res)
		}
	})

	t.Run("rejects text renamed to .pdf", func(t *testing.T) {
		s, _, _ := newSvc(t)
		c := register(t, s)
		_, err := s.UploadResume(ctx, Actor{}, c.ID, "cv.pdf", strings.NewReader("just text"))
		if apperr.KindOf(err) != apperr.KindValidation {
			t.Fatalf("want VALIDATION_ERROR, got %v", err)
		}
	})

	t.Run("rejects oversize file and leaves no blob", func(t *testing.T) {
		s, repo, dir := newSvc(t)
		c := register(t, s)
		big := append([]byte("%PDF-1.7\n"), bytes.Repeat([]byte("x"), MaxResumeBytes)...)
		_, err := s.UploadResume(ctx, Actor{}, c.ID, "cv.pdf", bytes.NewReader(big))
		if apperr.KindOf(err) != apperr.KindValidation || len(repo.resumes) != 0 {
			t.Fatalf("want VALIDATION_ERROR and no row, got %v", err)
		}
		if n := countFiles(t, dir); n != 0 {
			t.Fatalf("oversize upload left %d file(s) in storage", n)
		}
	})

	t.Run("DB failure removes stored file", func(t *testing.T) {
		s, repo, dir := newSvc(t)
		c := register(t, s)
		repo.failResume = true
		_, err := s.UploadResume(ctx, Actor{}, c.ID, "cv.pdf", strings.NewReader(pdf))
		if err == nil {
			t.Fatal("expected error")
		}
		// No resume row was written, and the compensating delete ran.
		if len(repo.resumes) != 0 {
			t.Fatal("resume row should not exist")
		}
		if n := countFiles(t, dir); n != 0 {
			t.Fatalf("failed DB insert left %d orphan file(s)", n)
		}
	})

	t.Run("unknown candidate", func(t *testing.T) {
		s, _, _ := newSvc(t)
		_, err := s.UploadResume(ctx, Actor{}, uuid.New(), "cv.pdf", strings.NewReader(pdf))
		if apperr.KindOf(err) != apperr.KindNotFound {
			t.Fatalf("want NOT_FOUND, got %v", err)
		}
	})
}

func TestSetExperiences_DateRules(t *testing.T) {
	s, _, _ := newSvc(t)
	c := register(t, s)
	start := time.Date(2024, 1, 1, 0, 0, 0, 0, time.UTC)
	end := start.AddDate(-1, 0, 0)
	_, err := s.SetExperiences(ctx, Actor{}, c.ID, []ExperienceInput{{Company: "Acme", Title: "Dev", StartDate: start, EndDate: &end}})
	if apperr.KindOf(err) != apperr.KindValidation {
		t.Fatalf("want VALIDATION_ERROR, got %v", err)
	}
}
