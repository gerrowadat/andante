// SPDX-License-Identifier: Apache-2.0
// Copyright (C) 2026 Dave O'Connor

//go:build e2e

package e2e

import (
	"context"
	"net/url"
	"os"
	"os/exec"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/chromedp/cdproto/cdp"
	"github.com/chromedp/cdproto/dom"
	"github.com/chromedp/cdproto/emulation"
	cdplog "github.com/chromedp/cdproto/log"
	"github.com/chromedp/cdproto/network"
	"github.com/chromedp/chromedp"
	"github.com/gerrowadat/andante/internal/discovery/sitetest"
	"github.com/gerrowadat/andante/internal/store/storetest"
)

// chrome finds a Chrome to drive: ANDANTE_E2E_CHROME, or one of the names chromedp
// looks for. Without one the browser tests skip, unless ANDANTE_REQUIRE_BROWSER is
// set, as it is in CI.
func chrome(t *testing.T) string {
	t.Helper()
	names := []string{os.Getenv("ANDANTE_E2E_CHROME"), "headless_shell", "headless-shell", "chromium", "chromium-browser", "google-chrome", "google-chrome-stable"}
	for _, name := range names {
		if name == "" {
			continue
		}
		if found, err := exec.LookPath(name); err == nil {
			return found
		}
	}
	if os.Getenv("ANDANTE_REQUIRE_BROWSER") != "" {
		t.Fatal("no Chrome to drive; set ANDANTE_E2E_CHROME")
	}
	t.Skip("no Chrome to drive; set ANDANTE_E2E_CHROME, or ANDANTE_REQUIRE_BROWSER to fail")
	return ""
}

// headless starts a browser, with JavaScript on or off, that fails the test on
// any script error or anything the Content-Security-Policy blocks.
func headless(t *testing.T, scripts bool) context.Context {
	t.Helper()
	// The sandbox needs user namespaces, which CI's runners don't allow, and the
	// browser only ever sees our own pages here.
	opts := append(chromedp.DefaultExecAllocatorOptions[:], chromedp.ExecPath(chrome(t)), chromedp.NoSandbox)
	alloc, stopAlloc := chromedp.NewExecAllocator(context.Background(), opts...)
	browser, stop := chromedp.NewContext(alloc)
	t.Cleanup(func() { stop(); stopAlloc() })
	// The first call starts the browser, which lives as long as its context.
	if err := chromedp.Do(browser); err != nil {
		t.Fatal(err)
	}
	if _, err := chromedp.Call(browser, cdplog.Enable, cdp.Empty{}); err != nil {
		t.Fatal(err)
	}
	if !scripts {
		if _, err := chromedp.Call(browser, emulation.SetScriptExecutionDisabled, emulation.SetScriptExecutionDisabledParams{Value: true}); err != nil {
			t.Fatal(err)
		}
	}
	messages := chromedp.Console(browser)
	var mu sync.Mutex
	var bad []string
	go func() {
		for m, err := range messages {
			if err != nil {
				return
			}
			t.Logf("browser: %s", m)
			if m.IsException() || strings.Contains(m.Text, "Content Security Policy") {
				mu.Lock()
				bad = append(bad, m.String())
				mu.Unlock()
			}
		}
	}()
	t.Cleanup(func() {
		mu.Lock()
		defer mu.Unlock()
		for _, m := range bad {
			t.Errorf("in the browser: %s", m)
		}
	})
	ctx, stopTimer := context.WithTimeout(browser, 2*time.Minute)
	t.Cleanup(stopTimer)
	return ctx
}

// loggedIn starts both services, makes the first admin through the website, and
// returns the server's address and the person, for a browser to carry on with.
// The catalogue asks a site for a page a second, so that looking at one takes a
// few seconds, long enough to watch.
func loggedIn(t *testing.T) (string, *browser, *process, *process) {
	t.Helper()
	const token = "a-bootstrap-token-of-at-least-32-characters"
	catalogue := startCatalogue(t, build(t, "andante-catalogue"), storetest.DSN(t), "--crawl-allow-private", "--crawl-interval=1s", "--crawl-poll-interval=20ms")
	catalogue.waitReady(t)
	// A plain http:// base URL, so that the session cookie isn't Secure, and the
	// browser sends it back.
	addr := freeAddr(t)
	server := startServer(t, buildServer(t), storetest.DSN(t), catalogue.flag(), "--bootstrap-token="+token,
		"--listen="+addr, "--base-url=http://"+addr)
	server.waitReady(t)
	b := &browser{address: addr, cookie: "andante_session"}
	invite := b.post(t, "/setup", url.Values{"token": {token}, "name": {"dave"}})
	b.post(t, invite, url.Values{"password": {"a long enough phrase"}})
	if b.session == "" {
		t.Fatal("no session after setting a password")
	}
	return "http://" + addr, b, server, catalogue
}

func withSession(base, session string) chromedp.Action[chromedp.Void] {
	return func(ctx context.Context, t *chromedp.Target) (chromedp.Void, error) {
		_, err := cdp.Call(ctx, t, network.SetCookie, network.SetCookieParams{
			Name: "andante_session", Value: session, URL: base + "/", HTTPOnly: new(true),
		})
		return chromedp.Void{}, err
	}
}

// startLooking asks the Add page to look at a site, and waits for the page that
// waits for the answer.
func startLooking(ctx context.Context, t *testing.T, base, session, address string, server, catalogue *process) {
	t.Helper()
	err := chromedp.Do(ctx,
		withSession(base, session),
		chromedp.Navigate(base+"/add"),
		chromedp.WaitReady(chromedp.ID("url")),
		chromedp.SendKeys(chromedp.ID("url"), address),
		chromedp.Click(chromedp.CSS(`form[action="/add"] button`)),
		chromedp.WaitReady(chromedp.ID("look")),
	)
	if err != nil {
		t.Fatalf("starting a look: %v. Logs:\n%s\n%s", err, server.output(), catalogue.output())
	}
}

// With JavaScript on, the waiting page shows what turns up without reloading.
func TestAddingASiteInABrowser(t *testing.T) {
	ctx := headless(t, true)
	base, b, server, catalogue := loggedIn(t)
	site := sitetest.Serve(t, "discover-comic")

	startLooking(ctx, t, base, b.session, site.URL+"/", server, catalogue)
	waiting, err := chromedp.Run(ctx, chromedp.Location())
	if err != nil {
		t.Fatal(err)
	}
	// Something to find later, which a reload would lose.
	looking, err := chromedp.Run(ctx, chromedp.Evaluate[bool](`window.notReloaded = true; document.querySelector("#look [aria-busy]") !== null`))
	if err != nil {
		t.Fatal(err)
	}
	if !looking {
		t.Fatal("the look was over before the page could watch it")
	}
	// The site has no feed, so the catalogue tries where one usually is, a second
	// apart, and the page shows each step as it starts.
	if err := chromedp.Do(ctx, chromedp.WaitVisible(chromedp.Search(`//li[@aria-busy="true" and starts-with(text(), "No feed is linked")]`))); err != nil {
		t.Fatalf("waiting for a step: %v. Logs:\n%s\n%s", err, server.output(), catalogue.output())
	}

	if err := chromedp.Do(ctx, chromedp.WaitVisible(chromedp.Search(`//button[text()="Add this"]`))); err != nil {
		t.Fatalf("waiting for what turned up: %v. Logs:\n%s\n%s", err, server.output(), catalogue.output())
	}
	stayed, err := chromedp.Run(ctx, chromedp.Evaluate[bool](`window.notReloaded === true`))
	if err != nil {
		t.Fatal(err)
	}
	at, err := chromedp.Run(ctx, chromedp.Location())
	if err != nil {
		t.Fatal(err)
	}
	if !stayed || at != waiting {
		t.Errorf("the page reloaded, or went from %s to %s", waiting, at)
	}
	if title, err := chromedp.Run(ctx, chromedp.Text(chromedp.CSS("#look article h2"))); err != nil || title != "The Comic" {
		t.Errorf("what turned up: %q, %v", title, err)
	}
	if steps, err := chromedp.Run(ctx, chromedp.Evaluate[int](`document.querySelectorAll("#look details.stages li").length`)); err != nil || steps < 3 {
		t.Errorf("the steps it took: %d, %v", steps, err)
	}

	// The candidate's form still works as a form.
	err = chromedp.Do(ctx,
		chromedp.Click(chromedp.Search(`//button[text()="Add this"]`)),
		chromedp.WaitReady(chromedp.CSS(`input[name="name"]`)),
	)
	if err != nil {
		t.Fatalf("adding it: %v", err)
	}
	if at, err := chromedp.Run(ctx, chromedp.Location()); err != nil || !strings.HasSuffix(at, "/track") {
		t.Errorf("adding it went to %s: %v", at, err)
	}
}

// With JavaScript off, the waiting page refreshes itself until there's an answer.
// It reads the page through the DOM alone, since chromedp's queries run scripts of
// their own.
func TestAddingASiteWithoutJavaScript(t *testing.T) {
	ctx := headless(t, false)
	base, b, server, catalogue := loggedIn(t)
	site := sitetest.Serve(t, "discover-comic")

	b.get(t, "/add")
	waiting := b.post(t, "/add", url.Values{"url": {site.URL + "/"}})
	if err := chromedp.Do(ctx, withSession(base, b.session), chromedp.Navigate(base+waiting)); err != nil {
		t.Fatal(err)
	}
	var page string
	looked, stepped := false, false
	for deadline := time.Now().Add(time.Minute); ; time.Sleep(200 * time.Millisecond) {
		// The page can be part way through refreshing, which is worth another try.
		if doc, err := chromedp.Call(ctx, dom.GetDocument, dom.GetDocumentParams{}); err == nil && doc.Root != nil {
			if html, err := chromedp.Call(ctx, dom.GetOuterHTML, dom.GetOuterHTMLParams{NodeID: doc.Root.NodeID}); err == nil {
				page = html.OuterHTML
			}
		}
		looked = looked || strings.Contains(page, "Looking.")
		stepped = stepped || strings.Contains(page, "No feed is linked") || strings.Contains(page, "Trying 1 of 1")
		if strings.Contains(page, "Add this") {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("still looking: %s. Logs:\n%s\n%s", page, server.output(), catalogue.output())
		}
	}
	if !looked {
		t.Error("the look was over before the page could watch it")
	}
	if !stepped {
		t.Error("no page showed a step while it looked")
	}
	if !strings.Contains(page, "The Comic") || !strings.Contains(page, "How andante looked") {
		t.Errorf("what turned up: %s", page)
	}
}

// The theme an admin picks is what the browser draws with, and its stylesheet gets
// past the Content-Security-Policy.
func TestTheThemeInABrowser(t *testing.T) {
	ctx := headless(t, true)
	base, b, _, _ := loggedIn(t)
	b.get(t, "/users")
	b.post(t, "/users/theme", url.Values{"theme": {"paper"}})

	if err := chromedp.Do(ctx, withSession(base, b.session), chromedp.Navigate(base+"/"), chromedp.WaitReady(chromedp.CSS("main"))); err != nil {
		t.Fatal(err)
	}
	radius, err := chromedp.Run(ctx, chromedp.Evaluate[string](`getComputedStyle(document.documentElement).getPropertyValue("--pico-border-radius").trim()`))
	if err != nil || radius != "0.125rem" {
		t.Errorf("--pico-border-radius = %q, %v; want paper's 0.125rem", radius, err)
	}
	font, err := chromedp.Run(ctx, chromedp.Evaluate[string](`getComputedStyle(document.body).fontFamily`))
	if err != nil || !strings.Contains(font, "Iowan Old Style") {
		t.Errorf("font-family = %q, %v; want paper's", font, err)
	}
}
