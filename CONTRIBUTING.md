# Contributing

Thanks for taking a look. This is a small, focused CLI — bug reports and pull
requests are welcome, and so is a plain question in an issue.

## Reporting a bug

Include the output of `gengoya version`, the exact command you ran, the model,
and what you expected instead. `--verbose` dumps the API request and response
to stderr — **redact prompts and anything private before pasting it.** Keys
are redacted. The provider's error body is the part that matters.

## Pull requests

Before opening one:

```bash
make fmt           # gofmt -w .
make vet           # go vet ./...
make test          # go test ./...
```

CI runs the same three (plus `-race`) on Linux and macOS, so a green local run
usually means a green PR.

House rules:

- **One concern per PR.** A bug fix and a refactor in the same diff take three
  times as long to review.
- **Tests never touch the network.** Request building and response parsing are
  pure functions in `internal/provider`; test those. Live calls cost money.
- **Models are data.** Ids, capabilities and prices live in
  `internal/registry/registry.yaml`, never in Go constants.
- **Verify against the live API.** Vendor docs for these endpoints have been
  wrong more than once (see `RESEARCH.md`); note what you checked live.
- **Keep stdout to paths.** Data goes to stdout, status and cost to stderr, so
  an agent can `Read` what was printed.
- **Update the docs in the same commit.** Any change to the CLI surface must
  also update `cmd/skill.md` (embedded in the binary, printed by `gengoya
  skill`) and `README.md`.
- **Conventional commit subjects** — `feat:`, `fix:`, `docs:`, `refactor:`,
  `test:`, `chore:`. Release notes are generated from them.

## Releases

Maintainer-only. Tag and push:

```bash
git tag -a v1.2.3 -m "v1.2.3"
git push origin v1.2.3
```

GoReleaser builds archives for linux/darwin/windows on amd64 and arm64 and
publishes the GitHub release with a generated changelog.
