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
	"github.com/gerrowadat/andante/internal/discovery"
	"github.com/gerrowadat/andante/internal/store"
	"github.com/getkin/kin-openapi/openapi3"
	"github.com/getkin/kin-openapi/openapi3filter"
	"github.com/getkin/kin-openapi/routers"
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
	Store      *store.Store
	Strategies *discovery.Registry
	Prober     *discovery.Prober
	Registry   prometheus.Registerer
	Logger     *slog.Logger
	// ProbeTimeout bounds POST /probes. It outlasts --write-timeout on purpose.
	ProbeTimeout time.Duration
}

type handler struct {
	store      *store.Store
	strategies *discovery.Registry
	prober     *discovery.Prober
	logger     *slog.Logger
	specJSON   []byte
	router     routers.Router

	probeTimeout time.Duration
}

// New returns the admin API. Mount it at BasePath.
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
		store: opts.Store, strategies: opts.Strategies, prober: opts.Prober, logger: opts.Logger,
		specJSON: specJSON, router: router, probeTimeout: opts.ProbeTimeout,
	}

	strict := gen.NewStrictHandlerWithOptions(h, nil, gen.StrictHTTPServerOptions{
		RequestErrorHandlerFunc: func(w http.ResponseWriter, _ *http.Request, err error) {
			writeProblem(w, requestProblem(err))
		},
		ResponseErrorHandlerFunc: h.writeError,
	})
	routes := gen.HandlerWithOptions(strict, gen.StdHTTPServerOptions{
		BaseURL:     BasePath,
		BaseRouter:  http.NewServeMux(),
		Middlewares: []gen.MiddlewareFunc{h.extendProbeDeadline, recordRoute},
		ErrorHandlerFunc: func(w http.ResponseWriter, _ *http.Request, err error) {
			writeProblem(w, requestProblem(err))
		},
	})

	validate := middleware.OapiRequestValidatorWithOptions(spec, &middleware.Options{
		Options:               openapi3filter.Options{AuthenticationFunc: h.authenticate},
		ErrorHandlerWithOpts:  h.validationError,
		SilenceServersWarning: true,
	})

	requests := promauto.With(opts.Registry).NewCounterVec(prometheus.CounterOpts{
		Name: "andante_api_requests_total",
		Help: "Admin API requests, by route pattern and status.",
	}, []string{"route", "status"})

	return withRequestID(countRequests(requests, h.recoverPanics(validate(routes)))), nil
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
