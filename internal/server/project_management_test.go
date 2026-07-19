package server

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	"dockertree/internal/config"
	"dockertree/internal/core"
	"dockertree/internal/docker"
)

type mutableScanner struct {
	paths    []string
	projects []core.Project
}

func (s *mutableScanner) Scan(context.Context) ([]core.Project, error) {
	return s.projects, nil
}

func (s *mutableScanner) SetScanPaths(paths []string) {
	s.paths = append([]string(nil), paths...)
}

type projectTestRunner map[string]string

func (r projectTestRunner) Run(_ context.Context, _ string, args ...string) ([]byte, error) {
	return []byte(r[strings.Join(args, " ")]), nil
}

func TestProjectSettingsPersistAndUpdateScannerWithoutRestart(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("DOCKERTREE_CONFIG_DIR", dir)
	scanRoot := filepath.Join(dir, "existing")
	projectRoot := filepath.Join(dir, "opt")
	scanner := &mutableScanner{}
	s := New(config.Config{
		Dir:         dir,
		ListenAddr:  "127.0.0.1:27680",
		AdminToken:  "secret",
		ProjectRoot: "/old",
		ScanPaths:   []string{"/old"},
	}, &fakeInventory{}, scanner, &fakeExecutor{})
	h := s.Handler()
	body := `{"projectRoot":"` + projectRoot + `","scanPaths":["` + scanRoot + `","` + scanRoot + `"]}`

	req := httptest.NewRequest(http.MethodPatch, "/api/settings/projects", strings.NewReader(body))
	req.Header.Set("Authorization", "Bearer secret")
	r := httptest.NewRecorder()
	h.ServeHTTP(r, req)
	if r.Code != http.StatusOK {
		t.Fatalf("PATCH settings status=%d body=%s", r.Code, r.Body.String())
	}
	wantScannerPaths := []string{scanRoot, projectRoot}
	if !reflect.DeepEqual(scanner.paths, wantScannerPaths) {
		t.Fatalf("scanner paths=%#v want %#v", scanner.paths, wantScannerPaths)
	}

	cfg, err := config.Load()
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}
	if cfg.ProjectRoot != projectRoot || !reflect.DeepEqual(cfg.ScanPaths, []string{scanRoot}) {
		t.Fatalf("settings were not persisted: %#v", cfg)
	}

	req = httptest.NewRequest(http.MethodGet, "/api/settings/projects", nil)
	req.Header.Set("Authorization", "Bearer secret")
	r = httptest.NewRecorder()
	h.ServeHTTP(r, req)
	if r.Code != http.StatusOK || !strings.Contains(r.Body.String(), projectRoot) || !strings.Contains(r.Body.String(), scanRoot) {
		t.Fatalf("GET settings status=%d body=%s", r.Code, r.Body.String())
	}
}

func TestProjectSettingsRejectRelativeDirectories(t *testing.T) {
	dir := t.TempDir()
	h := New(config.Config{Dir: dir, ListenAddr: "127.0.0.1:27680", AdminToken: "secret", ProjectRoot: "/opt"}, &fakeInventory{}, &mutableScanner{}, &fakeExecutor{}).Handler()
	req := httptest.NewRequest(http.MethodPatch, "/api/settings/projects", strings.NewReader(`{"projectRoot":"../opt","scanPaths":["relative"]}`))
	req.Header.Set("Authorization", "Bearer secret")
	r := httptest.NewRecorder()
	h.ServeHTTP(r, req)
	if r.Code != http.StatusBadRequest {
		t.Fatalf("relative settings status=%d body=%s", r.Code, r.Body.String())
	}
}

func TestProjectSettingsImmediatelyUpdateRealScanner(t *testing.T) {
	dir := t.TempDir()
	projectRoot := filepath.Join(dir, "opt")
	appDir := filepath.Join(projectRoot, "existing-app")
	if err := os.MkdirAll(appDir, 0o755); err != nil {
		t.Fatal(err)
	}
	composePath := filepath.Join(appDir, "compose.yml")
	if err := os.WriteFile(composePath, []byte("services: {}\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	runner := projectTestRunner{
		"compose ls --format json":  `[]`,
		"ps -a --format {{json .}}": ``,
	}
	scanner := docker.NewScanner(runner, []string{filepath.Join(dir, "old")})
	inv := &fakeInventory{}
	h := New(config.Config{
		Dir: dir, ListenAddr: "127.0.0.1:27680", AdminToken: "secret", ProjectRoot: filepath.Join(dir, "old"),
	}, inv, scanner, &fakeExecutor{}).Handler()

	req := httptest.NewRequest(http.MethodPatch, "/api/settings/projects", strings.NewReader(`{"projectRoot":"`+projectRoot+`","scanPaths":[]}`))
	req.Header.Set("Authorization", "Bearer secret")
	r := httptest.NewRecorder()
	h.ServeHTTP(r, req)
	if r.Code != http.StatusOK {
		t.Fatalf("PATCH settings status=%d body=%s", r.Code, r.Body.String())
	}

	req = httptest.NewRequest(http.MethodPost, "/api/scan", nil)
	req.Header.Set("Authorization", "Bearer secret")
	r = httptest.NewRecorder()
	h.ServeHTTP(r, req)
	if r.Code != http.StatusOK || !strings.Contains(r.Body.String(), "existing-app") || !strings.Contains(r.Body.String(), composePath) {
		t.Fatalf("hot-reloaded scan status=%d body=%s", r.Code, r.Body.String())
	}
}

func TestComposePreviewDerivesProjectPathFromConfiguredRoot(t *testing.T) {
	root := t.TempDir()
	h := New(config.Config{AdminToken: "secret", ProjectRoot: root}, &fakeInventory{}, &mutableScanner{}, &fakeExecutor{}).Handler()
	body := `{"name":"demo","composeContent":"services:\n  web:\n    image: nginx\n"}`
	req := httptest.NewRequest(http.MethodPost, "/api/deploy/compose/preview", strings.NewReader(body))
	req.Header.Set("Authorization", "Bearer secret")
	r := httptest.NewRecorder()
	h.ServeHTTP(r, req)
	wantPath := filepath.Join(root, "demo", "compose.yml")
	if r.Code != http.StatusOK || !strings.Contains(r.Body.String(), wantPath) {
		t.Fatalf("preview status=%d body=%s want path %s", r.Code, r.Body.String(), wantPath)
	}
	if _, err := os.Stat(wantPath); !os.IsNotExist(err) {
		t.Fatalf("preview wrote compose file: %v", err)
	}
}

func TestComposeSaveCreatesProjectWithoutStartingContainers(t *testing.T) {
	root := t.TempDir()
	exec := &fakeExecutor{}
	inv := &fakeInventory{}
	scanner := &mutableScanner{projects: []core.Project{{ID: "compose:demo", Name: "demo", Type: core.ProjectTypeCompose}}}
	h := New(config.Config{AdminToken: "secret", ProjectRoot: root}, inv, scanner, exec).Handler()
	body := `{"name":"demo","composeContent":"services:\n  web:\n    image: nginx\n"}`
	req := httptest.NewRequest(http.MethodPost, "/api/deploy/compose/save", strings.NewReader(body))
	req.Header.Set("Authorization", "Bearer secret")
	r := httptest.NewRecorder()
	h.ServeHTTP(r, req)
	if r.Code != http.StatusOK {
		t.Fatalf("save status=%d body=%s", r.Code, r.Body.String())
	}
	wantPath := filepath.Join(root, "demo", "compose.yml")
	if !strings.Contains(r.Body.String(), `"saved":true`) || !strings.Contains(r.Body.String(), `"deployed":false`) || !strings.Contains(r.Body.String(), wantPath) {
		t.Fatalf("save response missing outcome: %s", r.Body.String())
	}
	data, err := os.ReadFile(wantPath)
	if err != nil {
		t.Fatalf("saved compose file: %v", err)
	}
	if !strings.Contains(string(data), "image: nginx") {
		t.Fatalf("unexpected compose content: %s", data)
	}
	if len(exec.commands) != 1 || !strings.HasSuffix(exec.commands[0], " config") || strings.Contains(exec.commands[0], " up ") {
		t.Fatalf("save executed unexpected commands: %#v", exec.commands)
	}
	if inv.saves != 1 {
		t.Fatalf("save should refresh inventory, saves=%d", inv.saves)
	}
}

func TestComposeDeployDerivesPathAndStartsProject(t *testing.T) {
	root := t.TempDir()
	exec := &fakeExecutor{}
	scanner := &mutableScanner{projects: []core.Project{{ID: "compose:demo", Name: "demo", Type: core.ProjectTypeCompose}}}
	h := New(config.Config{AdminToken: "secret", ProjectRoot: root}, &fakeInventory{}, scanner, exec).Handler()
	body := `{"name":"demo","composeContent":"services:\n  web:\n    image: nginx\n"}`
	req := httptest.NewRequest(http.MethodPost, "/api/deploy/compose", strings.NewReader(body))
	req.Header.Set("Authorization", "Bearer secret")
	r := httptest.NewRecorder()
	h.ServeHTTP(r, req)
	if r.Code != http.StatusOK {
		t.Fatalf("deploy status=%d body=%s", r.Code, r.Body.String())
	}
	wantPath := filepath.Join(root, "demo", "compose.yml")
	if _, err := os.Stat(wantPath); err != nil {
		t.Fatalf("derived compose file: %v", err)
	}
	if len(exec.commands) != 2 || exec.commands[1] != "docker compose -f "+wantPath+" --progress json up -d" {
		t.Fatalf("derived deploy commands: %#v", exec.commands)
	}
}

func TestEditingMultiFileComposeDeployUsesEveryProjectFile(t *testing.T) {
	dir := t.TempDir()
	basePath := filepath.Join(dir, "compose.yml")
	overridePath := filepath.Join(dir, "compose.override.yml")
	for path, content := range map[string]string{
		basePath:     "services:\n  web:\n    image: nginx\n",
		overridePath: "services:\n  web:\n    ports:\n      - 8080:80\n",
	} {
		if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	project := core.Project{
		ID: "compose:demo", Name: "demo", Type: core.ProjectTypeCompose,
		WorkingDir: dir, ConfigFiles: []string{basePath, overridePath},
	}
	inv := &fakeInventory{projects: []core.Project{project}}
	scanner := &mutableScanner{projects: []core.Project{project}}
	exec := &fakeExecutor{}
	h := New(config.Config{AdminToken: "secret", ProjectRoot: dir}, inv, scanner, exec).Handler()
	body := `{"projectId":"compose:demo","name":"demo","composePath":"` + basePath + `","composeContent":"services:\n  web:\n    image: nginx:stable\n"}`

	req := httptest.NewRequest(http.MethodPost, "/api/deploy/compose/preview", strings.NewReader(body))
	req.Header.Set("Authorization", "Bearer secret")
	r := httptest.NewRecorder()
	h.ServeHTTP(r, req)
	if r.Code != http.StatusOK {
		t.Fatalf("preview status=%d body=%s", r.Code, r.Body.String())
	}
	var preview core.ComposeDeployPreview
	if err := json.NewDecoder(r.Body).Decode(&preview); err != nil {
		t.Fatal(err)
	}
	wantDeploy := "docker compose -f " + basePath + " -f " + overridePath + " --progress json up -d"
	if len(preview.Plan.Commands) != 1 || preview.Plan.Commands[0] != wantDeploy {
		t.Fatalf("multi-file preview commands=%#v want %q", preview.Plan.Commands, wantDeploy)
	}

	req = httptest.NewRequest(http.MethodPost, "/api/deploy/compose", strings.NewReader(body))
	req.Header.Set("Authorization", "Bearer secret")
	r = httptest.NewRecorder()
	h.ServeHTTP(r, req)
	if r.Code != http.StatusOK {
		t.Fatalf("deploy status=%d body=%s", r.Code, r.Body.String())
	}
	if len(exec.commands) != 2 {
		t.Fatalf("multi-file commands=%#v", exec.commands)
	}
	if !strings.Contains(exec.commands[0], " -f "+overridePath+" config") {
		t.Fatalf("validation omitted override file: %q", exec.commands[0])
	}
	if exec.commands[1] != wantDeploy {
		t.Fatalf("deploy omitted project files: %q want %q", exec.commands[1], wantDeploy)
	}
}

func TestComposeProjectNameCannotEscapeThroughSymlink(t *testing.T) {
	root := t.TempDir()
	outside := t.TempDir()
	if err := os.Symlink(outside, filepath.Join(root, "linked")); err != nil {
		t.Skipf("symlinks are unavailable: %v", err)
	}
	exec := &fakeExecutor{}
	h := New(config.Config{AdminToken: "secret", ProjectRoot: root}, &fakeInventory{}, &mutableScanner{}, exec).Handler()
	body := `{"name":"linked","composeContent":"services:\n  web:\n    image: nginx\n"}`
	req := httptest.NewRequest(http.MethodPost, "/api/deploy/compose/save", strings.NewReader(body))
	req.Header.Set("Authorization", "Bearer secret")
	r := httptest.NewRecorder()
	h.ServeHTTP(r, req)
	if r.Code != http.StatusBadRequest {
		t.Fatalf("symlink escape status=%d body=%s", r.Code, r.Body.String())
	}
	if _, err := os.Stat(filepath.Join(outside, "compose.yml")); !os.IsNotExist(err) {
		t.Fatalf("compose file escaped project root: %v", err)
	}
	if len(exec.commands) != 0 {
		t.Fatalf("symlink escape executed commands: %#v", exec.commands)
	}
}

func TestComposeProjectNameCannotEscapeProjectRoot(t *testing.T) {
	root := t.TempDir()
	exec := &fakeExecutor{}
	h := New(config.Config{AdminToken: "secret", ProjectRoot: root}, &fakeInventory{}, &mutableScanner{}, exec).Handler()
	body := `{"name":"../escape","composeContent":"services:\n  web:\n    image: nginx\n"}`
	req := httptest.NewRequest(http.MethodPost, "/api/deploy/compose/save", strings.NewReader(body))
	req.Header.Set("Authorization", "Bearer secret")
	r := httptest.NewRecorder()
	h.ServeHTTP(r, req)
	if r.Code != http.StatusBadRequest {
		t.Fatalf("traversal status=%d body=%s", r.Code, r.Body.String())
	}
	if len(exec.commands) != 0 {
		t.Fatalf("traversal executed commands: %#v", exec.commands)
	}
}

func TestProjectComposeEndpointReadsOnlyMatchedFiles(t *testing.T) {
	dir := t.TempDir()
	composePath := filepath.Join(dir, "compose.yml")
	otherPath := filepath.Join(dir, "secret.yml")
	if err := os.WriteFile(composePath, []byte("services:\n  web:\n    image: nginx\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(otherPath, []byte("secret"), 0o600); err != nil {
		t.Fatal(err)
	}
	inv := &fakeInventory{projects: []core.Project{{
		ID: "compose:demo", Name: "demo", Type: core.ProjectTypeCompose, WorkingDir: dir, ConfigFiles: []string{composePath},
	}}}
	h := New(config.Config{AdminToken: "secret", ProjectRoot: dir}, inv, &mutableScanner{}, &fakeExecutor{}).Handler()

	req := httptest.NewRequest(http.MethodGet, "/api/projects/compose:demo/compose?path="+url.QueryEscape(composePath), nil)
	req.Header.Set("Authorization", "Bearer secret")
	r := httptest.NewRecorder()
	h.ServeHTTP(r, req)
	if r.Code != http.StatusOK {
		t.Fatalf("read compose status=%d body=%s", r.Code, r.Body.String())
	}
	var response struct {
		ComposePath    string `json:"composePath"`
		ComposeContent string `json:"composeContent"`
	}
	if err := json.NewDecoder(r.Body).Decode(&response); err != nil {
		t.Fatal(err)
	}
	if response.ComposePath != composePath || !strings.Contains(response.ComposeContent, "image: nginx") {
		t.Fatalf("unexpected compose response: %#v", response)
	}

	req = httptest.NewRequest(http.MethodGet, "/api/projects/compose:demo/compose?path="+url.QueryEscape(otherPath), nil)
	req.Header.Set("Authorization", "Bearer secret")
	r = httptest.NewRecorder()
	h.ServeHTTP(r, req)
	if r.Code != http.StatusBadRequest || strings.Contains(r.Body.String(), "secret") {
		t.Fatalf("unmatched file status=%d body=%s", r.Code, r.Body.String())
	}
}

func TestProjectComposeEndpointRejectsSymlinksAndFilesOutsideConfiguredRoots(t *testing.T) {
	root := t.TempDir()
	outside := t.TempDir()
	outsidePath := filepath.Join(outside, "compose.yml")
	if err := os.WriteFile(outsidePath, []byte("services:\n  secret:\n    image: private\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	linkedPath := filepath.Join(root, "compose.yml")
	if err := os.Symlink(outsidePath, linkedPath); err != nil {
		t.Skipf("symlinks are unavailable: %v", err)
	}

	for _, composePath := range []string{linkedPath, outsidePath} {
		inv := &fakeInventory{projects: []core.Project{{
			ID: "compose:demo", Name: "demo", Type: core.ProjectTypeCompose,
			WorkingDir: filepath.Dir(composePath), ConfigFiles: []string{composePath},
		}}}
		h := New(config.Config{AdminToken: "secret", ProjectRoot: root}, inv, &mutableScanner{}, &fakeExecutor{}).Handler()
		req := httptest.NewRequest(http.MethodGet, "/api/projects/compose:demo/compose", nil)
		req.Header.Set("Authorization", "Bearer secret")
		r := httptest.NewRecorder()
		h.ServeHTTP(r, req)
		if r.Code != http.StatusBadRequest {
			t.Fatalf("unsafe compose path %q status=%d body=%s", composePath, r.Code, r.Body.String())
		}
		if strings.Contains(r.Body.String(), "private") {
			t.Fatalf("unsafe compose content leaked for %q: %s", composePath, r.Body.String())
		}
	}
}

func TestReplaceComposeFileRestorePreservesAConcurrentEdit(t *testing.T) {
	dir := t.TempDir()
	target := filepath.Join(dir, "compose.yml")
	staged := filepath.Join(dir, "staged.yml")
	oldContent := "services:\n  web:\n    image: nginx:old\n"
	newContent := "services:\n  web:\n    image: nginx:new\n"
	externalContent := "services:\n  web:\n    image: nginx:external\n"
	if err := os.WriteFile(target, []byte(oldContent), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(staged, []byte(newContent), 0o600); err != nil {
		t.Fatal(err)
	}
	restore, discard, err := replaceComposeFile(target, staged, docker.ComposeContentHash(oldContent))
	if err != nil {
		t.Fatal(err)
	}
	defer discard()
	if err := os.WriteFile(target, []byte(externalContent), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := restore(); !errors.Is(err, errComposeFileChanged) {
		t.Fatalf("restore error=%v want compose conflict", err)
	}
	data, err := os.ReadFile(target)
	if err != nil {
		t.Fatal(err)
	}
	if string(data) != externalContent {
		t.Fatalf("restore overwrote concurrent edit: %s", data)
	}
}

func TestComposePathLockSerializesTheSameTarget(t *testing.T) {
	s := New(config.Config{}, &fakeInventory{}, &mutableScanner{}, &fakeExecutor{})
	unlockFirst := s.lockComposePath("/opt/demo/compose.yml")
	acquired := make(chan struct{})
	go func() {
		unlockSecond := s.lockComposePath("/opt/demo/compose.yml")
		close(acquired)
		unlockSecond()
	}()
	select {
	case <-acquired:
		t.Fatal("second writer acquired the same compose path before release")
	case <-time.After(30 * time.Millisecond):
	}
	unlockFirst()
	select {
	case <-acquired:
	case <-time.After(time.Second):
		t.Fatal("second writer did not acquire compose path after release")
	}
}

func TestConfigUpdateLockSerializesSettingsWriters(t *testing.T) {
	dir := t.TempDir()
	s := New(config.Config{
		Dir: dir, ListenAddr: "127.0.0.1:27680", AdminToken: "secret",
		ProjectRoot: "/opt", ScanPaths: []string{"/opt"},
	}, &fakeInventory{}, &mutableScanner{}, &fakeExecutor{})
	h := s.Handler()
	unlock := s.lockConfigUpdates()
	responses := make(chan *httptest.ResponseRecorder, 2)
	requests := []*http.Request{
		httptest.NewRequest(http.MethodPatch, "/api/settings/projects", strings.NewReader(`{"projectRoot":"/srv/compose","scanPaths":["/srv/compose"]}`)),
		httptest.NewRequest(http.MethodPatch, "/api/settings/automation", strings.NewReader(`{"webhookType":"generic","notifyOnUpdates":true}`)),
	}
	for _, req := range requests {
		req.Header.Set("Authorization", "Bearer secret")
		go func(req *http.Request) {
			r := httptest.NewRecorder()
			h.ServeHTTP(r, req)
			responses <- r
		}(req)
	}
	select {
	case r := <-responses:
		t.Fatalf("settings writer bypassed config lock: status=%d body=%s", r.Code, r.Body.String())
	case <-time.After(30 * time.Millisecond):
	}
	unlock()
	for range requests {
		select {
		case r := <-responses:
			if r.Code != http.StatusOK {
				t.Fatalf("settings update status=%d body=%s", r.Code, r.Body.String())
			}
		case <-time.After(time.Second):
			t.Fatal("settings update remained blocked")
		}
	}
	cfg := s.currentConfig()
	if cfg.ProjectRoot != "/srv/compose" || !cfg.Automation.NotifyOnUpdates {
		t.Fatalf("concurrent settings update lost data: %#v", cfg)
	}
}

func TestProjectComposeEndpointRejectsMissingComposeProjects(t *testing.T) {
	inv := &fakeInventory{projects: []core.Project{{ID: "container:solo", Name: "solo", Type: core.ProjectTypeStandalone}}}
	h := New(config.Config{AdminToken: "secret", ProjectRoot: "/opt"}, inv, &mutableScanner{}, &fakeExecutor{}).Handler()
	for _, path := range []string{
		"/api/projects/container:solo/compose",
		"/api/projects/compose:missing/compose",
	} {
		req := httptest.NewRequest(http.MethodGet, path, nil)
		req.Header.Set("Authorization", "Bearer secret")
		r := httptest.NewRecorder()
		h.ServeHTTP(r, req)
		if path == "/api/projects/compose:missing/compose" && r.Code != http.StatusNotFound {
			t.Fatalf("missing project status=%d body=%s", r.Code, r.Body.String())
		}
		if path == "/api/projects/container:solo/compose" && (r.Code != http.StatusBadRequest || !strings.Contains(r.Body.String(), "no compose file")) {
			t.Fatalf("standalone project status=%d body=%s", r.Code, r.Body.String())
		}
	}
}
