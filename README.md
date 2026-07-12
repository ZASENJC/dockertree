# Dockertree

Dockertree 是一个用于管理 Docker 和 Docker Compose 项目的轻量级本地 Web 控制台。它以单个 Go 进程运行，使用 YAML 文件保存长期配置，并会索引现有的 Compose 项目位置，不会移动或改写项目文件。

## 功能特性

- 通过 `docker compose ls` 和 Docker Compose 容器标签自动发现现有的 Compose 项目。
- 对没有 Compose 标签的独立 Docker 容器单独进行管理。
- 默认将可迁移的配置和清单存储在 `~/.config/dockertree/` 下。
- 在 `127.0.0.1:27680` 提供简洁的内置 Web UI，并通过令牌保护 API 访问。
- 部署前提供保守的更新预览：依次执行 `pull`、可选的 `build`，再执行 `up -d`。
- 支持两种新服务部署方式：搜索镜像名称并通过 `docker run` 部署，或将 Compose 内容写入用户指定的路径后执行 `docker compose up -d`。
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

首次安装可以只下载管理脚本。脚本会检查 Git、Go 1.23+、Docker CLI 和 Docker Compose，缺失时根据操作系统自动安装，然后直接从 GitHub 获取源码并构建：

```bash
mkdir -p ~/.local/bin
curl -fsSL https://raw.githubusercontent.com/ZASENJC/dockertree/main/dockertree.sh -o ~/.local/bin/dockertreectl
chmod 755 ~/.local/bin/dockertreectl
~/.local/bin/dockertreectl install
~/.local/bin/dockertreectl start
```

也可以先克隆仓库，再使用仓库根目录下的脚本：

```bash
git clone https://github.com/ZASENJC/dockertree.git
cd dockertree
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

默认二进制文件路径为 `~/.local/bin/dockertree`，运行日志和 PID 文件存储在 `~/.local/state/dockertree/` 下。普通卸载会保留 `~/.config/dockertree/`。如需删除二进制文件、运行时文件和全部 Dockertree 配置，请执行 `./dockertree.sh uninstall --purge --yes`。

可通过 `DOCKERTREE_INSTALL_DIR`、`DOCKERTREE_STATE_DIR` 或 `DOCKERTREE_CONFIG_DIR` 覆盖这些路径。

环境自动补全支持 macOS Homebrew，以及 Linux 的 APT、DNF、YUM、Pacman 和 Zypper。macOS 未安装 Homebrew 时，脚本会停止并提示先安装 Homebrew；Linux 安装系统软件时可能要求输入 `sudo` 密码。Docker 安装完成后，脚本会尝试启动 Docker Desktop 或 Docker 服务；如果 daemon 尚未就绪，Dockertree 仍会完成安装并给出提示。

`doctor` 只检查环境，不安装软件或启动 Docker。设置 `DOCKERTREE_AUTO_INSTALL=0` 可以关闭 `install`、`update` 和 `start` 的自动环境补全。

`./dockertree.sh update` 会直接克隆 GitHub 仓库 `https://github.com/ZASENJC/dockertree.git` 的 `main` 分支，在临时目录完成构建后替换已安装的二进制。它不会修改当前源码目录，也不会覆盖配置；如果 Dockertree 原本正在运行，更新成功后会自动重启。GitHub 获取或编译失败时会保留当前二进制和运行中的进程。更新来源可通过 `DOCKERTREE_GITHUB_REPOSITORY` 和 `DOCKERTREE_GITHUB_REF` 覆盖。

## 运行

```bash
go run ./cmd/dockertree
```

服务器启动时会输出访问地址、配置目录和生成的管理员令牌。打开该地址，粘贴并保存令牌，然后点击 `扫描` 读取本地 Docker 状态。

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

- `config.yaml`：监听地址、管理员令牌、扫描路径、更新默认值和 UI 偏好。
- `inventory.yaml`：已发现的项目、原始 Compose 文件路径、工作目录、服务、端口、标签和收藏。
- `templates.yaml`：个人容器和 Compose 部署模板。
- `logs/operations.jsonl`：仅追加写入的脱敏操作历史。

Dockertree 默认只监听本机地址。如需监听局域网地址，必须在 `config.yaml` 中显式设置 `allowLan: true`。

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
- `Compose 部署`：输入项目名称、Compose 文件路径和 Compose YAML 内容。预览只生成命令；确认部署后，系统会将内容写入指定路径，并在该文件所在目录执行 `docker compose -f <path> up -d`。

预览接口绝不会写入文件或启动容器。部署接口会执行真实的 Docker 操作。

删除操作同样会执行真实的 Docker 命令。删除项目时使用 `docker compose down`，并且有意不传入 `-v`，因此命名数据卷会被保留。
删除容器或项目后，Dockertree 会立即重新扫描 Docker 并改写 `inventory.yaml`，确保 UI 展示当前 Docker 状态，而不是过期的清单数据。具体命令和 Docker 输出会显示在全局操作输出区域。

资源快照使用只读命令 `docker stats --no-stream`，仅在概览页面打开时或用户主动刷新时请求。页面不可见时，自动刷新也会跳过扫描。

更新检查使用 Docker Compose 的 dry-run 模式，不会拉取镜像。定时检查默认关闭，可配置为每小时、每 6 小时或每天执行。只有定时更新通知或用户明确执行测试通知时，才会请求 Webhook URL。

导出配置时会生成一个移除了管理员令牌的 YAML 备份。恢复时会保留当前正在使用的管理员令牌、监听地址和局域网绑定策略；恢复扫描路径或其他进程级设置后，请重启 Dockertree。
