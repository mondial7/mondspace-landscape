# Changelog

All notable changes to this project are documented here. The format follows
[Keep a Changelog](https://keepachangelog.com/en/1.1.0/) and the project uses
[Semantic Versioning](https://semver.org/).

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
