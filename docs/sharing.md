# Sharing and access control

Status: proposed. This builds on [authentication.md](authentication.md) and
follows its "Preparing for sharing and access control" section.

## Problem

Anyone who can reach a Trove deployment can open every artifact on it. That is
fine for most uploads, but people also publish things meant for a few
colleagues: a draft plan, a review, a doc with numbers that shouldn't travel.
Today the only options are "everyone" or "don't upload it".

People mostly share Trove links in Slack, so the place to help them share with
the right people is the moment they post a link there.

## Goals

- An artifact can be **restricted** to its owner and a list of people.
- Anyone who can open a restricted artifact can share it with more people.
- When a restricted link is posted in Slack, Trove offers the poster a one-click
  "share with the people here".
- Every way of reading an artifact (browser, raw, site files, API, MCP, Slack
  previews, notifications) applies the same check.
- Nothing changes for existing artifacts or for deployments that haven't
  enabled verified identity.

## Non-goals

- Group grants (identity-provider groups, Slack channels as live groups). The
  model leaves room for them; see [Later](#later).
- Roles beyond owner and member.
- Hiding that a slug exists from people who can reach the deployment. A
  restricted page says "you don't have access" so people can ask for it.

## The gate

Sharing is available exactly when `auth.mode` is `enforce`. There is no
separate flag.

Restrictions are only meaningful when the viewer is proven, and an asserted
email can claim to be anyone. So:

- In `off` and `shadow`, the access API routes are not registered (so they are
  not MCP tools either), the share controls are hidden, and an upload that asks
  for `visibility=restricted` gets 400 with a message saying sharing requires
  verified identity.
- If an operator moves from `enforce` back to `shadow` or `off` after artifacts
  were restricted, those artifacts **fail closed**: nobody can read them until
  enforcement returns. They never fall back to "everyone". Each denial logs
  `reason=auth_not_enforced`.
- In `enforce` with `enforce_on: writes`, reads of everyone-visible artifacts
  stay anonymous as today. Reads of a restricted artifact need a verified
  principal (`verifiedPrincipal(c)`), never the asserted email.

## Model

| Visibility | Who can open, comment, overwrite and share | Who can manage |
|---|---|---|
| `everyone` (default) | Anyone who can reach the deployment (today's behavior) | Owner |
| `restricted` | Owner and members | Owner |

**Manage** means: change visibility, remove members, transfer ownership and
delete. Sharing is open to every member, but undoing a restriction or removing
someone stays with the owner, otherwise any member could undo it.

Restricted access works like a document set to "anyone with access can
share": it keeps an artifact from being open to everyone, but it does not pin it
to a fixed group. Every grant records who added whom, and the artifact page
shows that list.

### Ownership

- New uploads in `enforce` have a verified owner.
- An existing artifact's `owner_email` is asserted. A verified principal whose
  email matches it can restrict the artifact. The risk is the same as today's
  unauthenticated overwrite: someone who previously overwrote an artifact under
  their own email could restrict it. Restricting is logged, and the original
  author can ask an operator to transfer it.
- Overwriting a restricted artifact keeps its policy and owner. Upload
  attribution (`FileMetadata.OwnerEmail`) still records who uploaded that
  version.

### Storage

The policy lives in its own object, `_access/<slug>.json`, next to the existing
`_comments/` and `_previews/` prefixes:

```json
{
  "visibility": "restricted",
  "owner": "ana@example.com",
  "owner_source": "proxy_jwt",
  "members": [
    {"email": "ben@example.com", "added_by": "ana@example.com", "added_at": "2026-11-06T18:00:00Z", "via": "web"},
    {"email": "cy@example.com", "added_by": "ben@example.com", "added_at": "2026-11-06T18:05:00Z", "via": "slack"}
  ]
}
```

- No object means `everyone`, so there is no migration.
- Kept apart from file metadata so an overwrite can't drop it, and so a
  multi-file site has one policy.
- Writes are conditional (S3 `If-Match` on the ETag) so concurrent grants don't
  lose each other. A conflict is retried by re-reading and re-applying.
- Behind a new `access.Repository` in the `storage.Store` contract, with the
  in-memory fake for tests.
- Deleting an artifact deletes its policy.

## One decision point

A new `access` package holds the rules and knows nothing about HTTP, Slack or
S3:

```go
type Action string // view, comment, write, share, manage

func Decide(p auth.Principal, policy Policy, action Action, enforced bool) Decision
```

The server calls one helper, `s.authorize(c, slug, action)`, at every route that
names an artifact:

| Route | Action |
|---|---|
| `GET /:slug`, `/:slug/raw`, `/:slug/*` | view |
| `GET /api/artifacts/:slug` | view |
| `GET /api/artifacts/:slug/comments` | view |
| comment create, reply, edit, delete, resolve | comment |
| preview card get | view |
| preview card put, delete | write |
| `POST /upload` with overwrite onto an existing slug | write |
| `DELETE /delete/:slug` | manage |
| access routes below | view, share or manage |

MCP tools are these same routes run in-process with forwarded identity headers
(see `cmd/server/mcp.go`), so they get the same checks without extra code.

A test walks the registered Echo routes and fails if any route with a `:slug`
parameter is missing from the table. New routes can't skip the check by
accident.

Other read paths:

- **History (`/mine`, index).** Records for artifacts the viewer can no longer
  open are dropped when rendering, so titles don't leak.
- **Comment notifications.** Recipients who no longer have access are skipped.
- **Slack previews.** See below.

Responses:

- API, raw and site requests without access get 404.
- The viewer page for a verified person without access renders "You don't have
  access to this artifact" with a **Request access** button. It shows no title,
  owner or content.
- Restricted responses send `Cache-Control: private, no-store`.

## API

Registered only when the gate is on. Request and response shapes follow the
existing JSON error style.

| Method and path | Action | Notes |
|---|---|---|
| `GET /api/artifacts/:slug/access` | view | Visibility, owner, members with who added them, and the caller's `can_manage` |
| `POST /api/artifacts/:slug/access/members` | share | `{"emails": [...]}`. Idempotent; returns the new member list |
| `DELETE /api/artifacts/:slug/access/members/:email` | manage | Members may also remove themselves |
| `PUT /api/artifacts/:slug/access/visibility` | manage | `{"visibility": "restricted" or "everyone"}` |
| `POST /api/artifacts/:slug/access/requests` | verified, no access needed | Notifies the owner and the people who shared it; see below |

Upload gains two optional fields: `visibility` and `share_with` (comma-separated
emails). Both show up on the MCP upload tool automatically. `llms.txt` gets a
short section telling agents when to restrict and how to share.

Optional config:

```yaml
sharing:
  allowed_domains: ["example.com"]  # emails outside these can't be added
  slack_max_grant: 50               # largest channel the Slack button will share with
```

Without `allowed_domains`, any normalized email can be added.

## Browser

- **Share dialog** on the viewer for anyone with access: visibility (owner
  only), a members list with who added each person, and an "add people" field.
- **Upload page** gets a "Restricted" toggle and a "share with" field.
- **No-access page** with Request access, as above.

## Slack

This extends the existing link-preview flow in `slackapp/` (audience check,
the poster-only prompt in `consent.go`, the guest directory in `guests.go`). The
user list the guest directory already fetches includes emails
(`users:read.email` is already in the app manifest), so mapping channel
members to emails needs no new scopes or calls.

When a link to a **restricted** artifact is posted:

1. **Who posted it.** Look up the poster's email. Slack delivers the event over
   the app's authenticated Socket Mode connection, so this is treated as a
   verified identity for this action. If the poster's Slack email isn't on the
   artifact, post no preview. Show them a prompt: "You don't have access to
   this Trove artifact" with **Request access**.
2. **Slack Connect channels.** Never preview and never grant, as today.
3. **Who's here.** List the channel's members, then drop bots, deleted users,
   guests and emails outside `allowed_domains`. Anyone left without access is
   "missing".
4. **Nobody missing.** Preview using today's audience rules (direct preview for
   members only, poster prompt when guests are present).
5. **Some missing.** Show the poster a prompt that only they can see, using the
   same unfurl-auth mechanism as the current consent prompt:
   > 6 people here can't open *Q3 pricing review*.
   > **[Share with them]** [Don't share]
6. **On click.** Re-check that the clicker is the poster and still has access.
   Recompute the members, since the channel may have changed. Add them with
   `via: "slack"` and `added_by` set to the poster, then post the preview.
7. **Large channels.** Above `slack_max_grant` there is no bulk button. The
   prompt points to the share dialog instead. The owner can switch the artifact
   to `everyone` there.
8. **DMs the bot can't see into.** There's no member list, so there's no bulk
   share. The prompt says to share from the artifact page.

**Request access**, from the no-access page or the Slack prompt, sends a Slack
DM through the existing notifier to the owner and to the person who shared the
link, if known. The DM has an **Approve** button. Approving re-checks that the
approver still has access, then adds the requester with `via: "request"`.

Slack identity depends on Slack emails matching identity-provider emails. When
they don't match, the poster or approver is treated as having no access and the
log says why.

## Observability

One line per decision that matters, with no artifact content:

```
INFO  access denied slug=q3-pricing action=view email="dee@example.com" reason=not_member
INFO  access shared slug=q3-pricing by="ben@example.com" added=6 via=slack
INFO  access visibility slug=q3-pricing by="ana@example.com" from=everyone to=restricted
INFO  access request slug=q3-pricing from="dee@example.com" notified=2
```

Operators can count restricted artifacts, shares by `via`, denials by reason and
request-to-approval time from these lines.

## Testing

- `access.Decide`: table tests over visibility, owner, member, non-member,
  unverified and gate-off for each action.
- Route coverage test described above.
- Handler tests for allow and deny on every route in the table, including MCP
  tool calls with forwarded headers and asserted-only callers.
- Storage: conditional writes, conflict retry, delete cleanup, fake parity.
- Slack: the cases in the flow above using the existing fake API, including
  guests, Slack Connect, oversized channels, non-poster clicks and membership
  changes between prompt and click.
- Gate: routes absent and restricted uploads rejected in `off` and `shadow`;
  existing restricted artifacts fail closed after a downgrade.

## Rollout

Each step is its own PR, and each is inert until the gate is on and someone
restricts an artifact.

1. `access` package, `access.Repository` and S3 storage, `s.authorize` on every
   route, fail-closed behavior and the route coverage test.
2. Access API, upload `visibility`/`share_with`, MCP exposure and `llms.txt`
   guidance.
3. Browser: share dialog, upload toggle, no-access page and request access.
4. Slack: restricted-aware previews, the share prompt and request access by
   DM.

Operators reach the gate through the existing authentication rollout: shadow,
then `enforce` with `enforce_on: writes`. Rolling back to `shadow` or `off`
keeps restricted artifacts closed. Removing their `_access/` objects returns
them to `everyone`.

## Later

- **Groups.** Add `Principal.Groups` from a configured identity-proxy claim and
  allow group members in a policy. This is the same open question as in
  authentication.md for token callers.
- **Slack channel as a live grant.** Checks channel membership on read, with
  caching. Add it only if one-time grants prove too manual.
- **Per-artifact "members can't reshare"** for the rare artifact that has to
  stay with a fixed group.
- **Exports and search indexing** (for example syncing to a knowledge tool):
  restricted artifacts are excluded unless a future policy says otherwise.

## Alternatives considered

- **Owner-only sharing.** Tighter, but it turns every "can you send me that"
  into a wait on one person, and people would work around it by switching to
  `everyone`.
- **Unguessable links instead of policies.** No identity needed, but links
  leak through forwarding and Slack, there is no revocation short of
  re-uploading, and nobody can see who has access.
- **Policy in object metadata.** Fewer objects, but every overwrite would have
  to carry it forward, and S3 can't change an object's metadata without copying
  the whole object, so every grant would rewrite the artifact.
