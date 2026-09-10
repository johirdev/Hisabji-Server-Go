package handler

import (
	"context"
	"strings"

	"github.com/gin-gonic/gin"
	"github.com/johirdev/Hisabji-Server/internal/core/apperr"
	"github.com/johirdev/Hisabji-Server/internal/core/query"
	"github.com/johirdev/Hisabji-Server/internal/core/response"
	"github.com/johirdev/Hisabji-Server/internal/core/validate"
)

// CRUDService is the contract a module's service satisfies to get five REST
// endpoints for free. T is the response model, C the create DTO, U the update
// DTO. Every method takes the owner id, so ownership is enforced in one place.
type CRUDService[T any, C any, U any] interface {
	List(ctx context.Context, userID string, opts *query.Options) (Page[T], error)
	Get(ctx context.Context, userID, id string) (T, error)
	Create(ctx context.Context, userID string, in C) (T, error)
	Update(ctx context.Context, userID, id string, in U) (T, error)
	Delete(ctx context.Context, userID, id string) error
}

// BulkDeleter is optional: implement it to also get DELETE /bulk.
type BulkDeleter interface {
	DeleteMany(ctx context.Context, userID string, ids []string) (int64, error)
}

// Summarizer is optional: implement it to also get GET /summary, which applies
// the same filters as the list endpoint but returns aggregates instead of rows.
type Summarizer interface {
	Summary(ctx context.Context, userID string, opts *query.Options) (any, error)
}

// CRUD wires a CRUDService onto a router group. It is the reason a new module
// needs a repository, a service and ~30 lines of routes rather than a full
// controller per endpoint.
//
//	crud := handler.CRUD[Expense, CreateRequest, UpdateRequest]{
//	    Resource: "Expense", Plural: "Expenses",
//	    Schema:   ExpenseSchema,
//	    Service:  svc,
//	}
//	crud.Mount(rg.Group("/expenses"))
//
// Which registers:
//
//	GET    /expenses          list + search + filter + sort + paginate
//	GET    /expenses/summary  aggregates over the same filters (if supported)
//	GET    /expenses/:id      fetch one
//	POST   /expenses          create
//	PATCH  /expenses/:id      partial update
//	PUT    /expenses/:id      partial update (alias, for clients that prefer PUT)
//	DELETE /expenses/:id      delete
//	DELETE /expenses/bulk     delete many (if supported)
type CRUD[T any, C any, U any] struct {
	// Resource is the singular human name used in messages: "Expense".
	Resource string
	// Plural is used in list messages: "Expenses". Defaults to Resource + "s".
	Plural string

	Schema  query.Schema
	Service CRUDService[T, C, U]

	// IDParam is the path parameter name. Defaults to "id".
	IDParam string

	// Skip disables individual routes, e.g. Skip: []string{"delete"}.
	Skip []string

	// Middleware runs on every route this CRUD registers (e.g. a plan gate).
	Middleware []gin.HandlerFunc

	// RouteMiddleware runs on one named route only, e.g.
	// RouteMiddleware: map[string][]gin.HandlerFunc{"create": {rateLimit}}.
	RouteMiddleware map[string][]gin.HandlerFunc
}

func (x *CRUD[T, C, U]) resource() string {
	if x.Resource == "" {
		return "Record"
	}
	return x.Resource
}

func (x *CRUD[T, C, U]) plural() string {
	if x.Plural != "" {
		return x.Plural
	}
	return x.resource() + "s"
}

func (x *CRUD[T, C, U]) idParam() string {
	if x.IDParam == "" {
		return "id"
	}
	return x.IDParam
}

func (x *CRUD[T, C, U]) skipped(name string) bool {
	for _, s := range x.Skip {
		if strings.EqualFold(s, name) {
			return true
		}
	}
	return false
}

// chain builds the middleware list for one route plus its final handler.
func (x *CRUD[T, C, U]) chain(name string, fn Func) []gin.HandlerFunc {
	out := make([]gin.HandlerFunc, 0, len(x.Middleware)+2)
	out = append(out, x.Middleware...)
	out = append(out, x.RouteMiddleware[name]...)
	return append(out, H(fn))
}

// Mount registers every enabled route on the group.
func (x *CRUD[T, C, U]) Mount(rg *gin.RouterGroup) {
	id := "/:" + x.idParam()

	if !x.skipped("list") {
		rg.GET("", x.chain("list", x.List)...)
	}
	if s, ok := x.Service.(Summarizer); ok && !x.skipped("summary") {
		rg.GET("/summary", x.chain("summary", x.summaryHandler(s))...)
	}
	if b, ok := x.Service.(BulkDeleter); ok && !x.skipped("bulk_delete") {
		// Registered before /:id so "bulk" is not captured as an id.
		rg.DELETE("/bulk", x.chain("bulk_delete", x.bulkDeleteHandler(b))...)
	}
	if !x.skipped("get") {
		rg.GET(id, x.chain("get", x.Get)...)
	}
	if !x.skipped("create") {
		rg.POST("", x.chain("create", x.Create)...)
	}
	if !x.skipped("update") {
		rg.PATCH(id, x.chain("update", x.Update)...)
		rg.PUT(id, x.chain("update", x.Update)...)
	}
	if !x.skipped("delete") {
		rg.DELETE(id, x.chain("delete", x.Delete)...)
	}
}

// List handles GET / — search, filter, sort, paginate.
func (x *CRUD[T, C, U]) List(c *gin.Context) (*response.Result, error) {
	userID, err := UserID(c)
	if err != nil {
		return nil, err
	}
	opts, err := ListOptions(c, x.Schema)
	if err != nil {
		return nil, err
	}
	page, err := x.Service.List(c.Request.Context(), userID, opts)
	if err != nil {
		return nil, err
	}
	return ListResult(page, opts, x.plural()+" fetched successfully."), nil
}

// Get handles GET /:id.
func (x *CRUD[T, C, U]) Get(c *gin.Context) (*response.Result, error) {
	userID, err := UserID(c)
	if err != nil {
		return nil, err
	}
	id, err := validate.UUIDParam(c, x.idParam())
	if err != nil {
		return nil, err
	}
	out, err := x.Service.Get(c.Request.Context(), userID, id)
	if err != nil {
		return nil, err
	}
	return response.OK(out, x.resource()+" fetched successfully."), nil
}

// Create handles POST /.
func (x *CRUD[T, C, U]) Create(c *gin.Context) (*response.Result, error) {
	userID, err := UserID(c)
	if err != nil {
		return nil, err
	}
	var in C
	if err := validate.Body(c, &in); err != nil {
		return nil, err
	}
	out, err := x.Service.Create(c.Request.Context(), userID, in)
	if err != nil {
		return nil, err
	}
	return response.Created(out, x.resource()+" created successfully."), nil
}

// Update handles PATCH/PUT /:id.
func (x *CRUD[T, C, U]) Update(c *gin.Context) (*response.Result, error) {
	userID, err := UserID(c)
	if err != nil {
		return nil, err
	}
	id, err := validate.UUIDParam(c, x.idParam())
	if err != nil {
		return nil, err
	}
	var in U
	if err := validate.Body(c, &in); err != nil {
		return nil, err
	}
	out, err := x.Service.Update(c.Request.Context(), userID, id, in)
	if err != nil {
		return nil, err
	}
	return response.OK(out, x.resource()+" updated successfully."), nil
}

// Delete handles DELETE /:id. It answers 200 with a message rather than 204,
// because mobile clients want something to show in a toast.
func (x *CRUD[T, C, U]) Delete(c *gin.Context) (*response.Result, error) {
	userID, err := UserID(c)
	if err != nil {
		return nil, err
	}
	id, err := validate.UUIDParam(c, x.idParam())
	if err != nil {
		return nil, err
	}
	if err := x.Service.Delete(c.Request.Context(), userID, id); err != nil {
		return nil, err
	}
	return response.OK(map[string]any{"id": id}, x.resource()+" deleted successfully."), nil
}

func (x *CRUD[T, C, U]) summaryHandler(s Summarizer) Func {
	return func(c *gin.Context) (*response.Result, error) {
		userID, err := UserID(c)
		if err != nil {
			return nil, err
		}
		opts, err := ListOptions(c, x.Schema)
		if err != nil {
			return nil, err
		}
		out, err := s.Summary(c.Request.Context(), userID, opts)
		if err != nil {
			return nil, err
		}
		return response.OK(out, x.resource()+" summary calculated successfully."), nil
	}
}

// BulkDeleteRequest is the body of DELETE /bulk.
type BulkDeleteRequest struct {
	IDs []string `json:"ids" binding:"required,min=1,max=200,dive,uuid"`
}

func (x *CRUD[T, C, U]) bulkDeleteHandler(b BulkDeleter) Func {
	return func(c *gin.Context) (*response.Result, error) {
		userID, err := UserID(c)
		if err != nil {
			return nil, err
		}
		var in BulkDeleteRequest
		if err := validate.Body(c, &in); err != nil {
			return nil, err
		}
		n, err := b.DeleteMany(c.Request.Context(), userID, in.IDs)
		if err != nil {
			return nil, err
		}
		if n == 0 {
			return nil, apperr.NotFound("None of the requested " + strings.ToLower(x.plural()))
		}
		return response.OK(map[string]any{"deleted_count": n},
			x.plural()+" deleted successfully."), nil
	}
}
