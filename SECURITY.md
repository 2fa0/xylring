# Security Policy

## Supported versions

Security fixes land on `main` and go out in the next release. Older releases are
not patched, so update to the latest version before reporting.

## Reporting a vulnerability

Report potential vulnerabilities privately to
[11802492+2fa0@users.noreply.github.com](mailto:11802492+2fa0@users.noreply.github.com), or use the private
[security advisory](https://github.com/2fa0/xylring/security/advisories/new)
form. Do not open a public issue for a suspected vulnerability.

Include the affected version, your platform, reproduction steps and the impact
you expect. Reports are reviewed on a best-effort basis.

## Areas worth extra care

Xylring drives the system `git` binary and reads repository metadata directly, so
these parts carry the most risk:

- the 年轮 (provenance) features, which install and restore a Git hook under
  `.git/hooks` and rewrite `AI-*` commit trailers;
- how refs, paths and revisions are passed to `git`;
- the updater, which downloads and installs release artifacts.

Logs live under `~/Library/Application Support/Xylring/logs/` on macOS and
`%AppData%\Xylring\logs\` on Windows. They can contain local paths and repository
details; remove credentials and sensitive data before sharing them.
