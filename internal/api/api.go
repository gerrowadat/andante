// SPDX-License-Identifier: Apache-2.0
// Copyright (C) 2026 Dave O'Connor

package api

import (
	"context"
	"fmt"
	"log/slog"
	"net/http"
	"time"

	"github.com/gerrowadat/andante/api"
	"github.com/gerrowadat/andante/internal/api/gen"
	"github.com/gerrowadat/andante/internal/app"
	"github.com/gerrowadat/andante/internal/bootstrap"
	"github.com/gerrowadat/andante/internal/buildinfo"
	"github.com/gerrowadat/andante/internal/clientip"
	"github.com/gerrowadat/andante/internal/httpapi"
	"github.com/gerrowadat/andante/internal/logring"
	"github.com/gerrowadat/andante/internal/reads"
	"github.com/gerrowadat/andante/internal/replica"
	"github.com/gerrowadat/andante/internal/store"
	"github.com/gerrowadat/andante/pkg/client"
	"github.com/getkin/kin-openapi/openapi3"
	"github.com/getkin/kin-openapi/openapi3filter"
	"github.com/getkin/kin-openapi/routers/gorillamux"
	middleware "github.com/oapi-codegen/nethttp-middleware"
	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promauto"
	"sigs.k8s.io/yaml"
)

// BasePath is where the API is mounted.
const BasePath = "/api/v1"

func init() {
	openapi3filter.RegisterBodyDecoder("text/x-opml", openapi3filter.PlainBodyDecoder)
}

// Options are what the API needs from the rest of the server.
type Options struct {
	Store     *store.Store
	Catalogue *replica.Client
	Sync      *replica.Syncer
	Reads     *reads.Service
	Clients   *clientip.Resolver
	BaseURL   string
	Registry  prometheus.Registerer
	Logger    *slog.Logger
	// ProbeTimeout bounds POST /probes. It outlasts --write-timeout on purpose.
	ProbeTimeout time.Duration
	// Bootstrap makes the first admin. Nil makes POST /bootstrap a 404.
	Bootstrap *bootstrap.Service
	// Logs is what GET /logs reads of the server's own log. Nil keeps nothing.
	Logs *logring.Ring
}

type handler struct {
	app       *app.App
	store     *store.Store
	catalogue *replica.Client
	sync      *replica.Syncer
	reads     *reads.Service
	clients   *clientip.Resolver
	baseURL   string
	logger    *slog.Logger
	specJSON  []byte
	kit       *httpapi.Kit
	bootstrap *bootstrap.Service
}

// New returns the API. Mount it at BasePath.
func New(opts Options) (http.Handler, error) {
	specJSON, err := yaml.YAMLToJSON(api.OpenAPI)
	if err != nil {
		return nil, fmt.Errorf("convert API description to JSON: %w", err)
	}
	loader := openapi3.NewLoader()
	spec, err := loader.LoadFromData(api.OpenAPI)
	if err != nil {
		return nil, fmt.Errorf("load API description: %w", err)
	}
	if err := spec.Validate(context.Background()); err != nil {
		return nil, fmt.Errorf("validate API description: %w", err)
	}

	router, err := gorillamux.NewRouter(spec)
	if err != nil {
		return nil, fmt.Errorf("route API description: %w", err)
	}

	h := &handler{
		app:   &app.App{Store: opts.Store, Reads: opts.Reads, Catalogue: opts.Catalogue, Sync: opts.Sync, Logger: opts.Logger, LogRing: opts.Logs},
		store: opts.Store, catalogue: opts.Catalogue, sync: opts.Sync, reads: opts.Reads, clients: opts.Clients, baseURL: opts.BaseURL, logger: opts.Logger,
		specJSON: specJSON, bootstrap: opts.Bootstrap,
	}
	kit := &httpapi.Kit{
		Logger: opts.Logger, Router: router, Valid: h.authenticate,
		SlowRoutes: []string{"POST " + BasePath + "/probes", "POST " + BasePath + "/discoveries"}, ProbeTimeout: opts.ProbeTimeout,
	}
	h.kit = kit

	strict := gen.NewStrictHandlerWithOptions(h, nil, gen.StrictHTTPServerOptions{
		RequestErrorHandlerFunc: func(w http.ResponseWriter, _ *http.Request, err error) {
			httpapi.WriteProblem(w, httpapi.RequestProblem(err))
		},
		ResponseErrorHandlerFunc: kit.WriteError,
	})
	routes := gen.HandlerWithOptions(strict, gen.StdHTTPServerOptions{
		BaseURL:     BasePath,
		BaseRouter:  http.NewServeMux(),
		Middlewares: []gen.MiddlewareFunc{kit.ExtendProbeDeadline, httpapi.RecordRoute},
		ErrorHandlerFunc: func(w http.ResponseWriter, _ *http.Request, err error) {
			httpapi.WriteProblem(w, httpapi.RequestProblem(err))
		},
	})

	validate := middleware.OapiRequestValidatorWithOptions(spec, &middleware.Options{
		Options:               openapi3filter.Options{AuthenticationFunc: kit.Authenticate},
		ErrorHandlerWithOpts:  kit.ValidationError,
		SilenceServersWarning: true,
	})

	requests := promauto.With(opts.Registry).NewCounterVec(prometheus.CounterOpts{
		Name: "andante_api_requests_total",
		Help: "Admin API requests, by route pattern and status.",
	}, []string{"route", "status"})

	return httpapi.WithRequestID(httpapi.CountRequests(requests, httpapi.LimitBody(kit.RecoverPanics(h.withActor(validate(routes)))))), nil
}

type (
	actorKey   struct{}
	addressKey struct{}
)

// withActor makes room in the context for who the request is for. The validator
// authenticates, and it can't change the request it passes on, so it fills this in.
func (h *handler) withActor(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		ctx := context.WithValue(r.Context(), actorKey{}, &store.Actor{})
		if h.clients != nil {
			ctx = context.WithValue(ctx, addressKey{}, h.clients.IP(r))
		}
		next.ServeHTTP(w, r.WithContext(ctx))
	})
}

func addressOf(ctx context.Context) string {
	address, _ := ctx.Value(addressKey{}).(string)
	return address
}

func (h *handler) authenticate(ctx context.Context, token string) (bool, error) {
	actor, ok, err := h.store.AuthenticateAPIToken(ctx, token)
	if err != nil || !ok {
		return false, err
	}
	if slot, found := ctx.Value(actorKey{}).(*store.Actor); found {
		*slot = actor
	}
	return true, nil
}

// actorOf returns who the request is for. An operation without a token has a zero
// Actor, which can't reach anything.
func actorOf(ctx context.Context) store.Actor {
	if slot, found := ctx.Value(actorKey{}).(*store.Actor); found {
		return *slot
	}
	return store.Actor{}
}

// VisitGetSpecResponse sends the document as it is, with no round trip through
// a map to lose its key order.
type specResponse []byte

func (s specResponse) VisitGetSpecResponse(w http.ResponseWriter) error {
	w.Header().Set("Content-Type", "application/json")
	_, err := w.Write(s)
	return err
}

func (h *handler) GetSpec(context.Context, gen.GetSpecRequestObject) (gen.GetSpecResponseObject, error) {
	return specResponse(h.specJSON), nil
}

func (h *handler) GetMe(ctx context.Context, _ gen.GetMeRequestObject) (gen.GetMeResponseObject, error) {
	user, err := h.store.GetUser(ctx, actorOf(ctx).UserID)
	if err != nil {
		return nil, err
	}
	return gen.GetMe200JSONResponse{ID: user.ID, Name: user.Name, Role: client.UserRole(user.Role), CreatedAt: user.CreatedAt}, nil
}

func (h *handler) GetVersion(context.Context, gen.GetVersionRequestObject) (gen.GetVersionResponseObject, error) {
	return gen.GetVersion200JSONResponse{
		Version: buildinfo.Version(), Commit: buildinfo.Commit(), Date: buildinfo.Date(),
	}, nil
}
