// Package httpx contains Gin helpers shared by all HTTP services:
// a consistent JSON error envelope, request binding and pagination.
package httpx

import (
	"errors"
	"log/slog"
	"net/http"
	"reflect"
	"strconv"
	"strings"

	"github.com/gin-gonic/gin"
	"github.com/gin-gonic/gin/binding"
	"github.com/go-playground/validator/v10"

	"github.com/usamatajemumba/smart-hire/pkg/apperr"
)

// ErrorBody is the JSON shape of every error response:
//
//	{"error": {"code": "NOT_FOUND", "message": "job not found", "request_id": "..."}}
type ErrorBody struct {
	Error ErrorPayload `json:"error"`
}

type ErrorPayload struct {
	Code      string         `json:"code"`
	Message   string         `json:"message"`
	Details   map[string]any `json:"details,omitempty"`
	RequestID string         `json:"request_id,omitempty"`
}

var statusByKind = map[apperr.Kind]int{
	apperr.KindValidation: http.StatusBadRequest,
	apperr.KindNotFound:   http.StatusNotFound,
	apperr.KindConflict:   http.StatusConflict,
	apperr.KindRule:       http.StatusUnprocessableEntity,
	apperr.KindInternal:   http.StatusInternalServerError,
}

// Error writes err as a JSON error response. Internal causes are logged, never leaked.
func Error(c *gin.Context, err error) {
	var ae *apperr.Error
	if !errors.As(err, &ae) {
		ae = apperr.Internal(err)
	}
	status := statusByKind[ae.Kind]
	if status == 0 {
		status = http.StatusInternalServerError
	}
	if status >= 500 {
		slog.ErrorContext(c.Request.Context(), "request failed",
			"error", err, "path", c.FullPath(), "request_id", c.GetString(RequestIDKey))
	}
	c.AbortWithStatusJSON(status, ErrorBody{Error: ErrorPayload{
		Code:      string(ae.Kind),
		Message:   ae.Message,
		Details:   ae.Details,
		RequestID: c.GetString(RequestIDKey),
	}})
}

// init makes validation errors use JSON field names ("recruiter_id") instead
// of Go struct field names ("RecruiterID"), so clients see their own keys.
func init() {
	if v, ok := binding.Validator.Engine().(*validator.Validate); ok {
		v.RegisterTagNameFunc(func(f reflect.StructField) string {
			name := strings.SplitN(f.Tag.Get("json"), ",", 2)[0]
			if name == "" || name == "-" {
				return f.Name
			}
			return name
		})
	}
}

// RequestIDKey is the gin.Context key where middleware stores the request ID.
const RequestIDKey = "request_id"

// BindJSON decodes and validates the body (via `binding` struct tags).
// On failure it returns a VALIDATION_ERROR with per-field messages.
func BindJSON(c *gin.Context, dst any) error {
	if err := c.ShouldBindJSON(dst); err != nil {
		var ve validator.ValidationErrors
		if errors.As(err, &ve) {
			fields := make(map[string]any, len(ve))
			for _, fe := range ve {
				fields[fe.Field()] = fieldMessage(fe)
			}
			return apperr.Validation("request validation failed").WithDetails(map[string]any{"fields": fields})
		}
		return apperr.Validation("malformed JSON body: " + err.Error())
	}
	return nil
}

func fieldMessage(fe validator.FieldError) string {
	switch fe.Tag() {
	case "required":
		return "is required"
	case "email":
		return "must be a valid email"
	case "oneof":
		return "must be one of: " + fe.Param()
	case "min", "gte":
		if fe.Kind() == reflect.String {
			return "must be at least " + fe.Param() + " characters"
		}
		if fe.Kind() == reflect.Slice {
			return "must have at least " + fe.Param() + " items"
		}
		return "must be at least " + fe.Param()
	case "max", "lte":
		if fe.Kind() == reflect.String {
			return "must be at most " + fe.Param() + " characters"
		}
		if fe.Kind() == reflect.Slice {
			return "must have at most " + fe.Param() + " items"
		}
		return "must be at most " + fe.Param()
	case "len":
		return "must be exactly " + fe.Param() + " characters"
	case "e164":
		return "must be an E.164 phone number, e.g. +923001234567"
	case "datetime":
		return "must be a date in format YYYY-MM-DD"
	case "uuid":
		return "must be a UUID"
	default:
		return "failed '" + fe.Tag() + "' validation"
	}
}

// Page holds limit/offset pagination parameters.
type Page struct {
	Limit  int `json:"limit"`
	Offset int `json:"offset"`
}

// PageFromQuery parses ?limit=&offset= with defaults (20) and a hard cap (100).
func PageFromQuery(c *gin.Context) Page {
	limit, _ := strconv.Atoi(c.DefaultQuery("limit", "20"))
	offset, _ := strconv.Atoi(c.DefaultQuery("offset", "0"))
	if limit <= 0 {
		limit = 20
	}
	if limit > 100 {
		limit = 100
	}
	if offset < 0 {
		offset = 0
	}
	return Page{Limit: limit, Offset: offset}
}

// List is the envelope for paginated collections.
type List[T any] struct {
	Items  []T   `json:"items"`
	Total  int64 `json:"total"`
	Limit  int   `json:"limit"`
	Offset int   `json:"offset"`
}
