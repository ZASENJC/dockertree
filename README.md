# Dockertree

Dockertree 是一个运行在本机的 Docker / Docker Compose Web 管理台。它以单个 Go 进程运行，可以自动发现现有容器和 Compose 项目，并通过中文界面完成查看、部署、更新、日志检查和安全清理。

Dockertree 不会在扫描时移动或改写已有 Compose 文件。配置、项目索引、部署模板和操作历史默认保存在 `~/.config/dockertree/`，便于备份和迁移。

## 适合哪些场景

- 在 Linux 服务器或 macOS 上集中查看本机 Docker 状态。
- 管理散落在不同目录中的 Compose 项目。
- 通过浏览器查看容器日志、健康状态、挂载和资源占用。
- 在实际执行前预览镜像部署、Compose 部署、更新和清理操作。
- 使用一个轻量级本地工具替代复杂的多服务管理平台。

## 主要功能

- 自动发现 Compose 项目和独立容器。
- 分页查看容器、项目和本地镜像，并按名称、状态、健康状况、类型、标签或收藏筛选。
- 查看容器日志、端口、网络、挂载、重启策略和资源快照。
- 启停、重启、更新、重新部署和保守删除容器或 Compose 项目。
- 通过镜像名称或 Compose YAML 部署新服务，支持部署模板。
- 编辑已发现的 Compose 文件，保存前进行校验和内容对比。
- 检查项目镜像更新，并可配置定时 Webhook 或 ntfy 通知。
- 查看脱敏后的操作历史，导出或恢复配置。
- 清理选中的已停止容器、悬空镜像和未使用的自定义网络。
- 支持浅色、深色主题和仅在页面可见时执行的自动刷新。

## 系统要求

支持 Linux 和 macOS，需要以下组件：

- Git
- Go 1.23 或更高版本
- Docker CLI 和可用的 Docker daemon
- Docker Compose V2，即 `docker compose`

管理脚本可以尝试补齐缺失组件。macOS 使用 Homebrew，Linux 支持 APT、DNF、YUM、Pacman 和 Zypper。安装系统软件或注册系统服务时可能需要输入 `sudo` 密码。

## 快速安装

请使用普通用户执行以下命令，不要在整个脚本前添加 `sudo`：

```bash
curl -fsSL https://raw.githubusercontent.com/ZASENJC/dockertree/main/dockertree.sh -o dockertree.sh
chmod 755 dockertree.sh
./dockertree.sh install
./dockertree.sh start
```

`install` 会从 GitHub 获取源码并构建 Dockertree，`start` 会启动程序并显示访问地址和管理员令牌。安装和启动还会注册自动启动任务：

- Linux：系统级 systemd 服务，在设备启动后运行。
- macOS：LaunchAgent，在当前用户登录后运行。

默认路径如下：

| 内容 | 路径 |
| --- | --- |
| 程序 | `~/.local/bin/dockertree` |
| 配置与数据 | `~/.config/dockertree/` |
| 日志与 PID | `~/.local/state/dockertree/` |
| Web 地址 | `http://<主机地址>:27680` |

## 首次使用

1. 执行 `./dockertree.sh start`，记下输出中的访问地址和管理员令牌。
2. 用浏览器打开访问地址。
3. 将管理员令牌粘贴到页面右上角并点击 `保存`。
4. 点击 `扫描`，读取当前 Docker 容器、Compose 项目和本地镜像。
5. 打开 `项目` 页面，确认新建项目根目录和附加扫描目录是否符合你的环境。

Dockertree 默认监听 `0.0.0.0:27680`，同一局域网中的设备可能可以访问。若只需本机使用，请参考[限制为本机访问](#限制为本机访问)。

## 页面说明

| 页面 | 用途 |
| --- | --- |
| `概览` | 查看项目、容器、镜像数量，需要关注的异常状态和资源快照。 |
| `容器` | 查看容器详情、日志和挂载，并执行启停、重启、更新、重新部署或删除。 |
| `项目` | 管理 Compose/独立项目、项目目录、标签、收藏、更新和重新部署。 |
| `镜像` | 查看或删除本地镜像，也可将镜像带入部署页面。 |
| `部署` | 通过镜像或 Compose YAML 部署服务，并保存个人模板。 |
| `历史` | 按目标或失败状态查询 Docker 操作记录。 |
| `维护` | 检查更新、配置通知、执行安全清理及备份恢复。 |

所有 Docker 命令的结果都会显示在页面顶部的全局操作输出区域。操作失败时，界面会展示实际命令、退出码和 Docker 输出，便于定位问题。

## 常用操作

### 扫描已有 Compose 项目

Dockertree 会读取 `docker compose ls` 和容器上的 Compose 标签，同时递归扫描已配置目录中的以下文件：

- `compose.yaml`
- `compose.yml`
- `docker-compose.yaml`
- `docker-compose.yml`

在 `项目` 页面填写新建项目根目录和附加扫描目录，每行一个绝对路径，然后点击 `保存并扫描`。扫描只建立索引，不会移动或改写原文件。

默认的新建项目根目录是 `/opt`。如果运行 Dockertree 的用户不能写入 `/opt`，建议改为该用户可写的目录，例如 `$HOME/docker-projects`，或只为专用目录授予必要权限。

### 使用镜像部署容器

1. 打开 `部署` 页面并选择 `镜像部署`。
2. 输入镜像名称，例如 `redis:7`，也可以先搜索 Docker Hub。
3. 按需配置端口、环境变量、挂载、网络和重启策略。
4. 点击 `预览命令` 检查即将执行的 `docker run` 命令。
5. 确认后点击 `部署`。

容器名称会根据镜像自动生成，也可以在高级选项中覆盖。

### 新建或编辑 Compose 项目

在 `部署` 页面选择 `Compose 部署`，填写项目名称和 Compose YAML。项目路径会自动生成为：

```text
<projectRoot>/<项目名>/compose.yml
```

你可以选择：

- `预览`：校验并展示规范化后的 Compose 内容，不写文件、不启动容器。
- `仅保存`：确认后写入 Compose 文件，但不启动或更新服务。
- `保存并部署`：写入文件并执行 `docker compose up -d`。

编辑已有项目时，Dockertree 会继续使用原文件路径。多 Compose 文件项目会保留原来的文件顺序。如果预览后文件被其他程序修改，Dockertree 会拒绝用过期内容覆盖它。

### 更新、重新部署和删除

`更新` 会先拉取最新镜像，再执行 `docker compose up -d` 应用更新，不会隐式重新构建本地镜像。`重新部署` 不拉取镜像，而是使用当前本地镜像执行 `docker compose up -d --force-recreate`；在容器详情中重新部署时，只重建对应的 Compose 服务并添加 `--no-deps`。更新检查使用 Compose dry-run，不会实际拉取镜像。

删除操作需要浏览器确认，并遵循以下边界：

- 删除容器：执行 `docker rm -f <id>`。
- 删除项目：只执行 `docker compose down`，不会添加 `-v`，命名数据卷会保留。
- 删除镜像：执行 `docker rmi <ref>`。
- 安全清理：不会执行 `docker system prune`，也不会删除数据卷。

删除容器或项目后，Dockertree 会重新扫描 Docker 并刷新清单。

## 管理命令

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

| 命令 | 说明 |
| --- | --- |
| `doctor` | 只检查 Git、Go、Docker 和 Compose，不安装软件或启动 Docker。 |
| `install` | 获取源码、构建程序、初始化配置并注册自动启动。 |
| `update` | 获取最新源码并构建；保留现有配置，运行中的实例会自动重启。 |
| `start` | 检查 Docker、处理端口冲突并启动 Dockertree。 |
| `status` | 显示当前运行状态。 |
| `restart` | 重启 Dockertree。 |
| `stop` | 停止 Dockertree。 |
| `uninstall` | 删除程序和运行状态，保留配置与管理脚本。 |

彻底删除程序和全部配置：

```bash
./dockertree.sh uninstall --purge --yes
```

更新失败时，脚本会保留当前二进制和正在运行的进程。运行不带参数的 `./dockertree.sh` 可以查看完整帮助。

## 配置与数据

默认配置目录为 `~/.config/dockertree/`：

| 文件 | 内容 |
| --- | --- |
| `config.yaml` | 监听地址、管理员令牌、项目目录、更新与通知设置。 |
| `inventory.yaml` | 已发现项目、Compose 路径、服务、端口、标签和收藏。 |
| `templates.yaml` | 个人镜像和 Compose 部署模板。 |
| `logs/operations.jsonl` | 仅追加写入的脱敏操作历史。 |

导出的配置备份不会包含管理员令牌。恢复备份时，当前管理员令牌、监听地址和局域网访问策略会被保留。

### 限制为本机访问

默认配置允许局域网访问。若不需要远程访问，请编辑 `~/.config/dockertree/config.yaml`：

```yaml
listenAddr: 127.0.0.1:27680
allowLan: false
```

修改后执行：

```bash
./dockertree.sh restart
```

管理员令牌可以控制 Docker 操作，请像密码一样保管。Dockertree 不会在容器详情 API 中返回容器环境变量，但仍应避免把管理端口直接暴露到互联网。

### 自定义路径和端口

以下环境变量可以覆盖默认设置：

| 变量 | 用途 |
| --- | --- |
| `DOCKERTREE_INSTALL_DIR` | 二进制安装目录。 |
| `DOCKERTREE_STATE_DIR` | 日志和 PID 目录。 |
| `DOCKERTREE_CONFIG_DIR` | 配置与长期数据目录。 |
| `DOCKERTREE_PORT` | 在非交互环境中指定并保存监听端口。 |
| `DOCKERTREE_AUTO_INSTALL=0` | 禁止脚本自动安装缺失组件。 |
| `DOCKERTREE_GITHUB_REPOSITORY` | 覆盖源码仓库地址。 |
| `DOCKERTREE_GITHUB_REF` | 覆盖构建使用的分支或标签，默认 `main`。 |

例如：

```bash
DOCKERTREE_PORT=28680 ./dockertree.sh start
```

如果默认端口已被占用，交互式启动会提示选择新端口并写入 `config.yaml`。

## 常见问题

### 页面能打开，但没有任何数据

先确认已在页面中保存正确的管理员令牌，然后点击 `扫描`。还可以运行：

```bash
./dockertree.sh status
docker info
docker compose version
```

### 启动时提示无法访问 Docker

确认 Docker Desktop 或 Docker 服务已经启动。Linux 用户还需要确保当前账号有权访问 Docker socket；完成用户组调整后通常需要重新登录。

### 扫描不到已有 Compose 项目

在 `项目` 页面把项目所在的绝对路径加入扫描目录并点击 `保存并扫描`。运行 Dockertree 的用户必须能够遍历目录并读取 Compose 文件。符号链接文件会被拒绝。

### 无法在 `/opt` 新建项目

Linux 上的 `/opt` 通常不可由普通用户写入。请把新建项目根目录改为可写的绝对路径，或预先创建一个专用子目录并授予运行用户最小必要权限。

### 端口被占用

重新运行 `./dockertree.sh start` 并按提示选择端口，或者显式指定：

```bash
DOCKERTREE_PORT=28680 ./dockertree.sh start
```

### 之前使用 `sudo` 安装过

不要执行 `sudo ./dockertree.sh install`、`update`、`start` 或 `restart`，否则程序和配置会落到 `/root`。迁移旧的 root 安装时，可以先停止并卸载旧实例，再以普通用户重新安装：

```bash
sudo ./dockertree.sh stop
sudo ./dockertree.sh uninstall
./dockertree.sh install
./dockertree.sh start
```

## 从源码运行

```bash
git clone https://github.com/ZASENJC/dockertree.git
cd dockertree
go run ./cmd/dockertree
```

服务器启动时会输出访问地址、配置目录和管理员令牌。也可以使用独立配置目录进行测试：

```bash
DOCKERTREE_CONFIG_DIR=/tmp/dockertree-dev go run ./cmd/dockertree
```

构建单个可执行文件：

```bash
go build -o dockertree ./cmd/dockertree
./dockertree
```

## 开发与测试

```bash
go test ./...
go test ./... -cover
go build -o dockertree ./cmd/dockertree
```

测试使用模拟 Docker runner/executor，不会修改真实 Docker 容器。
