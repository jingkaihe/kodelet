# Working on Kodelet

Kodelet is an agentic coding CLI: Go backend in `cmd/kodelet/` and `pkg/`, React/TypeScript UI in `pkg/webui/frontend/`.

## Conventions

- Use `github.com/pkg/errors` (`errors.Wrap`/`Wrapf`) rather than `fmt.Errorf` for stack traces.
- Use `pkg/logger` for diagnostics and `pkg/presenter` for user-facing CLI output.
- Write tests with testify assertions in Go and Vitest in the frontend.
- Update documentation when changing the CLI interface.
- Keep Markdown prose paragraphs on one source line.
- Use sentence case in the Web UI; no all-caps copy or CSS `text-transform: uppercase` except codes and established acronyms.
- Support Linux/macOS on amd64/arm64 only unless explicitly asked otherwise.
- Do not add token environment or argv scrubbing: agents and tools share the trusted process environment, so selective scrubbing is not a security boundary.
- After editing `pkg/webui/frontend/src/assets/logo.svg`, run `mise run frontend-icons` and commit the PNGs in `pkg/webui/frontend/public/assets/`.

## Checks

Use `mise` tasks. For code changes, run the relevant lint and tests:

| Area | Lint | Tests |
| --- | --- | --- |
| Go | `mise run lint` | `mise run test` |
| Frontend | `mise run frontend-lint` | `mise run frontend-test` |

## Read as needed

- [Development reference](skills/kodelet/references/development.md): code layout, build tasks, architecture, and opt-in tests.
- [Kodelet skill](skills/kodelet/SKILL.md): CLI usage, configuration, recipes, skills, extensions, and SDK references.
- [Manual](docs/MANUAL.md): full user-facing documentation.
