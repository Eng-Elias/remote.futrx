---
name: browser
description: "Drive a real web browser the user logs into - open pages, read content, search, fill forms, click, and act in the user's authenticated sessions (social media, dashboards, any site). Use when a task needs a live browser: 'go to <site>', 'search X on <site>', 'log into my <account> and do Y', 'check my messages/notifications', or automating a site that needs a real logged-in session. NOT for previewing this project's own dev server (that's the Browser drawer) and NOT for a one-off screenshot of a public URL (use scripts/browser.mjs)."
---

# Browser

You can drive this project's isolated BrowserContext in Remote's shared host
Chromium. With this skill active, the context starts automatically and you
receive `browser_*` MCP tools over a project-scoped authenticated connection.
If the user opens the Browser pane, they see the same tabs and can log in by
hand; you inherit this project's cookies without handling credentials. Other
projects have separate contexts, storage, tabs, and credentials.

This skill is for the **Live Agent Browser** only. For one-off public
screenshots, recordings, or cookie-authenticated headless recipes, use
`/workspace/scripts/browser.mjs` instead.

## How to work
Use a hybrid perception loop:

1. `browser_navigate`, then `browser_snapshot`. The snapshot is the default
   way to read structure, text, roles, and element refs.
2. Take a screenshot when the task is visual: layout verification, charts,
   maps, canvas, image content, custom widgets, anything opaque or missing
   from the snapshot, and after actions with visible consequences.
3. Act through refs when the snapshot gives reliable targets. Use
   screenshot-grounded coordinate interaction when the DOM is misleading.
4. Observe again after each action with `browser_snapshot`,
   `browser_take_screenshot`, or `browser_wait_for`. Do not assume an action
   worked.

Keep screenshot cost bounded: viewport screenshots by default, JPEG/moderate
quality when options are available, full-page screenshots only when needed.
The shared browser viewport is 1280x720.

## Pacing

Wait for navigation/load states before acting. Use `browser_wait_for` when
content is dynamic. Take one meaningful action, then observe. Avoid blind
multi-action bursts, especially on authenticated sites.

## Logging in

This browser exits via the server's datacenter IP, so strict providers may
show verification challenges. If a task needs a login or challenge response:

1. Ask the user to open the Browser pane and log in by hand.
2. Wait until they say the login is complete.
3. Continue with the `browser_*` tools against the shared session.

Never type the user's credentials yourself. There is no project-local X server
or raw CDP endpoint; use the scoped `browser_*` tools for all agent input.
Host-file upload/drop and unsafe server-side code tools are intentionally not
available. Browser file upload is currently unavailable in pooled mode; ask
the user for an API-based or other non-browser transfer path when needed.

## Write policy

Reading (search, timelines, messages) is fine on your own. Before any public
or irreversible write - posting, replying, DMing, following, buying, or
changing settings - say exactly what you're about to do and get the user's
confirmation first. They can watch and stop you in the Browser pane.
