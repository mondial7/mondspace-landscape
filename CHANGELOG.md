# Changelog

All notable changes to this project are documented here. The format follows
[Keep a Changelog](https://keepachangelog.com/en/1.1.0/) and the project uses
[Semantic Versioning](https://semver.org/).

## [Unreleased]

### Security
- A repository can no longer read files outside itself. A `README.md` or `.gitignore` symlinked
  to e.g. `~/.ssh/id_rsa` or `/dev/zero` used to be read, shown in the diagram and sent to the
  model; repositories are now opened with `os.OpenRoot`, only regular files up to 1 MiB are read,
  and clones check symlinks out as plain files.
- git runs hardened: inherited `GIT_*` variables are dropped, hooks, fsmonitor, submodules and
  LFS downloads are off, git never looks above the cache directory, and an interrupted clone is
  cloned again instead of updated.
- The local server refuses cross-origin writes (`Origin` / `Sec-Fetch-Site`) and sends a strict
  Content-Security-Policy, `X-Frame-Options: DENY` and cross-origin isolation headers. At most
  3 analyses run at once and 50 are kept; the HTTP server has read and idle timeouts.
- Paths under your home directory are shown as `~/...`, so screenshots do not reveal it.
- The model client caps response size, does not follow redirects (which would re-send code),
  and descriptions are stripped of control and bidi characters. Prompts mark repository content
  as untrusted data.
- Crafted JavaScript files and huge `.gitignore` files can no longer make analysis take hours.
- Built with Go 1.26.11 (GO-2026-6859 in `net/http`). YAML parsing moved from the archived
  `gopkg.in/yaml.v3` to the maintained `go.yaml.in/yaml/v3`.
- Releases: GitHub Actions pinned to commit SHAs (kept current by Dependabot), least-privilege
  permissions, pinned goreleaser and govulncheck, and signed build provenance for every archive
  (`gh attestation verify <archive> --repo mondial7/mondspace-landscape`).

### Changed
- UI: a dark theme sharing its palette with the Jobs Cockpit (teal accent, warm/hot/blue
  highlights); README and site screenshots regenerated, and the GitHub Pages site restyled.
- UI: the six kinds of box are clearly distinct (own colour and shape each: violet person pills,
  navy system, blue containers, teal components, document-shaped files, dashed grey externals).
  The legend shows them as zoom levels (System › Container › Component › File) plus what is
  around, with counts for the current view; clicking one (or keys `1`–`6`) highlights that kind.
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
- A container holding only test code (tests whose target is not resolvable) is tagged `test`,
  so it is hidden with the other tests instead of appearing as a part of the system.
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
