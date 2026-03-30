# Trove

File sharing service. Upload any file, get a shareable link.

HTML renders in the browser, images display inline, code gets syntax highlighting, markdown renders beautifully, and everything else gets a download button. Upload ZIP files to host multi-page websites.

Trove exposes an [MCP](https://modelcontextprotocol.io/) endpoint so AI tools can discover and use its API programmatically.

## Quick Start

```bash
docker compose up
```

Open http://localhost:8080 — upload a file and get a link.

## Features

- Drag-and-drop or paste any file to upload
- Custom slugs (`/my-report`) or auto-generated short URLs
- HTML, markdown, code, CSV, images, video — all render in-browser
- Multi-page website hosting via ZIP upload
- `/mine` dashboard tracks your uploads and views
- MCP endpoint at `/mcp` for AI tool integration
- `/.well-known/agent.json` and `/llms.txt` for agent discovery
- Range request support for streaming large files
- OpenAPI spec at `/swagger/` and `/openapi.json`

## Configuration

Trove loads config from `trove.yaml` in the working directory:

```yaml
port: "8080"
base_url: "https://your-domain.com"

store:
  type: s3
  s3:
    bucket: "your-bucket"
    endpoint: ""         # leave empty for real AWS S3
    region: "us-west-2"
```

For local development, the default `trove.yaml` points to minio (started by docker compose).

For deployments, set the `ENVIRONMENT` env var and create a `trove-{environment}.yaml` file — e.g. `trove-production.yaml`. The app loads the environment-specific file if it exists, otherwise falls back to `trove.yaml`.

## Self-Hosting

### Docker Compose (recommended)

```bash
docker compose up -d
```

This starts trove + minio (S3-compatible storage). Files are stored in a minio volume and persist across restarts.

### Docker with real S3

```bash
docker build -t trove .

docker run -p 8080:8080 \
  -e AWS_ACCESS_KEY_ID=... \
  -e AWS_SECRET_ACCESS_KEY=... \
  -v /path/to/trove-production.yaml:/app/trove-production.yaml \
  -e ENVIRONMENT=production \
  trove
```

### Binary

```bash
go build -o trove ./cmd/server
./trove
```

Trove looks for `trove.yaml` in the current directory.

## API

```bash
# Upload a file
curl -X POST http://localhost:8080/upload -F file=@report.html -F slug=my-report

# Upload a multi-page website
zip -r site.zip my-site/
curl -X POST http://localhost:8080/upload -F file=@site.zip -F slug=my-site

# View
open http://localhost:8080/my-report

# Raw download
curl http://localhost:8080/my-report/raw

# Delete (not exposed in MCP or UI)
curl -X DELETE http://localhost:8080/delete/my-report
```

## MCP

Trove implements the [Model Context Protocol](https://modelcontextprotocol.io/) via Streamable HTTP at `/mcp`. AI tools can discover available operations and call them programmatically.

The `cmd/mcp-client` directory contains a CLI for testing the MCP endpoint:

```bash
go run ./cmd/mcp-client tools                          # list tools
go run ./cmd/mcp-client call GET_llmstxt               # call a tool
go run ./cmd/mcp-client call GET_slug_raw slug=my-file  # call with args
go run ./cmd/mcp-client run                            # full e2e flow
```

## Development

```bash
make run              # docker compose up (hot reload)
make test             # unit tests
make lint             # golangci-lint
make integration-test # e2e tests with minio
make test-all         # lint + test + integration-test
```

## License

MIT
