# storekit — Agent entrypoint

storekit contains three independently versioned Go libraries and one test-only integration module.
Use fast-fail boundaries and keep common execution paths simple. Preserve concurrent user changes.

## Routing

- Read `README.md` for commands and `ARCHITECTURE.md` before changing module boundaries.
- For changes, formal review or Harness operations, read `.repo-foundry/skills/repo-foundry-ai/SKILL.md`.
- Governance is `strict`; use `.agents/skills/engineering-specs/SKILL.md` and the canonical Router.
- Invoke `$engineering-specs`; the locked navigation index is `docs/agent-guides/managed/index.md`.
- Before writes, list planned relative paths, inspect candidates and Applicability, activate exact Requirements,
  and retain the digest-verified capsule. Audit changed paths and the five-label handoff before completion.
- Ordinary explanation and navigation do not activate the full workflow.
- For engineering prose, read the owning professional Skill's `references/controlled-writing.md`.
- Do not manually edit `docs/agent-guides/managed/`; update through previewed RepoFoundry operations.
- Codex Hook configuration is installed in `.codex/hooks.json`; runtime enforcement requires host trust.
  Without trusted Hooks use manual `begin → candidates → requirements → activate → audit`.

## Ownership and boundaries

- `clickhouse/schema`: storage DSL, no third-party dependencies.
- `migrator`: migration execution, depends on schema.
- `sqlstore`: GORM wrapper, depends on migrator; driver packages are opt-in.
- `integration`: test-only consumer of the three libraries; never publish an integration tag.
- Versions ending in `-0.local` are temporary verification artifacts, not release tags.
- No root Go module. Do not commit go.work or module replace directives.
- Production libraries must not import integration or host applications.
- Application defaults belong to the host. Inject the TracerProvider; do not use a global provider.
- Keep published migration effects stable. SQL/schema changes need real ClickHouse 26.2 evidence.
- Branches use `<type>/<short-kebab-description>` and commits use Conventional Commits.
- Do not publish tags or apply business database migrations without explicit user authorization.

## Verification

- `make test`: formatting, Python boundary tests, four-module tidy/no-diff, vet/race and driver isolation.
- `make test-integration`: above checks plus disposable Docker PostgreSQL/MySQL/ClickHouse services.
- `make test-integration-ci`: same tests using isolated GitHub Actions service aliases; rejects non-CI execution.
- `make tidy`: explicitly refresh local verification candidate checksums and module metadata.
- `make check-harness`: pinned RepoFoundry Harness, Spec and engineering-document validators.
- `.github/workflows/verify.yml` has validation jobs only; there is no release or deployment job.

## Documentation and handoff

- `docs/index.md` maps project guidance; module READMEs own API usage.
- `docs/RELIABILITY.md` and `docs/SECURITY.md` record failure and trust boundaries.
- `docs/QUALITY_SCORE.md` records checks and remaining acceptance gaps.
- Keep verification evidence tied to this repository revision; do not infer consumer acceptance from library checks.
- Generated lifecycle indexes are empty until this repository owns lifecycle artifacts.
- End governed work with Activated specifications, Activated requirements, Verification, Exceptions,
  and Compatibility or migration. Distinguish local checks, remote CI and release/consumer acceptance.
