# Contributing to telegym

Thanks for considering a contribution. telegym is a small, focused
project - this guide is intentionally short.

## Dev setup

See [README - Prerequisites](README.md#prerequisites). TL;DR:

```bash
make init                       # one-time
make build build-examples       # mock + proxy + echobot + custom k6
./examples/echobot/run.sh       # smoke test the full stack
```

## Before opening a PR

Run the local equivalent of CI:

```bash
make preflight
```

This runs `gofmt`, `go vet`, `golangci-lint`, `actionlint`, `yamllint`,
`govulncheck`, `trivy fs`, the test suite with `-race`, and a
cross-platform build matrix (linux / darwin / windows x amd64 / arm64).
Whichever tools you don't have installed are skipped with a hint on how
to install them; CI has them all and will fail if any one fails.

If your change shows up in the terminal output or the debug chat, re-record
the README and site demos with `make demo` (`docs/demo.gif`,
`docs/debug-chat.gif`). It needs vhs, Docker, Node and Chrome and tells you
how to install whatever is missing; the recording scripts live in
`scripts/demo/`.

## Commit messages

We use [Conventional Commits](https://www.conventionalcommits.org/):

```
feat(brand): add gold dumbbell variant
fix(mock): reject empty bot tokens with 401
refactor(xk6): collapse pool helpers into a single module
chore(ci): bump actions/setup-go to v5
docs(readme): clarify proxy setup
```

Scopes are usually a package name (`mock`, `xk6`, `proxy`, `brand`) or a
top-level concern (`ci`, `make`, `docs`). Keep the subject under 70
characters; put detail in the body.

## What we welcome

Planned work is listed as [issues labelled `roadmap`](https://github.com/kolomiichenko/telegym/issues?q=is%3Aissue+is%3Aopen+label%3Aroadmap),
each with where to start in the code; [`good first issue`](https://github.com/kolomiichenko/telegym/issues?q=is%3Aissue+is%3Aopen+label%3A%22good+first+issue%22)
marks the self-contained ones. Comment on an issue before starting so work
isn't duplicated. Also welcome:

- Bug fixes with a reproducer
- Bot API coverage gaps (explicit handlers for currently-generic methods)
- Performance improvements with before/after numbers
- Docs corrections and examples for real-world bots
- New k6 scenarios under `examples/`

## What needs prior discussion

Open an issue first if you want to:

- Add a new top-level binary or package
- Change the public Go API (anything exported under `pkg/`)
- Add a new dependency (each one needs justification)
- Restructure the repo or modify CI substantially

## Releasing

Maintainers tag `main`; the `Release` workflow does the rest:

```bash
git tag -a v0.6.0 -m "v0.6.0" && git push origin v0.6.0
```

It pushes `ghcr.io/kolomiichenko/telegym-mock:{0.6.0,0.6,latest}` and
drafts a GitHub Release with binaries for every platform, k6 with
`xk6-telegym`, `checksums.txt` and generated notes. Edit the draft before
publishing: give it a title that starts with the version
(`v0.6.0 Flood limits & prebuilt releases`), a short summary grouped by area (Mock,
Debug chat, Performance, Security...) and thank the contributors who made
the release, then press **Publish**. Use
[semver](https://semver.org/): bump the minor version for features, the
patch version for fixes. A tag like `v0.6.0-rc.1` is published as well,
without moving `latest`.

## Code style

Standard Go conventions, enforced by `gofmt` and `golangci-lint`. Code
comments in English. No new dependencies without justification.

## License

By contributing you agree your contributions are licensed under the same
terms as the project (MIT, see [LICENSE](LICENSE)).
