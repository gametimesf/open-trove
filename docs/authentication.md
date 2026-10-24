# Authentication design

Status: proposed. This document describes how Trove moves from asserted email
attribution to verified, per-user identity without breaking existing browser,
API, or MCP clients.

## Problem

Trove identifies people by an email they type into the browser or send in
`X-Trove-User-Email`. Nothing verifies it. That was a deliberate first step
(attribution, not authentication), but it now limits the product:

- Comment edit/delete ownership is a convenience check, not a boundary.
- Notifications (for example, to Slack) would go to whoever an uploader claimed
  to be.
- History is scoped to a browser cookie because emails can't be trusted to link
  sessions.
- MCP tool calls carry the email as a tool argument chosen by the agent, so an
  agent can attribute work to anyone.

## Goals

- One verified identity per request, used by every handler through the existing
  `userEmail(c)` seam.
- Pluggable credential sources so self-hosters and operators behind an identity
  proxy can each use what they already have.
- Browser, HTTP API, and MCP clients all supported.
- A shadow rollout that measures impact before anything is blocked.

## Non-goals

- Authorization roles. Verified identity comes first; per-artifact permissions
  are a separate design.
- Replacing an operator's identity proxy. Trove verifies what the proxy asserts;
  it doesn't run its own identity provider in this phase.

## Identity model

The resolver produces a principal for each request:

```go
type Principal struct {
	Email    string // normalized, lowercase
	Source   Source // proxy_jwt, token, asserted
	Verified bool   // false only for asserted
}
```

Handlers keep calling `userEmail(c)`. A new `verifiedPrincipal(c)` is available
to code that must distinguish verified identity, such as token minting.

## Credential resolver

Sources are tried in order. The first source that presents a credential decides
the outcome; a credential that is present but invalid is an error, never a
silent fallthrough to a weaker source.

| Order | Source | Credential | Verified |
|---|---|---|---|
| 1 | `token` | `Authorization: Bearer trv_...` issued by Trove | yes |
| 2 | `proxy_jwt` | Signed JWT injected by an identity-aware proxy (Cloudflare Access first) | yes |
| 3 | `asserted` | `X-Trove-User-Email` header or the browser email cookie | no |

If a verified source is present and an asserted email is also sent, the verified
email wins. The resolver logs the mismatch and handlers never see the asserted
value.

### Proxy JWT (Cloudflare Access)

Verification uses only public keys, so Trove holds no secret for this source:

- Header `Cf-Access-Jwt-Assertion` only. Plain-text identity headers such as
  `Cf-Access-Authenticated-User-Email` are ignored: anything that can reach the
  origin can set them.
- RS256 only. Signature checked against the team JWKS
  (`https://<team>/cdn-cgi/access/certs`), cached and refreshed on an unknown
  key ID.
- `iss` must equal the configured team domain, `aud` must contain the configured
  application audience tag, and `exp` must be present and unexpired (small clock
  skew allowed).
- `email` claim required and normalized.

Network allowlists are not a substitute for the audience check. Many proxies
share egress addresses across tenants; only a token signed for this
application's audience proves the request came through this application's
policy.

### Personal access tokens

For API clients, scripts, and MCP clients that can't pass through the identity
proxy's browser login:

- Minted only by a request whose principal is verified by a different source
  (`proxy_jwt` today). An asserted email can never mint a token.
- Format `trv_` + 32 random bytes, base64url. Shown once at creation.
- Stored as a SHA-256 hash, keyed by hash, with owner email, name, creation
  time, expiry (default 90 days, maximum 1 year), and throttled last-used time.
  The plaintext is never stored or logged.
- Creating, listing and revoking require the same identity-proxy session, so a
  leaked token can't mint more tokens or revoke its owner's others. Users only
  see their own tokens. Revocation takes effect on the next request.
- Storage lives behind the existing `storage.Store` contract, so S3 and the
  in-memory fake both work.

### Asserted email

Today's behavior. It stays available, and is the default, so self-hosted
deployments without an identity proxy keep working unchanged. In `enforce`
mode an asserted email never satisfies a request in scope, so sending only the
header can't bypass the verified sources. It still attributes out-of-scope
reads for logs and history.

## Modes

```yaml
auth:
  mode: off          # off | shadow | enforce
  enforce_on: writes # only "writes" for now; see below
  cloudflare_access:
    team_domain: "example.cloudflareaccess.com"
    audience: "<application audience tag>"
  tokens:
    enabled: false
```

| Mode | Resolution | Blocking |
|---|---|---|
| `off` | Asserted email only. The current behavior and the default. | Writes without an email return 400, as today. |
| `shadow` | All configured sources are evaluated and logged. Handlers receive the asserted email when one is sent, and the verified email only when none is, so token-only clients can write. | Nothing new is blocked. |
| `enforce` | Handlers receive the verified principal; out-of-scope requests without one keep asserted attribution. | Requests in `enforce_on` scope without a verified principal return 401. An invalid credential always returns 401. A credential that can't be checked because a dependency failed (for example the token store) returns 503. |

Startup fails if `enforce` is set without at least one verified source
configured, or if `cloudflare_access` is configured without both fields.

`enforce_on: all` is not accepted yet. It would also gate `/healthz`, icons and
other public routes, so it needs an explicit public-route list first.

### Shadow logging

One structured line per request, with no credential material:

```
INFO  request identity method=POST path="/upload" source=proxy_jwt verified=true email="a@example.com" asserted_email="a@example.com" mismatch=false
```

Exit criteria for enforcement: every client type (browser, HTTP API, MCP) is
seen with a verified source, and mismatches are explained.

## Browser

When a request already has a verified principal, the page renders that email and
skips the email prompt and the change-email control. Without one, the browser
behaves as it does today.

The token UI lives on My Trove: create (name and expiry), list, and revoke.

## MCP

MCP tool calls run as in-process requests created by `echo-mcp`. The library
builds each request with a fresh context and copies only declared tool
arguments, so neither a Bearer token nor a proxy JWT on the outer `/mcp`
request reaches the handler. The email is a tool argument today.

Plan:

1. Contribute an `echo-mcp` change that runs tool requests with the incoming
   request's context and forwards a configured header allowlist
   (`Authorization`, `Cf-Access-Jwt-Assertion`). Proposed upstream as
   BrunoKrugel/echo-mcp#15; adopt the release that includes it.
2. Forward only those headers. Tool arguments named like identity headers are
   ignored in `enforce` mode.

Until step 1 ships, MCP stays on asserted identity: `shadow` is safe, `enforce`
must not be enabled for deployments that rely on MCP.

Later: MCP's OAuth authorization flow (browser callback, PKCE) can replace
copy-pasted tokens. It reuses the token store and adds an authorization
endpoint; it is out of scope for this phase.

## Observability

- Identity log line above, plus a single `auth denied` line per 401 with reason
  (`missing`, `invalid_signature`, `audience`, `expired`, `revoked_token`,
  `unknown_token`).
- Token create and revoke events logged with token ID (hash prefix) and owner.

## Rollout

1. Resolver, Cloudflare Access verification, and modes (`off` default).
2. Personal access tokens.
3. `echo-mcp` context and header forwarding.
4. Operator enables `shadow`, observes, then `enforce` with `enforce_on: writes`.
5. Revisit `enforce_on: all` and per-artifact authorization separately.

## Preparing for sharing and access control

Per-artifact access control (share with a person or a group, private
artifacts) is the next design, not this one. This phase keeps it possible:

- **Groups.** Identity proxies can put group membership in the signed token
  (for example an Okta groups claim forwarded by Cloudflare Access). When
  sharing lands, `Principal` gains `Groups []string`, read from a configured
  claim, so membership changes apply without redeploying Trove. Open question:
  token-authenticated requests carry no proxy claim, so groups for token
  callers need another source (a directory lookup, or groups captured at mint
  time and refreshed).
- **Trustworthy ownership.** Artifacts already record `owner_email`. Once
  verification is enforced, new uploads should also record how the owner was
  identified (`owner_source`), so access rules can treat legacy, asserted
  ownership differently from verified ownership.
- **One decision point.** Handlers already read identity through
  `userEmail(c)` and `verifiedPrincipal(c)`. Authorization should be a service
  that takes a principal and an artifact and returns allow or deny, called from
  the same places, rather than checks spread across handlers.
- **Default visibility.** Existing artifacts are visible to everyone who can
  reach the deployment. That remains the default; private and shared are
  opt-in per artifact, so no migration changes who can see existing files.

## Alternatives considered

- **Email magic links in Trove.** Works without an identity provider but needs
  outbound mail, login state, and abuse controls. Reasonable later as another
  verified source for self-hosters; not needed where an identity proxy exists.
- **Trusting plain identity headers from a proxy.** Simpler, but only safe when
  the network guarantees the proxy is the sole path to the origin and strips
  client-supplied copies. Signed assertions don't depend on that.
- **Direct OIDC login in Trove.** Viable for deployments without a proxy; more
  surface (sessions, callback, client secrets) than verifying the proxy's JWT.
