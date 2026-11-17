// Package domain holds candidate-service entities.
package domain

import (
	"encoding/json"
	"time"

	"github.com/google/uuid"
	"gorm.io/gorm"
)

// Candidate is the aggregate root for a candidate's profile.
type Candidate struct {
	ID              uuid.UUID      `gorm:"type:uuid;primaryKey" json:"id"`
	Email           string         `json:"email"`
	FullName        string         `json:"full_name"`
	Phone           string         `json:"phone"`
	Location        string         `json:"location"`
	Headline        string         `json:"headline"`
	Summary         string         `json:"summary"`
	YearsExperience int            `json:"years_experience"`
	Version         int            `json:"version"`
	CreatedAt       time.Time      `json:"created_at"`
	UpdatedAt       time.Time      `json:"updated_at"`
	DeletedAt       gorm.DeletedAt `json:"-"`

	Skills      []Skill          `gorm:"foreignKey:CandidateID" json:"skills"`
	Experiences []WorkExperience `gorm:"foreignKey:CandidateID" json:"experiences"`
	Educations  []Education      `gorm:"foreignKey:CandidateID" json:"educations"`
	// Resume is the latest upload (nil if none). Not a DB column.
	Resume *Resume `gorm:"-" json:"resume"`
}

type Skill struct {
	ID          uuid.UUID `gorm:"type:uuid;primaryKey" json:"id"`
	CandidateID uuid.UUID `gorm:"type:uuid" json:"-"`
	Name        string    `json:"name"`
	Level       string    `json:"level"`
	Years       int       `json:"years"`
	CreatedAt   time.Time `json:"created_at"`
}

func (Skill) TableName() string { return "candidate_skills" }

type WorkExperience struct {
	ID          uuid.UUID  `gorm:"type:uuid;primaryKey" json:"id"`
	CandidateID uuid.UUID  `gorm:"type:uuid" json:"-"`
	Company     string     `json:"company"`
	Title       string     `json:"title"`
	StartDate   time.Time  `gorm:"type:date" json:"start_date"`
	EndDate     *time.Time `gorm:"type:date" json:"end_date"`
	Description string     `json:"description"`
	CreatedAt   time.Time  `json:"created_at"`
}

type Education struct {
	ID           uuid.UUID `gorm:"type:uuid;primaryKey" json:"id"`
	CandidateID  uuid.UUID `gorm:"type:uuid" json:"-"`
	Institution  string    `json:"institution"`
	Degree       string    `json:"degree"`
	FieldOfStudy string    `json:"field_of_study"`
	StartYear    int       `json:"start_year"`
	EndYear      *int      `json:"end_year"`
	CreatedAt    time.Time `json:"created_at"`
}

// Resume is metadata for an uploaded file; the bytes live in blob storage.
type Resume struct {
	ID          uuid.UUID `gorm:"type:uuid;primaryKey" json:"id"`
	CandidateID uuid.UUID `gorm:"type:uuid" json:"-"`
	FileName    string    `json:"file_name"`
	ContentType string    `json:"content_type"`
	SizeBytes   int64     `json:"size_bytes"`
	StorageKey  string    `json:"-"` // internal path, never exposed
	UploadedAt  time.Time `json:"uploaded_at"`
}

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

// CandidateFilter narrows List queries.
type CandidateFilter struct {
	Skill    string // candidates having this skill (case-insensitive)
	Location string
	Query    string // matches name, email or headline
}
