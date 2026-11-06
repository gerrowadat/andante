// SPDX-License-Identifier: Apache-2.0
// Copyright (C) 2026 Dave O'Connor

package api

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"net/http"
	"regexp"
	"runtime/debug"
	"strconv"
	"strings"
	"time"

	"github.com/gerrowadat/andante/pkg/client"
	"github.com/getkin/kin-openapi/openapi3"
	"github.com/getkin/kin-openapi/openapi3filter"
	"github.com/getkin/kin-openapi/routers"
	middleware "github.com/oapi-codegen/nethttp-middleware"
	"github.com/prometheus/client_golang/prometheus"
	"github.com/santhosh-tekuri/jsonschema/v6"
	"github.com/santhosh-tekuri/jsonschema/v6/kind"
	"golang.org/x/text/language"
	"golang.org/x/text/message"
)

const (
	requestIDHeader = "X-Request-ID"
	probeRoute      = "POST " + BasePath + "/probes"

	// probeDeadlineMargin leaves time to send a probe's answer after it times out.
	probeDeadlineMargin = 5 * time.Second
)

// unmatchedRoute is the metric label for requests that matched no route. The raw
// path would let a client create unbounded label values.
const unmatchedRoute = "unmatched"

var validRequestID = regexp.MustCompile(`^[A-Za-z0-9._-]{1,64}$`)

type contextKey int

const (
	requestIDKey contextKey = iota
	routeKey
)

func requestID(ctx context.Context) string {
	id, _ := ctx.Value(requestIDKey).(string)
	return id
}

// withRequestID echoes a usable inbound request ID, or makes one up.
func withRequestID(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		id := r.Header.Get(requestIDHeader)
		if !validRequestID.MatchString(id) {
			b := make([]byte, 8)
			_, _ = rand.Read(b)
			id = hex.EncodeToString(b)
		}
		w.Header().Set(requestIDHeader, id)
		next.ServeHTTP(w, r.WithContext(context.WithValue(r.Context(), requestIDKey, id)))
	})
}

type statusRecorder struct {
	http.ResponseWriter
	status int
}

func (r *statusRecorder) WriteHeader(status int) {
	r.status = status
	r.ResponseWriter.WriteHeader(status)
}

// Unwrap lets http.ResponseController reach the connection.
func (r *statusRecorder) Unwrap() http.ResponseWriter { return r.ResponseWriter }

// countRequests records andante_api_requests_total. The route pattern is only
// known inside the mux, on a request this middleware never sees, so recordRoute
// passes it back through a pointer in the context.
func countRequests(requests *prometheus.CounterVec, next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		route := unmatchedRoute
		rec := &statusRecorder{ResponseWriter: w, status: http.StatusOK}
		defer func() {
			requests.WithLabelValues(route, strconv.Itoa(rec.status)).Inc()
		}()
		next.ServeHTTP(rec, r.WithContext(context.WithValue(r.Context(), routeKey, &route)))
	})
}

func recordRoute(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if route, ok := r.Context().Value(routeKey).(*string); ok {
			*route = r.Pattern
		}
		next.ServeHTTP(w, r)
	})
}

// extendProbeDeadline gives a probe longer than --write-timeout. It waits out the
// crawl interval between pages, so it takes minutes at the default interval, and
// the server would otherwise cut the response off.
func (h *handler) extendProbeDeadline(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Pattern == probeRoute {
			deadline := time.Now().Add(h.probeTimeout + probeDeadlineMargin)
			if err := http.NewResponseController(w).SetWriteDeadline(deadline); err != nil && !errors.Is(err, http.ErrNotSupported) {
				h.logger.Warn("could not extend the probe's deadline", "error", err)
			}
		}
		next.ServeHTTP(w, r)
	})
}

func (h *handler) recoverPanics(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		defer func() {
			v := recover()
			if v == nil {
				return
			}
			if v == http.ErrAbortHandler { //nolint:errorlint // recover returns the value itself.
				panic(v)
			}
			h.logger.Error("panic serving API request",
				"request_id", requestID(r.Context()), "panic", v, "stack", string(debug.Stack()))
			h.writeError(w, r, errPanic)
		}()
		next.ServeHTTP(w, r)
	})
}

var errPanic = errors.New("panic")

// authUnavailableError means authentication could not be attempted, which is a
// server failure and not a bad token.
type authUnavailableError struct{ err error }

func (e *authUnavailableError) Error() string { return e.err.Error() }

// authenticate checks the bearer token on any operation the spec secures. Being
// driven by the spec, a new operation needs a token unless it says otherwise.
func (h *handler) authenticate(ctx context.Context, input *openapi3filter.AuthenticationInput) error {
	token, ok := strings.CutPrefix(input.RequestValidationInput.Request.Header.Get("Authorization"), "Bearer ")
	if !ok || token == "" {
		return errors.New("missing bearer token")
	}
	valid, err := h.store.AuthenticateAPIToken(ctx, token)
	if err != nil {
		return &authUnavailableError{err: err}
	}
	if !valid {
		return errors.New("invalid bearer token")
	}
	return nil
}

// validationError renders whatever the request validator rejected.
func (h *handler) validationError(_ context.Context, err error, w http.ResponseWriter, r *http.Request, opts middleware.ErrorHandlerOpts) {
	var unavailable *authUnavailableError
	if errors.As(err, &unavailable) {
		h.writeError(w, r, unavailable.err)
		return
	}

	// The middleware suggests 404 for any request it cannot route.
	if errors.Is(err, routers.ErrMethodNotAllowed) {
		w.Header().Set("Allow", strings.Join(h.allowedMethods(r), ", "))
		writeProblem(w, newProblem(http.StatusMethodNotAllowed, problemBlank, r.Method+" is not allowed here").problem)
		return
	}

	switch opts.StatusCode {
	case http.StatusUnauthorized:
		w.Header().Set("WWW-Authenticate", `Bearer realm="andante"`)
		writeProblem(w, newProblem(http.StatusUnauthorized, problemUnauthorized, "a valid bearer token is required").problem)
	case http.StatusNotFound:
		writeProblem(w, newProblem(http.StatusNotFound, problemBlank, "there is nothing here").problem)
	case http.StatusBadRequest:
		writeProblem(w, requestProblem(err))
	default:
		h.writeError(w, r, err)
	}
}

// allowedMethods lists what the path accepts, for the Allow header a 405 needs.
func (h *handler) allowedMethods(r *http.Request) []string {
	var allowed []string
	for _, method := range []string{http.MethodGet, http.MethodPost, http.MethodPatch, http.MethodDelete} {
		probe := r.Clone(r.Context())
		probe.Method = method
		if _, _, err := h.router.FindRoute(probe); err == nil {
			allowed = append(allowed, method)
		}
	}
	return allowed
}

// fieldErrors flattens a JSON Schema validation failure to its leaves. It works
// from the validator's own structure because the error's text is meant for
// schema authors: it names the validator's internal schema URL.
func fieldErrors(v *jsonschema.ValidationError) []client.FieldError {
	if len(v.Causes) > 0 {
		var all []client.FieldError
		for _, cause := range v.Causes {
			all = append(all, fieldErrors(cause)...)
		}
		return all
	}

	field := func(detail string, location ...string) client.FieldError {
		pointer := "/" + strings.Join(location, "/")
		return client.FieldError{Pointer: &pointer, Detail: detail}
	}
	switch k := v.ErrorKind.(type) {
	case *kind.Required:
		var all []client.FieldError
		for _, name := range k.Missing {
			all = append(all, field("is required", append(v.InstanceLocation, name)...))
		}
		return all
	case *kind.AdditionalProperties:
		var all []client.FieldError
		for _, name := range k.Properties {
			all = append(all, field("is not allowed", append(v.InstanceLocation, name)...))
		}
		return all
	}
	return []client.FieldError{field(v.ErrorKind.LocalizedString(englishPrinter), v.InstanceLocation...)}
}

var englishPrinter = message.NewPrinter(language.English)

// requestProblem turns a validation failure into problem details, field by field
// where the validator says which field.
func requestProblem(err error) client.Problem {
	p := newProblem(http.StatusBadRequest, problemInvalidRequest, "the request does not match the API description").problem

	var requestErr *openapi3filter.RequestError
	if errors.As(err, &requestErr) && requestErr.Parameter != nil {
		detail := "parameter " + requestErr.Parameter.Name + ": " + requestErr.Reason
		p.Errors = &[]client.FieldError{{Detail: detail}}
		return p
	}
	var validationErr *jsonschema.ValidationError
	if errors.As(err, &validationErr) {
		fields := fieldErrors(validationErr)
		p.Errors = &fields
		return p
	}
	var schemaErr *openapi3.SchemaError
	if errors.As(err, &schemaErr) {
		pointer := "/" + strings.Join(schemaErr.JSONPointer(), "/")
		p.Errors = &[]client.FieldError{{Pointer: &pointer, Detail: schemaErr.Reason}}
		return p
	}
	detail := err.Error()
	p.Detail = &detail
	return p
}
