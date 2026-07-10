# AGENTS.md

## Project Overview

Dockertree is a lightweight local web console for managing Docker and Docker Compose projects. It is a Go single-process app with embedded static frontend assets.

Core goals:

- Run locally as one Go binary.
- Store portable long-lived configuration under `~/.config/dockertree/` or `DOCKERTREE_CONFIG_DIR`.
- Auto-discover Docker Compose projects and standalone containers.
- Manage containers, Compose projects, local images, deploy flows, update previews, and conservative deletion through a token-protected Web UI.

## Repository Layout

- `cmd/dockertree/main.go` - application entrypoint.
- `internal/config/` - config directory, defaults, YAML config loading/saving.
- `internal/core/` - shared project/service/update-plan data types.
- `internal/docker/` - Docker CLI runner, scanner, command planning, deploy/delete helpers.
- `internal/server/` - HTTP routes, auth, API handlers, static asset serving.
- `internal/store/` - YAML inventory persistence.
- `internal/web/` - embedded frontend assets and static asset tests.
- `internal/web/static/` - HTML/CSS/JS frontend.

## Common Commands

```bash
go test ./...
go test ./... -cover
go build -o dockertree ./cmd/dockertree
DOCKERTREE_CONFIG_DIR=/tmp/dockertree-dev go run ./cmd/dockertree
```

The app defaults to `127.0.0.1:27680` and prints the admin token on startup.

## Development Rules

- Use TDD for feature changes and bug fixes. Add or update tests first, confirm the expected RED state, then implement.
- Prefer focused tests with fake Docker runners/executors. Do not require tests to mutate the real Docker daemon.
- Run `gofmt -w` on changed Go files before testing.
- Run `go test ./...` before considering work complete.
- Run `go build -o dockertree ./cmd/dockertree` after user-facing or integration changes.

## Docker Safety Boundaries

Docker actions can mutate the user's machine. Be conservative.

- Search, inspect, scan, preview, and list operations are safe to run locally.
- Do not run real deploy, delete, update, stop, restart, `docker compose up`, `docker compose down`, `docker rm`, or `docker rmi` during verification unless the user explicitly asks for that exact mutation.
- Use API preview endpoints and fake executors in tests for deploy/delete behavior.
- If you must verify a destructive route, verify auth/route shape without a valid token or use a fake executor test.
- Project deletion must stay conservative: `docker compose down` only. Do not add `-v` by default and do not delete Compose files.
- Container deletion uses `docker rm -f <id>` only after user confirmation in the UI.
- Image deletion uses `docker rmi <ref>` only after user confirmation in the UI.

## API / Behavior Notes

- All non-static API routes require `Authorization: Bearer <admin-token>`.
- Scanning uses `docker compose ls --format json` and `docker ps -a --format '{{json .}}'` plus Compose labels.
- Compose projects are indexed by original Compose file path and working directory; do not move or rewrite existing project files during scans.
- Deploy preview endpoints must not write files or start containers.
- Compose deploy writes the user-provided Compose content only in the confirm deploy endpoint.
- Docker command failures should return command, output, exit code, and error text so the frontend can show the real Docker cause.
- After container or project deletion, refresh the inventory by rescanning Docker and saving `inventory.yaml`.

## Frontend Rules

- Frontend assets are plain embedded files in `internal/web/static/`.
- UI language is currently Chinese.
- Keep the interface minimal and operational, not marketing-like.
- The primary navigation is click-switched pages: `容器`, `项目`, `镜像`. Do not show these three pages simultaneously as columns.
- Maintain the global operation output area so users can see command results after actions.
- Preserve light/dark mode behavior using `dockertree.theme` in `localStorage`.
- All UI elements must remain square: no rounded corners. Keep `border-radius: 0 !important` and update `internal/web/static_test.go` when changing style rules.
- Avoid nested buttons. If a row needs both row-click behavior and action buttons, use a non-button row container with `role="button"` and standalone buttons inside it.

## Testing Guidance

- `internal/docker/*_test.go` should cover command construction, path quoting, name derivation, and scanner aggregation.
- `internal/server/server_test.go` should cover auth, route behavior, fake executor commands, and inventory refresh after mutating operations.
- `internal/web/static_test.go` should cover static UI contract: pages, theme support, no-radius styling, deploy controls, delete controls, and local images controls.
- Prefer verifying real Docker only through read-only commands such as `docker ps`, `docker images`, and API list endpoints.

## Current UX Contracts

- `容器` page lists containers/services. Clicking a container switches to its project on the `项目` page.
- `项目` page lists Compose/standalone projects and shows project actions/details.
- `镜像` page lists local images and contains image/Compose deploy flows.
- Image deploy uses one primary image search/name input and derives the container name automatically; advanced options can override the name.
- Deletion and deploy actions must ask for browser confirmation before calling mutating APIs.
