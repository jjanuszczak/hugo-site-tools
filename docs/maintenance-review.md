# Code health and maintenance review

Date: 2026-09-14
Scope: repository organization, tooling, documentation, and legal hygiene.
Method: inspection of the tracked tree, GitHub workflows, and dependencies, plus
`gofmt -l cmd internal` and `go vet ./...` (both clean).

This review records maintenance and organization findings rather than product
scope. Product direction and the `doctor` specification remain in
[roadmap.md](roadmap.md).

## What is already in good shape

- `cmd/hs/main.go` is a genuine 11-line shim; `Run`/`run` is a clean test seam.
- The exit-code convention is consistent: `0` success, `1` findings, `2` invalid
  arguments, `3` unavailable Hugo or unreadable project, `4` remote failure.
- JSON and SARIF output exist, builds use temporary destinations only, and the
  dependency set stays small (Bubble Tea, Lipgloss, go-toml, go-qrcode,
  `golang.org/x/net/html`).
- Web assets are embedded with `//go:embed web_static` plus an `HS_WEB_ASSETS`
  override, so `go install` works without shipping side files.
- Tests are self-contained: temporary fixtures and a fake Hugo executable mean
  no network access and no globally installed Hugo. `gofmt` and `go vet` are
  clean.

## Finding 1: `internal/app/app.go` is a monolith

`internal/app/app.go` is 3,122 lines and defines 116 top-level functions with no
section markers. It mixes at least six concerns:

| Concern | Approx. functions | Examples |
| --- | --- | --- |
| CLI dispatch and option parsing | ~30 | `run`, `runContent`, `parseDoctorOptions`, `resolveContentProject` |
| Hugo configuration parsing | ~15 | `readContentConfig`, `parseTOMLConfig`, `parseYAMLConfig`, `configFromMap` |
| Front matter parsing | ~10 | `parseFrontMatter`, `frontMatterLine`, `parseValue` |
| Content collection | ~10 | `collectContent`, `collectPosts`, `contentSources` |
| Doctor checks | ~30 | `doctorContent`, `doctorHTML`, `doctorOutputs`, `doctorRemote` |
| Report formatting | ~8 | `writeDoctorReport`, `writeBuildReport`, `writeURLReport` |

`docs/roadmap.md` already specifies the intended package layout
(`internal/config`, `internal/doctor`, `internal/doctor/checks`,
`internal/report`) and its delivery sequence says to split `internal/app`
*before* substantial new `doctor` work. The code and the documented plan
disagree. A mechanical, behavior-preserving extraction would align them and make
the function surface navigable.

## Finding 2: two documentation trees that do not reference each other

- `docs/*.md` holds the roadmap, PRDs, and the release runbook, linked from
  `README.md`.
- `hs-docs/content/docs/*` is the published Docsy site.

The trees do not cross-reference each other, so the PRDs are invisible on the
published site and coverage drifts. For example, `docs/web-interface-plan.md` has
no published counterpart, while `hs-docs/content/docs/reference/` covers
adjacent material. Contributor-facing structure is also duplicated across
`AGENTS.md` and `hs-docs/content/docs/contributing/docs.md`. One canonical home
should be chosen, and the other tree should link to it.

## Finding 3: `testdata/fixtures/` is empty scaffolding

The directory contains only `README.md`, and `AGENTS.md` directs contributors to
it. Every test instead builds fixtures inline with `t.TempDir()` (see
`writeGeneratedHTML`, `writePost`, `installFakeHugo`). The directory is a guide
that points at nothing. Either migrate the larger configuration and front-matter
cases into real fixture directories — which the roadmap's test section calls for
("small fixture projects for configuration discovery and front matter cases") —
or remove the directory and update `AGENTS.md`.

## Finding 4: no linter configuration

There is no `.golangci.yml`, `revive`, or `staticcheck` configuration. For a
codebase of this size with hand-rolled configuration and front-matter parsing
and its own HTTP handling, `golangci-lint` (or at minimum `staticcheck`) in
`test.yml` would catch defects that `go vet` does not. This is cheap to add and
high-signal for a single-maintainer project.

## Finding 5: vendored JavaScript ships without license text

`internal/app/web_static/web_vendor/` redistributes `JOG.min.js` and what is
plainly a bundled Chart.js distribution (`ChartJOG.Controls.js`, 6,700+ lines)
with no `LICENSE`, `NOTICE`, or attribution comment. These files are copied into
every release archive and embedded in the binary. Upstream license files should
sit beside each bundle.

## Finding 6: smaller issues

- **User-Agent gap.** `docs/roadmap.md` specifies that the remote client sends a
  descriptive `hs/<version>` User-Agent and has no cookie jar. No request in
  `internal/app/app.go` sets a User-Agent. Implement it or remove the
  requirement from the specification.
- **No version embedding.** There is no `--version` flag, and the SARIF tool
  `driver` is only `"hs"` with no tool version (`internal/app/app.go`,
  `internal/app/web.go`). A `-ldflags -X` version variable would improve bug
  reports and give SARIF consumers a tool version.
- **`go 1.26` without a `toolchain` directive.** CI pins `1.26.x`, but local
  builds float. A `toolchain go1.26.5` directive would make builds reproducible.
- **No `CHANGELOG`.** Relying on GitHub `--generate-notes` is acceptable for
  now; revisit only if curated release notes are wanted.

## Recommended order

1. Add the linter configuration and vendored license files. Small, immediate,
   and safe.
2. Resolve `testdata/fixtures/` and the documentation-tree duplication. Small
   and clarifying.
3. Split `internal/app` into the packages the roadmap prescribes. Large, but it
   unblocks the remaining work and is best done before further `doctor` work.

The findings above are tracked as GitHub issues:

- Issue 1 covers Finding 4, Finding 5, and the tooling items of Finding 6.
- Issue 2 covers Finding 2 and Finding 3.
- Issue 3 covers Finding 1 and the code-behavior items of Finding 6.