# Security policy

## Reporting a vulnerability

Please report vulnerabilities privately through GitHub Security Advisories for
this repository. Do not include secrets, customer data, or exploit details in a
public issue.

## Deployment assumptions

Trove is designed for self-hosted, trusted-user environments.

- `X-Trove-User-Email` is asserted audit metadata, not authentication.
- The application does not implement authorization.
- Anyone who can reach a write route can choose an email and mutate content.
- Uploaded HTML and site ZIPs contain active, user-controlled code.
- The viewer currently needs same-origin iframe access for element and text
  comments. A page that allows scripts and same-origin access is not an
  isolation boundary against a malicious uploader.

Do not expose anonymous uploads on the public Internet. Put Trove behind an
authentication/authorization proxy, restrict writers, and use a dedicated
origin with no sensitive cookies. A deployment that accepts untrusted writers
should serve artifacts from a separate content origin and use a constrained
`postMessage` bridge instead of parent DOM access.

## Operator checklist

- Pin released container images by digest.
- Keep the object bucket private and enable encryption and versioning.
- Inject provider keys through a secret manager, never configuration files.
- Set upload, expanded ZIP, file-count, and per-entry limits for your runtime.
- Apply request timeouts and body limits at the ingress as well as the app.
- Monitor rejected writes, intake dependency failures, and storage errors.
- Back up data before changing the documented S3 layout.
- Review third-party browser CDN dependencies or self-host them for high-trust
  deployments.

## Built-in safeguards

Trove validates slugs and comment resources, bounds upload and ZIP expansion,
rejects unsafe ZIP paths, uses a sandboxed viewer iframe, requires attribution
on writes, and keeps intake failure behavior explicit. These controls reduce
risk but do not replace an access-control boundary.

## Supported versions

Security fixes are provided for the latest released version. Until the first
tagged release, use the latest commit on `main` and pin its image digest.
