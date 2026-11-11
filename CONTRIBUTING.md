# Contributing to Xylring

Thanks for taking the time to contribute. This document covers what you need to
build the project locally, run the tests, and get a pull request merged.

## Prerequisites

- **Go 1.27+**
- **Git** installed on your system. Xylring is a Git client that drives the
  system `git` binary, so the app will not run without it.
- macOS or Windows for building the packaged desktop app. The Go code itself
  builds anywhere.

## Getting started

```sh
git clone https://github.com/2fa0/xylring.git
cd xylring

# Run the app against any repository
go run ./cmd/gitclient /path/to/some/repo
```

## Build and test

```sh
# Run the full test suite (takes about a minute)
go test ./...

# Build a native app bundle for local development
cd cmd/gitclient
go tool mygo build -skip-dmg -skip-notarize
```

Packaging signed release artifacts for all platforms is done by
`./build.command`. You normally do not need it for development, and it requires
tooling that is not part of the test suite.

## Project layout

| Path | Contents |
| :--- | :--- |
| `cmd/gitclient/` | The desktop application: UI, Git operations, graph rendering |
| `internal/crashlog/` | Crash logging helpers |
| `docs/` | Design notes and requirements documents |
| `scripts/` | Release helper scripts |

## Commit messages

Use a short imperative subject line with a type prefix, for example
`fix: handle repository loss after .git is deleted` or
`refactor(ui): combine top-bar pull and push into a sync menu`.

### Commit trailers (required)

This repository records how a commit was produced. **AI-assisted commits must
end with a blank line followed by the `AI-Agent` and `AI-Model` trailers**, and
both values must be truthful — use the real agent product name and the real
model name. Do not guess, and do not leave placeholders such as
`<actual model name>` in place.

```text
fix: handle repository loss after .git is deleted

AI-Agent: <the agent product you used>
AI-Model: <the model that produced the change>
```

A commit written entirely by a human should declare `AI-Agent: human` and omit
the model. Existing human authorship, co-author, and `Signed-off-by` trailers
must be preserved.

Note that a CI workflow checks these trailers on pull requests. If your commit
is rejected for a missing or malformed trailer, fix the commit message and push
again — please do not bypass it with `--no-verify` or by altering the hooks.

## Pull requests

1. Fork the repository and create a branch from `main`.
2. Keep the change focused. Separate unrelated refactors from behavior changes.
3. Add or update tests when you change behavior, and make sure `go test ./...`
   passes on your machine.
4. Describe what changed and why. Screenshots help a lot for UI changes.
5. Make sure your commits follow the trailer rules above.

## License

Xylring is released under the [MIT License](LICENSE). By submitting a pull
request you agree that your contribution is licensed under the same terms.
