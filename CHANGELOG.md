# Changelog

All notable changes to this project are documented here. The format follows
[Keep a Changelog](https://keepachangelog.com/en/1.1.0/) and the project uses
[Semantic Versioning](https://semver.org/).

## [Unreleased]

### Changed
- UI: large views stay readable. Very wide ranks are folded into staggered rows that fit the
  screen shape, zoomed-out boxes show their name in a large font, and on busy views
  relationship labels appear on hover and selection (toggle with `L`).
- UI: hovering a box highlights what it talks to; selecting from a list brings the box into view.
- UI: with nothing selected, the side panel describes the current level and lists every box in
  it (with ⊕ zoom buttons), instead of always showing the repository summary.
- UI: relationship filters are coloured chips that double as the legend; toolbar icons, a
  keyboard-shortcut sheet (`?`), `D` to switch layout direction.
- UI: a progress card while a repository is fetched and analyzed, example repositories on the
  start screen, collapsible long lists (routes, connections), search highlights matches.
- UI: the diagram refits when the window is resized; the status pill no longer covers boxes.

### Fixed
- `landscape serve --port 9000` (flags after `serve` or a path) failed with "expected at most
  one repository".
- Monorepo analysis: Claude worktrees and nested git checkouts are skipped, nested `go.mod`
  modules are resolved, compose services attach to the code containers with the same name,
  more backing images are recognised and files listing many hosts become one external.

### Development
- `LANDSCAPE_WEB_DIR` serves the UI from disk (`make ui`); UI layout helpers have unit tests
  (`make test-js`, run in CI).

## [0.1.1-alpha] - 2026-11-08

### Security

- Build with Go 1.26.10, fixing standard-library vulnerabilities reported by
  govulncheck in `net/http` (GO-2026-6611, -6612, -6613, -6617) and `crypto/x509`
  (GO-2026-6809).

### Fixed

- Release configuration: the Homebrew tap token now defaults cleanly when unset.

## [0.1.0-alpha] - 2026-11-08

First public alpha: the foundation for analysing a repository and drawing it.

### Added

- `landscape` CLI: analyze the current directory, a local path or a public GitHub
  repository (shallow clone into the user cache); `serve` mode; `--json` export.
- Local web UI: zoomable C4-style levels (context → containers → components → code),
  edge lifting across levels, details panel (descriptions, metrics, routes, SQL tables,
  deployment, connections), whole-model search, relationship filters, test toggle,
  layout direction, PNG export, light/dark themes, shareable per-view URLs.
- Static analyzers: Go (go/parser: packages, per-file imports, routes, embeds, entry
  points, docs), JavaScript/TypeScript, Python, Swift (module-aware, type references),
  HTML templates (links, forms, htmx), infrastructure (Dockerfile, docker-compose,
  fly.toml) and language-agnostic literals (external hosts, SQL tables).
- Architecture assembly: containers from entry points and module roots, shared
  libraries, embedded assets, UI → route HTTP flows, data flows with table names,
  people (browser/app users, operators), deployment details.
- Local LLM enrichment (Ollama or LM Studio / OpenAI-compatible), bottom-up and
  streamed into the open diagram; reasoning disabled for fast answers; retries on
  transient server errors.
- Local-only server hardening: loopback bind, Host check, JSON-only API.
