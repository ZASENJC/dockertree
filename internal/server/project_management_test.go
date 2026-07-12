package server

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"dockertree/internal/config"
	"dockertree/internal/core"
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
	h := New(config.Config{AdminToken: "secret", ProjectRoot: "/opt"}, inv, &mutableScanner{}, &fakeExecutor{}).Handler()

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
