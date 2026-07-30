# Contributing to Trove

Thanks for your interest in contributing!

## Getting Started

```bash
git clone https://github.com/gametimesf/open-trove.git
cd open-trove
docker compose up
```

Open http://localhost:8080 to verify it's running.

## Development

```bash
make test             # unit tests
make lint             # golangci-lint
make integration-test # e2e tests with minio
make test-all         # all of the above
```

All tests run in Docker — no local Go installation required.

If you prefer running Go locally:

```bash
make local-test       # go test ./...
make local-run        # go run ./cmd/server
```

## Pull Requests

- Keep PRs small and focused
- Include tests for new functionality
- Run `make test-all` before submitting
- Use conventional commit messages (`feat:`, `fix:`, `chore:`, etc.)
- Preserve the documented S3 layout unless the change includes a migration
- Keep deployment-specific hosts, credentials, routing, and policy out of this repository

Security-sensitive reports belong in a private GitHub Security Advisory, not a
public issue. See [SECURITY.md](SECURITY.md).

## Adding a Storage Backend

Trove uses a `storage.Store` interface. To add a new backend (e.g. GCS, Azure Blob, local disk):

1. Create `storage/yourbackend.go` implementing the `Store` interface
2. Add a case in `storage.NewStore()` for your backend's type name
3. Add config fields to `internal/config/config.go` if needed
4. Add tests — see `storage/fake/store.go` for the pattern

Prefer caller-oriented capabilities over expanding a broad storage abstraction.
Keep vendor SDK models inside the adapter.

## Reporting Issues

Open an issue on GitHub with:
- What you expected
- What happened
- Steps to reproduce
- The released version, commit, or image digest
