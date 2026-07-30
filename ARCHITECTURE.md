# Architecture

Trove is a single Go service with explicit boundaries:

- `cmd/server`: HTTP, browser UI, MCP registration, and application wiring
- `comments`: comment/thread rules independent of HTTP and S3
- `intake`: optional upload inspection and provider adapters
- `storage`: persistence contracts, S3 implementation, and shared records
- `internal/config`: strict deployment configuration
- `docs`: embedded agent documentation and generated API specifications

The application depends on the `storage.Store` contract. S3-specific SDK types
do not cross into HTTP or comment logic. The intake boundary similarly exposes
an `Inspector`; disabled deployments receive a no-op implementation.

## Request flow

1. Identity middleware normalizes the asserted email and requires it for writes.
2. Upload handlers bound request size and ZIP expansion before persistence.
3. The configured intake inspector accepts, flags, or rejects the content.
4. The storage adapter writes the artifact/site and attribution record.
5. Viewers render type-specific content and load the comments client.

## Deployment boundary

This repository owns the portable application and its OCI image. A deployment
repository should own cloud infrastructure, authentication policy, secrets,
environment YAML, and promotion of an immutable image digest. Deployment-only
behavior belongs in configuration, not a private application fork.

## Compatibility

The S3 key layout is part of the deployment contract and is documented in the
README. Additive schema changes must continue reading older objects. Breaking
storage changes require an explicit migration and release note.
