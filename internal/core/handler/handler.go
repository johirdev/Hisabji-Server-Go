// Package handler removes the repeated plumbing from every controller.
//
// Without it, each endpoint repeats the same six lines: bind, check error,
// write status, marshal, log, return. With it, a controller method is only the
// part that is actually different:
//
//	func (h *Handler) Create(c *gin.Context) (*response.Result, error) {
//	    var req CreateExpenseRequest
//	    if err := validate.Body(c, &req); err != nil {
//	        return nil, err                       // -> 422 with field details
//	    }
//	    out, err := h.Service.Create(c.Request.Context(), contextx.UserID(c), req)
//	    if err != nil {
//	        return nil, err                       // -> mapped status + code
//	    }
//	    return response.Created(out, "Expense added successfully."), nil
//	}
//
// Registered with handler.H(h.Create). That single wrapper is the project-wide
// "try/catch": nothing below it ever writes to the ResponseWriter directly.
package handler

import (
	"context"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/johirdev/Hisabji-Server/internal/core/apperr"
	"github.com/johirdev/Hisabji-Server/internal/core/contextx"
	"github.com/johirdev/Hisabji-Server/internal/core/query"
	"github.com/johirdev/Hisabji-Server/internal/core/response"
)

// Func is the signature every controller method in this codebase uses.
type Func func(c *gin.Context) (*response.Result, error)

// H adapts a Func into a gin.HandlerFunc: it writes the success envelope, or
// converts any error into the standard failure envelope.
func H(fn Func) gin.HandlerFunc {
	return func(c *gin.Context) {
		res, err := fn(c)
		if err != nil {
			response.Fail(c, err)
			return
		}
		response.Write(c, res)
	}
}

// HT is H with a per-handler timeout. Use it on endpoints that call slow
// dependencies (AI generation, payment providers) so one stuck upstream cannot
// pin a connection for the server's whole write timeout.
func HT(timeout time.Duration, fn Func) gin.HandlerFunc {
	return func(c *gin.Context) {
		ctx, cancel := context.WithTimeout(c.Request.Context(), timeout)
		defer cancel()
		c.Request = c.Request.WithContext(ctx)

		res, err := fn(c)
		if err != nil {
			if ctx.Err() == context.DeadlineExceeded {
				response.Fail(c, apperr.Timeout("").WithCause(err))
				return
			}
			response.Fail(c, err)
			return
		}
		response.Write(c, res)
	}
}

// UserID returns the authenticated caller, or a 401 error when the route was
// mounted without auth middleware. Calling this instead of reading the context
// key directly makes an unprotected route fail loudly rather than silently
// query with an empty owner id.
func UserID(c *gin.Context) (string, error) {
	id := contextx.UserID(c)
	if id == "" {
		return "", apperr.Unauthorized("You must be signed in to perform this action.")
	}
	return id, nil
}

// ListOptions parses page/limit/sort/search/filters against a schema.
func ListOptions(c *gin.Context, schema query.Schema) (*query.Options, error) {
	return query.Parse(c, schema)
}

// Page bundles a slice of rows with the total count, which is what every
// repository List method returns.
type Page[T any] struct {
	Items []T
	Total int64
}

// ListResult turns a Page plus the parsed options into a ready envelope,
// including pagination meta and an echo of the applied filters.
func ListResult[T any](p Page[T], opts *query.Options, message string) *response.Result {
	meta := response.NewMeta(opts.Page, opts.Limit, p.Total, len(p.Items))
	meta.Sort = opts.SortString()
	meta.Search = opts.Search
	meta.Filters = opts.FilterMap()
	return response.List(p.Items, meta, message)
}

// OptionalUserID returns the caller's id, or "" when the route allows
// anonymous access. Use it on routes mounted with Authenticator.Optional().
func OptionalUserID(c *gin.Context) string { return contextx.UserID(c) }
