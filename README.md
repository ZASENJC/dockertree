# Dockertree

Dockertree is a lightweight local web console for managing Docker and Docker Compose projects. It runs as a single Go process, stores long-lived configuration in YAML files, and indexes existing Compose locations without moving or rewriting your project files.

## Features

- Auto-discovers existing Compose projects from `docker compose ls` and Docker Compose container labels.
- Tracks standalone Docker containers separately when no Compose labels are present.
- Stores portable configuration and inventory under `~/.config/dockertree/` by default.
- Serves a minimal built-in web UI on `127.0.0.1:27680` with token-protected API access.
- Provides conservative update preview before deploy: `pull`, optional `build`, then `up -d`.
- Deploys new services in two ways: search an image name and deploy it with `docker run`, or write Compose content to a user-provided path and run `docker compose up -d`.
- Lists locally downloaded Docker images and lets you reuse one for image-based deployment.
- Uses click-switched web pages for containers, Compose projects, and images/deployment.
- Supports conservative deletion: remove containers, run Compose `down` for projects without deleting volumes, and remove local images.
- Uses square, no-radius UI styling throughout the web frontend.
- Supports light/dark mode switching with the preference stored in the browser.

## Run

```bash
go run ./cmd/dockertree
```

The server prints the URL, config directory, and generated admin token on startup. Open the URL, paste the token, save it, then click `扫描` to read local Docker state.

Use a portable config directory when testing or migrating:

```bash
DOCKERTREE_CONFIG_DIR=/path/to/dockertree-config go run ./cmd/dockertree
```

## Build

```bash
go build -o dockertree ./cmd/dockertree
./dockertree
```

## Config Files

- `config.yaml`: listen address, admin token, scan paths, update defaults, UI preferences.
- `inventory.yaml`: discovered projects, original Compose file paths, working directories, services, ports, tags, favorites.
- `logs/`: reserved for command logs and future operation history output.

By default Dockertree only binds to localhost. To bind to a LAN address, set `allowLan: true` explicitly in `config.yaml`.

## Tests

```bash
go test ./...
go test ./... -cover
```

The tests use fake Docker runners/executors for API and deploy behavior, so they do not mutate your real containers.

Docker command failures return the command, exit code, and raw Docker output so errors such as container-name conflicts are visible in the UI instead of only showing `exit status 125`.

## New Deployments

The web UI has a `部署新容器` panel with two modes:

- `镜像部署`: enter one image search/name value, optionally search Docker Hub, preview the `docker run -d --name ...` command, then deploy. Dockertree derives a container name from the image automatically; use advanced options to override it.
- `Compose 部署`: enter a project name, Compose file path, and Compose YAML content. Preview only renders the command. Deploy writes the content to the provided path and then runs `docker compose -f <path> up -d` from that file's directory.

Preview endpoints never write files or start containers. Deploy endpoints perform real Docker operations.

Delete actions are also real Docker operations. Project deletion uses `docker compose down` and intentionally does not pass `-v`, so named volumes are preserved.
After container or project deletion, Dockertree immediately rescans Docker and rewrites `inventory.yaml`, so the UI reflects the current Docker state instead of stale inventory data. The command and Docker output are shown in the global operation output area.
