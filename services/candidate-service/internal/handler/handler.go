// Package handler exposes candidate-service over HTTP using Gin.
package handler

import (
	"errors"
	"fmt"
	"io"
	"net/http"
	"strconv"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"

	"github.com/usamatajemumba/smart-hire/pkg/apperr"
	"github.com/usamatajemumba/smart-hire/pkg/httpx"
	"github.com/usamatajemumba/smart-hire/services/candidate-service/internal/domain"
	"github.com/usamatajemumba/smart-hire/services/candidate-service/internal/service"
)

const ActorHeader = "X-User-ID"

type Handler struct{ svc *service.Service }

func New(svc *service.Service) *Handler { return &Handler{svc: svc} }

func (h *Handler) Register(rg *gin.RouterGroup) {
	c := rg.Group("/candidates")
	c.POST("", h.register)
	c.GET("", h.list)
	c.GET("/:id", h.get)
	c.PATCH("/:id", h.update)
	c.DELETE("/:id", h.delete)
	c.PUT("/:id/skills", h.setSkills)
	c.PUT("/:id/experiences", h.setExperiences)
	c.PUT("/:id/educations", h.setEducations)
	c.POST("/:id/resume", h.uploadResume)
	c.GET("/:id/resume", h.downloadResume)
}

// ---------- request DTOs ----------

type skillReq struct {
	Name  string `json:"name" binding:"required,max=100"`
	Level string `json:"level" binding:"omitempty,oneof=beginner intermediate advanced expert"`
	Years int    `json:"years" binding:"min=0,max=60"`
}

type registerReq struct {
	Email           string     `json:"email" binding:"required,email,max=254"`
	FullName        string     `json:"full_name" binding:"required,min=2,max=150"`
	Phone           string     `json:"phone" binding:"omitempty,e164"`
	Location        string     `json:"location" binding:"max=150"`
	Headline        string     `json:"headline" binding:"max=200"`
	Summary         string     `json:"summary" binding:"max=5000"`
	YearsExperience int        `json:"years_experience" binding:"min=0,max=60"`
	Skills          []skillReq `json:"skills" binding:"omitempty,max=100,dive"`
}

type updateReq struct {
	Version         int     `json:"version" binding:"required,min=1"`
	FullName        *string `json:"full_name" binding:"omitempty,min=2,max=150"`
	Phone           *string `json:"phone" binding:"omitempty,e164"`
	Location        *string `json:"location" binding:"omitempty,max=150"`
	Headline        *string `json:"headline" binding:"omitempty,max=200"`
	Summary         *string `json:"summary" binding:"omitempty,max=5000"`
	YearsExperience *int    `json:"years_experience" binding:"omitempty,min=0,max=60"`
}

type skillsReq struct {
	Skills []skillReq `json:"skills" binding:"max=100,dive"`
}

type experienceReq struct {
	Company     string  `json:"company" binding:"required,max=150"`
	Title       string  `json:"title" binding:"required,max=150"`
	StartDate   string  `json:"start_date" binding:"required,datetime=2006-01-02"`
	EndDate     *string `json:"end_date" binding:"omitempty,datetime=2006-01-02"`
	Description string  `json:"description" binding:"max=5000"`
}

type experiencesReq struct {
	Experiences []experienceReq `json:"experiences" binding:"max=50,dive"`
}

type educationReq struct {
	Institution  string `json:"institution" binding:"required,max=150"`
	Degree       string `json:"degree" binding:"required,max=150"`
	FieldOfStudy string `json:"field_of_study" binding:"max=150"`
	StartYear    int    `json:"start_year" binding:"required,min=1950,max=2100"`
	EndYear      *int   `json:"end_year" binding:"omitempty,min=1950,max=2100"`
}

type educationsReq struct {
	Educations []educationReq `json:"educations" binding:"max=20,dive"`
}

// ---------- handlers ----------

func (h *Handler) register(c *gin.Context) {
	var req registerReq
	if err := httpx.BindJSON(c, &req); err != nil {
		httpx.Error(c, err)
		return
	}
	cand, err := h.svc.Register(c.Request.Context(), actor(c), service.RegisterInput{
		Email: req.Email, FullName: req.FullName, Phone: req.Phone, Location: req.Location,
		Headline: req.Headline, Summary: req.Summary, YearsExperience: req.YearsExperience,
		Skills: toSkillInputs(req.Skills),
	})
	if err != nil {
		httpx.Error(c, err)
		return
	}
	c.Header("Location", "/api/v1/candidates/"+cand.ID.String())
	c.JSON(http.StatusCreated, cand)
}

func (h *Handler) list(c *gin.Context) {
	page := httpx.PageFromQuery(c)
	f := domain.CandidateFilter{Skill: c.Query("skill"), Location: c.Query("location"), Query: c.Query("q")}
	items, total, err := h.svc.List(c.Request.Context(), f, page.Limit, page.Offset)
	if err != nil {
		httpx.Error(c, err)
		return
	}
	c.JSON(http.StatusOK, httpx.List[domain.Candidate]{Items: items, Total: total, Limit: page.Limit, Offset: page.Offset})
}

func (h *Handler) get(c *gin.Context) {
	id, ok := pathID(c)
	if !ok {
		return
	}
	cand, err := h.svc.Get(c.Request.Context(), id)
	respond(c, cand, err)
}

func (h *Handler) update(c *gin.Context) {
	id, ok := pathID(c)
	if !ok {
		return
	}
	var req updateReq
	if err := httpx.BindJSON(c, &req); err != nil {
		httpx.Error(c, err)
		return
	}
	cand, err := h.svc.Update(c.Request.Context(), actor(c), id, service.UpdateInput{
		Version: req.Version, FullName: req.FullName, Phone: req.Phone, Location: req.Location,
		Headline: req.Headline, Summary: req.Summary, YearsExperience: req.YearsExperience,
	})
	respond(c, cand, err)
}

func (h *Handler) delete(c *gin.Context) {
	id, ok := pathID(c)
	if !ok {
		return
	}
	if err := h.svc.Delete(c.Request.Context(), actor(c), id); err != nil {
		httpx.Error(c, err)
		return
	}
	c.Status(http.StatusNoContent)
}

func (h *Handler) setSkills(c *gin.Context) {
	id, ok := pathID(c)
	if !ok {
		return
	}
	var req skillsReq
	if err := httpx.BindJSON(c, &req); err != nil {
		httpx.Error(c, err)
		return
	}
	cand, err := h.svc.SetSkills(c.Request.Context(), actor(c), id, toSkillInputs(req.Skills))
	respond(c, cand, err)
}

func (h *Handler) setExperiences(c *gin.Context) {
	id, ok := pathID(c)
	if !ok {
		return
	}
	var req experiencesReq
	if err := httpx.BindJSON(c, &req); err != nil {
		httpx.Error(c, err)
		return
	}
	in := make([]service.ExperienceInput, len(req.Experiences))
	for i, e := range req.Experiences {
		start, _ := time.Parse(time.DateOnly, e.StartDate) // format validated by binding
		in[i] = service.ExperienceInput{Company: e.Company, Title: e.Title, StartDate: start, Description: e.Description}
		if e.EndDate != nil {
			end, _ := time.Parse(time.DateOnly, *e.EndDate)
			in[i].EndDate = &end
		}
	}
	cand, err := h.svc.SetExperiences(c.Request.Context(), actor(c), id, in)
	respond(c, cand, err)
}

func (h *Handler) setEducations(c *gin.Context) {
	id, ok := pathID(c)
	if !ok {
		return
	}
	var req educationsReq
	if err := httpx.BindJSON(c, &req); err != nil {
		httpx.Error(c, err)
		return
	}
	in := make([]service.EducationInput, len(req.Educations))
	for i, e := range req.Educations {
		in[i] = service.EducationInput{
			Institution: e.Institution, Degree: e.Degree, FieldOfStudy: e.FieldOfStudy,
			StartYear: e.StartYear, EndYear: e.EndYear,
		}
	}
	cand, err := h.svc.SetEducations(c.Request.Context(), actor(c), id, in)
	respond(c, cand, err)
}

// uploadResume accepts multipart/form-data with a "file" field.
func (h *Handler) uploadResume(c *gin.Context) {
	id, ok := pathID(c)
	if !ok {
		return
	}
	// Hard cap on the whole request body (file + multipart overhead) so a
	// client cannot stream gigabytes at us.
	c.Request.Body = http.MaxBytesReader(c.Writer, c.Request.Body, service.MaxResumeBytes+1<<20)
	fh, err := c.FormFile("file")
	if err != nil {
		var mbe *http.MaxBytesError
		if errors.As(err, &mbe) {
			httpx.Error(c, apperr.Validation(fmt.Sprintf("resume exceeds %d MiB", service.MaxResumeBytes>>20)))
			return
		}
		httpx.Error(c, apperr.Validation(`multipart field "file" is required`))
		return
	}
	f, err := fh.Open()
	if err != nil {
		httpx.Error(c, apperr.Internal(err))
		return
	}
	defer f.Close()

	res, err := h.svc.UploadResume(c.Request.Context(), actor(c), id, fh.Filename, f)
	if err != nil {
		httpx.Error(c, err)
		return
	}
	c.JSON(http.StatusCreated, res)
}

func (h *Handler) downloadResume(c *gin.Context) {
	id, ok := pathID(c)
	if !ok {
		return
	}
	res, rc, err := h.svc.OpenResume(c.Request.Context(), id)
	if err != nil {
		httpx.Error(c, err)
		return
	}
	defer rc.Close()
	c.Header("Content-Disposition", fmt.Sprintf("attachment; filename=%q", res.FileName))
	c.Header("Content-Length", strconv.FormatInt(res.SizeBytes, 10))
	c.Header("X-Content-Type-Options", "nosniff")
	c.Header("Content-Type", res.ContentType)
	c.Status(http.StatusOK)
	_, _ = io.Copy(c.Writer, rc)
}

// ---------- helpers ----------

func respond(c *gin.Context, cand *domain.Candidate, err error) {
	if err != nil {
		httpx.Error(c, err)
		return
	}
	c.JSON(http.StatusOK, cand)
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

func toSkillInputs(in []skillReq) []service.SkillInput {
	out := make([]service.SkillInput, len(in))
	for i, s := range in {
		out[i] = service.SkillInput{Name: s.Name, Level: s.Level, Years: s.Years}
	}
	return out
}
