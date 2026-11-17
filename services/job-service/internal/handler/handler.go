// Package handler exposes job-service over HTTP using Gin.
// Handlers only translate HTTP <-> service calls; no business rules live here.
package handler

import (
	"context"
	"net/http"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"

	"github.com/usamatajemumba/smart-hire/pkg/apperr"
	"github.com/usamatajemumba/smart-hire/pkg/httpx"
	"github.com/usamatajemumba/smart-hire/services/job-service/internal/domain"
	"github.com/usamatajemumba/smart-hire/services/job-service/internal/service"
)

// ActorHeader carries the caller identity until auth exists (Week 1 only).
const ActorHeader = "X-User-ID"

type Handler struct{ svc *service.Service }

func New(svc *service.Service) *Handler { return &Handler{svc: svc} }

// Register mounts all job routes under rg (e.g. /api/v1).
func (h *Handler) Register(rg *gin.RouterGroup) {
	jobs := rg.Group("/jobs")
	jobs.POST("", h.create)
	jobs.GET("", h.list)
	jobs.GET("/:id", h.get)
	jobs.PATCH("/:id", h.update)
	jobs.DELETE("/:id", h.delete)
	jobs.PUT("/:id/requirements", h.setRequirements)
	jobs.PUT("/:id/stages", h.setStages)
	jobs.POST("/:id/publish", h.publish)
	jobs.POST("/:id/close", h.close)
	jobs.POST("/:id/archive", h.archive)
	jobs.GET("/:id/audit", h.audit)
}

// ---------- request DTOs (validation via `binding` tags) ----------

type requirementReq struct {
	Type        string `json:"type" binding:"required,oneof=skill qualification experience other"`
	Value       string `json:"value" binding:"required,max=255"`
	IsMandatory *bool  `json:"is_mandatory"` // pointer: absent -> default true
}

type stageReq struct {
	Name      string `json:"name" binding:"required,max=100"`
	StageType string `json:"stage_type" binding:"required,oneof=screening assessment interview offer"`
	Position  int    `json:"position" binding:"required,min=1"`
}

type createJobReq struct {
	RecruiterID    string           `json:"recruiter_id" binding:"required,uuid"`
	Title          string           `json:"title" binding:"required,min=3,max=200"`
	Description    string           `json:"description" binding:"max=20000"`
	Department     string           `json:"department" binding:"max=100"`
	Location       string           `json:"location" binding:"max=150"`
	EmploymentType string           `json:"employment_type" binding:"required,oneof=full_time part_time contract internship temporary"`
	WorkMode       string           `json:"work_mode" binding:"required,oneof=onsite remote hybrid"`
	ExperienceMin  *int             `json:"experience_min" binding:"omitempty,min=0,max=60"`
	ExperienceMax  *int             `json:"experience_max" binding:"omitempty,min=0,max=60"`
	SalaryMin      *int64           `json:"salary_min" binding:"omitempty,min=0"`
	SalaryMax      *int64           `json:"salary_max" binding:"omitempty,min=0"`
	Currency       *string          `json:"currency" binding:"omitempty,len=3,alpha"`
	Requirements   []requirementReq `json:"requirements" binding:"omitempty,max=50,dive"`
	Stages         []stageReq       `json:"stages" binding:"omitempty,max=20,dive"`
}

type updateJobReq struct {
	Version        int     `json:"version" binding:"required,min=1"`
	Title          *string `json:"title" binding:"omitempty,min=3,max=200"`
	Description    *string `json:"description" binding:"omitempty,max=20000"`
	Department     *string `json:"department" binding:"omitempty,max=100"`
	Location       *string `json:"location" binding:"omitempty,max=150"`
	EmploymentType *string `json:"employment_type" binding:"omitempty,oneof=full_time part_time contract internship temporary"`
	WorkMode       *string `json:"work_mode" binding:"omitempty,oneof=onsite remote hybrid"`
	ExperienceMin  *int    `json:"experience_min" binding:"omitempty,min=0,max=60"`
	ExperienceMax  *int    `json:"experience_max" binding:"omitempty,min=0,max=60"`
	SalaryMin      *int64  `json:"salary_min" binding:"omitempty,min=0"`
	SalaryMax      *int64  `json:"salary_max" binding:"omitempty,min=0"`
	Currency       *string `json:"currency" binding:"omitempty,len=3,alpha"`
}

type requirementsReq struct {
	Requirements []requirementReq `json:"requirements" binding:"max=50,dive"`
}

type stagesReq struct {
	Stages []stageReq `json:"stages" binding:"max=20,dive"`
}

// ---------- handlers ----------

func (h *Handler) create(c *gin.Context) {
	var req createJobReq
	if err := httpx.BindJSON(c, &req); err != nil {
		httpx.Error(c, err)
		return
	}
	job, err := h.svc.CreateJob(c.Request.Context(), actor(c), service.CreateJobInput{
		RecruiterID:    uuid.MustParse(req.RecruiterID), // already validated by binding
		Title:          req.Title,
		Description:    req.Description,
		Department:     req.Department,
		Location:       req.Location,
		EmploymentType: req.EmploymentType,
		WorkMode:       req.WorkMode,
		ExperienceMin:  req.ExperienceMin,
		ExperienceMax:  req.ExperienceMax,
		SalaryMin:      req.SalaryMin,
		SalaryMax:      req.SalaryMax,
		Currency:       req.Currency,
		Requirements:   toRequirementInputs(req.Requirements),
		Stages:         toStageInputs(req.Stages),
	})
	if err != nil {
		httpx.Error(c, err)
		return
	}
	c.Header("Location", "/api/v1/jobs/"+job.ID.String())
	c.JSON(http.StatusCreated, job)
}

func (h *Handler) list(c *gin.Context) {
	page := httpx.PageFromQuery(c)
	f := domain.JobFilter{
		Status:   domain.JobStatus(c.Query("status")),
		Location: c.Query("location"),
		Query:    c.Query("q"),
	}
	switch f.Status {
	case "", domain.StatusDraft, domain.StatusPublished, domain.StatusClosed, domain.StatusArchived:
	default:
		httpx.Error(c, apperr.Validation("status must be one of: draft published closed archived"))
		return
	}
	if rid := c.Query("recruiter_id"); rid != "" {
		id, err := uuid.Parse(rid)
		if err != nil {
			httpx.Error(c, apperr.Validation("recruiter_id must be a UUID"))
			return
		}
		f.RecruiterID = &id
	}
	jobs, total, err := h.svc.ListJobs(c.Request.Context(), f, page.Limit, page.Offset)
	if err != nil {
		httpx.Error(c, err)
		return
	}
	c.JSON(http.StatusOK, httpx.List[domain.Job]{Items: jobs, Total: total, Limit: page.Limit, Offset: page.Offset})
}

func (h *Handler) get(c *gin.Context) {
	id, ok := pathID(c)
	if !ok {
		return
	}
	job, err := h.svc.GetJob(c.Request.Context(), id)
	respond(c, job, err)
}

func (h *Handler) update(c *gin.Context) {
	id, ok := pathID(c)
	if !ok {
		return
	}
	var req updateJobReq
	if err := httpx.BindJSON(c, &req); err != nil {
		httpx.Error(c, err)
		return
	}
	job, err := h.svc.UpdateJob(c.Request.Context(), actor(c), id, service.UpdateJobInput{
		Version:        req.Version,
		Title:          req.Title,
		Description:    req.Description,
		Department:     req.Department,
		Location:       req.Location,
		EmploymentType: req.EmploymentType,
		WorkMode:       req.WorkMode,
		ExperienceMin:  req.ExperienceMin,
		ExperienceMax:  req.ExperienceMax,
		SalaryMin:      req.SalaryMin,
		SalaryMax:      req.SalaryMax,
		Currency:       req.Currency,
	})
	respond(c, job, err)
}

func (h *Handler) delete(c *gin.Context) {
	id, ok := pathID(c)
	if !ok {
		return
	}
	if err := h.svc.DeleteJob(c.Request.Context(), actor(c), id); err != nil {
		httpx.Error(c, err)
		return
	}
	c.Status(http.StatusNoContent)
}

func (h *Handler) setRequirements(c *gin.Context) {
	id, ok := pathID(c)
	if !ok {
		return
	}
	var req requirementsReq
	if err := httpx.BindJSON(c, &req); err != nil {
		httpx.Error(c, err)
		return
	}
	job, err := h.svc.SetRequirements(c.Request.Context(), actor(c), id, toRequirementInputs(req.Requirements))
	respond(c, job, err)
}

func (h *Handler) setStages(c *gin.Context) {
	id, ok := pathID(c)
	if !ok {
		return
	}
	var req stagesReq
	if err := httpx.BindJSON(c, &req); err != nil {
		httpx.Error(c, err)
		return
	}
	job, err := h.svc.SetStages(c.Request.Context(), actor(c), id, toStageInputs(req.Stages))
	respond(c, job, err)
}

func (h *Handler) publish(c *gin.Context) { h.lifecycle(c, h.svc.PublishJob) }
func (h *Handler) close(c *gin.Context)   { h.lifecycle(c, h.svc.CloseJob) }
func (h *Handler) archive(c *gin.Context) { h.lifecycle(c, h.svc.ArchiveJob) }

type lifecycleFn func(ctx context.Context, a service.Actor, id uuid.UUID) (*domain.Job, error)

func (h *Handler) lifecycle(c *gin.Context, fn lifecycleFn) {
	id, ok := pathID(c)
	if !ok {
		return
	}
	job, err := fn(c.Request.Context(), actor(c), id)
	respond(c, job, err)
}

func (h *Handler) audit(c *gin.Context) {
	id, ok := pathID(c)
	if !ok {
		return
	}
	page := httpx.PageFromQuery(c)
	logs, total, err := h.svc.ListAudit(c.Request.Context(), id, page.Limit, page.Offset)
	if err != nil {
		httpx.Error(c, err)
		return
	}
	c.JSON(http.StatusOK, httpx.List[domain.AuditLog]{Items: logs, Total: total, Limit: page.Limit, Offset: page.Offset})
}

// ---------- helpers ----------

func respond(c *gin.Context, job *domain.Job, err error) {
	if err != nil {
		httpx.Error(c, err)
		return
	}
	c.JSON(http.StatusOK, job)
}

func pathID(c *gin.Context) (uuid.UUID, bool) {
	id, err := uuid.Parse(c.Param("id"))
	if err != nil {
		httpx.Error(c, apperr.Validation("id must be a UUID"))
		return uuid.Nil, false
	}
	return id, true
}

func actor(c *gin.Context) service.Actor {
	return service.Actor{ID: c.GetHeader(ActorHeader), RequestID: c.GetString(httpx.RequestIDKey)}
}

func toRequirementInputs(in []requirementReq) []service.RequirementInput {
	out := make([]service.RequirementInput, len(in))
	for i, r := range in {
		mandatory := true
		if r.IsMandatory != nil {
			mandatory = *r.IsMandatory
		}
		out[i] = service.RequirementInput{Type: r.Type, Value: r.Value, IsMandatory: mandatory}
	}
	return out
}

func toStageInputs(in []stageReq) []service.StageInput {
	out := make([]service.StageInput, len(in))
	for i, s := range in {
		out[i] = service.StageInput{Name: s.Name, StageType: s.StageType, Position: s.Position}
	}
	return out
}
