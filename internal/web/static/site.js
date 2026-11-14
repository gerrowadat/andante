// SPDX-License-Identifier: Apache-2.0
// Copyright (C) 2026 Dave O'Connor

// What htmx doesn't do. Every page works without this, and without htmx: it shows
// the copy buttons, and says so when a request htmx made fails, since otherwise the
// button someone pressed would seem to do nothing.
"use strict";

// say puts a message in the page's status area, which screen readers announce.
function say(text) {
  const status = document.getElementById("status");
  if (!status) {
    return;
  }
  const p = document.createElement("p");
  p.className = "alert";
  p.setAttribute("role", "alert");
  p.textContent = text;
  status.replaceChildren(p);
}

// The clipboard is only there on https and localhost, and a button that can't copy
// stays hidden.
function showCopyButtons() {
  if (!navigator.clipboard) {
    return;
  }
  for (const button of document.querySelectorAll("button[data-copy]")) {
    button.hidden = false;
  }
}

document.addEventListener("DOMContentLoaded", showCopyButtons);
document.addEventListener("htmx:after:swap", showCopyButtons);

document.addEventListener("click", async (event) => {
  const button = event.target.closest("button[data-copy]");
  if (!button) {
    return;
  }
  try {
    await navigator.clipboard.writeText(button.dataset.copy);
    button.textContent = "Copied";
  } catch {
    say("That couldn't be copied. Select the link and copy it instead.");
  }
});

// A page that refuses something renders itself again with the reason, and htmx
// swaps that in like any other page. Anything else that fails is a line of plain
// text, which goes in the status area and not in place of the page.
document.addEventListener("htmx:after:request", (event) => {
  const ctx = event.detail.ctx;
  const type = ctx.response.headers.get("Content-Type") || "";
  if (ctx.response.status >= 400 && !type.startsWith("text/html")) {
    ctx.swap = "none";
    say(ctx.text.trim() || "That didn't work.");
  }
});

document.addEventListener("htmx:error", () => {
  say("That didn't work: the server couldn't be reached.");
});
