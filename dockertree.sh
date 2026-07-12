#!/bin/sh

set -u

SCRIPT_DIR=$(CDPATH= cd "$(dirname "$0")" && pwd)
SOURCE_DIR=${DOCKERTREE_SOURCE_DIR:-$SCRIPT_DIR}
GITHUB_REPOSITORY=${DOCKERTREE_GITHUB_REPOSITORY:-https://github.com/ZASENJC/dockertree.git}
GITHUB_REF=${DOCKERTREE_GITHUB_REF:-main}

if [ -z "${HOME:-}" ]; then
  echo "错误: HOME 环境变量未设置。" >&2
  exit 1
fi

INSTALL_DIR=${DOCKERTREE_INSTALL_DIR:-${XDG_BIN_HOME:-$HOME/.local/bin}}
STATE_DIR=${DOCKERTREE_STATE_DIR:-${XDG_STATE_HOME:-$HOME/.local/state}/dockertree}
CONFIG_DIR=${DOCKERTREE_CONFIG_DIR:-${XDG_CONFIG_HOME:-$HOME/.config}/dockertree}
BINARY=$INSTALL_DIR/dockertree
PID_FILE=$STATE_DIR/dockertree.pid
LOG_FILE=$STATE_DIR/dockertree.log
START_TIMEOUT=${DOCKERTREE_START_TIMEOUT:-1}
STOP_TIMEOUT=${DOCKERTREE_STOP_TIMEOUT:-10}
AUTO_INSTALL=${DOCKERTREE_AUTO_INSTALL:-1}
MIN_GO_MAJOR=1
MIN_GO_MINOR=23

usage() {
  cat <<EOF
用法: $0 <命令> [选项]

命令:
  doctor                  检查 Git、Go、Docker 和 Compose
  install                 编译并安装 Dockertree
  update                  从 GitHub 更新 Dockertree
  start                   后台启动 Dockertree
  stop                    停止 Dockertree
  restart                 重启 Dockertree
  status                  查看运行状态
  uninstall               卸载程序，保留配置
  uninstall --purge --yes 卸载程序并删除全部配置
  help                    显示帮助

默认路径:
  程序: $BINARY
  配置: $CONFIG_DIR
  日志: $LOG_FILE

可通过 DOCKERTREE_INSTALL_DIR、DOCKERTREE_STATE_DIR、
DOCKERTREE_CONFIG_DIR 和 DOCKERTREE_SOURCE_DIR 覆盖默认路径。
设置 DOCKERTREE_AUTO_INSTALL=0 可关闭缺失环境的自动安装。
EOF
}

fail() {
  echo "错误: $*" >&2
  exit 1
}

platform_name() {
  case "$(uname -s 2>/dev/null)" in
    Darwin) printf '%s\n' "macOS" ;;
    Linux) printf '%s\n' "Linux" ;;
    *) printf '%s\n' "unsupported" ;;
  esac
}

go_version_supported() {
  command -v go >/dev/null 2>&1 || return 1
  version_output=$(go version 2>/dev/null) || return 1
  version=""
  for token in $version_output; do
    case "$token" in
      go[0-9]*) version=${token#go}; break ;;
    esac
  done
  [ -n "$version" ] || return 1
  major=${version%%.*}
  remainder=${version#*.}
  minor=${remainder%%.*}
  case "$major" in
    ''|*[!0-9]*) return 1 ;;
  esac
  case "$minor" in
    ''|*[!0-9]*) return 1 ;;
  esac
  [ "$major" -gt "$MIN_GO_MAJOR" ] || {
    [ "$major" -eq "$MIN_GO_MAJOR" ] && [ "$minor" -ge "$MIN_GO_MINOR" ]
  }
}

component_ready() {
  case "$1" in
    git) command -v git >/dev/null 2>&1 ;;
    go) go_version_supported ;;
    docker) command -v docker >/dev/null 2>&1 ;;
    compose) command -v docker >/dev/null 2>&1 && docker compose version >/dev/null 2>&1 ;;
    *) return 1 ;;
  esac
}

component_label() {
  case "$1" in
    git) printf '%s' "Git" ;;
    go) printf 'Go %s.%s+' "$MIN_GO_MAJOR" "$MIN_GO_MINOR" ;;
    docker) printf '%s' "Docker CLI" ;;
    compose) printf '%s' "Docker Compose" ;;
  esac
}

missing_components() {
  missing=""
  for component in "$@"; do
    if ! component_ready "$component"; then
      missing="$missing $component"
    fi
  done
  printf '%s\n' "${missing# }"
}

runtime_report() {
  for component in "$@"; do
    label=$(component_label "$component")
    if component_ready "$component"; then
      echo "[就绪] $label"
    else
      echo "[缺失] $label"
    fi
  done
}

run_privileged() {
  if [ "$(id -u 2>/dev/null)" = "0" ]; then
    "$@"
    return $?
  fi
  if command -v sudo >/dev/null 2>&1; then
    sudo "$@"
    return $?
  fi
  echo "错误: 安装系统依赖需要 root 权限或 sudo。" >&2
  return 1
}

detect_package_manager() {
  detected_platform=$1
  if [ "$detected_platform" = "macOS" ]; then
    if command -v brew >/dev/null 2>&1; then
      printf '%s\n' "brew"
      return 0
    fi
    return 1
  fi
  for manager in apt-get dnf yum pacman zypper; do
    if command -v "$manager" >/dev/null 2>&1; then
      printf '%s\n' "$manager"
      return 0
    fi
  done
  return 1
}

prepare_package_manager() {
  case "$1" in
    apt-get) run_privileged apt-get update ;;
    *) return 0 ;;
  esac
}

install_component() {
  manager=$1
  component=$2
  case "$manager:$component" in
    brew:git) brew install git ;;
    brew:go) brew install go ;;
    brew:docker|brew:compose) brew install --cask docker ;;
    apt-get:git) run_privileged apt-get install -y git ;;
    apt-get:go) run_privileged apt-get install -y golang-go ;;
    apt-get:docker) run_privileged apt-get install -y docker.io ;;
    apt-get:compose)
      run_privileged apt-get install -y docker-compose-v2 ||
        run_privileged apt-get install -y docker-compose-plugin
      ;;
    dnf:git|yum:git) run_privileged "$manager" install -y git ;;
    dnf:go|yum:go) run_privileged "$manager" install -y golang ;;
    dnf:docker|yum:docker) run_privileged "$manager" install -y docker ;;
    dnf:compose|yum:compose) run_privileged "$manager" install -y docker-compose-plugin ;;
    pacman:git) run_privileged pacman -Sy --needed --noconfirm git ;;
    pacman:go) run_privileged pacman -Sy --needed --noconfirm go ;;
    pacman:docker) run_privileged pacman -Sy --needed --noconfirm docker ;;
    pacman:compose) run_privileged pacman -Sy --needed --noconfirm docker-compose ;;
    zypper:git) run_privileged zypper --non-interactive install git ;;
    zypper:go) run_privileged zypper --non-interactive install go ;;
    zypper:docker) run_privileged zypper --non-interactive install docker ;;
    zypper:compose) run_privileged zypper --non-interactive install docker-compose ;;
    *) return 1 ;;
  esac
}

try_start_docker() {
  command -v docker >/dev/null 2>&1 || return 0
  if docker info >/dev/null 2>&1; then
    return 0
  fi
  detected_platform=$1
  if [ "$detected_platform" = "Linux" ] && command -v systemctl >/dev/null 2>&1; then
    echo "正在启动 Docker 服务..."
    run_privileged systemctl enable --now docker >/dev/null 2>&1 || true
  elif [ "$detected_platform" = "macOS" ] && command -v open >/dev/null 2>&1; then
    echo "正在启动 Docker Desktop..."
    open -gja Docker >/dev/null 2>&1 || true
  fi
  if ! docker info >/dev/null 2>&1; then
    echo "提示: Docker CLI 已安装，但 Docker daemon 尚未运行；启动 Docker 后再执行扫描。"
  fi
}

report_docker_daemon() {
  command -v docker >/dev/null 2>&1 || return 0
  if ! docker info >/dev/null 2>&1; then
    echo "提示: Docker daemon 尚未运行；启动 Docker 后再执行扫描。"
  fi
}

validate_github_settings() {
  case "$GITHUB_REPOSITORY" in
    https://github.com/*.git) ;;
    *) fail "GitHub 仓库必须是 https://github.com/...git 地址" ;;
  esac
  case "$GITHUB_REF" in
    ''|-*|*..*|*[!A-Za-z0-9._/-]*) fail "无效的 GitHub 分支或标签: $GITHUB_REF" ;;
  esac
}

ensure_runtime() {
  case "$AUTO_INSTALL" in
    0|1) ;;
    *) fail "DOCKERTREE_AUTO_INSTALL 必须是 0 或 1" ;;
  esac
  detected_platform=$(platform_name)
  arch=$(uname -m 2>/dev/null || printf '%s' "unknown")
  if [ "$detected_platform" = "unsupported" ]; then
    fail "不支持的操作系统: $(uname -s 2>/dev/null || printf '%s' "unknown")"
  fi
  echo "检测到 $detected_platform ($arch)。"
  missing=$(missing_components "$@")
  if [ -z "$missing" ]; then
    runtime_report "$@"
    echo "运行环境已就绪。"
    try_start_docker "$detected_platform"
    return 0
  fi

  runtime_report "$@"
  if [ "$AUTO_INSTALL" = "0" ]; then
    fail "运行环境不完整，自动安装已关闭"
  fi
  manager=$(detect_package_manager "$detected_platform") || {
    if [ "$detected_platform" = "macOS" ]; then
      fail "未找到 Homebrew，无法自动补全运行环境；请先安装 Homebrew"
    fi
    fail "未找到受支持的包管理器，无法自动补全运行环境"
  }
  echo "正在自动补全运行环境（${manager}）..."
  prepare_package_manager "$manager" || fail "更新软件包索引失败"
  for component in $missing; do
    if component_ready "$component"; then
      continue
    fi
    label=$(component_label "$component")
    echo "正在安装 ${label}..."
    install_component "$manager" "$component" || fail "安装 $label 失败"
  done
  remaining=$(missing_components "$@")
  if [ -n "$remaining" ]; then
    runtime_report "$@"
    fail "自动安装完成后运行环境仍不完整: $remaining"
  fi
  runtime_report "$@"
  echo "运行环境已就绪。"
  try_start_docker "$detected_platform"
}

doctor_app() {
  detected_platform=$(platform_name)
  arch=$(uname -m 2>/dev/null || printf '%s' "unknown")
  if [ "$detected_platform" = "unsupported" ]; then
    echo "不支持的操作系统: $(uname -s 2>/dev/null || printf '%s' "unknown")" >&2
    return 1
  fi
  echo "检测到 $detected_platform ($arch)。"
  runtime_report git go docker compose
  missing=$(missing_components git go docker compose)
  if [ -n "$missing" ]; then
    echo "运行环境不完整: $missing" >&2
    return 1
  fi
  echo "运行环境已就绪。"
  report_docker_daemon
}

validate_timeout() {
  name=$1
  value=$2
  case "$value" in
    ''|*[!0-9]*) fail "$name 必须是非负整数" ;;
  esac
}

process_matches_binary() {
  pid=$1
  if ! kill -0 "$pid" 2>/dev/null; then
    return 1
  fi
  command_line=$(ps -p "$pid" -o command= 2>/dev/null) || return 1
  case " $command_line " in
    *" $BINARY "*) return 0 ;;
  esac
  return 1
}

running_pid() {
  if [ ! -f "$PID_FILE" ]; then
    return 1
  fi

  pid=$(sed -n '1p' "$PID_FILE" 2>/dev/null)
  case "$pid" in
    ''|*[!0-9]*)
      rm -f "$PID_FILE"
      return 1
      ;;
  esac

  if process_matches_binary "$pid"; then
    printf '%s\n' "$pid"
    return 0
  fi

  rm -f "$PID_FILE"
  return 1
}

install_app() {
  ensure_runtime git go docker compose
  mkdir -p "$INSTALL_DIR" "$STATE_DIR" || fail "无法创建安装目录"
  install_source=$SOURCE_DIR
  install_checkout=""
  if [ ! -f "$install_source/go.mod" ] || [ ! -d "$install_source/cmd/dockertree" ]; then
    validate_github_settings
    install_checkout=$(mktemp -d "$STATE_DIR/dockertree-install-XXXXXX") || fail "无法创建安装临时目录"
    install_source=$install_checkout/source
    echo "正在从 GitHub 获取 Dockertree ($GITHUB_REF)..."
    if ! git clone --depth 1 --branch "$GITHUB_REF" --single-branch "$GITHUB_REPOSITORY" "$install_source"; then
      rm -rf "$install_checkout"
      fail "从 GitHub 获取安装源码失败"
    fi
    if [ ! -f "$install_source/go.mod" ] || [ ! -d "$install_source/cmd/dockertree" ]; then
      rm -rf "$install_checkout"
      fail "GitHub 安装内容不是有效的 Dockertree 源码"
    fi
  fi

  build_tmp=$INSTALL_DIR/.dockertree-build-$$
  trap 'rm -f "$build_tmp"; if [ -n "$install_checkout" ]; then rm -rf "$install_checkout"; fi' EXIT HUP INT TERM

  echo "正在编译 Dockertree..."
  if ! (cd "$install_source" && go build -trimpath -ldflags "-s -w" -o "$build_tmp" ./cmd/dockertree); then
    fail "编译失败"
  fi
  chmod 755 "$build_tmp" || fail "无法设置程序权限"
  mv -f "$build_tmp" "$BINARY" || fail "无法安装程序到 $BINARY"
  if [ -n "$install_checkout" ]; then
    rm -rf "$install_checkout"
  fi

  trap - EXIT HUP INT TERM
  echo "安装完成: $BINARY"
  echo "运行 '$0 start' 启动服务。"
}

update_app() {
  ensure_runtime git go docker compose
  if [ ! -x "$BINARY" ]; then
    fail "尚未安装，请先运行 '$0 install'"
  fi
  validate_github_settings

  mkdir -p "$INSTALL_DIR" "$STATE_DIR" || fail "无法创建更新目录"
  update_dir=$(mktemp -d "$STATE_DIR/dockertree-update-XXXXXX") || fail "无法创建更新临时目录"
  update_source=$update_dir/source
  update_candidate=$update_dir/dockertree
  install_tmp=$INSTALL_DIR/.dockertree-update-$$
  previous_binary=$INSTALL_DIR/.dockertree-previous-$$
  trap 'rm -rf "$update_dir"; rm -f "$install_tmp"' EXIT HUP INT TERM

  echo "正在从 GitHub 获取 Dockertree ($GITHUB_REF)..."
  if ! git clone --depth 1 --branch "$GITHUB_REF" --single-branch "$GITHUB_REPOSITORY" "$update_source"; then
    fail "从 GitHub 获取更新失败"
  fi
  if [ ! -f "$update_source/go.mod" ] || [ ! -d "$update_source/cmd/dockertree" ]; then
    fail "GitHub 更新内容不是有效的 Dockertree 源码"
  fi

  echo "正在编译 GitHub 更新..."
  if ! (cd "$update_source" && go build -trimpath -ldflags "-s -w" -o "$update_candidate" ./cmd/dockertree); then
    fail "编译失败，已保留当前版本"
  fi
  chmod 755 "$update_candidate" || fail "无法设置更新程序权限"
  cp "$update_candidate" "$install_tmp" || fail "无法暂存更新程序"
  chmod 755 "$install_tmp" || fail "无法设置更新程序权限"

  was_running=false
  if running_pid >/dev/null; then
    was_running=true
    stop_app || return $?
  fi

  if ! mv "$BINARY" "$previous_binary"; then
    if [ "$was_running" = true ]; then
      start_app || true
    fi
    fail "无法备份当前程序"
  fi
  if ! mv "$install_tmp" "$BINARY"; then
    mv "$previous_binary" "$BINARY" 2>/dev/null || true
    if [ "$was_running" = true ]; then
      start_app || true
    fi
    fail "无法安装 GitHub 更新"
  fi

  if [ "$was_running" = true ] && ! start_app; then
    echo "新版启动失败，正在回滚当前版本。" >&2
    rm -f "$BINARY"
    if ! mv "$previous_binary" "$BINARY"; then
      fail "回滚失败，旧程序保留在 $previous_binary"
    fi
    if ! start_app; then
      echo "错误: 已恢复旧程序，但服务启动失败，请检查日志: $LOG_FILE" >&2
    fi
    return 1
  fi

  rm -f "$previous_binary"
  rm -rf "$update_dir"
  trap - EXIT HUP INT TERM
  echo "Dockertree GitHub 更新完成。"
  if [ "$was_running" != true ]; then
    echo "服务当前未运行，可执行 '$0 start' 启动。"
  fi
}

start_app() {
  ensure_runtime docker compose
  validate_timeout DOCKERTREE_START_TIMEOUT "$START_TIMEOUT"
  if pid=$(running_pid); then
    echo "Dockertree 已在运行，PID: $pid"
    return 0
  fi
  if [ ! -x "$BINARY" ]; then
    fail "尚未安装，请先运行 '$0 install'"
  fi

  mkdir -p "$STATE_DIR" "$CONFIG_DIR" || fail "无法创建运行目录"
  touch "$LOG_FILE" || fail "无法写入日志: $LOG_FILE"
  chmod 600 "$LOG_FILE" 2>/dev/null || true

  nohup env DOCKERTREE_CONFIG_DIR="$CONFIG_DIR" "$BINARY" >>"$LOG_FILE" 2>&1 &
  pid=$!
  printf '%s\n' "$pid" >"$PID_FILE" || fail "无法写入 PID 文件"
  chmod 600 "$PID_FILE" 2>/dev/null || true

  elapsed=0
  while [ "$elapsed" -lt "$START_TIMEOUT" ]; do
    sleep 1
    if ! process_matches_binary "$pid"; then
      rm -f "$PID_FILE"
      echo "错误: Dockertree 启动失败。" >&2
      if [ -s "$LOG_FILE" ]; then
        echo "最近日志:" >&2
        tail -n 20 "$LOG_FILE" >&2
      fi
      return 1
    fi
    elapsed=$((elapsed + 1))
  done

  if ! process_matches_binary "$pid"; then
    rm -f "$PID_FILE"
    echo "错误: Dockertree 启动失败。" >&2
    if [ -s "$LOG_FILE" ]; then
      echo "最近日志:" >&2
      tail -n 20 "$LOG_FILE" >&2
    fi
    return 1
  fi

  echo "Dockertree 已启动，PID: $pid"
  echo "日志: $LOG_FILE"
}

stop_app() {
  validate_timeout DOCKERTREE_STOP_TIMEOUT "$STOP_TIMEOUT"
  if ! pid=$(running_pid); then
    echo "Dockertree 未运行。"
    return 0
  fi

  if ! kill "$pid" 2>/dev/null; then
    rm -f "$PID_FILE"
    fail "无法停止 PID $pid"
  fi

  elapsed=0
  while process_matches_binary "$pid"; do
    if [ "$elapsed" -ge "$STOP_TIMEOUT" ]; then
      fail "等待 PID $pid 停止超时，请检查进程状态"
    fi
    sleep 1
    elapsed=$((elapsed + 1))
  done

  rm -f "$PID_FILE"
  echo "Dockertree 已停止。"
}

status_app() {
  if pid=$(running_pid); then
    echo "Dockertree 正在运行，PID: $pid"
    echo "程序: $BINARY"
    echo "配置: $CONFIG_DIR"
    echo "日志: $LOG_FILE"
    return 0
  fi

  echo "Dockertree 未运行。"
  return 3
}

uninstall_app() {
  purge=false
  confirmed=false
  shift
  while [ "$#" -gt 0 ]; do
    case "$1" in
      --purge) purge=true ;;
      --yes) confirmed=true ;;
      *)
        echo "错误: 未知选项: $1" >&2
        usage >&2
        return 2
        ;;
    esac
    shift
  done

  if [ "$purge" = true ] && [ "$confirmed" != true ]; then
    echo "错误: 删除配置必须同时传入 --purge --yes。" >&2
    return 2
  fi
  if [ "$purge" != true ] && [ "$confirmed" = true ]; then
    echo "错误: --yes 只能与 --purge 一起使用。" >&2
    return 2
  fi

  stop_app || return $?
  rm -f "$BINARY" "$PID_FILE" "$LOG_FILE" || fail "卸载文件失败"
  rmdir "$STATE_DIR" 2>/dev/null || true

  if [ "$purge" = true ]; then
    case "$CONFIG_DIR" in
      ''|/|"$HOME"|"$INSTALL_DIR"|"$STATE_DIR")
        fail "拒绝删除不安全的配置目录: $CONFIG_DIR"
        ;;
    esac
    rm -rf "$CONFIG_DIR" || fail "删除配置目录失败: $CONFIG_DIR"
    echo "Dockertree 已卸载，配置已删除: $CONFIG_DIR"
    return 0
  fi

  echo "Dockertree 已卸载。"
  echo "配置已保留: $CONFIG_DIR"
  echo "如需彻底删除，请运行 '$0 uninstall --purge --yes'。"
}

command=${1:-}
case "$command" in
  doctor)
    if [ "$#" -ne 1 ]; then
      echo "错误: doctor 不接受选项。" >&2
      usage >&2
      exit 2
    fi
    doctor_app
    ;;
  install)
    if [ "$#" -ne 1 ]; then
      echo "错误: install 不接受选项。" >&2
      usage >&2
      exit 2
    fi
    install_app
    ;;
  update)
    if [ "$#" -ne 1 ]; then
      echo "错误: update 不接受选项。" >&2
      usage >&2
      exit 2
    fi
    update_app
    ;;
  start)
    if [ "$#" -ne 1 ]; then
      echo "错误: start 不接受选项。" >&2
      usage >&2
      exit 2
    fi
    start_app
    ;;
  stop)
    if [ "$#" -ne 1 ]; then
      echo "错误: stop 不接受选项。" >&2
      usage >&2
      exit 2
    fi
    stop_app
    ;;
  restart)
    if [ "$#" -ne 1 ]; then
      echo "错误: restart 不接受选项。" >&2
      usage >&2
      exit 2
    fi
    stop_app && start_app
    ;;
  status)
    if [ "$#" -ne 1 ]; then
      echo "错误: status 不接受选项。" >&2
      usage >&2
      exit 2
    fi
    status_app
    ;;
  uninstall)
    uninstall_app "$@"
    ;;
  help|-h|--help)
    usage
    ;;
  '')
    usage >&2
    exit 2
    ;;
  *)
    echo "错误: 未知命令: $command" >&2
    usage >&2
    exit 2
    ;;
esac
