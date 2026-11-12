// SPDX-License-Identifier: Apache-2.0
// Copyright (C) 2026 Dave O'Connor

package api_test

import (
	"net/http"
	"strings"
	"testing"

	"github.com/gerrowadat/andante/internal/api"
	"github.com/gerrowadat/andante/internal/stacktest"
	"github.com/gerrowadat/andante/internal/store/storetest"
	"github.com/gerrowadat/andante/pkg/client"
)

func TestBootstrappingOverTheAPI(t *testing.T) {
	token := strings.Repeat("b", 32)
	stack := stacktest.NewWith(t, stacktest.Options{BootstrapToken: token})
	ctx := t.Context()
	// No API token: the bootstrap token is the authentication.
	c, err := client.NewClientWithResponses(stack.URL + api.BasePath)
	if err != nil {
		t.Fatal(err)
	}
	wrong, err := c.BootstrapWithResponse(ctx, client.BootstrapRequest{Token: "guess", Name: "dave"})
	if err != nil || wrong.StatusCode() != http.StatusUnauthorized {
		t.Errorf("a wrong token: %v %d %s", err, wrong.StatusCode(), wrong.Body)
	}
	bad, err := c.BootstrapWithResponse(ctx, client.BootstrapRequest{Token: token, Name: "Not A Name"})
	if err != nil || bad.StatusCode() != http.StatusBadRequest {
		t.Errorf("a bad name: %v %d", err, bad.StatusCode())
	}
	made, err := c.BootstrapWithResponse(ctx, client.BootstrapRequest{Token: token, Name: "dave"})
	if err != nil || made.JSON201 == nil {
		t.Fatalf("bootstrap: %v %s", err, made.Body)
	}
	if made.JSON201.User.Role != client.UserRoleAdmin || made.JSON201.PasswordURL == nil ||
		!strings.HasPrefix(*made.JSON201.PasswordURL, "https://andante.example/invite/") {
		t.Errorf("bootstrap = %s", made.Body)
	}
	admin, err := client.NewClientWithResponses(stack.URL+api.BasePath, client.WithRequestEditorFn(bearer(made.JSON201.Token)))
	if err != nil {
		t.Fatal(err)
	}
	if me, err := admin.GetMeWithResponse(ctx); err != nil || me.JSON200 == nil || me.JSON200.Name != "dave" {
		t.Errorf("me with the new token: %v %s", err, me.Body)
	}
	again, err := c.BootstrapWithResponse(ctx, client.BootstrapRequest{Token: token, Name: "sam"})
	if err != nil || again.StatusCode() != http.StatusNotFound {
		t.Errorf("with an admin: %v %d", err, again.StatusCode())
	}
}

func TestBootstrapIsThrottledAndNeverTakesOver(t *testing.T) {
	token := strings.Repeat("b", 32)
	stack := stacktest.NewWith(t, stacktest.Options{BootstrapToken: token})
	ctx := t.Context()
	storetest.User(t, stack.Store, "alex", false)
	c, err := client.NewClientWithResponses(stack.URL + api.BasePath)
	if err != nil {
		t.Fatal(err)
	}
	taken, err := c.BootstrapWithResponse(ctx, client.BootstrapRequest{Token: token, Name: "alex"})
	if err != nil || taken.StatusCode() != http.StatusConflict {
		t.Errorf("taking over alex: %v %d", err, taken.StatusCode())
	}
	var last *client.BootstrapResponse
	for range 30 {
		if last, err = c.BootstrapWithResponse(ctx, client.BootstrapRequest{Token: "guess", Name: "dave"}); err != nil || last.StatusCode() == http.StatusTooManyRequests {
			break
		}
	}
	if err != nil || last.StatusCode() != http.StatusTooManyRequests || last.HTTPResponse.Header.Get("Retry-After") == "" {
		t.Errorf("after guessing: %v %d", err, last.StatusCode())
	}
}

func TestBootstrapIsOffWithoutTheService(t *testing.T) {
	f := newFixture(t)
	got, err := f.client.BootstrapWithResponse(t.Context(), client.BootstrapRequest{Token: "x", Name: "dave"})
	if err != nil || got.StatusCode() != http.StatusNotFound {
		t.Errorf("bootstrap: %v %d", err, got.StatusCode())
	}
}
