# Security Policy

## Supported versions

The latest release is the supported one. Fixes land on `main` and go out in the
next tag.

## Reporting a vulnerability

Please do **not** open a public issue for a security problem.

Use GitHub's private vulnerability reporting instead:
[Security → Report a vulnerability](https://github.com/iamnikolie/gengoya/security/advisories/new).
That opens a private advisory visible only to the maintainers.

Include what you did, what happened, and the impact you think it has. Expect a
first response within a week — this is a spare-time project, not a product with
an on-call rotation.

## Scope notes

Some things are known and by design rather than vulnerabilities:

- **API keys are stored in plain text** in `~/.gengoya/<profile>/config.yaml`
  at mode 0600 — the same posture as `~/.aws/credentials` or `.netrc`. Use
  `OPENAI_API_KEY` / `GEMINI_API_KEY` from a secret manager if you need better.
- **`--verbose` prints request and response bodies to stderr.** Keys are
  redacted, but prompts, base64 media and returned content are not. Redact
  before pasting output into an issue.
- **Every generation is billed by the provider.** gengoya estimates cost and asks
  before expensive calls, and never re-sends a billed request on a 5xx, but the
  estimate is not a guarantee — check your provider's usage dashboard.
