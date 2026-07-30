# Trove

Self-hosted file sharing for humans and agents. Upload a file, receive a short
URL, and review it in the browser.

Trove renders HTML, Markdown, code, CSV, images, video, and DOCX files; hosts
multi-page sites from ZIP archives; exposes HTTP, OpenAPI, and MCP interfaces;
and keeps persistent comment threads anchored to files, text, and HTML
elements.

## Quick start

```bash
docker compose up
```

Open <http://localhost:8080>. MinIO stores data in a Docker volume, so uploads
survive container restarts.

## Features

- Drag-and-drop, paste, HTTP, and agent uploads
- Custom or generated slugs, overwrite support, and range downloads
- Multi-page site hosting from ZIP files
- Browser and API audit attribution through `X-Trove-User-Email`
- Whole-file comments on every artifact type
- Element and text comments on rendered HTML, including existing artifacts
- Threaded replies, edit/delete, collapse, resolve/reopen, and resolved history
- Optional LLM-backed upload intake with bounded, observable failure behavior
- S3 and S3-compatible storage, including MinIO
- MCP at `/mcp`, OpenAPI at `/openapi.json`, and agent guidance at `/llms.txt`

## Identity and access

Trove's built-in identity is intentionally **attribution, not authentication**.
The browser asks for an email once, stores it locally, and sends
`X-Trove-User-Email`. API and agent clients must send the same header on every
request. It is required for `POST`, `PUT`, `PATCH`, and `DELETE`.

The server validates the address format but does not verify ownership. Put
Trove behind your own authentication and authorization layer when access
control matters.

```bash
export TROVE_BASE_URL=http://localhost:8080
export TROVE_USER_EMAIL=you@example.com

curl -X POST "$TROVE_BASE_URL/upload" \
  -H "X-Trove-User-Email: $TROVE_USER_EMAIL" \
  -F file=@report.html \
  -F slug=my-report
```

## Comments

Every viewer has a Comments mode. Opening the drawer resizes the artifact
instead of covering it. Users can comment on the whole file, select a rendered
element, or highlight text. Threads support replies, editing, deletion,
collapse, resolution, reopening, and resolved-history filtering.

No special markup is required. For durable anchors across regenerated HTML,
add unique, stable component identifiers:

```html
<section
  data-trove-id="revenue-chart"
  data-trove-label="Quarterly revenue chart">
  ...
</section>
```

Use `data-trove-comment-ignore` for transient controls that should not be
selectable. See [`docs/llms.txt`](docs/llms.txt) for the complete authoring and
comments API contract.

## Configuration

Resolution order, from lowest to highest precedence:

1. Built-in defaults
2. `trove-$ENVIRONMENT.yaml`, when present
3. `trove.yaml`, as the fallback file
4. The explicit file named by `TROVE_CONFIG`
5. Inline YAML in `TROVE_CONFIG_YAML`
6. `TROVE_PORT` and `TROVE_BASE_URL`

Unknown YAML fields and invalid URLs fail startup instead of silently falling
back.

```yaml
port: "8080"
base_url: "https://trove.example.com"

store:
  type: s3
  s3:
    bucket: "trove-uploads"
    endpoint: "" # empty for AWS S3
    region: "us-west-2"

uploads:
  max_bytes: 209715200
  max_site_files: 2000
  max_site_bytes: 209715200
  max_site_file_bytes: 104857600

# Optional: publish selected slugs through another route.
share_url_rules:
  - slug_prefix: "partner-"
    base_url: "https://proxy.example.com"
    path_prefix: "/shared/trove"

content_review:
  contact_name: "the security team"
  contact_email: "security@example.com"

intake:
  enabled: false
  provider: "anthropic"
  model: "claude-sonnet-4-6"
  fail_mode: "closed"
  max_check_bytes: 204800
  timeout_ms: 15000
```

When Anthropic intake is enabled, provide `ANTHROPIC_API_KEY` through your
secret manager. Guidance can come from `prompt_inline`, `prompt_path`, or the
S3 `prompt_source_bucket` and `prompt_source_key` fields. Do not commit secrets
or private review guidance.

For orchestrators that cannot mount files, pass the deployment YAML through
`TROVE_CONFIG_YAML`.

## Self-hosting

### Published image

Images are published for `linux/amd64` and `linux/arm64`:

```bash
docker pull ghcr.io/gametimesf/open-trove:latest
docker run --rm -p 8080:8080 \
  -e TROVE_CONFIG_YAML="$(cat trove-production.yaml)" \
  -e AWS_REGION=us-west-2 \
  ghcr.io/gametimesf/open-trove:latest
```

Production deployments should pin the immutable image digest rather than
`latest`.

### Build locally

```bash
docker build -t open-trove .
go build -o trove ./cmd/server
```

## API and MCP

```bash
# Upload a multi-page website
zip -r site.zip my-site/
curl -X POST "$TROVE_BASE_URL/upload" \
  -H "X-Trove-User-Email: $TROVE_USER_EMAIL" \
  -F file=@site.zip \
  -F slug=my-site

# View or download
open "$TROVE_BASE_URL/my-site"
curl "$TROVE_BASE_URL/my-report/raw" \
  -H "X-Trove-User-Email: $TROVE_USER_EMAIL"
```

The MCP endpoint uses Streamable HTTP at `/mcp`. A test client is included:

```bash
go run ./cmd/mcp-client -url "$TROVE_BASE_URL" -email "$TROVE_USER_EMAIL" tools
go run ./cmd/mcp-client -url "$TROVE_BASE_URL" -email "$TROVE_USER_EMAIL" run
```

Discovery surfaces:

- `GET /llms.txt`
- `GET /.well-known/agent.json`
- `GET /openapi.json`
- `GET /swagger/`

## Storage compatibility

Trove keeps the established S3 object layout:

- single-file artifacts at `<slug>`
- site manifests at `_sites/<slug>.json`
- site files at `<slug>/<path>`
- browser activity at `_users/<id>.json`
- comments at `_comments/<slug>/<id>.json`

Upgrading from an existing Trove deployment does not require a data migration.

## Security model

Uploaded HTML is active, user-controlled content. The current same-origin
viewer enables scripts and same-origin access so element/text comments work.
This is appropriate for trusted-user deployments behind an access-control
boundary; it is **not** a safe anonymous Internet upload service.

Read [`SECURITY.md`](SECURITY.md) before exposing a deployment. At minimum,
require authentication in front of Trove, restrict writers, use a dedicated
origin, and set conservative upload limits.

## Development

```bash
make test
make lint
make integration-test
make test-all
```

See [`CONTRIBUTING.md`](CONTRIBUTING.md) and
[`ARCHITECTURE.md`](ARCHITECTURE.md).

## License

[MIT](LICENSE)
