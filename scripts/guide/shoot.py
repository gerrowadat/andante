# SPDX-License-Identifier: Apache-2.0
# Copyright (C) 2026 Dave O'Connor
#
# Walks through andante as a new user would, taking the user guide's
# screenshots. Run by scripts/guide/screenshots.sh, inside its Docker network.

import os
import re
import sys

from playwright.sync_api import expect, sync_playwright

ANDANTE = "https://andante.example"
READER = "http://reader.example"
OUT = sys.argv[1]
PASSWORD = "gannets and tide tables"
expect.set_options(timeout=120_000)


def shot(page, name, full=False):
    page.wait_for_load_state("load")
    page.wait_for_timeout(500)
    page.screenshot(path=f"{OUT}/{name}.png", full_page=full)
    print(name)


def browser_context(browser):
    ctx = browser.new_context(viewport={"width": 1440, "height": 900}, color_scheme="light",
                              ignore_https_errors=True)
    ctx.set_default_timeout(120_000)
    return ctx


def admin(browser):
    """Makes the first admin, adds the podcast, and invites Sam."""
    page = browser_context(browser).new_page()
    page.goto(f"{ANDANTE}/setup")
    page.fill("input[name=token]", os.environ["BOOTSTRAP_TOKEN"])
    page.fill("input[name=name]", "dave")
    page.click("button")
    page.fill("input[name=password]", PASSWORD)
    page.click("button")

    page.goto(f"{ANDANTE}/add")
    page.fill("#url", "http://tidetables.example/")
    page.click("text=Look")
    page.click("text=Add this")
    page.wait_for_url("**/track")
    page.goto(page.url.removesuffix("/track"))
    expect(page.locator("#progress")).to_contain_text("12 so far")

    page.goto(f"{ANDANTE}/invites")
    page.fill("input[name=name]", "sam")
    page.click("text=Make a link")
    url = page.locator(".note .url").inner_text()
    page.context.close()
    return url


def sam(browser, invite):
    ctx = browser_context(browser)
    page = ctx.new_page()

    page.goto(invite)
    page.fill("input[name=password]", PASSWORD)
    shot(page, "invite")
    page.click("button")
    shot(page, "home-empty")

    login = browser_context(ctx.browser).new_page()
    login.goto(f"{ANDANTE}/login")
    login.fill("input[name=name]", "sam")
    shot(login, "login")
    login.close()

    page.goto(f"{ANDANTE}/add")
    page.fill("#url", "http://gannetpoint.example/")
    shot(page, "add")
    page.click("text=Look")
    page.wait_for_selector("text=Add this")
    shot(page, "found", full=True)
    page.click("text=Add this")
    page.wait_for_url("**/track")
    page.fill("input[name=name]", "Before work")
    page.check("input[value=weekdays]")
    page.fill("input[name=at]", "07:30")
    shot(page, "track", full=True)
    page.click("text=Start reading")
    comic = page.url

    page.get_by_role("link", name="Gannet Point").click()
    expect(page.locator("#progress")).to_contain_text("24 so far")
    shot(page, "source", full=True)
    page.goto(f"{ANDANTE}/sources")
    shot(page, "browse")
    page.goto(comic)

    page.fill("#seq", "5")
    page.click("text=Jump")
    expect(page.locator("#summary")).not_to_contain_text("1 of 24")
    page.fill("#link-for", "Alex")
    page.click("button:text-is('Add')")
    expect(page.locator("table").first).to_contain_text("Alex")
    shot(page, "read", full=True)
    mine = page.locator("tr", has_text="me").locator("a.url").get_attribute("href")
    alex = page.locator("tr", has_text="Alex").locator("a.url").get_attribute("href")

    page.goto(f"{ANDANTE}/sources")
    page.get_by_role("link", name="Tide Tables").click()
    page.click("text=Track this")
    page.fill("input[name=name]", "Tide Tables, fast")
    page.check("input[value=replay]")
    page.fill("input[name=speed]", "14")
    page.fill("input[name=max_gap]", "2")
    shot(page, "track-replay", full=True)
    page.click("text=Start reading")
    page.click("text=Release the next one now")
    expect(page.locator("#summary")).to_contain_text("1 of")

    page.goto(comic)
    page.click("text=Change the pace")
    shot(page, "repace", full=True)

    page.goto(f"{ANDANTE}/")
    shot(page, "home")

    page.goto(alex)
    shot(page, "feed-page", full=True)
    page.goto(mine)
    atom = page.get_by_role("link", name="Atom").get_attribute("href")
    ctx.close()
    return atom


def reader(browser, feed):
    page = browser_context(browser).new_page()
    page.goto(f"{READER}/")
    page.fill("input[name=username]", "sam")
    page.fill("input[name=password]", "guide-password")
    page.click("button")
    page.goto(f"{READER}/subscribe")
    page.fill("input[name=url]", feed)
    shot(page, "reader-subscribe")
    # Only because these screenshots use a self-signed certificate.
    page.eval_on_selector("input[name=allow_self_signed_certificates]", "e => e.checked = true")
    page.click("button:has-text('Find a feed')")
    if page.locator("text=Choose a Subscription").count():
        page.locator("button").first.click()
    page.wait_for_url(re.compile(r"/feed/\d+/entries"))
    shot(page, "reader-entries")
    page.locator("article.item .item-title a").first.click()
    shot(page, "reader-entry", full=True)


with sync_playwright() as p:
    b = p.chromium.launch()
    try:
        feed = sam(b, admin(b))
        reader(b, feed)
    except Exception:
        for i, pg in enumerate(pg for c in b.contexts for pg in c.pages):
            pg.screenshot(path=f"{OUT}/failed-{i}.png", full_page=True)
            print(f"failed-{i}: {pg.url}")
        raise
    b.close()
