---
name: trove
description: Share files and pages through Trove. Upload HTML, Markdown, images, code, or a zipped site to get a shareable link, update it later, and read or reply to its comments. Use when the user wants to share, publish, or host something on Trove, get a Trove link for a file, or mentions a Trove URL or its comments.
---

# Trove

Trove is a self-hosted file-sharing server: upload a file, get a link people
can view and comment on. Each deployment documents itself; follow its guide.

## 1. Find the server

Use the first of these that exists, and never guess:

1. `$TROVE_BASE_URL` (read settings with `printenv TROVE_BASE_URL TROVE_USER_EMAIL`)
2. A Trove link the user gave you: the base is its scheme and host
   (`https://trove.example.com/my-report` → `https://trove.example.com`)
3. The project's agent instructions or config (`AGENTS.md`, `CLAUDE.md`, `.env`)
4. Ask the user for their Trove URL, and suggest they set `TROVE_BASE_URL`

Confirm it is Trove: `curl -fsS "$BASE/llms.txt"` starts with `# Trove`.

## 2. Read its guide

Read `$BASE/llms.txt` once per session before your first request. It is the
API contract for that server (uploads, sites, safe updates, link previews,
comments, MCP) and wins over anything here.

## 3. Send these headers on every request

- `X-Trove-Client: trove-plugin/0.1.0`
- `X-Trove-User-Email: <the user's email>`: from `$TROVE_USER_EMAIL`, else
  `git config user.email`; use it without asking and say which one you used.
  Ask only when neither exists. Required for writes; Trove uses it for
  attribution, not access.
- `Authorization: Bearer $TROVE_TOKEN`, when that is set.

If a request is redirected to a login page or returns 401, the server sits
behind an identity proxy. Ask the user to connect the way they normally reach
it (for example a VPN), or to create a personal access token on the server's
My Trove page and set `TROVE_TOKEN`.

## 4. Share

```bash
curl -fsS -X POST "$BASE/upload" \
  -H "X-Trove-Client: trove-plugin/0.1.0" -H "X-Trove-User-Email: $EMAIL" \
  -F file=@report.html -F slug=my-report
```

Give the user the returned `url`. Act on any `hints` (title, summary, preview)
and re-upload before sharing. To change an existing artifact, follow the
guide's safe-update steps (`if_match` with the last `version`) instead of a
blind overwrite.
