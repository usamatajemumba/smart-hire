// Package domain holds job-service entities and pure business rules.
// It has no knowledge of HTTP or SQL beyond GORM mapping tags.
package domain

import (
	"encoding/json"
	"strings"
	"time"

	"github.com/google/uuid"
	"gorm.io/gorm"
)

// JobStatus is the job lifecycle state.
type JobStatus string

const (
	StatusDraft     JobStatus = "draft"
	StatusPublished JobStatus = "published"
	StatusClosed    JobStatus = "closed"
	StatusArchived  JobStatus = "archived"
	// Week 2: StatusProcessing is inserted between draft and published when
	// the Temporal publishing workflow takes over.
)

// transitions is the job state machine. Anything not listed is rejected.
//
//	draft ──publish──▶ published ──close──▶ closed ──archive──▶ archived
//	  └───────────────────archive──────────────────────────────▶
var transitions = map[JobStatus][]JobStatus{
	StatusDraft:     {StatusPublished, StatusArchived},
	StatusPublished: {StatusClosed},
	StatusClosed:    {StatusArchived},
}

// CanTransition reports whether the state machine allows from -> to.
func CanTransition(from, to JobStatus) bool {
	for _, s := range transitions[from] {
		if s == to {
			return true
		}
	}
	return false
}

// IsEditable reports whether job fields may still change in this state.
func (s JobStatus) IsEditable() bool { return s == StatusDraft || s == StatusPublished }

// Allowed enum values, shared by validation and docs.
var (
	EmploymentTypes = []string{"full_time", "part_time", "contract", "internship", "temporary"}
	WorkModes       = []string{"onsite", "remote", "hybrid"}
	RequirementType = []string{"skill", "qualification", "experience", "other"}
	StageTypes      = []string{"screening", "assessment", "interview", "offer"}
)

// Job is the aggregate root: requirements and stages only change through it.
type Job struct {
	ID             uuid.UUID      `gorm:"type:uuid;primaryKey" json:"id"`
	RecruiterID    uuid.UUID      `gorm:"type:uuid" json:"recruiter_id"`
	Title          string         `json:"title"`
	Description    string         `json:"description"`
	Department     string         `json:"department"`
	Location       string         `json:"location"`
	EmploymentType string         `json:"employment_type"`
	WorkMode       string         `json:"work_mode"`
	ExperienceMin  *int           `json:"experience_min"`
	ExperienceMax  *int           `json:"experience_max"`
	SalaryMin      *int64         `json:"salary_min"`
	SalaryMax      *int64         `json:"salary_max"`
	Currency       *string        `json:"currency"`
	Status         JobStatus      `json:"status"`
	Version        int            `json:"version"`
	PublishedAt    *time.Time     `json:"published_at"`
	ClosedAt       *time.Time     `json:"closed_at"`
	CreatedAt      time.Time      `json:"created_at"`
	UpdatedAt      time.Time      `json:"updated_at"`
	DeletedAt      gorm.DeletedAt `json:"-"`

	Requirements []Requirement `gorm:"foreignKey:JobID" json:"requirements"`
	Stages       []HiringStage `gorm:"foreignKey:JobID" json:"stages"`
}

// PublishProblems returns every reason the job cannot be published yet
// (empty slice means publishable). Returning all problems at once gives the
// recruiter one actionable error instead of fix-one-retry loops.
func (j *Job) PublishProblems() []string {
	var p []string
	if strings.TrimSpace(j.Title) == "" {
		p = append(p, "title is required")
	}
	if len(strings.TrimSpace(j.Description)) < 30 {
		p = append(p, "description must be at least 30 characters")
	}
	if strings.TrimSpace(j.Location) == "" && j.WorkMode != "remote" {
		p = append(p, "location is required unless work_mode is remote")
	}
	if len(j.Requirements) == 0 {
		p = append(p, "at least one requirement is required")
	}
	if len(j.Stages) == 0 {
		p = append(p, "at least one hiring stage is required")
	}
	if (j.SalaryMin != nil || j.SalaryMax != nil) && j.Currency == nil {
		p = append(p, "currency is required when salary is set")
	}
	return p
}

// Requirement is a single job requirement (e.g. skill "Go", mandatory).
type Requirement struct {
	ID          uuid.UUID `gorm:"type:uuid;primaryKey" json:"id"`
	JobID       uuid.UUID `gorm:"type:uuid" json:"-"`
	Type        string    `json:"type"`
	Value       string    `json:"value"`
	IsMandatory bool      `json:"is_mandatory"`
	CreatedAt   time.Time `json:"created_at"`
}

func (Requirement) TableName() string { return "job_requirements" }

// HiringStage is one step of the job's hiring pipeline, ordered by Position.
type HiringStage struct {
	ID        uuid.UUID `gorm:"type:uuid;primaryKey" json:"id"`
	JobID     uuid.UUID `gorm:"type:uuid" json:"-"`
	Name      string    `json:"name"`
	StageType string    `json:"stage_type"`
	Position  int       `json:"position"`
	CreatedAt time.Time `json:"created_at"`
}

// AuditLog records who changed what and when.
type AuditLog struct {
	ID         uuid.UUID       `gorm:"type:uuid;primaryKey" json:"id"`
	EntityType string          `json:"entity_type"`
	EntityID   uuid.UUID       `gorm:"type:uuid" json:"entity_id"`
	Action     string          `json:"action"`
	ActorID    string          `json:"actor_id"`
	Changes    json.RawMessage `gorm:"type:jsonb" json:"changes"`
	RequestID  string          `json:"request_id"`
	CreatedAt  time.Time       `json:"created_at"`
}

// JobFilter narrows List queries. Zero values mean "no filter".
type JobFilter struct {
	Status      JobStatus
	RecruiterID *uuid.UUID
	Location    string
	Query       string // free-text match on title/description
}
