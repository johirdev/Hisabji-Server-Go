// Package response owns the single JSON envelope every endpoint returns.
//
// Success and failure share the same shape, so a frontend can write one
// interceptor and never special-case anything:
//
//	{
//	  "success":    true,
//	  "code":       "OK",
//	  "message":    "Expenses fetched successfully.",
//	  "data":       [...],
//	  "meta":       { "page": 1, "limit": 20, "total_items": 134, ... },
//	  "errors":     [ { "field": "amount", "rule": "required", "message": "..." } ],
//	  "request_id": "01J...",
//	  "timestamp":  "2026-09-10T12:00:00Z"
//	}
package response

import (
	"time"

	"github.com/johirdev/Hisabji-Server/internal/core/apperr"
)

// Envelope is the wire format. Nothing else is ever written to the body.
type Envelope struct {
	Success   bool                 `json:"success"`
	Code      string               `json:"code"`
	Message   string               `json:"message"`
	Data      any                  `json:"data,omitempty"`
	Meta      *Meta                `json:"meta,omitempty"`
	Errors    []apperr.FieldError  `json:"errors,omitempty"`
	Hint      string               `json:"hint,omitempty"`
	Details   any                  `json:"details,omitempty"`
	RequestID string               `json:"request_id,omitempty"`
	Timestamp string               `json:"timestamp"`
}

// Meta carries pagination plus an echo of the query the client actually got,
// which makes debugging a filtered list from the frontend trivial.
type Meta struct {
	Page       int            `json:"page"`
	Limit      int            `json:"limit"`
	TotalItems int64          `json:"total_items"`
	TotalPages int            `json:"total_pages"`
	Count      int            `json:"count"` // items in THIS page
	HasNext    bool           `json:"has_next"`
	HasPrev    bool           `json:"has_prev"`
	NextPage   *int           `json:"next_page,omitempty"`
	PrevPage   *int           `json:"prev_page,omitempty"`
	Sort       string         `json:"sort,omitempty"`
	Search     string         `json:"search,omitempty"`
	Filters    map[string]any `json:"filters,omitempty"`
	Extra      map[string]any `json:"extra,omitempty"` // totals, sums, aggregates
}

// NewMeta computes every derived pagination field from the three numbers a
// repository actually knows: which page, how big, how many rows in total.
func NewMeta(page, limit int, totalItems int64, count int) *Meta {
	if page < 1 {
		page = 1
	}
	if limit < 1 {
		limit = 1
	}
	totalPages := 0
	if totalItems > 0 {
		totalPages = int((totalItems + int64(limit) - 1) / int64(limit))
	}
	m := &Meta{
		Page:       page,
		Limit:      limit,
		TotalItems: totalItems,
		TotalPages: totalPages,
		Count:      count,
		HasPrev:    page > 1,
		HasNext:    page < totalPages,
	}
	if m.HasNext {
		n := page + 1
		m.NextPage = &n
	}
	if m.HasPrev {
		p := page - 1
		m.PrevPage = &p
	}
	return m
}

// WithExtra attaches aggregate values (total_spent, average, ...) to the meta.
func (m *Meta) WithExtra(key string, value any) *Meta {
	if m == nil {
		return m
	}
	if m.Extra == nil {
		m.Extra = map[string]any{}
	}
	m.Extra[key] = value
	return m
}

// Result is what a handler returns. The writer turns it into an Envelope.
// Handlers never touch gin.Context.JSON directly.
type Result struct {
	Status  int               // defaults to 200
	Code    string            // defaults to "OK"
	Message string            // defaults to "Request completed successfully."
	Data    any               // marshalled into `data`
	Meta    *Meta             // marshalled into `meta`
	Headers map[string]string // extra response headers (Location, ETag, ...)
}

// ---------------------------------------------------------------------------
// Result constructors — the vocabulary handlers use.
// ---------------------------------------------------------------------------

// OK is a 200 with a payload.
func OK(data any, message string) *Result {
	return &Result{Status: 200, Code: "OK", Message: def(message, "Request completed successfully."), Data: data}
}

// Created is a 201 for a freshly inserted resource.
func Created(data any, message string) *Result {
	return &Result{Status: 201, Code: "CREATED", Message: def(message, "Created successfully."), Data: data}
}

// Accepted is a 202 for work that was queued rather than finished.
func Accepted(data any, message string) *Result {
	return &Result{Status: 202, Code: "ACCEPTED", Message: def(message, "Request accepted for processing."), Data: data}
}

// NoContent is a 204 — used by DELETE when there is nothing to return. Prefer
// OK(nil, "...") when the frontend wants a confirmation message to show.
func NoContent() *Result { return &Result{Status: 204, Code: "NO_CONTENT"} }

// List is a 200 carrying a page of items plus its pagination meta. `items` is
// normalised to an empty array (never null) so the frontend can always .map().
func List[T any](items []T, meta *Meta, message string) *Result {
	if items == nil {
		items = []T{}
	}
	if meta != nil {
		meta.Count = len(items)
	}
	return &Result{Status: 200, Code: "OK", Message: def(message, "Fetched successfully."), Data: items, Meta: meta}
}

// WithHeader attaches a response header (e.g. Location on create).
func (r *Result) WithHeader(k, v string) *Result {
	if r.Headers == nil {
		r.Headers = map[string]string{}
	}
	r.Headers[k] = v
	return r
}

func def(v, fallback string) string {
	if v == "" {
		return fallback
	}
	return v
}

func now() string { return time.Now().UTC().Format(time.RFC3339Nano) }
