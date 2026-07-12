package scripts_test

import (
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"
)

type harness struct {
	home       string
	installDir string
	stateDir   string
	configDir  string
	goLog      string
	record     string
	env        []string
}

func TestManagerLifecycle(t *testing.T) {
	h := newHarness(t)

	result := h.run(t, "install")
	if result.exitCode != 0 {
		t.Fatalf("install failed: %s", result.output)
	}
	assertExecutable(t, filepath.Join(h.installDir, "dockertree"))
	goLog, err := os.ReadFile(h.goLog)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(goLog), projectRoot(t)+"|build ") || !strings.Contains(string(goLog), " ./cmd/dockertree") {
		t.Fatalf("install did not build from the repository root: %s", goLog)
	}

	result = h.run(t, "start")
	if result.exitCode != 0 {
		t.Fatalf("start failed: %s", result.output)
	}
	pid := readTrimmed(t, filepath.Join(h.stateDir, "dockertree.pid"))
	if pid == "" {
		t.Fatal("start did not create a pid file")
	}
	waitForFileContent(t, h.record, h.configDir)

	result = h.run(t, "start")
	if result.exitCode != 0 || !strings.Contains(result.output, pid) {
		t.Fatalf("second start should report the existing process: code=%d output=%s", result.exitCode, result.output)
	}
	if got := readTrimmed(t, filepath.Join(h.stateDir, "dockertree.pid")); got != pid {
		t.Fatalf("second start replaced pid %s with %s", pid, got)
	}

	result = h.run(t, "status")
	if result.exitCode != 0 || !strings.Contains(result.output, pid) {
		t.Fatalf("status did not report the running process: code=%d output=%s", result.exitCode, result.output)
	}

	result = h.run(t, "stop")
	if result.exitCode != 0 {
		t.Fatalf("stop failed: %s", result.output)
	}
	if _, err := os.Stat(filepath.Join(h.stateDir, "dockertree.pid")); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("stop left the pid file behind: %v", err)
	}

	result = h.run(t, "stop")
	if result.exitCode != 0 {
		t.Fatalf("stop should be idempotent: %s", result.output)
	}
	result = h.run(t, "status")
	if result.exitCode != 3 {
		t.Fatalf("stopped status code = %d, want 3; output=%s", result.exitCode, result.output)
	}

	if err := os.MkdirAll(h.configDir, 0o700); err != nil {
		t.Fatal(err)
	}
	configMarker := filepath.Join(h.configDir, "keep-me")
	if err := os.WriteFile(configMarker, []byte("config"), 0o600); err != nil {
		t.Fatal(err)
	}
	result = h.run(t, "uninstall")
	if result.exitCode != 0 {
		t.Fatalf("uninstall failed: %s", result.output)
	}
	if _, err := os.Stat(filepath.Join(h.installDir, "dockertree")); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("uninstall left the binary behind: %v", err)
	}
	if _, err := os.Stat(configMarker); err != nil {
		t.Fatalf("uninstall should preserve config: %v", err)
	}
}

func TestManagerUninstallPurgeDeletesConfig(t *testing.T) {
	h := newHarness(t)
	if result := h.run(t, "install"); result.exitCode != 0 {
		t.Fatalf("install failed: %s", result.output)
	}
	if err := os.MkdirAll(h.configDir, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(h.configDir, "config.yaml"), []byte("token: secret"), 0o600); err != nil {
		t.Fatal(err)
	}

	result := h.run(t, "uninstall", "--purge")
	if result.exitCode != 2 || !strings.Contains(result.output, "--yes") {
		t.Fatalf("purge without confirmation should fail: code=%d output=%s", result.exitCode, result.output)
	}
	if _, err := os.Stat(h.configDir); err != nil {
		t.Fatalf("unconfirmed purge changed config: %v", err)
	}

	result = h.run(t, "uninstall", "--purge", "--yes")
	if result.exitCode != 0 {
		t.Fatalf("confirmed purge failed: %s", result.output)
	}
	if _, err := os.Stat(h.configDir); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("confirmed purge left config behind: %v", err)
	}
}

func TestManagerCleansUpFailedAndStaleProcesses(t *testing.T) {
	h := newHarness(t)
	if result := h.run(t, "install"); result.exitCode != 0 {
		t.Fatalf("install failed: %s", result.output)
	}
	h.env = append(h.env, "FAKE_DOCKERTREE_FAIL=1")
	result := h.run(t, "start")
	if result.exitCode == 0 || !strings.Contains(result.output, "fake startup failure") {
		t.Fatalf("failed startup was not reported: code=%d output=%s", result.exitCode, result.output)
	}
	pidFile := filepath.Join(h.stateDir, "dockertree.pid")
	if _, err := os.Stat(pidFile); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("failed startup left a pid file behind: %v", err)
	}

	h.env = removeEnv(h.env, "FAKE_DOCKERTREE_FAIL")
	if err := os.WriteFile(pidFile, []byte("999999\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	result = h.run(t, "status")
	if result.exitCode != 3 {
		t.Fatalf("stale status code = %d, want 3; output=%s", result.exitCode, result.output)
	}
	if _, err := os.Stat(pidFile); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("status left a stale pid file behind: %v", err)
	}
}

func TestManagerRejectsUnknownCommands(t *testing.T) {
	h := newHarness(t)
	result := h.run(t, "unknown")
	if result.exitCode != 2 {
		t.Fatalf("unknown command code = %d, want 2; output=%s", result.exitCode, result.output)
	}
	for _, command := range []string{"install", "update", "start", "stop", "restart", "status", "uninstall"} {
		if !strings.Contains(result.output, command) {
			t.Fatalf("help output does not mention %q: %s", command, result.output)
		}
	}
}

func TestManagerUpdateInstallsFromGitHubAndRestartsRunningApp(t *testing.T) {
	h := newHarness(t)
	configureFakeGit(t, h)
	if result := h.run(t, "install"); result.exitCode != 0 {
		t.Fatalf("install failed: %s", result.output)
	}
	if result := h.run(t, "start"); result.exitCode != 0 {
		t.Fatalf("start failed: %s", result.output)
	}
	oldPID := readTrimmed(t, filepath.Join(h.stateDir, "dockertree.pid"))

	result := h.run(t, "update")
	if result.exitCode != 0 {
		t.Fatalf("update failed: %s", result.output)
	}
	gitLog := readTrimmed(t, filepath.Join(h.home, "git.log"))
	for _, want := range []string{"clone", "--depth 1", "--branch main", "--single-branch", "https://github.com/ZASENJC/dockertree.git"} {
		if !strings.Contains(gitLog, want) {
			t.Fatalf("GitHub update command missing %q: %s", want, gitLog)
		}
	}
	newPID := readTrimmed(t, filepath.Join(h.stateDir, "dockertree.pid"))
	if newPID == "" || newPID == oldPID {
		t.Fatalf("update should restart the running app: old=%q new=%q output=%s", oldPID, newPID, result.output)
	}
	waitForFileContent(t, h.record, h.configDir)
	if !strings.Contains(result.output, "GitHub") || !strings.Contains(result.output, "更新完成") {
		t.Fatalf("update output should identify the GitHub source and completion: %s", result.output)
	}
}

func TestManagerUpdateBuildFailureKeepsInstalledBinaryAndProcess(t *testing.T) {
	h := newHarness(t)
	configureFakeGit(t, h)
	if result := h.run(t, "install"); result.exitCode != 0 {
		t.Fatalf("install failed: %s", result.output)
	}
	if result := h.run(t, "start"); result.exitCode != 0 {
		t.Fatalf("start failed: %s", result.output)
	}
	binaryPath := filepath.Join(h.installDir, "dockertree")
	before, err := os.ReadFile(binaryPath)
	if err != nil {
		t.Fatal(err)
	}
	oldPID := readTrimmed(t, filepath.Join(h.stateDir, "dockertree.pid"))
	h.env = append(h.env, "FAKE_GO_FAIL_UPDATE=1")

	result := h.run(t, "update")
	if result.exitCode == 0 || !strings.Contains(result.output, "编译失败") {
		t.Fatalf("failed GitHub build should fail update: code=%d output=%s", result.exitCode, result.output)
	}
	after, err := os.ReadFile(binaryPath)
	if err != nil {
		t.Fatal(err)
	}
	if string(after) != string(before) {
		t.Fatal("failed update replaced the installed binary")
	}
	if got := readTrimmed(t, filepath.Join(h.stateDir, "dockertree.pid")); got != oldPID {
		t.Fatalf("failed update changed the running process: old=%s new=%s", oldPID, got)
	}
}

func configureFakeGit(t *testing.T, h *harness) {
	t.Helper()
	fakeGit := filepath.Join(h.home, "fake-bin", "git")
	fakeGitBody := `#!/bin/sh
set -eu
printf '%s\n' "$*" >> "${FAKE_GIT_LOG:?}"
target=""
for arg in "$@"; do
  target="$arg"
done
mkdir -p "$target/cmd/dockertree"
printf 'module dockertree\n\ngo 1.23\n' > "$target/go.mod"
printf 'package main\nfunc main() {}\n' > "$target/cmd/dockertree/main.go"
`
	writeExecutable(t, fakeGit, fakeGitBody)
	h.env = append(h.env, "FAKE_GIT_LOG="+filepath.Join(h.home, "git.log"))
}

func newHarness(t *testing.T) *harness {
	t.Helper()
	home := t.TempDir()
	fakeBin := filepath.Join(home, "fake-bin")
	installDir := filepath.Join(home, "install")
	stateDir := filepath.Join(home, "state")
	configDir := filepath.Join(home, "config")
	for _, dir := range []string{fakeBin, stateDir} {
		if err := os.MkdirAll(dir, 0o700); err != nil {
			t.Fatal(err)
		}
	}

	template := filepath.Join(home, "fake-dockertree")
	templateBody := `#!/bin/sh
set -eu
if [ "${FAKE_DOCKERTREE_FAIL:-0}" = "1" ]; then
  echo "fake startup failure" >&2
  exit 17
fi
printf '%s\n' "${DOCKERTREE_CONFIG_DIR:-}" > "${FAKE_DOCKERTREE_RECORD:?}"
trap 'exit 0' TERM INT
while :; do
  sleep 1
done
`
	writeExecutable(t, template, templateBody)

	fakeGo := filepath.Join(fakeBin, "go")
fakeGoBody := `#!/bin/sh
set -eu
printf '%s|%s\n' "$PWD" "$*" >> "${FAKE_GO_LOG:?}"
case "$PWD" in
  *dockertree-update-*/source)
    if [ "${FAKE_GO_FAIL_UPDATE:-0}" = "1" ]; then
      echo "fake update build failure" >&2
      exit 31
    fi
    ;;
esac
output=""
while [ "$#" -gt 0 ]; do
  if [ "$1" = "-o" ]; then
    shift
    output="${1:-}"
  fi
  shift
done
if [ -z "$output" ]; then
  echo "missing -o" >&2
  exit 2
fi
cp "${FAKE_DOCKERTREE_TEMPLATE:?}" "$output"
chmod 755 "$output"
`
	writeExecutable(t, fakeGo, fakeGoBody)

	return &harness{
		home:       home,
		installDir: installDir,
		stateDir:   stateDir,
		configDir:  configDir,
		goLog:      filepath.Join(home, "go.log"),
		record:     filepath.Join(home, "dockertree.record"),
		env: []string{
			"HOME=" + home,
			"PATH=" + fakeBin + string(os.PathListSeparator) + os.Getenv("PATH"),
			"DOCKERTREE_INSTALL_DIR=" + installDir,
			"DOCKERTREE_STATE_DIR=" + stateDir,
			"DOCKERTREE_CONFIG_DIR=" + configDir,
			"DOCKERTREE_START_TIMEOUT=1",
			"DOCKERTREE_STOP_TIMEOUT=2",
			"FAKE_GO_LOG=" + filepath.Join(home, "go.log"),
			"FAKE_DOCKERTREE_TEMPLATE=" + template,
			"FAKE_DOCKERTREE_RECORD=" + filepath.Join(home, "dockertree.record"),
		},
	}
}

type commandResult struct {
	output   string
	exitCode int
}

func (h *harness) run(t *testing.T, args ...string) commandResult {
	t.Helper()
	cmd := exec.Command(managerPath(t), args...)
	cmd.Dir = h.home
	cmd.Env = append(os.Environ(), h.env...)
	output, err := cmd.CombinedOutput()
	if err == nil {
		return commandResult{output: string(output)}
	}
	var exitErr *exec.ExitError
	if errors.As(err, &exitErr) {
		return commandResult{output: string(output), exitCode: exitErr.ExitCode()}
	}
	t.Fatalf("run manager: %v", err)
	return commandResult{}
}

func managerPath(t *testing.T) string {
	t.Helper()
	return filepath.Join(projectRoot(t), "dockertree.sh")
}

func projectRoot(t *testing.T) string {
	t.Helper()
	_, file, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("resolve test file path")
	}
	root, err := filepath.Abs(filepath.Join(filepath.Dir(file), ".."))
	if err != nil {
		t.Fatal(err)
	}
	return root
}

func writeExecutable(t *testing.T, path, content string) {
	t.Helper()
	if err := os.WriteFile(path, []byte(content), 0o755); err != nil {
		t.Fatal(err)
	}
}

func assertExecutable(t *testing.T, path string) {
	t.Helper()
	info, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if info.Mode()&0o111 == 0 {
		t.Fatalf("%s is not executable: %v", path, info.Mode())
	}
}

func readTrimmed(t *testing.T, path string) string {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	return strings.TrimSpace(string(data))
}

func waitForFileContent(t *testing.T, path, want string) {
	t.Helper()
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		data, err := os.ReadFile(path)
		if err == nil && strings.TrimSpace(string(data)) == want {
			return
		}
		time.Sleep(20 * time.Millisecond)
	}
	t.Fatalf("%s did not contain %q", path, want)
}

func removeEnv(env []string, key string) []string {
	prefix := key + "="
	filtered := env[:0]
	for _, item := range env {
		if !strings.HasPrefix(item, prefix) {
			filtered = append(filtered, item)
		}
	}
	return filtered
}
