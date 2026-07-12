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
	manager    string
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
	for _, want := range []string{"访问地址: http://0.0.0.0:27680", "Admin token: test-admin-token"} {
		if !strings.Contains(result.output, want) {
			t.Fatalf("start output missing %q: %s", want, result.output)
		}
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

func TestManagerInstallAndStartEnableLinuxAutostart(t *testing.T) {
	h := newHarness(t)
	fakeBin := filepath.Join(h.home, "fake-bin")
	writeExecutable(t, filepath.Join(fakeBin, "uname"), "#!/bin/sh\nif [ \"${1:-}\" = \"-m\" ]; then echo x86_64; else echo Linux; fi\n")
	writeExecutable(t, filepath.Join(fakeBin, "id"), "#!/bin/sh\ncase \"${1:-}\" in -u) echo 0 ;; -un) echo dockertree-test ;; *) exit 2 ;; esac\n")
	systemctlLog := filepath.Join(h.home, "systemctl.log")
	writeExecutable(t, filepath.Join(fakeBin, "systemctl"), "#!/bin/sh\nprintf '%s\\n' \"$*\" >> \"${FAKE_SYSTEMCTL_LOG:?}\"\n")
	systemdDir := filepath.Join(h.home, "systemd")
	h.env = append(h.env, "FAKE_SYSTEMCTL_LOG="+systemctlLog, "DOCKERTREE_SYSTEMD_UNIT_DIR="+systemdDir)

	result := h.run(t, "install")
	if result.exitCode != 0 {
		t.Fatalf("install failed: %s", result.output)
	}
	unitPath := filepath.Join(systemdDir, "dockertree.service")
	unit := readTrimmed(t, unitPath)
	for _, want := range []string{h.managerPath(t), "start", "User=dockertree-test", "Environment=\"HOME=" + h.home + "\"", "WantedBy=multi-user.target"} {
		if !strings.Contains(unit, want) {
			t.Fatalf("systemd unit missing %q: %s", want, unit)
		}
	}
	if log := readTrimmed(t, systemctlLog); !strings.Contains(log, "daemon-reload") || !strings.Contains(log, "enable dockertree.service") || strings.Contains(log, "--user") {
		t.Fatalf("install did not enable the system service: %s", log)
	}
	if !strings.Contains(result.output, "已启用设备重启后自动启动") {
		t.Fatalf("install did not report autostart: %s", result.output)
	}

	if err := os.Remove(unitPath); err != nil {
		t.Fatal(err)
	}
	if err := os.Remove(systemctlLog); err != nil {
		t.Fatal(err)
	}
	result = h.run(t, "start")
	if result.exitCode != 0 {
		t.Fatalf("start failed: %s", result.output)
	}
	if _, err := os.Stat(unitPath); err != nil {
		t.Fatalf("start did not restore the systemd unit: %v", err)
	}
	if log := readTrimmed(t, systemctlLog); !strings.Contains(log, "enable dockertree.service") || strings.Contains(log, "--user enable") {
		t.Fatalf("start did not re-enable the system service: %s", log)
	}

	if result := h.run(t, "uninstall"); result.exitCode != 0 {
		t.Fatalf("uninstall failed: %s", result.output)
	}
	if _, err := os.Stat(unitPath); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("uninstall left the systemd unit behind: %v", err)
	}
	if log := readTrimmed(t, systemctlLog); !strings.Contains(log, "disable dockertree.service") || strings.Contains(log, "--user disable") {
		t.Fatalf("uninstall did not disable the system service: %s", log)
	}
}

func TestManagerInstallAndUninstallManageMacOSLaunchAgent(t *testing.T) {
	h := newHarness(t)
	fakeBin := filepath.Join(h.home, "fake-bin")
	writeExecutable(t, filepath.Join(fakeBin, "uname"), "#!/bin/sh\nif [ \"${1:-}\" = \"-m\" ]; then echo arm64; else echo Darwin; fi\n")

	result := h.run(t, "install")
	if result.exitCode != 0 {
		t.Fatalf("install failed: %s", result.output)
	}
	plistPath := filepath.Join(h.home, "Library", "LaunchAgents", "io.github.zasenjc.dockertree.plist")
	plist := readTrimmed(t, plistPath)
	for _, want := range []string{h.managerPath(t), "<string>start</string>", "<key>RunAtLoad</key>", "<key>SuccessfulExit</key>", "<integer>10</integer>"} {
		if !strings.Contains(plist, want) {
			t.Fatalf("launch agent missing %q: %s", want, plist)
		}
	}
	if !strings.Contains(result.output, "已启用设备重启后自动启动") {
		t.Fatalf("install did not report autostart: %s", result.output)
	}

	if result := h.run(t, "uninstall"); result.exitCode != 0 {
		t.Fatalf("uninstall failed: %s", result.output)
	}
	if _, err := os.Stat(plistPath); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("uninstall left the launch agent behind: %v", err)
	}
}

func TestManagerWithoutCommandShowsHelp(t *testing.T) {
	h := newHarness(t)
	result := h.run(t)
	if result.exitCode != 0 {
		t.Fatalf("manager without arguments should show help successfully: code=%d output=%s", result.exitCode, result.output)
	}
	for _, command := range []string{"doctor", "install", "update", "start", "stop", "restart", "status", "uninstall", "help"} {
		if !strings.Contains(result.output, command) {
			t.Fatalf("help output does not mention %q: %s", command, result.output)
		}
	}
}

func TestManagerPromptsForOccupiedPortDuringInstallAndSavesChoice(t *testing.T) {
	h := newHarness(t)
	h.env = append(h.env, "FAKE_OCCUPIED_PORT=27680")

	result := h.runWithInput(t, "28681\n", "install")
	if result.exitCode != 0 {
		t.Fatalf("install should accept a replacement port: code=%d output=%s", result.exitCode, result.output)
	}
	if !strings.Contains(result.output, "端口 27680 已被占用") {
		t.Fatalf("install should explain the port conflict: %s", result.output)
	}
	config := readTrimmed(t, filepath.Join(h.configDir, "config.yaml"))
	if !strings.Contains(config, "listenAddr: 0.0.0.0:28681") {
		t.Fatalf("install did not save the selected port: %s", config)
	}
}

func TestManagerPromptsForOccupiedPortDuringStartAndSavesChoice(t *testing.T) {
	h := newHarness(t)
	if result := h.run(t, "install"); result.exitCode != 0 {
		t.Fatalf("install failed: %s", result.output)
	}
	h.env = append(h.env, "FAKE_OCCUPIED_PORT=27680")

	result := h.runWithInput(t, "28682\n", "start")
	if result.exitCode != 0 {
		t.Fatalf("start should accept a replacement port: code=%d output=%s", result.exitCode, result.output)
	}
	for _, want := range []string{"端口 27680 已被占用", "访问地址: http://0.0.0.0:28682"} {
		if !strings.Contains(result.output, want) {
			t.Fatalf("start output missing %q: %s", want, result.output)
		}
	}
	config := readTrimmed(t, filepath.Join(h.configDir, "config.yaml"))
	if !strings.Contains(config, "listenAddr: 0.0.0.0:28682") {
		t.Fatalf("start did not save the selected port: %s", config)
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
	for _, command := range []string{"doctor", "install", "update", "start", "stop", "restart", "status", "uninstall"} {
		if !strings.Contains(result.output, command) {
			t.Fatalf("help output does not mention %q: %s", command, result.output)
		}
	}
}

func TestManagerDoctorReportsCompleteRuntime(t *testing.T) {
	h := newHarness(t)
	result := h.run(t, "doctor")
	if result.exitCode != 0 {
		t.Fatalf("doctor failed for complete runtime: %s", result.output)
	}
	for _, want := range []string{"Git", "Go", "Docker CLI", "Docker Compose", "运行环境已就绪"} {
		if !strings.Contains(result.output, want) {
			t.Fatalf("doctor output missing %q: %s", want, result.output)
		}
	}
}

func TestManagerDoctorDoesNotStartDockerDaemon(t *testing.T) {
	h := newHarness(t)
	openLog := filepath.Join(h.home, "open.log")
	writeExecutable(t, filepath.Join(h.home, "fake-bin", "open"), "#!/bin/sh\nprintf '%s\\n' \"$*\" >> \"${FAKE_OPEN_LOG:?}\"\n")
	h.env = append(h.env, "FAKE_DOCKER_INFO_FAIL=1", "FAKE_OPEN_LOG="+openLog)
	result := h.run(t, "doctor")
	if result.exitCode != 0 || !strings.Contains(result.output, "Docker daemon 尚未运行") {
		t.Fatalf("doctor should report an inactive daemon without failing: code=%d output=%s", result.exitCode, result.output)
	}
	if _, err := os.Stat(openLog); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("doctor attempted to start Docker Desktop: %v", err)
	}
}

func TestManagerInstallAutoProvisionsMissingLinuxRuntime(t *testing.T) {
	h := newMissingLinuxRuntimeHarness(t)
	result := h.run(t, "install")
	if result.exitCode != 0 {
		t.Fatalf("install should provision missing runtime: %s", result.output)
	}
	assertExecutable(t, filepath.Join(h.installDir, "dockertree"))
	packageLog := readTrimmed(t, filepath.Join(h.home, "packages.log"))
	for _, want := range []string{"update", "install -y git", "install -y golang-go", "install -y docker.io", "install -y docker-compose-v2"} {
		if !strings.Contains(packageLog, want) {
			t.Fatalf("package manager log missing %q: %s", want, packageLog)
		}
	}
	for _, want := range []string{"检测到 Linux", "正在自动补全运行环境", "运行环境已就绪", "安装完成"} {
		if !strings.Contains(result.output, want) {
			t.Fatalf("install output missing %q: %s", want, result.output)
		}
	}
}

func TestManagerInstallFetchesGitHubSourceOutsideCheckout(t *testing.T) {
	h := newHarness(t)
	configureFakeGit(t, h)
	h.env = append(h.env, "DOCKERTREE_SOURCE_DIR="+filepath.Join(h.home, "missing-source"))
	result := h.run(t, "install")
	if result.exitCode != 0 {
		t.Fatalf("install should fetch source from GitHub: %s", result.output)
	}
	gitLog := readTrimmed(t, filepath.Join(h.home, "git.log"))
	for _, want := range []string{"clone", "--depth 1", "--branch main", "https://github.com/ZASENJC/dockertree.git"} {
		if !strings.Contains(gitLog, want) {
			t.Fatalf("GitHub install command missing %q: %s", want, gitLog)
		}
	}
	if !strings.Contains(result.output, "正在从 GitHub 获取 Dockertree") {
		t.Fatalf("install should explain its GitHub source: %s", result.output)
	}
}

func TestStandaloneManagerRefreshesItselfFromGitHub(t *testing.T) {
	h := newHarness(t)
	configureFakeGit(t, h)
	managerDir := filepath.Join(h.home, "manager")
	if err := os.MkdirAll(managerDir, 0o700); err != nil {
		t.Fatal(err)
	}
	managerData, err := os.ReadFile(managerPath(t))
	if err != nil {
		t.Fatal(err)
	}
	h.manager = filepath.Join(managerDir, "dockertree.sh")
	writeExecutable(t, h.manager, string(managerData))
	managerTemplate := filepath.Join(h.home, "github-dockertree.sh")
	writeExecutable(t, managerTemplate, string(managerData)+"\n# refreshed-manager\n")
	h.env = append(h.env, "FAKE_MANAGER_TEMPLATE="+managerTemplate)

	result := h.run(t, "install")
	if result.exitCode != 0 {
		t.Fatalf("standalone install failed: %s", result.output)
	}
	refreshed, err := os.ReadFile(h.manager)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(refreshed), "# refreshed-manager") {
		t.Fatalf("standalone manager did not refresh itself from GitHub: %s", result.output)
	}
	if result := h.run(t, "uninstall"); result.exitCode != 0 {
		t.Fatalf("standalone uninstall failed: %s", result.output)
	}
	assertExecutable(t, h.manager)
}

func TestReadmeDocumentsSingleScriptBootstrap(t *testing.T) {
	data, err := os.ReadFile(filepath.Join(projectRoot(t), "README.md"))
	if err != nil {
		t.Fatal(err)
	}
	readme := string(data)
	for _, want := range []string{
		"-o dockertree.sh",
		"./dockertree.sh install",
		"./dockertree.sh update",
		"./dockertree.sh uninstall",
		"0.0.0.0:27680",
		"DOCKERTREE_PORT",
		"不带参数",
	} {
		if !strings.Contains(readme, want) {
			t.Fatalf("single-script documentation missing %q", want)
		}
	}
	for _, forbidden := range []string{"git clone https://github.com/ZASENJC/dockertree.git", "dockertreectl"} {
		if strings.Contains(readme, forbidden) {
			t.Fatalf("single-script documentation should not require %q", forbidden)
		}
	}
}

func TestManagerCanDisableAutomaticRuntimeProvisioning(t *testing.T) {
	h := newMissingLinuxRuntimeHarness(t)
	h.env = append(h.env, "DOCKERTREE_AUTO_INSTALL=0")
	result := h.run(t, "install")
	if result.exitCode == 0 || !strings.Contains(result.output, "自动安装已关闭") {
		t.Fatalf("disabled provisioning should report missing runtime: code=%d output=%s", result.exitCode, result.output)
	}
	if _, err := os.Stat(filepath.Join(h.home, "packages.log")); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("disabled provisioning invoked the package manager: %v", err)
	}
}

func TestStartAndUpdateDoNotProvisionBeforeInstall(t *testing.T) {
	for _, command := range []string{"start", "update"} {
		t.Run(command, func(t *testing.T) {
			h := newMissingLinuxRuntimeHarness(t)
			result := h.run(t, command)
			if result.exitCode == 0 || !strings.Contains(result.output, "尚未安装") {
				t.Fatalf("%s should fail before provisioning: code=%d output=%s", command, result.exitCode, result.output)
			}
			if _, err := os.Stat(filepath.Join(h.home, "packages.log")); !errors.Is(err, os.ErrNotExist) {
				t.Fatalf("%s invoked the package manager before checking installation: %v", command, err)
			}
		})
	}
}

func TestStartRejectsUnavailableDockerDaemon(t *testing.T) {
	h := newHarness(t)
	if result := h.run(t, "install"); result.exitCode != 0 {
		t.Fatalf("install failed: %s", result.output)
	}
	writeExecutable(t, filepath.Join(h.home, "fake-bin", "open"), "#!/bin/sh\nexit 0\n")
	h.env = append(h.env, "FAKE_DOCKER_INFO_FAIL=1")
	result := h.run(t, "start")
	if result.exitCode == 0 || !strings.Contains(result.output, "无法访问 Docker daemon") {
		t.Fatalf("start should reject unavailable Docker access: code=%d output=%s", result.exitCode, result.output)
	}
	if _, err := os.Stat(filepath.Join(h.stateDir, "dockertree.pid")); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("failed start created a pid file: %v", err)
	}
}

func TestPurgeRejectsConfigDirectoryContainingManager(t *testing.T) {
	h := newHarness(t)
	if err := os.MkdirAll(h.configDir, 0o700); err != nil {
		t.Fatal(err)
	}
	managerData, err := os.ReadFile(managerPath(t))
	if err != nil {
		t.Fatal(err)
	}
	h.manager = filepath.Join(h.configDir, "dockertree.sh")
	writeExecutable(t, h.manager, string(managerData))
	marker := filepath.Join(h.configDir, "keep-me")
	if err := os.WriteFile(marker, []byte("config"), 0o600); err != nil {
		t.Fatal(err)
	}

	result := h.run(t, "uninstall", "--purge", "--yes")
	if result.exitCode == 0 || !strings.Contains(result.output, "管理脚本") {
		t.Fatalf("purge should reject the manager directory: code=%d output=%s", result.exitCode, result.output)
	}
	assertExecutable(t, h.manager)
	if _, err := os.Stat(marker); err != nil {
		t.Fatalf("rejected purge changed configuration: %v", err)
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
if [ "${1:-}" = "--version" ]; then
  echo "git version 2.50.0"
  exit 0
fi
printf '%s\n' "$*" >> "${FAKE_GIT_LOG:?}"
target=""
for arg in "$@"; do
  target="$arg"
done
mkdir -p "$target/cmd/dockertree"
printf 'module dockertree\n\ngo 1.23\n' > "$target/go.mod"
printf 'package main\nfunc main() {}\n' > "$target/cmd/dockertree/main.go"
if [ -n "${FAKE_MANAGER_TEMPLATE:-}" ]; then
  cp "$FAKE_MANAGER_TEMPLATE" "$target/dockertree.sh"
  chmod 755 "$target/dockertree.sh"
fi
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
config_file="${DOCKERTREE_CONFIG_DIR:?}/config.yaml"
if [ "${1:-}" = "config" ]; then
  case "${2:-}" in
    init)
      mkdir -p "${DOCKERTREE_CONFIG_DIR:?}"
      if [ ! -f "$config_file" ]; then
        printf 'listenAddr: 0.0.0.0:27680\nadminToken: test-admin-token\nallowLan: true\n' > "$config_file"
      fi
      ;;
    port)
      sed -n 's/^listenAddr: .*://p' "$config_file"
      ;;
    set-port)
      port=${3:-}
      case "$port" in ''|*[!0-9]*) exit 2 ;; esac
      sed "s/^listenAddr: .*/listenAddr: 0.0.0.0:$port/" "$config_file" > "$config_file.tmp"
      mv "$config_file.tmp" "$config_file"
      ;;
    check-port)
      port=$(sed -n 's/^listenAddr: .*://p' "$config_file")
      if [ "$port" = "${FAKE_OCCUPIED_PORT:-}" ]; then exit 3; fi
      ;;
    *) exit 2 ;;
  esac
  exit 0
fi
if [ "${FAKE_DOCKERTREE_FAIL:-0}" = "1" ]; then
  echo "fake startup failure" >&2
  exit 17
fi
printf '%s\n' "${DOCKERTREE_CONFIG_DIR:-}" > "${FAKE_DOCKERTREE_RECORD:?}"
port=$(sed -n 's/^listenAddr: .*://p' "$config_file")
echo "Dockertree listening on http://0.0.0.0:$port"
echo "Config dir: ${DOCKERTREE_CONFIG_DIR:-}"
echo "Admin token: test-admin-token"
trap 'exit 0' TERM INT
while :; do
  sleep 1
done
`
	writeExecutable(t, template, templateBody)

	fakeGo := filepath.Join(fakeBin, "go")
	fakeGoBody := `#!/bin/sh
set -eu
if [ "${1:-}" = "version" ]; then
  echo "go version go1.23.0 test/amd64"
  exit 0
fi
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

	fakeGit := filepath.Join(fakeBin, "git")
	writeExecutable(t, fakeGit, "#!/bin/sh\necho 'git version 2.50.0'\n")
	fakeDocker := filepath.Join(fakeBin, "docker")
	fakeDockerBody := `#!/bin/sh
set -eu
case "${1:-}" in
  --version) echo "Docker version 28.0.0, build test" ;;
  compose)
    if [ "${2:-}" = "version" ]; then
      echo "Docker Compose version v2.35.0"
    fi
    ;;
  info)
    if [ "${FAKE_DOCKER_INFO_FAIL:-0}" = "1" ]; then exit 1; fi
    exit 0
    ;;
esac
`
	writeExecutable(t, fakeDocker, fakeDockerBody)

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

func newMissingLinuxRuntimeHarness(t *testing.T) *harness {
	t.Helper()
	h := newHarness(t)
	fakeBin := filepath.Join(h.home, "fake-bin")
	for _, name := range []string{"go", "git", "docker"} {
		if err := os.Remove(filepath.Join(fakeBin, name)); err != nil {
			t.Fatal(err)
		}
	}
	for _, name := range []string{"cat", "chmod", "cp", "dirname", "env", "mkdir", "mktemp", "mv", "nohup", "ps", "rm", "rmdir", "sed", "sleep", "tail", "touch"} {
		target, err := exec.LookPath(name)
		if err != nil {
			t.Fatalf("locate test command %s: %v", name, err)
		}
		if err := os.Symlink(target, filepath.Join(fakeBin, name)); err != nil {
			t.Fatal(err)
		}
	}
	writeExecutable(t, filepath.Join(fakeBin, "uname"), "#!/bin/sh\nif [ \"${1:-}\" = \"-m\" ]; then echo x86_64; else echo Linux; fi\n")
	writeExecutable(t, filepath.Join(fakeBin, "id"), "#!/bin/sh\necho 0\n")

	runtimeDir := filepath.Join(h.home, "runtime-templates")
	if err := os.MkdirAll(runtimeDir, 0o700); err != nil {
		t.Fatal(err)
	}
	writeExecutable(t, filepath.Join(runtimeDir, "git"), "#!/bin/sh\necho 'git version 2.50.0'\n")
	goTemplate := `#!/bin/sh
set -eu
if [ "${1:-}" = "version" ]; then
  echo "go version go1.23.0 linux/amd64"
  exit 0
fi
output=""
while [ "$#" -gt 0 ]; do
  if [ "$1" = "-o" ]; then shift; output="${1:-}"; fi
  shift
done
cp "${FAKE_DOCKERTREE_TEMPLATE:?}" "$output"
chmod 755 "$output"
`
	writeExecutable(t, filepath.Join(runtimeDir, "go"), goTemplate)
	dockerTemplate := `#!/bin/sh
set -eu
case "${1:-}" in
  --version) echo "Docker version 28.0.0, build test" ;;
  compose)
    if [ "${2:-}" = "version" ] && [ -f "${FAKE_RUNTIME_DIR:?}/compose.ready" ]; then
      echo "Docker Compose version v2.35.0"
      exit 0
    fi
    exit 1
    ;;
  info) exit 0 ;;
esac
`
	writeExecutable(t, filepath.Join(runtimeDir, "docker"), dockerTemplate)
	aptBody := `#!/bin/sh
set -eu
printf '%s\n' "$*" >> "${FAKE_PACKAGE_LOG:?}"
case " $* " in
  *" git "*) cp "${FAKE_RUNTIME_DIR:?}/git" "${FAKE_BIN:?}/git" ;;
esac
case " $* " in
  *" golang-go "*) cp "${FAKE_RUNTIME_DIR:?}/go" "${FAKE_BIN:?}/go" ;;
esac
case " $* " in
  *" docker.io "*) cp "${FAKE_RUNTIME_DIR:?}/docker" "${FAKE_BIN:?}/docker" ;;
esac
case " $* " in
  *" docker-compose-v2 "*) touch "${FAKE_RUNTIME_DIR:?}/compose.ready" ;;
esac
`
	writeExecutable(t, filepath.Join(fakeBin, "apt-get"), aptBody)
	h.env = append(removeEnv(h.env, "PATH"),
		"PATH="+fakeBin,
		"FAKE_BIN="+fakeBin,
		"FAKE_RUNTIME_DIR="+runtimeDir,
		"FAKE_PACKAGE_LOG="+filepath.Join(h.home, "packages.log"),
	)
	return h
}

type commandResult struct {
	output   string
	exitCode int
}

func (h *harness) run(t *testing.T, args ...string) commandResult {
	t.Helper()
	return h.runWithInput(t, "", args...)
}

func (h *harness) runWithInput(t *testing.T, input string, args ...string) commandResult {
	t.Helper()
	manager := h.manager
	if manager == "" {
		manager = managerPath(t)
	}
	cmd := exec.Command(manager, args...)
	cmd.Dir = h.home
	cmd.Env = append(os.Environ(), h.env...)
	cmd.Stdin = strings.NewReader(input)
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

func (h *harness) managerPath(t *testing.T) string {
	t.Helper()
	if h.manager != "" {
		return h.manager
	}
	return managerPath(t)
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
