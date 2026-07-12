package server

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"dockertree/internal/config"
	"dockertree/internal/core"
	"dockertree/internal/docker"
	"dockertree/internal/store"
	"gopkg.in/yaml.v3"
)

type fakeNotifier struct {
	calls []Notification
	err   error
}

type composeMutationExecutor struct {
	*fakeExecutor
	path    string
	content string
}

func (e *composeMutationExecutor) Execute(ctx context.Context, cmd docker.Command) (docker.Result, error) {
	if strings.HasSuffix(cmd.String(), " config") {
		if err := os.WriteFile(e.path, []byte(e.content), 0o600); err != nil {
			return docker.Result{}, err
		}
	}
	return e.fakeExecutor.Execute(ctx, cmd)
}

type timeoutUpdateExecutor struct {
	*fakeExecutor
	mu    sync.Mutex
	calls []string
}

func (e *timeoutUpdateExecutor) CheckUpdate(ctx context.Context, project core.Project) (core.UpdateCheck, error) {
	e.mu.Lock()
	e.calls = append(e.calls, project.ID)
	e.mu.Unlock()
	check := core.UpdateCheck{ProjectID: project.ID, ProjectName: project.Name, Status: "unknown"}
	if project.Name == "slow" {
		<-ctx.Done()
		check.Error = ctx.Err().Error()
		return check, ctx.Err()
	}
	check.Status = "current"
	return check, nil
}

func (f *fakeNotifier) Notify(_ context.Context, notification Notification) error {
	f.calls = append(f.calls, notification)
	return f.err
}

func TestProjectMetadataSupportsValidatedServiceLinks(t *testing.T) {
	inv := &fakeInventory{projects: []core.Project{{ID: "compose:app", Name: "app", Type: core.ProjectTypeCompose}}}
	h := New(config.Config{AdminToken: "secret"}, inv, fakeScanner{}, &fakeExecutor{}).Handler()
	req := httptest.NewRequest(http.MethodPatch, "/api/projects/compose:app/metadata", strings.NewReader(`{"links":[{"name":" Home ","url":"http://localhost:8080"},{"name":"Home","url":"http://localhost:8080"}]}`))
	req.Header.Set("Authorization", "Bearer secret")
	r := httptest.NewRecorder()
	h.ServeHTTP(r, req)
	if r.Code != http.StatusOK || len(inv.projects[0].Links) != 1 || inv.projects[0].Links[0].Name != "Home" {
		t.Fatalf("status=%d links=%#v body=%s", r.Code, inv.projects[0].Links, r.Body.String())
	}

	req = httptest.NewRequest(http.MethodPatch, "/api/projects/compose:app/metadata", strings.NewReader(`{"links":[{"name":"Bad","url":"javascript:alert(1)"}]}`))
	req.Header.Set("Authorization", "Bearer secret")
	r = httptest.NewRecorder()
	h.ServeHTTP(r, req)
	if r.Code != http.StatusBadRequest {
		t.Fatalf("unsafe link status=%d body=%s", r.Code, r.Body.String())
	}
}

func TestContainerInspectEndpointReturnsSafeDetails(t *testing.T) {
	exec := &fakeExecutor{inspect: core.ContainerInspect{ContainerID: "abc123", Name: "redis", RestartPolicy: "unless-stopped", Networks: []string{"proxy"}}}
	h := New(config.Config{AdminToken: "secret"}, &fakeInventory{}, fakeScanner{}, exec).Handler()
	req := httptest.NewRequest(http.MethodGet, "/api/containers/abc123/inspect", nil)
	req.Header.Set("Authorization", "Bearer secret")
	r := httptest.NewRecorder()
	h.ServeHTTP(r, req)
	if r.Code != http.StatusOK || !strings.Contains(r.Body.String(), "unless-stopped") || strings.Contains(r.Body.String(), "Env") {
		t.Fatalf("status=%d body=%s", r.Code, r.Body.String())
	}
}

func TestOperationHistoryRecordsRedactedDockerCommands(t *testing.T) {
	operations := store.NewOperationStore(filepath.Join(t.TempDir(), "operations.jsonl"))
	exec := &fakeExecutor{}
	scanner := fakeScanner{projects: []core.Project{{ID: "container:redis-local", Name: "redis-local", Type: core.ProjectTypeStandalone}}}
	s := New(config.Config{AdminToken: "secret"}, &fakeInventory{}, scanner, exec).WithOperationLog(operations)
	h := s.Handler()
	req := httptest.NewRequest(http.MethodPost, "/api/deploy/container", strings.NewReader(`{"name":"redis-local","image":"redis:7","env":["PASSWORD=top-secret"]}`))
	req.Header.Set("Authorization", "Bearer secret")
	r := httptest.NewRecorder()
	h.ServeHTTP(r, req)
	if r.Code != http.StatusOK {
		t.Fatalf("deploy status=%d body=%s", r.Code, r.Body.String())
	}
	if strings.Contains(r.Body.String(), "top-secret") || !strings.Contains(r.Body.String(), "PASSWORD=***") {
		t.Fatalf("deploy response leaked environment value: %s", r.Body.String())
	}

	req = httptest.NewRequest(http.MethodGet, "/api/operations?limit=10", nil)
	req.Header.Set("Authorization", "Bearer secret")
	r = httptest.NewRecorder()
	h.ServeHTTP(r, req)
	if r.Code != http.StatusOK || !strings.Contains(r.Body.String(), "PASSWORD=***") || strings.Contains(r.Body.String(), "top-secret") {
		t.Fatalf("operations status=%d body=%s", r.Code, r.Body.String())
	}
}

func TestUpdateChecksAutomationSettingsAndTestNotification(t *testing.T) {
	dir := t.TempDir()
	cfg := config.Config{Dir: dir, ListenAddr: "127.0.0.1:27680", AdminToken: "secret", Automation: config.AutomationConfig{WebhookType: "generic", NotifyOnUpdates: true}}
	project := core.Project{ID: "compose:app", Name: "app", Type: core.ProjectTypeCompose, ConfigFiles: []string{"/srv/app/compose.yml"}}
	exec := &fakeExecutor{checks: map[string]core.UpdateCheck{"compose:app": {ProjectID: "compose:app", ProjectName: "app", Status: "available"}}}
	notifier := &fakeNotifier{}
	s := New(cfg, &fakeInventory{projects: []core.Project{project}}, fakeScanner{}, exec).WithNotifier(notifier)
	h := s.Handler()

	req := httptest.NewRequest(http.MethodPatch, "/api/settings/automation", strings.NewReader(`{"updateCheckIntervalMinutes":60,"webhookURL":"http://localhost:9090/topic","webhookType":"ntfy","notifyOnUpdates":true}`))
	req.Header.Set("Authorization", "Bearer secret")
	r := httptest.NewRecorder()
	h.ServeHTTP(r, req)
	if r.Code != http.StatusOK || !strings.Contains(r.Body.String(), `"webhookType":"ntfy"`) {
		t.Fatalf("settings status=%d body=%s", r.Code, r.Body.String())
	}
	if _, err := os.Stat(filepath.Join(dir, "config.yaml")); err != nil {
		t.Fatalf("settings were not persisted: %v", err)
	}

	req = httptest.NewRequest(http.MethodPost, "/api/updates/check", nil)
	req.Header.Set("Authorization", "Bearer secret")
	r = httptest.NewRecorder()
	h.ServeHTTP(r, req)
	if r.Code != http.StatusOK || !strings.Contains(r.Body.String(), `"status":"available"`) {
		t.Fatalf("update status=%d body=%s", r.Code, r.Body.String())
	}

	req = httptest.NewRequest(http.MethodPost, "/api/notifications/test", nil)
	req.Header.Set("Authorization", "Bearer secret")
	r = httptest.NewRecorder()
	h.ServeHTTP(r, req)
	if r.Code != http.StatusOK || len(notifier.calls) != 1 || notifier.calls[0].WebhookType != "ntfy" {
		t.Fatalf("notification status=%d calls=%#v body=%s", r.Code, notifier.calls, r.Body.String())
	}
}

func TestHTTPNotifierSupportsGenericNtfyAndHTTPFailures(t *testing.T) {
	var mu sync.Mutex
	bodies := []string{}
	status := http.StatusOK
	hook := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		data, _ := io.ReadAll(r.Body)
		mu.Lock()
		bodies = append(bodies, string(data))
		currentStatus := status
		mu.Unlock()
		w.WriteHeader(currentStatus)
	}))
	defer hook.Close()
	notifier := HTTPNotifier{Client: hook.Client()}
	for _, webhookType := range []string{"generic", "ntfy"} {
		if err := notifier.Notify(context.Background(), Notification{WebhookURL: hook.URL, WebhookType: webhookType, Title: "Title", Message: "Message"}); err != nil {
			t.Fatalf("Notify(%s) error = %v", webhookType, err)
		}
	}
	mu.Lock()
	if len(bodies) != 2 || !strings.Contains(bodies[0], `"event":"dockertree"`) || !strings.Contains(bodies[1], `"tags"`) {
		t.Fatalf("webhook bodies = %#v", bodies)
	}
	status = http.StatusBadGateway
	mu.Unlock()
	if err := notifier.Notify(context.Background(), Notification{WebhookURL: hook.URL, WebhookType: "generic", Title: "Title", Message: "Message"}); err == nil {
		t.Fatal("expected non-2xx webhook error")
	}
	if err := notifier.Notify(context.Background(), Notification{}); err == nil {
		t.Fatal("expected missing URL error")
	}
}

func TestAutomationReadValidationProjectCheckAndSchedule(t *testing.T) {
	project := core.Project{ID: "compose:app", Name: "app", Type: core.ProjectTypeCompose, ConfigFiles: []string{"/srv/app/compose.yml"}}
	exec := &fakeExecutor{checks: map[string]core.UpdateCheck{"compose:app": {ProjectID: project.ID, ProjectName: project.Name, Status: "available"}}}
	notifier := &fakeNotifier{}
	cfg := config.Config{AdminToken: "secret", Automation: config.AutomationConfig{UpdateCheckIntervalMinutes: 60, WebhookURL: "http://localhost/hook", WebhookType: "generic", NotifyOnUpdates: true}}
	s := New(cfg, &fakeInventory{projects: []core.Project{project}}, fakeScanner{}, exec).WithNotifier(notifier)
	h := s.Handler()

	for _, tc := range []struct {
		method string
		path   string
		body   string
		status int
	}{
		{http.MethodGet, "/api/settings/automation", "", http.StatusOK},
		{http.MethodPatch, "/api/settings/automation", `{"updateCheckIntervalMinutes":5,"webhookType":"generic"}`, http.StatusBadRequest},
		{http.MethodPost, "/api/projects/compose:app/actions/check-update", "", http.StatusOK},
	} {
		req := httptest.NewRequest(tc.method, tc.path, strings.NewReader(tc.body))
		req.Header.Set("Authorization", "Bearer secret")
		r := httptest.NewRecorder()
		h.ServeHTTP(r, req)
		if r.Code != tc.status {
			t.Fatalf("%s %s status=%d body=%s", tc.method, tc.path, r.Code, r.Body.String())
		}
	}

	s.lastAutomationCheck = time.Now().Add(-2 * time.Hour)
	now := time.Now()
	s.runScheduledUpdateCheck(context.Background(), now)
	s.runScheduledUpdateCheck(context.Background(), now.Add(5*time.Minute))
	if len(notifier.calls) != 1 {
		t.Fatalf("scheduled notifications = %#v", notifier.calls)
	}
	ctx, cancel := context.WithCancel(context.Background())
	s.StartAutomation(ctx)
	cancel()
}

func TestScheduledUpdateChecksTimeoutOneProjectAndContinue(t *testing.T) {
	projects := []core.Project{
		{ID: "compose:slow", Name: "slow", Type: core.ProjectTypeCompose, ConfigFiles: []string{"/srv/slow/compose.yml"}},
		{ID: "compose:fast", Name: "fast", Type: core.ProjectTypeCompose, ConfigFiles: []string{"/srv/fast/compose.yml"}},
	}
	exec := &timeoutUpdateExecutor{fakeExecutor: &fakeExecutor{}}
	s := New(config.Config{}, &fakeInventory{projects: projects}, fakeScanner{}, exec)
	s.updateCheckTimeout = 20 * time.Millisecond

	started := time.Now()
	checks, err := s.runUpdateChecks(context.Background(), false)
	if err != nil {
		t.Fatalf("runUpdateChecks() error = %v", err)
	}
	if elapsed := time.Since(started); elapsed > 500*time.Millisecond {
		t.Fatalf("runUpdateChecks() took %s", elapsed)
	}
	if len(checks) != 2 || checks[0].Error != context.DeadlineExceeded.Error() || checks[1].Status != "current" {
		t.Fatalf("checks = %#v", checks)
	}
	exec.mu.Lock()
	defer exec.mu.Unlock()
	if strings.Join(exec.calls, ",") != "compose:slow,compose:fast" {
		t.Fatalf("check calls = %#v", exec.calls)
	}
}

func TestNotificationWithoutWebhookAndUpdateCheckMissingProject(t *testing.T) {
	h := New(config.Config{AdminToken: "secret", Automation: config.AutomationConfig{WebhookType: "generic"}}, &fakeInventory{}, fakeScanner{}, &fakeExecutor{}).Handler()
	for _, path := range []string{"/api/notifications/test", "/api/projects/compose:missing/actions/check-update"} {
		req := httptest.NewRequest(http.MethodPost, path, nil)
		req.Header.Set("Authorization", "Bearer secret")
		r := httptest.NewRecorder()
		h.ServeHTTP(r, req)
		if path == "/api/notifications/test" && r.Code != http.StatusBadRequest {
			t.Fatalf("notification status=%d body=%s", r.Code, r.Body.String())
		}
		if strings.Contains(path, "missing") && r.Code != http.StatusNotFound {
			t.Fatalf("missing check status=%d body=%s", r.Code, r.Body.String())
		}
	}
}

func TestCleanupExecutesOnlyFreshPreviewCandidates(t *testing.T) {
	preview := core.CleanupPreview{Containers: []core.CleanupCandidate{{Type: "container", ID: "c1", Name: "old"}}}
	exec := &fakeExecutor{cleanup: preview}
	scanner := fakeScanner{projects: []core.Project{}}
	h := New(config.Config{AdminToken: "secret"}, &fakeInventory{}, scanner, exec).Handler()

	req := httptest.NewRequest(http.MethodPost, "/api/cleanup", strings.NewReader(`{"items":[{"type":"image","id":"invented"}]}`))
	req.Header.Set("Authorization", "Bearer secret")
	r := httptest.NewRecorder()
	h.ServeHTTP(r, req)
	if r.Code != http.StatusBadRequest || len(exec.commands) != 0 {
		t.Fatalf("invalid cleanup status=%d commands=%#v body=%s", r.Code, exec.commands, r.Body.String())
	}

	req = httptest.NewRequest(http.MethodPost, "/api/cleanup", strings.NewReader(`{"items":[{"type":"container","id":"c1"}]}`))
	req.Header.Set("Authorization", "Bearer secret")
	r = httptest.NewRecorder()
	h.ServeHTTP(r, req)
	if r.Code != http.StatusOK || len(exec.commands) != 1 || exec.commands[0] != "docker rm c1" {
		t.Fatalf("cleanup status=%d commands=%#v body=%s", r.Code, exec.commands, r.Body.String())
	}
}

func TestCleanupPreviewAndEmptySelectionEndpoints(t *testing.T) {
	exec := &fakeExecutor{cleanup: core.CleanupPreview{Images: []core.CleanupCandidate{{Type: "image", ID: "i1"}}}}
	h := New(config.Config{AdminToken: "secret"}, &fakeInventory{}, fakeScanner{}, exec).Handler()
	req := httptest.NewRequest(http.MethodGet, "/api/cleanup/preview", nil)
	req.Header.Set("Authorization", "Bearer secret")
	r := httptest.NewRecorder()
	h.ServeHTTP(r, req)
	if r.Code != http.StatusOK || !strings.Contains(r.Body.String(), "i1") {
		t.Fatalf("preview status=%d body=%s", r.Code, r.Body.String())
	}
	req = httptest.NewRequest(http.MethodPost, "/api/cleanup", strings.NewReader(`{"items":[]}`))
	req.Header.Set("Authorization", "Bearer secret")
	r = httptest.NewRecorder()
	h.ServeHTTP(r, req)
	if r.Code != http.StatusBadRequest {
		t.Fatalf("empty cleanup status=%d body=%s", r.Code, r.Body.String())
	}
}

func TestBackupExportRedactsTokenAndRestorePreservesRuntimeSecurity(t *testing.T) {
	dir := t.TempDir()
	cfg := config.Config{Dir: dir, ListenAddr: "127.0.0.1:27680", AdminToken: "secret", ScanPaths: []string{"/old"}, Automation: config.AutomationConfig{WebhookType: "generic"}}
	inv := &fakeInventory{projects: []core.Project{{ID: "compose:old", Name: "old", Type: core.ProjectTypeCompose}}}
	s := New(cfg, inv, fakeScanner{}, &fakeExecutor{})
	h := s.Handler()
	req := httptest.NewRequest(http.MethodGet, "/api/config/export", nil)
	req.Header.Set("Authorization", "Bearer secret")
	r := httptest.NewRecorder()
	h.ServeHTTP(r, req)
	if r.Code != http.StatusOK || strings.Contains(r.Body.String(), "secret") {
		t.Fatalf("export status=%d body=%s", r.Code, r.Body.String())
	}
	var bundle backupBundle
	if err := yaml.Unmarshal(r.Body.Bytes(), &bundle); err != nil {
		t.Fatal(err)
	}
	bundle.Config.AdminToken = "replacement"
	bundle.Config.ListenAddr = "0.0.0.0:9999"
	bundle.Config.ScanPaths = []string{"/restored"}
	bundle.Inventory = []core.Project{{ID: "compose:new", Name: "new", Type: core.ProjectTypeCompose}}
	body, _ := yaml.Marshal(bundle)
	req = httptest.NewRequest(http.MethodPost, "/api/config/restore", strings.NewReader(string(body)))
	req.Header.Set("Authorization", "Bearer secret")
	r = httptest.NewRecorder()
	h.ServeHTTP(r, req)
	if r.Code != http.StatusOK || s.currentConfig().AdminToken != "secret" || s.currentConfig().ListenAddr != "127.0.0.1:27680" || s.currentConfig().ScanPaths[0] != "/restored" || inv.projects[0].ID != "compose:new" {
		t.Fatalf("restore status=%d cfg=%#v projects=%#v body=%s", r.Code, s.currentConfig(), inv.projects, r.Body.String())
	}
}

func TestRestoreRejectsInvalidAndUnsupportedBackups(t *testing.T) {
	h := New(config.Config{AdminToken: "secret"}, &fakeInventory{}, fakeScanner{}, &fakeExecutor{}).Handler()
	for _, body := range []string{"not: [", "version: 2\n"} {
		req := httptest.NewRequest(http.MethodPost, "/api/config/restore", strings.NewReader(body))
		req.Header.Set("Authorization", "Bearer secret")
		r := httptest.NewRecorder()
		h.ServeHTTP(r, req)
		if r.Code != http.StatusBadRequest {
			t.Fatalf("body=%q status=%d response=%s", body, r.Code, r.Body.String())
		}
	}
}

func TestTemplateCRUD(t *testing.T) {
	templates := store.NewTemplateStore(filepath.Join(t.TempDir(), "templates.yaml"))
	s := New(config.Config{AdminToken: "secret"}, &fakeInventory{}, fakeScanner{}, &fakeExecutor{}).WithTemplateStore(templates)
	h := s.Handler()
	req := httptest.NewRequest(http.MethodPost, "/api/templates", strings.NewReader(`{"name":"Redis","mode":"container","container":{"image":"redis:7","ports":["6379:6379"]}}`))
	req.Header.Set("Authorization", "Bearer secret")
	r := httptest.NewRecorder()
	h.ServeHTTP(r, req)
	if r.Code != http.StatusOK {
		t.Fatalf("create status=%d body=%s", r.Code, r.Body.String())
	}
	var created core.DeployTemplate
	_ = json.NewDecoder(r.Body).Decode(&created)
	if created.ID == "" {
		t.Fatal("template id should be generated")
	}

	req = httptest.NewRequest(http.MethodGet, "/api/templates", nil)
	req.Header.Set("Authorization", "Bearer secret")
	r = httptest.NewRecorder()
	h.ServeHTTP(r, req)
	if r.Code != http.StatusOK || !strings.Contains(r.Body.String(), "Redis") {
		t.Fatalf("list status=%d body=%s", r.Code, r.Body.String())
	}

	req = httptest.NewRequest(http.MethodDelete, "/api/templates/"+created.ID, nil)
	req.Header.Set("Authorization", "Bearer secret")
	r = httptest.NewRecorder()
	h.ServeHTTP(r, req)
	if r.Code != http.StatusOK {
		t.Fatalf("delete status=%d body=%s", r.Code, r.Body.String())
	}
}

func TestTemplateValidationUpdateAndNotFound(t *testing.T) {
	templates := store.NewTemplateStore(filepath.Join(t.TempDir(), "templates.yaml"))
	s := New(config.Config{AdminToken: "secret"}, &fakeInventory{}, fakeScanner{}, &fakeExecutor{}).WithTemplateStore(templates)
	h := s.Handler()
	for _, body := range []string{
		`{"name":"","mode":"container","container":{"image":"redis:7"}}`,
		`{"name":"Bad","mode":"container","container":{"image":"redis:7","ports":["bad"]}}`,
		`{"name":"Bad","mode":"other"}`,
	} {
		req := httptest.NewRequest(http.MethodPost, "/api/templates", strings.NewReader(body))
		req.Header.Set("Authorization", "Bearer secret")
		r := httptest.NewRecorder()
		h.ServeHTTP(r, req)
		if r.Code != http.StatusBadRequest {
			t.Fatalf("body=%s status=%d response=%s", body, r.Code, r.Body.String())
		}
	}

	req := httptest.NewRequest(http.MethodPost, "/api/templates", strings.NewReader(`{"id":"fixed","name":"Redis","mode":"container","container":{"image":"redis:7"}}`))
	req.Header.Set("Authorization", "Bearer secret")
	r := httptest.NewRecorder()
	h.ServeHTTP(r, req)
	if r.Code != http.StatusOK {
		t.Fatalf("create status=%d body=%s", r.Code, r.Body.String())
	}
	req = httptest.NewRequest(http.MethodPost, "/api/templates", strings.NewReader(`{"id":"fixed","name":"Redis 8","mode":"container","container":{"image":"redis:8"}}`))
	req.Header.Set("Authorization", "Bearer secret")
	r = httptest.NewRecorder()
	h.ServeHTTP(r, req)
	if r.Code != http.StatusOK || !strings.Contains(r.Body.String(), "Redis 8") {
		t.Fatalf("update status=%d body=%s", r.Code, r.Body.String())
	}
	req = httptest.NewRequest(http.MethodDelete, "/api/templates/missing", nil)
	req.Header.Set("Authorization", "Bearer secret")
	r = httptest.NewRecorder()
	h.ServeHTTP(r, req)
	if r.Code != http.StatusNotFound {
		t.Fatalf("missing delete status=%d body=%s", r.Code, r.Body.String())
	}
}

func TestOperationHistoryValidationAndEmptyStore(t *testing.T) {
	emptyHandler := New(config.Config{AdminToken: "secret"}, &fakeInventory{}, fakeScanner{}, &fakeExecutor{}).Handler()
	req := httptest.NewRequest(http.MethodGet, "/api/operations", nil)
	req.Header.Set("Authorization", "Bearer secret")
	r := httptest.NewRecorder()
	emptyHandler.ServeHTTP(r, req)
	if r.Code != http.StatusOK || strings.TrimSpace(r.Body.String()) != "[]" {
		t.Fatalf("empty history status=%d body=%s", r.Code, r.Body.String())
	}
	h := New(config.Config{AdminToken: "secret"}, &fakeInventory{}, fakeScanner{}, &fakeExecutor{}).WithOperationLog(store.NewOperationStore(filepath.Join(t.TempDir(), "operations.jsonl"))).Handler()
	for _, path := range []string{"/api/operations?limit=0", "/api/operations?failed=nope"} {
		req := httptest.NewRequest(http.MethodGet, path, nil)
		req.Header.Set("Authorization", "Bearer secret")
		r := httptest.NewRecorder()
		h.ServeHTTP(r, req)
		if r.Code != http.StatusBadRequest {
			t.Fatalf("invalid history path=%s status=%d body=%s", path, r.Code, r.Body.String())
		}
	}
	if got := truncateOperationText(strings.Repeat("x", 4100)); !strings.HasSuffix(got, "[truncated]") {
		t.Fatalf("truncated text suffix missing: %q", got[len(got)-20:])
	}
}

func TestNotifierPropagatesClientErrors(t *testing.T) {
	client := &http.Client{Transport: roundTripFunc(func(*http.Request) (*http.Response, error) { return nil, errors.New("network down") })}
	err := (HTTPNotifier{Client: client}).Notify(context.Background(), Notification{WebhookURL: "http://localhost/hook", WebhookType: "generic"})
	if err == nil || !strings.Contains(err.Error(), "network down") {
		t.Fatalf("Notify() error = %v", err)
	}
}

type roundTripFunc func(*http.Request) (*http.Response, error)

func (f roundTripFunc) RoundTrip(req *http.Request) (*http.Response, error) { return f(req) }

func TestComposePreviewReturnsNormalizedBeforeAfterContent(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "compose.yml")
	if err := os.WriteFile(path, []byte("services:\n  web:\n    image: nginx:1\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	h := New(config.Config{AdminToken: "secret"}, &fakeInventory{}, fakeScanner{}, &fakeExecutor{}).Handler()
	body := `{"name":"app","composePath":"` + path + `","composeContent":"services:\n  web:\n    image: nginx:2\n"}`
	req := httptest.NewRequest(http.MethodPost, "/api/deploy/compose/preview", strings.NewReader(body))
	req.Header.Set("Authorization", "Bearer secret")
	r := httptest.NewRecorder()
	h.ServeHTTP(r, req)
	if r.Code != http.StatusOK || !strings.Contains(r.Body.String(), `"overwrites":true`) || !strings.Contains(r.Body.String(), "nginx:1") || !strings.Contains(r.Body.String(), "nginx:2") {
		t.Fatalf("preview status=%d body=%s", r.Code, r.Body.String())
	}
	contents, _ := os.ReadFile(path)
	if strings.Contains(string(contents), "nginx:2") {
		t.Fatal("preview must not modify compose file")
	}
}

func TestComposeDeployRejectsFileCreatedAfterNewFilePreview(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "compose.yml")
	h := New(config.Config{AdminToken: "secret"}, &fakeInventory{}, fakeScanner{}, &fakeExecutor{}).Handler()
	body := `{"name":"app","composePath":"` + path + `","composeContent":"services:\n  web:\n    image: nginx:2\n"}`
	req := httptest.NewRequest(http.MethodPost, "/api/deploy/compose/preview", strings.NewReader(body))
	req.Header.Set("Authorization", "Bearer secret")
	r := httptest.NewRecorder()
	h.ServeHTTP(r, req)
	if r.Code != http.StatusOK || !strings.Contains(r.Body.String(), `"existingHash":"absent"`) {
		t.Fatalf("preview status=%d body=%s", r.Code, r.Body.String())
	}
	if err := os.WriteFile(path, []byte("services:\n  other:\n    image: busybox\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	body = strings.TrimSuffix(body, "}") + `,"expectedExistingHash":"absent"}`
	req = httptest.NewRequest(http.MethodPost, "/api/deploy/compose", strings.NewReader(body))
	req.Header.Set("Authorization", "Bearer secret")
	r = httptest.NewRecorder()
	h.ServeHTTP(r, req)
	if r.Code != http.StatusConflict || !strings.Contains(r.Body.String(), "changed after preview") {
		t.Fatalf("deploy status=%d body=%s", r.Code, r.Body.String())
	}
}

func TestComposeDeployRejectsFileChangedDuringValidation(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "compose.yml")
	original := "services:\n  web:\n    image: nginx:1\n"
	concurrent := "services:\n  web:\n    image: nginx:concurrent\n"
	if err := os.WriteFile(path, []byte(original), 0o600); err != nil {
		t.Fatal(err)
	}
	exec := &composeMutationExecutor{fakeExecutor: &fakeExecutor{}, path: path, content: concurrent}
	h := New(config.Config{AdminToken: "secret"}, &fakeInventory{}, fakeScanner{}, exec).Handler()
	body := `{"name":"app","composePath":"` + path + `","composeContent":"services:\n  web:\n    image: nginx:2\n","expectedExistingHash":"` + docker.ComposeContentHash(original) + `"}`
	req := httptest.NewRequest(http.MethodPost, "/api/deploy/compose", strings.NewReader(body))
	req.Header.Set("Authorization", "Bearer secret")
	r := httptest.NewRecorder()
	h.ServeHTTP(r, req)

	if r.Code != http.StatusConflict || !strings.Contains(r.Body.String(), "changed after preview") {
		t.Fatalf("deploy status=%d body=%s", r.Code, r.Body.String())
	}
	contents, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if string(contents) != concurrent {
		t.Fatalf("concurrent compose edit was overwritten:\n%s", contents)
	}
}

var _ docker.Executor = (*fakeExecutor)(nil)
