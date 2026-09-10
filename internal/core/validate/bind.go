package validate

import (
	"fmt"
	"strconv"
	"strings"

	"github.com/gin-gonic/gin"
	"github.com/gin-gonic/gin/binding"
	"github.com/johirdev/Hisabji-Server/internal/core/apperr"
	"github.com/johirdev/Hisabji-Server/internal/core/query"
)

// Body decodes and validates a JSON request body into dst.
//
//	var req CreateExpenseRequest
//	if err := validate.Body(c, &req); err != nil { return nil, err }
//
// Every failure mode — empty body, bad JSON, wrong type, failed rule — comes
// back as a 4xx *apperr.Error with per-field detail. Handlers never inspect it.
func Body(c *gin.Context, dst any) error {
	if ct := c.ContentType(); ct != "" && ct != "application/json" && ct != "application/x-www-form-urlencoded" && ct != "multipart/form-data" {
		return apperr.New(415, apperr.CodeUnsupportedMedia,
			fmt.Sprintf("Content-Type %q is not supported. Use application/json.", ct))
	}
	if err := c.ShouldBindJSON(dst); err != nil {
		return Translate(err)
	}
	if v, ok := dst.(SelfValidator); ok {
		if err := v.Validate(); err != nil {
			return Translate(err)
		}
	}
	return nil
}

// Query decodes and validates query-string parameters into a struct that uses
// `form:"..."` tags. Use it for endpoint-specific params; use query.Parse for
// the generic list controls (page/limit/sort/filters).
func Query(c *gin.Context, dst any) error {
	if err := c.ShouldBindWith(dst, binding.Query); err != nil {
		return Translate(err)
	}
	if v, ok := dst.(SelfValidator); ok {
		if err := v.Validate(); err != nil {
			return Translate(err)
		}
	}
	return nil
}

// Form binds multipart/form-data (file uploads with fields).
func Form(c *gin.Context, dst any) error {
	if err := c.ShouldBindWith(dst, binding.FormMultipart); err != nil {
		return Translate(err)
	}
	if v, ok := dst.(SelfValidator); ok {
		if err := v.Validate(); err != nil {
			return Translate(err)
		}
	}
	return nil
}

// SelfValidator lets a DTO run cross-field checks that struct tags cannot
// express (for example "end_date must be after start_date"). Return an
// *apperr.Error — usually apperr.Validation(...) — or nil.
type SelfValidator interface {
	Validate() error
}

// UUIDParam reads a path parameter and rejects anything that is not a UUID,
// turning what would be a Postgres 500 into a clean 422.
func UUIDParam(c *gin.Context, name string) (string, error) {
	raw := strings.TrimSpace(c.Param(name))
	if raw == "" {
		return "", apperr.MissingField(name, fmt.Sprintf("%s is required in the URL path.", name))
	}
	if !query.IsUUID(raw) {
		return "", apperr.Validation("The id in the URL is not valid.",
			apperr.FieldError{
				Field: name, Rule: "uuid", Value: raw,
				Message:   fmt.Sprintf("%s must be a valid id.", name),
				MessageBN: fmt.Sprintf("%s একটি সঠিক id নয়।", name),
			})
	}
	return raw, nil
}

// IntParam reads an integer path parameter.
func IntParam(c *gin.Context, name string) (int, error) {
	raw := strings.TrimSpace(c.Param(name))
	v, err := strconv.Atoi(raw)
	if err != nil {
		return 0, apperr.Validation("The value in the URL is not valid.",
			apperr.FieldError{
				Field: name, Rule: "integer", Value: raw,
				Message:   fmt.Sprintf("%s must be a whole number.", name),
				MessageBN: fmt.Sprintf("%s অবশ্যই একটি সংখ্যা হতে হবে।", name),
			})
	}
	return v, nil
}

// SlugParam reads a short code path parameter (plan codes, feature codes) and
// keeps it to a safe character set.
func SlugParam(c *gin.Context, name string) (string, error) {
	raw := strings.ToLower(strings.TrimSpace(c.Param(name)))
	if raw == "" {
		return "", apperr.MissingField(name, fmt.Sprintf("%s is required in the URL path.", name))
	}
	if len(raw) > 64 {
		return "", apperr.InvalidField(name, fmt.Sprintf("%s is too long.", name))
	}
	for _, r := range raw {
		ok := (r >= 'a' && r <= 'z') || (r >= '0' && r <= '9') || r == '_' || r == '-'
		if !ok {
			return "", apperr.InvalidField(name,
				fmt.Sprintf("%s may only contain lowercase letters, numbers, hyphens and underscores.", name))
		}
	}
	return raw, nil
}

// Errors is a small accumulator for hand-written cross-field validation inside
// a DTO's Validate() method.
//
//	func (r CreateBudgetRequest) Validate() error {
//	    e := validate.NewErrors()
//	    e.Add("period_end", "after", "period_end must be after period_start.", "শেষ তারিখ শুরুর পরে হতে হবে।")
//	    return e.Err()
//	}
type Errors struct{ fields []apperr.FieldError }

// NewErrors returns an empty accumulator.
func NewErrors() *Errors { return &Errors{} }

// Add records one field problem.
func (e *Errors) Add(field, rule, message, messageBN string) *Errors {
	e.fields = append(e.fields, apperr.FieldError{
		Field: field, Rule: rule, Message: message, MessageBN: messageBN,
	})
	return e
}

// AddIf records the problem only when cond is true.
func (e *Errors) AddIf(cond bool, field, rule, message, messageBN string) *Errors {
	if cond {
		e.Add(field, rule, message, messageBN)
	}
	return e
}

// Any reports whether anything was recorded.
func (e *Errors) Any() bool { return len(e.fields) > 0 }

// Err returns nil when clean, or a 422 *apperr.Error listing every problem.
func (e *Errors) Err() error {
	if len(e.fields) == 0 {
		return nil
	}
	return apperr.Validation("Some fields are missing or invalid.", e.fields...)
}
