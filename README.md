# Dockertree

Dockertree 是一个用于管理 Docker 和 Docker Compose 项目的轻量级本地 Web 控制台。它以单个 Go 进程运行，使用 YAML 文件保存长期配置，并会索引现有的 Compose 项目位置，不会移动或改写项目文件。

## 功能特性

- 通过 `docker compose ls` 和 Docker Compose 容器标签自动发现现有的 Compose 项目。
- 可在 `项目` 页面配置新建项目根目录和附加扫描目录，保存后立即递归扫描其中的 Compose 文件；新安装默认使用 `/opt`。
- 可从已发现项目的 Compose 文件列表进入编辑器，文件继续保留在原目录，不会因扫描或编辑而被迁移。
- 对没有 Compose 标签的独立 Docker 容器单独进行管理。
- 默认将可迁移的配置和清单存储在 `~/.config/dockertree/` 下。
- 默认在 `0.0.0.0:27680` 提供简洁的内置 Web UI，并通过令牌保护 API 访问。
- 部署前提供保守的更新预览：依次执行 `pull`、可选的 `build`，再执行 `up -d`。
- 支持两种新服务部署方式：搜索镜像名称并通过 `docker run` 部署，或按项目名称自动创建 Compose 项目目录，并选择仅保存配置或保存后执行 `docker compose up -d`。
- 列出本地已下载的 Docker 镜像，并可将其复用于镜像部署。
- 容器、Compose 项目和镜像/部署采用点击切换的独立页面。
- 提供运行概览，包括项目、容器和镜像数量，需要关注的已停止或不健康项目，以及按需获取的 Docker 资源快照。
- 支持全局搜索、状态/健康状况/类型/标签/收藏筛选、排序，以及持久化保存项目收藏、标签和别名。
- 在详情中显示容器健康状况和挂载信息，并提供可配置的项目/容器日志查看器，支持服务、尾部行数、时间戳、关键词和刷新选项。
- 支持仅在页面可见时按 30 秒、60 秒或 5 分钟自动刷新；设置保存在浏览器中，默认关闭。
- 将脱敏后的 Docker 操作历史持久化到 `logs/operations.jsonl`，并可在 Web UI 中按目标和失败状态筛选。
- 安全查看容器的重启策略、网络、创建时间和挂载信息，不返回容器环境变量。
- 支持项目服务链接、本地部署模板、更新检查、可选的定时 Webhook/ntfy 通知，以及 YAML 配置备份与恢复。
- 提供保守的清理中心，可清理选中的已停止容器、悬空镜像和未使用的自定义网络；绝不会执行 `docker system prune` 或删除数据卷。
- 支持高级镜像部署，可配置并校验端口、环境变量、挂载、网络和重启策略。
- 在内存中校验并规范化 Compose YAML，覆盖前显示当前内容和规范化后的内容，并拒绝写入预览后发生变化的文件。
- 支持保守删除：删除容器、对项目执行 Compose `down`（不删除数据卷），以及删除本地镜像。
- Web 前端全局采用直角、无圆角样式。
- 支持浅色/深色模式切换，并将偏好保存在浏览器中。

## 脚本管理

Dockertree 只需要一个 `dockertree.sh` 管理脚本。脚本会检查 Git、Go 1.23+、Docker CLI 和 Docker Compose，缺失时根据操作系统自动安装，然后直接从 GitHub 获取源码并构建，不需要手动克隆仓库：

```bash
curl -fsSL https://raw.githubusercontent.com/ZASENJC/dockertree/main/dockertree.sh -o dockertree.sh
chmod 755 dockertree.sh
./dockertree.sh install
./dockertree.sh start
```

管理命令包括：

```bash
./dockertree.sh doctor
./dockertree.sh install
./dockertree.sh update
./dockertree.sh start
./dockertree.sh status
./dockertree.sh restart
./dockertree.sh stop
./dockertree.sh uninstall
```

不带参数运行 `./dockertree.sh` 会显示包含全部指令的帮助信息。

默认二进制文件路径为 `~/.local/bin/dockertree`，运行日志和 PID 文件存储在 `~/.local/state/dockertree/` 下。新安装会创建默认监听 `0.0.0.0:27680` 的配置。普通卸载会保留 `~/.config/dockertree/`。如需删除二进制文件、运行时文件和全部 Dockertree 配置，请执行 `./dockertree.sh uninstall --purge --yes`。

`install` 和 `start` 会自动注册设备重启后的启动任务：Linux 使用系统级 `/etc/systemd/system/dockertree.service`，并以执行安装的普通用户身份运行 Dockertree，因此不依赖登录会话或用户级 D-Bus；安装 unit 和执行 `systemctl enable` 时可能要求输入 `sudo` 密码。macOS 使用 `~/Library/LaunchAgents/io.github.zasenjc.dockertree.plist`，在用户登录后自动启动。Docker 尚未就绪时，两种任务都会延迟后重试。`uninstall` 会同时禁用并删除这些自启动配置。请使用普通用户执行管理脚本，不要对整个脚本使用 `sudo`。

可通过 `DOCKERTREE_INSTALL_DIR`、`DOCKERTREE_STATE_DIR` 或 `DOCKERTREE_CONFIG_DIR` 覆盖这些路径。安装或启动时如果监听端口已被占用，脚本会提示输入新端口并保存到 `config.yaml`；非交互环境可通过 `DOCKERTREE_PORT=28680 ./dockertree.sh start` 指定并保存端口。

环境自动补全支持 macOS Homebrew，以及 Linux 的 APT、DNF、YUM、Pacman 和 Zypper。macOS 未安装 Homebrew 时，脚本会停止并提示先安装 Homebrew；Linux 安装系统软件时可能要求输入 `sudo` 密码。Docker 安装完成后，脚本会尝试启动 Docker Desktop 或 Docker 服务；如果 daemon 尚未就绪，Dockertree 仍会完成安装并给出提示。`start` 会要求 `docker info` 成功，避免启动一个无法管理 Docker 的实例；Linux 用户还需确保当前账号具有 Docker socket 访问权限。

`doctor` 只检查环境，不安装软件或启动 Docker。设置 `DOCKERTREE_AUTO_INSTALL=0` 可以关闭 `install`、`update` 和 `start` 的自动环境补全。

`./dockertree.sh install` 和 `./dockertree.sh update` 都会直接从 GitHub 仓库 `https://github.com/ZASENJC/dockertree.git` 获取源码，在临时目录完成构建，并同步刷新 `dockertree.sh` 自身。源码临时目录会在操作结束后删除。更新不会覆盖配置；如果 Dockertree 原本正在运行，更新成功后会自动重启。GitHub 获取或编译失败时会保留当前二进制和运行中的进程。更新来源可通过 `DOCKERTREE_GITHUB_REPOSITORY` 和 `DOCKERTREE_GITHUB_REF` 覆盖。

`./dockertree.sh uninstall` 会删除已安装程序和运行状态，但保留这个管理脚本；使用 `--purge --yes` 时还会删除 Dockertree 配置。如果配置目录包含管理脚本，清理会被拒绝，避免删除唯一的 `dockertree.sh`。之后仍可使用同一个 `dockertree.sh install` 重新安装。

## 运行

```bash
go run ./cmd/dockertree
```

服务器启动时会将访问地址、配置目录和生成的管理员令牌写入日志。`./dockertree.sh start` 会从本次启动日志中提取并显示访问地址和管理员令牌。打开该地址，粘贴并保存令牌，然后点击 `扫描` 读取本地 Docker 状态。

测试或迁移时，可以指定一个可迁移的配置目录：

```bash
DOCKERTREE_CONFIG_DIR=/path/to/dockertree-config go run ./cmd/dockertree
```

## 构建

```bash
go build -o dockertree ./cmd/dockertree
./dockertree
```

## 配置文件

- `config.yaml`：监听地址、管理员令牌、新建项目根目录 `projectRoot`、附加扫描路径 `scanPaths`、更新默认值和 UI 偏好。
- `inventory.yaml`：已发现的项目、原始 Compose 文件路径、工作目录、服务、端口、标签和收藏。
- `templates.yaml`：个人容器和 Compose 部署模板。
- `logs/operations.jsonl`：仅追加写入的脱敏操作历史。

Dockertree 默认监听所有网络接口，并在 `config.yaml` 中启用 `allowLan: true`。如需限制为本机访问，可将 `listenAddr` 改为 `127.0.0.1:<端口>` 并将 `allowLan` 设为 `false`。

## Compose 项目目录

新安装的 `projectRoot` 默认为 `/opt`，用于确定新建 Compose 项目的保存位置。`项目` 页面可以修改该根目录，并可在 `扫描目录` 中按每行一个绝对路径填写其他已有项目目录。保存设置后无需重启，Dockertree 会立即递归扫描这些目录；新建项目根目录始终会包含在实际扫描范围中。

目录扫描识别 `compose.yaml`、`compose.yml`、`docker-compose.yaml` 和 `docker-compose.yml`。因此，对于已经手动放在 `/opt/<项目目录>/` 或其他已配置目录中的项目，点击 `保存并扫描` 后即可在项目列表中看到匹配的 Compose 文件。扫描只建立索引，不会移动或改写原文件。展开项目后，可以点击对应文件旁的 `编辑`，在 Compose 编辑器中读取和修改该文件；保存时仍使用它原来的绝对路径。多 Compose 文件项目在校验和部署时会继续按清单中的原顺序带上全部 `-f` 文件，不会只用当前编辑的单个文件重建服务。

新建 Compose 项目时只需填写项目名称和 Compose 内容。路径由只读字段展示，并按 `<projectRoot>/<项目名>/compose.yml` 自动派生；使用默认设置时即为 `/opt/<项目名>/compose.yml`。项目名称必须是单层文件夹名称，系统会拒绝 `..`、路径分隔符以及通过符号链接逃出新建项目根目录的路径。

运行 Dockertree 的系统账号必须能够遍历和读取扫描目录；新建项目时还必须能够在 `projectRoot` 下创建子目录和文件，编辑已有项目时必须拥有对应文件及目录的写入权限。为避免越界读取和写入，编辑入口只接受已配置项目目录内的普通文件，并拒绝符号链接。Linux 上的 `/opt` 通常不允许普通用户直接写入，可预先创建并授权专用目录，或把 `projectRoot` 改为该账号可写的绝对路径。不要用开放所有权限的方式替代最小化授权。管理 Compose 文件和执行 Docker 命令的 API 均受管理员令牌保护；若无局域网访问需求，建议仅监听本机地址，并妥善保管令牌和 Docker socket 访问权限。

## 测试

```bash
go test ./...
go test ./... -cover
```

测试使用模拟的 Docker runner/executor 验证 API 和部署行为，不会改动真实容器。

Docker 命令失败时会返回具体命令、退出码和原始 Docker 输出，因此容器名称冲突等错误会直接显示在 UI 中，而不只是显示 `exit status 125`。

## 新建部署

Web UI 的 `部署新容器` 面板提供两种模式：

- `镜像部署`：输入镜像搜索词或镜像名称，可选择搜索 Docker Hub，预览 `docker run -d --name ...` 命令后再部署。Dockertree 会根据镜像自动生成容器名称，也可通过高级选项覆盖该名称。
- `Compose 部署`：新建时输入项目名称和 Compose YAML 内容，系统按 `projectRoot` 自动派生只读的 Compose 路径并创建缺少的项目目录；编辑已发现项目时沿用原文件路径。`仅保存` 会在校验并确认后写入文件，但不会启动或更新容器；`保存并部署` 会写入文件，并在该文件所在目录执行 `docker compose -f <path> up -d`。

预览接口绝不会写入文件或启动容器。`仅保存` 和 `保存并部署` 都会先调用 Compose 配置校验，并在覆盖文件前要求浏览器确认；只有 `保存并部署` 会执行真实的部署操作。编辑期间如果磁盘上的原文件发生变化，系统会拒绝使用过期预览覆盖它。

删除操作同样会执行真实的 Docker 命令。删除项目时使用 `docker compose down`，并且有意不传入 `-v`，因此命名数据卷会被保留。
删除容器或项目后，Dockertree 会立即重新扫描 Docker 并改写 `inventory.yaml`，确保 UI 展示当前 Docker 状态，而不是过期的清单数据。具体命令和 Docker 输出会显示在全局操作输出区域。

资源快照使用只读命令 `docker stats --no-stream`，仅在概览页面打开时或用户主动刷新时请求。页面不可见时，自动刷新也会跳过扫描。

更新检查使用 Docker Compose 的 dry-run 模式，不会拉取镜像。定时检查默认关闭，可配置为每小时、每 6 小时或每天执行。只有定时更新通知或用户明确执行测试通知时，才会请求 Webhook URL。

导出配置时会生成一个移除了管理员令牌的 YAML 备份。恢复时会保留当前正在使用的管理员令牌、监听地址和局域网绑定策略；恢复后的项目扫描路径会立即应用，其他需要重新初始化的进程级设置仍应在重启 Dockertree 后确认生效。
