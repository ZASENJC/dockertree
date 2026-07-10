#!/bin/sh

set -u

SCRIPT_DIR=$(CDPATH= cd "$(dirname "$0")" && pwd)
SOURCE_DIR=${DOCKERTREE_SOURCE_DIR:-$SCRIPT_DIR}

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

usage() {
  cat <<EOF
用法: $0 <命令> [选项]

命令:
  install                 编译并安装 Dockertree
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
EOF
}

fail() {
  echo "错误: $*" >&2
  exit 1
}

require_command() {
  if ! command -v "$1" >/dev/null 2>&1; then
    fail "缺少命令: $1"
  fi
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
  require_command go
  if [ ! -f "$SOURCE_DIR/go.mod" ] || [ ! -d "$SOURCE_DIR/cmd/dockertree" ]; then
    fail "找不到 Dockertree 源码目录: $SOURCE_DIR"
  fi

  mkdir -p "$INSTALL_DIR" "$STATE_DIR" || fail "无法创建安装目录"
  build_tmp=$INSTALL_DIR/.dockertree-build-$$
  trap 'rm -f "$build_tmp"' EXIT HUP INT TERM

  echo "正在编译 Dockertree..."
  if ! (cd "$SOURCE_DIR" && go build -trimpath -ldflags "-s -w" -o "$build_tmp" ./cmd/dockertree); then
    fail "编译失败"
  fi
  chmod 755 "$build_tmp" || fail "无法设置程序权限"
  mv -f "$build_tmp" "$BINARY" || fail "无法安装程序到 $BINARY"

  trap - EXIT HUP INT TERM
  echo "安装完成: $BINARY"
  echo "运行 '$0 start' 启动服务。"
}

start_app() {
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
  install)
    if [ "$#" -ne 1 ]; then
      echo "错误: install 不接受选项。" >&2
      usage >&2
      exit 2
    fi
    install_app
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
