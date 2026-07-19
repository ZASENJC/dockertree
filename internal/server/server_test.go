package server

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"reflect"
	"strings"
	"testing"
	"time"

	"dockertree/internal/config"
	"dockertree/internal/core"
	"dockertree/internal/docker"
)

type streamingFakeExecutor struct {
	fakeExecutor
	release chan struct{}
}

type progressStreamingExecutor struct {
	fakeExecutor
}

func (f *progressStreamingExecutor) ExecuteStream(_ context.Context, cmd docker.Command, emit func([]byte)) (docker.Result, error) {
	f.commands = append(f.commands, cmd.String())
	if strings.HasSuffix(cmd.String(), " config --format json") {
		output := `{"services":{"app":{"image":"registry/app:latest"}}}`
		emit([]byte(output))
		return docker.Result{Command: cmd.String(), Output: output}, nil
	}
	if strings.Contains(cmd.String(), " --progress json pull") {
		output := strings.Join([]string{
			`{"id":"Image registry/app:latest","status":"Working","text":"Pulling"}`,
			`{"id":"Image registry/app:latest","status":"Done","text":"Pulled"}`,
		}, "\n") + "\n"
		split := strings.Index(output, `"status":"Done"`) - 4
		emit([]byte(output[:split]))
		emit([]byte(output[split:]))
		return docker.Result{Command: cmd.String(), Output: output}, nil
	}
	emit([]byte("done\n"))
	return docker.Result{Command: cmd.String(), Output: "done\n"}, nil
}

func (f *streamingFakeExecutor) Execute(_ context.Context, cmd docker.Command) (docker.Result, error) {
	<-f.release
	return docker.Result{Command: cmd.String(), Output: "pulling\ndone\n"}, nil
}

func (f *streamingFakeExecutor) ExecuteStream(_ context.Context, cmd docker.Command, emit func([]byte)) (docker.Result, error) {
	emit([]byte("pulling\n"))
	<-f.release
	emit([]byte("done\n"))
	return docker.Result{Command: cmd.String(), Output: "pulling\ndone\n"}, nil
}

type fakeInventory struct {
	projects []core.Project
	saves    int
	loadErr  error
	saveErr  error
}

func (f *fakeInventory) LoadInventory() ([]core.Project, error) {
	if f.loadErr != nil {
		return nil, f.loadErr
	}
	return f.projects, nil
}
func (f *fakeInventory) SaveInventory(p []core.Project) error {
	if f.saveErr != nil {
		return f.saveErr
	}
	f.projects = p
	f.saves++
	return nil
}

type fakeScanner struct {
	projects []core.Project
	err      error
}

func (f fakeScanner) Scan(context.Context) ([]core.Project, error) {
	return f.projects, f.err
}

type fakeExecutor struct {
	commands []string
	logs     string
	logCalls []docker.LogOptions
	search   []docker.SearchResult
	images   []docker.LocalImage
	stats    []core.ContainerStats
	inspect  core.ContainerInspect
	checks   map[string]core.UpdateCheck
	cleanup  core.CleanupPreview
	outputs  map[string]string
	result   docker.Result
	err      error
	failCall int
}

func (f *fakeExecutor) Execute(_ context.Context, cmd docker.Command) (docker.Result, error) {
	f.commands = append(f.commands, cmd.String())
	if f.err != nil && (f.failCall == 0 || f.failCall == len(f.commands)) {
		if f.result.Command == "" {
			f.result.Command = cmd.String()
		}
		return f.result, f.err
	}
	output := "ok"
	if configured, ok := f.outputs[cmd.String()]; ok {
		output = configured
	} else if strings.HasSuffix(cmd.String(), " config --format json") {
		output = `{"services":{}}`
	}
	return docker.Result{Command: cmd.String(), Output: output, ExitCode: 0}, nil
}

func (f *fakeExecutor) Logs(_ context.Context, project core.Project, service string, options docker.LogOptions) (string, error) {
	f.logCalls = append(f.logCalls, options)
	return f.logs + project.Name + ":" + service, nil
}

func (f *fakeExecutor) SearchImages(_ context.Context, term string) ([]docker.SearchResult, error) {
	if term == "redis" {
		return f.search, nil
	}
	return nil, nil
}

func (f *fakeExecutor) LocalImages(_ context.Context) ([]docker.LocalImage, error) {
	return f.images, nil
}

func (f *fakeExecutor) Stats(_ context.Context) ([]core.ContainerStats, error) {
	return f.stats, f.err
}

func (f *fakeExecutor) Inspect(_ context.Context, id string) (core.ContainerInspect, error) {
	if f.inspect.ContainerID == "" {
		f.inspect.ContainerID = id
	}
	return f.inspect, f.err
}

func (f *fakeExecutor) CheckUpdate(_ context.Context, project core.Project) (core.UpdateCheck, error) {
	if check, ok := f.checks[project.ID]; ok {
		return check, f.err
	}
	return core.UpdateCheck{ProjectID: project.ID, ProjectName: project.Name, Status: "current"}, f.err
}

func (f *fakeExecutor) CleanupPreview(_ context.Context) (core.CleanupPreview, error) {
	return f.cleanup, f.err
}

func TestAPIsRequireToken(t *testing.T) {
	h := New(config.Config{AdminToken: "secret"}, &fakeInventory{}, fakeScanner{}, &fakeExecutor{}).Handler()
	r := httptest.NewRecorder()
	h.ServeHTTP(r, httptest.NewRequest(http.MethodGet, "/api/projects", nil))
	if r.Code != http.StatusUnauthorized {
		t.Fatalf("status = %d", r.Code)
	}
}

func TestScanUpdatesInventoryAndProjectsEndpoint(t *testing.T) {
	inv := &fakeInventory{}
	scanner := fakeScanner{projects: []core.Project{{ID: "compose:mtp", Name: "mtp", Type: core.ProjectTypeCompose}}}
	h := New(config.Config{AdminToken: "secret"}, inv, scanner, &fakeExecutor{}).Handler()

	req := httptest.NewRequest(http.MethodPost, "/api/scan", nil)
	req.Header.Set("Authorization", "Bearer secret")
	r := httptest.NewRecorder()
	h.ServeHTTP(r, req)
	if r.Code != http.StatusOK {
		t.Fatalf("scan status = %d body=%s", r.Code, r.Body.String())
	}

	req = httptest.NewRequest(http.MethodGet, "/api/projects", nil)
	req.Header.Set("Authorization", "Bearer secret")
	r = httptest.NewRecorder()
	h.ServeHTTP(r, req)
	var projects []core.Project
	if err := json.NewDecoder(r.Body).Decode(&projects); err != nil {
		t.Fatal(err)
	}
	if len(projects) != 1 || !strings.Contains(projects[0].ID, "mtp") {
		t.Fatalf("unexpected projects: %#v", projects)
	}
}

func TestScanPreservesLongLivedProjectMetadata(t *testing.T) {
	inv := &fakeInventory{projects: []core.Project{{
		ID:           "compose:app",
		Name:         "app",
		Type:         core.ProjectTypeCompose,
		WorkingDir:   "/srv/app",
		ConfigFiles:  []string{"/srv/app/compose.yml"},
		Aliases:      []string{"primary"},
		Tags:         []string{"photos"},
		Favorite:     true,
		LastAction:   "restart",
		LastExitCode: 17,
	}}}
	scanner := fakeScanner{projects: []core.Project{{
		ID:          "compose:app:abc123",
		Name:        "app",
		Type:        core.ProjectTypeCompose,
		Status:      "running",
		WorkingDir:  "/srv/app",
		ConfigFiles: []string{"/srv/app/compose.yml"},
		Services:    []core.Service{{Name: "api", ContainerID: "new-container"}},
	}}}
	h := New(config.Config{AdminToken: "secret"}, inv, scanner, &fakeExecutor{}).Handler()

	req := httptest.NewRequest(http.MethodPost, "/api/scan", nil)
	req.Header.Set("Authorization", "Bearer secret")
	r := httptest.NewRecorder()
	h.ServeHTTP(r, req)

	if r.Code != http.StatusOK {
		t.Fatalf("status=%d body=%s", r.Code, r.Body.String())
	}
	if len(inv.projects) != 1 {
		t.Fatalf("projects=%#v", inv.projects)
	}
	got := inv.projects[0]
	if got.ID != "compose:app:abc123" || got.Status != "running" || got.Services[0].ContainerID != "new-container" {
		t.Fatalf("scan data was not refreshed: %#v", got)
	}
	if !got.Favorite || !reflect.DeepEqual(got.Tags, []string{"photos"}) || !reflect.DeepEqual(got.Aliases, []string{"primary"}) || got.LastAction != "restart" || got.LastExitCode != 17 {
		t.Fatalf("long-lived metadata was lost: %#v", got)
	}
}

func TestProjectDetailAndPreviewEndpoints(t *testing.T) {
	inv := &fakeInventory{projects: []core.Project{{
		ID:          "compose:mtp",
		Name:        "mtp",
		Type:        core.ProjectTypeCompose,
		WorkingDir:  "/srv/mtp",
		ConfigFiles: []string{"/srv/mtp/docker-compose.yml"},
		Services:    []core.Service{{Name: "app", Image: "registry/app:latest"}},
	}}}
	h := New(config.Config{AdminToken: "secret"}, inv, fakeScanner{}, &fakeExecutor{}).Handler()

	req := httptest.NewRequest(http.MethodGet, "/api/projects/compose:mtp", nil)
	req.Header.Set("Authorization", "Bearer secret")
	r := httptest.NewRecorder()
	h.ServeHTTP(r, req)
	if r.Code != http.StatusOK || !strings.Contains(r.Body.String(), "mtp") {
		t.Fatalf("detail status=%d body=%s", r.Code, r.Body.String())
	}

	req = httptest.NewRequest(http.MethodPost, "/api/projects/compose:mtp/actions/preview-update", nil)
	req.Header.Set("Authorization", "Bearer secret")
	r = httptest.NewRecorder()
	h.ServeHTTP(r, req)
	if r.Code != http.StatusOK || !strings.Contains(r.Body.String(), "docker compose") {
		t.Fatalf("preview status=%d body=%s", r.Code, r.Body.String())
	}
}

func TestProjectMetadataEndpointNormalizesAndPersistsValues(t *testing.T) {
	inv := &fakeInventory{projects: []core.Project{{ID: "compose:mtp", Name: "mtp", Type: core.ProjectTypeCompose}}}
	h := New(config.Config{AdminToken: "secret"}, inv, fakeScanner{}, &fakeExecutor{}).Handler()

	req := httptest.NewRequest(http.MethodPatch, "/api/projects/compose:mtp/metadata", strings.NewReader(`{"favorite":true,"tags":[" photos ","Photos","","home"],"aliases":[" primary ","primary","相册"]}`))
	req.Header.Set("Authorization", "Bearer secret")
	r := httptest.NewRecorder()
	h.ServeHTTP(r, req)

	if r.Code != http.StatusOK {
		t.Fatalf("status=%d body=%s", r.Code, r.Body.String())
	}
	if inv.saves != 1 || len(inv.projects) != 1 {
		t.Fatalf("metadata was not saved: saves=%d projects=%#v", inv.saves, inv.projects)
	}
	got := inv.projects[0]
	if !got.Favorite || !reflect.DeepEqual(got.Tags, []string{"photos", "home"}) || !reflect.DeepEqual(got.Aliases, []string{"primary", "相册"}) {
		t.Fatalf("metadata was not normalized: %#v", got)
	}
}

func TestProjectMetadataEndpointRejectsOverlongItems(t *testing.T) {
	inv := &fakeInventory{projects: []core.Project{{
		ID:      "compose:mtp",
		Name:    "mtp",
		Type:    core.ProjectTypeCompose,
		Tags:    []string{"existing"},
		Aliases: []string{"primary"},
	}}}
	h := New(config.Config{AdminToken: "secret"}, inv, fakeScanner{}, &fakeExecutor{}).Handler()
	req := httptest.NewRequest(http.MethodPatch, "/api/projects/compose:mtp/metadata", strings.NewReader(`{"tags":["replacement"],"aliases":["`+strings.Repeat("a", 65)+`"]}`))
	req.Header.Set("Authorization", "Bearer secret")
	r := httptest.NewRecorder()
	h.ServeHTTP(r, req)
	if r.Code != http.StatusBadRequest || !strings.Contains(r.Body.String(), "64 characters") || inv.saves != 0 {
		t.Fatalf("status=%d saves=%d body=%s", r.Code, inv.saves, r.Body.String())
	}
	if !reflect.DeepEqual(inv.projects[0].Tags, []string{"existing"}) || !reflect.DeepEqual(inv.projects[0].Aliases, []string{"primary"}) {
		t.Fatalf("invalid patch partially changed metadata: %#v", inv.projects[0])
	}
}

func TestPreviewUpdateDoesNotInspectOrBuildComposeConfiguration(t *testing.T) {
	project := core.Project{
		ID:          "compose:app",
		Name:        "app",
		Type:        core.ProjectTypeCompose,
		WorkingDir:  "/srv/app",
		ConfigFiles: []string{"/srv/app/compose.yml"},
		Services:    []core.Service{{Name: "api", Image: "registry/app:dev"}},
	}
	exec := &fakeExecutor{err: errors.New("compose config must not run")}
	h := New(config.Config{AdminToken: "secret"}, &fakeInventory{projects: []core.Project{project}}, fakeScanner{}, exec).Handler()

	req := httptest.NewRequest(http.MethodPost, "/api/projects/compose:app/actions/preview-update", nil)
	req.Header.Set("Authorization", "Bearer secret")
	r := httptest.NewRecorder()
	h.ServeHTTP(r, req)

	if r.Code != http.StatusOK || strings.Contains(r.Body.String(), `"requiresBuild":true`) || strings.Contains(r.Body.String(), " build") {
		t.Fatalf("preview status=%d body=%s", r.Code, r.Body.String())
	}
	if len(exec.commands) != 0 {
		t.Fatalf("commands=%#v", exec.commands)
	}
}

func TestStaticAssetsAreServed(t *testing.T) {
	s := New(config.Config{AdminToken: "secret"}, &fakeInventory{}, fakeScanner{}, &fakeExecutor{})
	h := s.Handler()

	for _, path := range []string{"/", "/styles.css", "/app.js"} {
		r := httptest.NewRecorder()
		h.ServeHTTP(r, httptest.NewRequest(http.MethodGet, path, nil))
		if r.Code != http.StatusOK {
			t.Fatalf("%s status = %d", path, r.Code)
		}
		if got := r.Header().Get("Cache-Control"); got != "no-store" {
			t.Fatalf("%s Cache-Control = %q", path, got)
		}
	}

	r := httptest.NewRecorder()
	s.asset(r, httptest.NewRequest(http.MethodGet, "/missing.js", nil))
	if r.Code != http.StatusNotFound {
		t.Fatalf("missing asset status = %d", r.Code)
	}
}

func TestProjectEndpointsReturnNotFoundAndStoreErrors(t *testing.T) {
	h := New(config.Config{AdminToken: "secret"}, &fakeInventory{}, fakeScanner{}, &fakeExecutor{}).Handler()
	for _, tc := range []struct {
		method string
		path   string
	}{
		{http.MethodGet, "/api/projects/compose:missing"},
		{http.MethodPost, "/api/projects/compose:missing/actions/preview-update"},
		{http.MethodPost, "/api/projects/compose:missing/actions/redeploy"},
		{http.MethodPost, "/api/projects/compose:missing/actions/restart"},
		{http.MethodGet, "/api/projects/compose:missing/logs"},
		{http.MethodDelete, "/api/projects/compose:missing"},
	} {
		req := httptest.NewRequest(tc.method, tc.path, nil)
		req.Header.Set("Authorization", "Bearer secret")
		r := httptest.NewRecorder()
		h.ServeHTTP(r, req)
		if r.Code != http.StatusNotFound {
			t.Fatalf("%s %s status=%d body=%s", tc.method, tc.path, r.Code, r.Body.String())
		}
	}

	h = New(config.Config{AdminToken: "secret"}, &fakeInventory{loadErr: errors.New("load boom")}, fakeScanner{}, &fakeExecutor{}).Handler()
	req := httptest.NewRequest(http.MethodGet, "/api/projects", nil)
	req.Header.Set("Authorization", "Bearer secret")
	r := httptest.NewRecorder()
	h.ServeHTTP(r, req)
	if r.Code != http.StatusInternalServerError || !strings.Contains(r.Body.String(), "load boom") {
		t.Fatalf("load error status=%d body=%s", r.Code, r.Body.String())
	}
}

func TestBadRequestEndpointsReturnReadableErrors(t *testing.T) {
	standalone := core.Project{ID: "container:solo", Name: "solo", Type: core.ProjectTypeStandalone}
	h := New(config.Config{AdminToken: "secret"}, &fakeInventory{projects: []core.Project{standalone}}, fakeScanner{}, &fakeExecutor{}).Handler()
	for _, tc := range []struct {
		method string
		path   string
		body   string
		want   string
	}{
		{http.MethodDelete, "/api/images", `{}`, "image ref is required"},
		{http.MethodPost, "/api/deploy/container/preview", `not-json`, "invalid character"},
		{http.MethodPost, "/api/deploy/container/preview", `{}`, "image name is required"},
		{http.MethodPost, "/api/deploy/compose/preview", `{}`, "project name is required"},
		{http.MethodDelete, "/api/projects/container:solo", ``, "project has no compose file"},
	} {
		req := httptest.NewRequest(tc.method, tc.path, strings.NewReader(tc.body))
		req.Header.Set("Authorization", "Bearer secret")
		r := httptest.NewRecorder()
		h.ServeHTTP(r, req)
		if r.Code != http.StatusBadRequest || !strings.Contains(r.Body.String(), tc.want) {
			t.Fatalf("%s %s status=%d body=%s want=%q", tc.method, tc.path, r.Code, r.Body.String(), tc.want)
		}
	}

	req := httptest.NewRequest(http.MethodPost, "/api/projects/container:solo/actions/deploy", nil)
	req.Header.Set("Authorization", "Bearer secret")
	r := httptest.NewRecorder()
	h.ServeHTTP(r, req)
	if r.Code != http.StatusBadRequest || !strings.Contains(r.Body.String(), "Standalone containers cannot be safely recreated") {
		t.Fatalf("standalone deploy status=%d body=%s", r.Code, r.Body.String())
	}

	req = httptest.NewRequest(http.MethodPost, "/api/projects/container:solo/actions/redeploy", nil)
	req.Header.Set("Authorization", "Bearer secret")
	r = httptest.NewRecorder()
	h.ServeHTTP(r, req)
	if r.Code != http.StatusBadRequest || !strings.Contains(r.Body.String(), "compose file") {
		t.Fatalf("standalone redeploy status=%d body=%s", r.Code, r.Body.String())
	}
}

func TestScanReturnsPartialResultWhenSavingInventoryFails(t *testing.T) {
	inv := &fakeInventory{saveErr: errors.New("save boom")}
	scanner := fakeScanner{projects: []core.Project{{ID: "compose:mtp", Name: "mtp", Type: core.ProjectTypeCompose}}}
	h := New(config.Config{AdminToken: "secret"}, inv, scanner, &fakeExecutor{}).Handler()

	req := httptest.NewRequest(http.MethodPost, "/api/scan", nil)
	req.Header.Set("Authorization", "Bearer secret")
	r := httptest.NewRecorder()
	h.ServeHTTP(r, req)

	if r.Code != http.StatusInternalServerError {
		t.Fatalf("status=%d body=%s", r.Code, r.Body.String())
	}
	for _, want := range []string{"save boom", "compose:mtp", "result"} {
		if !strings.Contains(r.Body.String(), want) {
			t.Fatalf("body missing %q: %s", want, r.Body.String())
		}
	}
}

func TestDeployExecutesConservativeComposeCommands(t *testing.T) {
	inv := &fakeInventory{projects: []core.Project{{
		ID:          "compose:mtp",
		Name:        "mtp",
		Type:        core.ProjectTypeCompose,
		WorkingDir:  "/srv/mtp",
		ConfigFiles: []string{"/srv/mtp/docker-compose.yml"},
		Services:    []core.Service{{Name: "app", Image: "mtp-app"}},
	}}}
	exec := &fakeExecutor{}
	h := New(config.Config{AdminToken: "secret"}, inv, fakeScanner{}, exec).Handler()

	req := httptest.NewRequest(http.MethodPost, "/api/projects/compose:mtp/actions/deploy", nil)
	req.Header.Set("Authorization", "Bearer secret")
	r := httptest.NewRecorder()
	h.ServeHTTP(r, req)

	if r.Code != http.StatusOK {
		t.Fatalf("status = %d body=%s", r.Code, r.Body.String())
	}
	want := []string{
		"docker compose -f /srv/mtp/docker-compose.yml --progress json pull",
		"docker compose -f /srv/mtp/docker-compose.yml up -d",
	}
	if strings.Join(exec.commands, "\n") != strings.Join(want, "\n") {
		t.Fatalf("commands = %#v", exec.commands)
	}
}

func TestProjectRedeployUsesCurrentImagesAndForcesRecreation(t *testing.T) {
	project := core.Project{
		ID: "compose:mtp", Name: "mtp", Type: core.ProjectTypeCompose, WorkingDir: "/srv/mtp",
		ConfigFiles: []string{"/srv/mtp/docker-compose.yml"}, Services: []core.Service{{Name: "app", Image: "mtp-app"}},
	}
	inv := &fakeInventory{projects: []core.Project{project}}
	exec := &fakeExecutor{}
	h := New(config.Config{AdminToken: "secret"}, inv, fakeScanner{projects: []core.Project{project}}, exec).Handler()

	req := httptest.NewRequest(http.MethodPost, "/api/projects/compose:mtp/actions/redeploy", nil)
	req.Header.Set("Authorization", "Bearer secret")
	r := httptest.NewRecorder()
	h.ServeHTTP(r, req)

	if r.Code != http.StatusOK {
		t.Fatalf("status=%d body=%s", r.Code, r.Body.String())
	}
	want := []string{"docker compose -f /srv/mtp/docker-compose.yml up -d --force-recreate"}
	if !reflect.DeepEqual(exec.commands, want) {
		t.Fatalf("commands=%#v want=%#v", exec.commands, want)
	}
	if inv.saves != 1 {
		t.Fatalf("inventory saves=%d", inv.saves)
	}
}

func TestDeployRefreshesInventoryAfterComposeUpdate(t *testing.T) {
	inv := &fakeInventory{projects: []core.Project{{
		ID:          "compose:mtp",
		Name:        "mtp",
		Type:        core.ProjectTypeCompose,
		WorkingDir:  "/srv/mtp",
		ConfigFiles: []string{"/srv/mtp/docker-compose.yml"},
		Services:    []core.Service{{Name: "app", Image: "registry/app:latest"}},
	}}}
	scanner := fakeScanner{projects: []core.Project{{ID: "compose:after-update", Name: "after-update", Type: core.ProjectTypeCompose}}}
	h := New(config.Config{AdminToken: "secret"}, inv, scanner, &fakeExecutor{}).Handler()

	req := httptest.NewRequest(http.MethodPost, "/api/projects/compose:mtp/actions/deploy", nil)
	req.Header.Set("Authorization", "Bearer secret")
	r := httptest.NewRecorder()
	h.ServeHTTP(r, req)

	if r.Code != http.StatusOK {
		t.Fatalf("status = %d body=%s", r.Code, r.Body.String())
	}
	if inv.saves != 1 || inv.projects[0].ID != "compose:after-update" {
		t.Fatalf("inventory was not refreshed after deploy: saves=%d projects=%#v", inv.saves, inv.projects)
	}
}

func TestLifecycleAndLogsEndpointsUseProjectContext(t *testing.T) {
	projects := []core.Project{{
		ID:          "compose:mtp",
		Name:        "mtp",
		Type:        core.ProjectTypeCompose,
		WorkingDir:  "/srv/mtp",
		ConfigFiles: []string{"/srv/mtp/docker-compose.yml"},
	}}
	inv := &fakeInventory{projects: projects}
	exec := &fakeExecutor{logs: "tail:"}
	h := New(config.Config{AdminToken: "secret"}, inv, fakeScanner{projects: projects}, exec).Handler()

	req := httptest.NewRequest(http.MethodPost, "/api/projects/compose:mtp/actions/restart", nil)
	req.Header.Set("Authorization", "Bearer secret")
	r := httptest.NewRecorder()
	h.ServeHTTP(r, req)
	if r.Code != http.StatusOK || exec.commands[0] != "docker compose -f /srv/mtp/docker-compose.yml restart" {
		t.Fatalf("restart failed status=%d commands=%#v", r.Code, exec.commands)
	}

	req = httptest.NewRequest(http.MethodGet, "/api/projects/compose:mtp/logs?service=app&tail=1000&timestamps=true", nil)
	req.Header.Set("Authorization", "Bearer secret")
	r = httptest.NewRecorder()
	h.ServeHTTP(r, req)
	if r.Code != http.StatusOK || !strings.Contains(r.Body.String(), "tail:mtp:app") {
		t.Fatalf("logs failed status=%d body=%s", r.Code, r.Body.String())
	}
	if len(exec.logCalls) != 1 || exec.logCalls[0].Tail != 1000 || !exec.logCalls[0].Timestamps {
		t.Fatalf("log options = %#v", exec.logCalls)
	}
}

func TestOperationRequestStreamsCommandOutputBeforeCompletion(t *testing.T) {
	projects := []core.Project{{
		ID:          "compose:mtp",
		Name:        "mtp",
		Type:        core.ProjectTypeCompose,
		WorkingDir:  "/srv/mtp",
		ConfigFiles: []string{"/srv/mtp/docker-compose.yml"},
	}}
	exec := &streamingFakeExecutor{release: make(chan struct{})}
	h := New(config.Config{AdminToken: "secret"}, &fakeInventory{projects: projects}, fakeScanner{projects: projects}, exec).Handler()
	ts := httptest.NewServer(h)
	defer ts.Close()

	request, err := http.NewRequest(http.MethodPost, ts.URL+"/api/projects/compose:mtp/actions/restart", nil)
	if err != nil {
		t.Fatal(err)
	}
	request.Header.Set("Authorization", "Bearer secret")
	request.Header.Set("Accept", "application/x-ndjson")

	responseCh := make(chan *http.Response, 1)
	errorCh := make(chan error, 1)
	go func() {
		response, requestErr := ts.Client().Do(request)
		if requestErr != nil {
			errorCh <- requestErr
			return
		}
		responseCh <- response
	}()

	var response *http.Response
	select {
	case response = <-responseCh:
	case requestErr := <-errorCh:
		close(exec.release)
		t.Fatal(requestErr)
	case <-time.After(300 * time.Millisecond):
		close(exec.release)
		t.Fatal("streaming response headers were not flushed before command completion")
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK || response.Header.Get("Content-Type") != "application/x-ndjson" {
		close(exec.release)
		t.Fatalf("status=%d content-type=%q", response.StatusCode, response.Header.Get("Content-Type"))
	}

	reader := bufio.NewReader(response.Body)
	for _, want := range []string{`"type":"start"`, `"type":"command"`, `"data":"pulling\n"`} {
		line, readErr := reader.ReadString('\n')
		if readErr != nil {
			close(exec.release)
			t.Fatalf("read stream event: %v", readErr)
		}
		if !strings.Contains(line, want) {
			close(exec.release)
			t.Fatalf("stream event %q does not contain %q", line, want)
		}
	}

	close(exec.release)
	remaining, err := reader.ReadString('\n')
	if err != nil {
		t.Fatalf("read final output event: %v", err)
	}
	if !strings.Contains(remaining, `"data":"done\n"`) {
		t.Fatalf("final output event = %q", remaining)
	}
	resultEvent, err := reader.ReadString('\n')
	if err != nil {
		t.Fatalf("read result event: %v", err)
	}
	if !strings.Contains(resultEvent, `"type":"result"`) || !strings.Contains(resultEvent, `"command":"docker compose`) {
		t.Fatalf("result event = %q", resultEvent)
	}
}

func TestOperationStreamPreservesJSONRequestBody(t *testing.T) {
	h := New(config.Config{AdminToken: "secret"}, &fakeInventory{}, fakeScanner{}, &fakeExecutor{}).Handler()
	ts := httptest.NewServer(h)
	defer ts.Close()

	request, err := http.NewRequest(http.MethodPost, ts.URL+"/api/deploy/container/preview", strings.NewReader(`{"name":"stream-preview","image":"redis:7"}`))
	if err != nil {
		t.Fatal(err)
	}
	request.Header.Set("Authorization", "Bearer secret")
	request.Header.Set("Accept", "application/x-ndjson")
	request.Header.Set("Content-Type", "application/json")
	response, err := ts.Client().Do(request)
	if err != nil {
		t.Fatal(err)
	}
	defer response.Body.Close()
	body, err := io.ReadAll(response.Body)
	if err != nil {
		t.Fatal(err)
	}
	text := string(body)
	if strings.Contains(text, "invalid Read on closed Body") || !strings.Contains(text, `"type":"result"`) || !strings.Contains(text, `docker run -d --name stream-preview redis:7`) {
		t.Fatalf("streamed preview lost its request body: %s", text)
	}
}

func TestContainerStatsEndpointReturnsReadOnlySnapshot(t *testing.T) {
	exec := &fakeExecutor{stats: []core.ContainerStats{{ContainerID: "abc123", Name: "redis", CPUPercent: 1.25, MemoryUsage: "42MiB / 1GiB", PIDs: 7}}}
	h := New(config.Config{AdminToken: "secret"}, &fakeInventory{}, fakeScanner{}, exec).Handler()
	req := httptest.NewRequest(http.MethodGet, "/api/containers/stats", nil)
	req.Header.Set("Authorization", "Bearer secret")
	r := httptest.NewRecorder()
	h.ServeHTTP(r, req)
	if r.Code != http.StatusOK || !strings.Contains(r.Body.String(), `"containerId":"abc123"`) || len(exec.commands) != 0 {
		t.Fatalf("status=%d commands=%#v body=%s", r.Code, exec.commands, r.Body.String())
	}
}

func TestLogEndpointsValidateTailLimit(t *testing.T) {
	project := core.Project{ID: "compose:mtp", Name: "mtp", Type: core.ProjectTypeCompose}
	h := New(config.Config{AdminToken: "secret"}, &fakeInventory{projects: []core.Project{project}}, fakeScanner{}, &fakeExecutor{}).Handler()
	for _, path := range []string{
		"/api/projects/compose:mtp/logs?tail=5001",
		"/api/containers/abc123/logs?tail=invalid",
	} {
		req := httptest.NewRequest(http.MethodGet, path, nil)
		req.Header.Set("Authorization", "Bearer secret")
		r := httptest.NewRecorder()
		h.ServeHTTP(r, req)
		if r.Code != http.StatusBadRequest || !strings.Contains(r.Body.String(), "tail must be between 1 and 5000") {
			t.Fatalf("path=%s status=%d body=%s", path, r.Code, r.Body.String())
		}
	}
}

func TestContainerLifecycleAndLogsEndpointsUseContainerID(t *testing.T) {
	projects := []core.Project{{ID: "compose:after-container-action", Name: "after-container-action", Type: core.ProjectTypeCompose}}
	inv := &fakeInventory{}
	exec := &fakeExecutor{}
	h := New(config.Config{AdminToken: "secret"}, inv, fakeScanner{projects: projects}, exec).Handler()

	req := httptest.NewRequest(http.MethodPost, "/api/containers/abc123/actions/restart", nil)
	req.Header.Set("Authorization", "Bearer secret")
	r := httptest.NewRecorder()
	h.ServeHTTP(r, req)
	if r.Code != http.StatusOK || exec.commands[0] != "docker restart abc123" {
		t.Fatalf("container restart status=%d commands=%#v body=%s", r.Code, exec.commands, r.Body.String())
	}
	if inv.saves != 1 || inv.projects[0].ID != "compose:after-container-action" {
		t.Fatalf("inventory was not refreshed after container lifecycle: saves=%d projects=%#v", inv.saves, inv.projects)
	}

	req = httptest.NewRequest(http.MethodGet, "/api/containers/abc123/logs", nil)
	req.Header.Set("Authorization", "Bearer secret")
	r = httptest.NewRecorder()
	h.ServeHTTP(r, req)
	if r.Code != http.StatusOK || exec.commands[1] != "docker logs --tail 300 abc123" || !strings.Contains(r.Body.String(), "ok") {
		t.Fatalf("container logs status=%d commands=%#v body=%s", r.Code, exec.commands, r.Body.String())
	}
}

func TestContainerUpdateCheckAndDeployTargetOnlySelectedComposeService(t *testing.T) {
	project := core.Project{
		ID:          "compose:app",
		Name:        "app",
		Type:        core.ProjectTypeCompose,
		WorkingDir:  "/srv/app",
		ConfigFiles: []string{"/srv/app/compose.yml"},
		Services: []core.Service{
			{Name: "web", ContainerID: "web123", Image: "registry/web:latest"},
			{Name: "worker", ContainerID: "worker456", Image: "registry/worker:latest"},
		},
	}
	checkCommand := "docker compose -f /srv/app/compose.yml --dry-run pull web"
	exec := &fakeExecutor{outputs: map[string]string{
		checkCommand: "DRY-RUN MODE - web Pulled",
	}}
	inv := &fakeInventory{projects: []core.Project{project}}
	scanner := fakeScanner{projects: []core.Project{project}}
	h := New(config.Config{AdminToken: "secret"}, inv, scanner, exec).Handler()

	req := httptest.NewRequest(http.MethodPost, "/api/containers/web123/actions/check-update", nil)
	req.Header.Set("Authorization", "Bearer secret")
	r := httptest.NewRecorder()
	h.ServeHTTP(r, req)
	if r.Code != http.StatusOK || !strings.Contains(r.Body.String(), `"status":"available"`) || !strings.Contains(r.Body.String(), checkCommand) {
		t.Fatalf("check status=%d body=%s commands=%#v", r.Code, r.Body.String(), exec.commands)
	}

	req = httptest.NewRequest(http.MethodPost, "/api/containers/web123/actions/deploy", nil)
	req.Header.Set("Authorization", "Bearer secret")
	r = httptest.NewRecorder()
	h.ServeHTTP(r, req)
	if r.Code != http.StatusOK {
		t.Fatalf("deploy status=%d body=%s", r.Code, r.Body.String())
	}
	want := []string{
		checkCommand,
		"docker compose -f /srv/app/compose.yml --progress json pull web",
		"docker compose -f /srv/app/compose.yml up -d --no-deps web",
	}
	if !reflect.DeepEqual(exec.commands, want) {
		t.Fatalf("commands=%#v want=%#v", exec.commands, want)
	}
	if inv.saves != 1 {
		t.Fatalf("inventory saves=%d", inv.saves)
	}
}

func TestContainerRedeployTargetsOnlySelectedComposeService(t *testing.T) {
	project := core.Project{
		ID: "compose:app", Name: "app", Type: core.ProjectTypeCompose, WorkingDir: "/srv/app",
		ConfigFiles: []string{"/srv/app/compose.yml"},
		Services: []core.Service{
			{Name: "web", ContainerID: "web123", Image: "registry/web:latest"},
			{Name: "worker", ContainerID: "worker456", Image: "registry/worker:latest"},
		},
	}
	inv := &fakeInventory{projects: []core.Project{project}}
	exec := &fakeExecutor{}
	h := New(config.Config{AdminToken: "secret"}, inv, fakeScanner{projects: []core.Project{project}}, exec).Handler()

	req := httptest.NewRequest(http.MethodPost, "/api/containers/web123/actions/redeploy", nil)
	req.Header.Set("Authorization", "Bearer secret")
	r := httptest.NewRecorder()
	h.ServeHTTP(r, req)

	if r.Code != http.StatusOK {
		t.Fatalf("status=%d body=%s", r.Code, r.Body.String())
	}
	want := []string{"docker compose -f /srv/app/compose.yml up -d --no-deps --force-recreate web"}
	if !reflect.DeepEqual(exec.commands, want) {
		t.Fatalf("commands=%#v want=%#v", exec.commands, want)
	}
	if inv.saves != 1 {
		t.Fatalf("inventory saves=%d", inv.saves)
	}
}

func TestComposePullStreamUsesProgressEventsWithoutRawJSONOutput(t *testing.T) {
	project := core.Project{
		ID:          "compose:app",
		Name:        "app",
		Type:        core.ProjectTypeCompose,
		WorkingDir:  "/srv/app",
		ConfigFiles: []string{"/srv/app/compose.yml"},
		Services:    []core.Service{{Name: "app", Image: "registry/app:latest"}},
	}
	exec := &progressStreamingExecutor{}
	h := New(config.Config{AdminToken: "secret"}, &fakeInventory{projects: []core.Project{project}}, fakeScanner{projects: []core.Project{project}}, exec).Handler()

	req := httptest.NewRequest(http.MethodPost, "/api/projects/compose:app/actions/deploy", nil)
	req.Header.Set("Authorization", "Bearer secret")
	req.Header.Set("Accept", "application/x-ndjson")
	r := httptest.NewRecorder()
	h.ServeHTTP(r, req)

	body := r.Body.String()
	if r.Code != http.StatusOK {
		t.Fatalf("status=%d body=%s", r.Code, body)
	}
	for _, want := range []string{
		`"type":"progress"`,
		`"id":"Image registry/app:latest"`,
		`"status":"Done"`,
		`"output":"镜像拉取完成：1/1"`,
	} {
		if !strings.Contains(body, want) {
			t.Fatalf("progress stream missing %q: %s", want, body)
		}
	}
	if strings.Contains(body, `"type":"output","data":"{\"id\":\"Image registry/app:latest`) {
		t.Fatalf("raw Compose progress JSON must not be appended to operation logs: %s", body)
	}
}

func TestContainerUpdateRejectsStandaloneContainers(t *testing.T) {
	standalone := core.Project{
		ID:       "container:redis",
		Name:     "redis",
		Type:     core.ProjectTypeStandalone,
		Services: []core.Service{{Name: "redis", ContainerID: "redis123", Image: "redis:7"}},
	}
	h := New(config.Config{AdminToken: "secret"}, &fakeInventory{projects: []core.Project{standalone}}, fakeScanner{}, &fakeExecutor{}).Handler()
	for _, action := range []string{"check-update", "deploy", "redeploy"} {
		req := httptest.NewRequest(http.MethodPost, "/api/containers/redis123/actions/"+action, nil)
		req.Header.Set("Authorization", "Bearer secret")
		r := httptest.NewRecorder()
		h.ServeHTTP(r, req)
		if r.Code != http.StatusBadRequest || !strings.Contains(r.Body.String(), "Compose") {
			t.Fatalf("%s status=%d body=%s", action, r.Code, r.Body.String())
		}
	}
}

func TestContainerUpdateEndpointsReturnUsefulFailures(t *testing.T) {
	project := core.Project{
		ID: "compose:app", Name: "app", Type: core.ProjectTypeCompose, WorkingDir: "/srv/app",
		ConfigFiles: []string{"/srv/app/compose.yml"},
		Services:    []core.Service{{Name: "web", ContainerID: "web123", Image: "registry/web:latest"}},
	}
	authorizedRequest := func(method, path string) *http.Request {
		req := httptest.NewRequest(method, path, nil)
		req.Header.Set("Authorization", "Bearer secret")
		return req
	}

	t.Run("missing container", func(t *testing.T) {
		h := New(config.Config{AdminToken: "secret"}, &fakeInventory{projects: []core.Project{project}}, fakeScanner{}, &fakeExecutor{}).Handler()
		for _, action := range []string{"check-update", "deploy", "redeploy"} {
			r := httptest.NewRecorder()
			h.ServeHTTP(r, authorizedRequest(http.MethodPost, "/api/containers/missing/actions/"+action))
			if r.Code != http.StatusNotFound {
				t.Fatalf("%s status=%d body=%s", action, r.Code, r.Body.String())
			}
		}
	})

	t.Run("inventory load failure", func(t *testing.T) {
		h := New(config.Config{AdminToken: "secret"}, &fakeInventory{loadErr: errors.New("inventory boom")}, fakeScanner{}, &fakeExecutor{}).Handler()
		r := httptest.NewRecorder()
		h.ServeHTTP(r, authorizedRequest(http.MethodPost, "/api/containers/web123/actions/check-update"))
		if r.Code != http.StatusInternalServerError || !strings.Contains(r.Body.String(), "inventory boom") {
			t.Fatalf("status=%d body=%s", r.Code, r.Body.String())
		}
	})

	t.Run("check command failure", func(t *testing.T) {
		exec := &fakeExecutor{err: errors.New("dry run failed"), failCall: 1}
		h := New(config.Config{AdminToken: "secret"}, &fakeInventory{projects: []core.Project{project}}, fakeScanner{}, exec).Handler()
		r := httptest.NewRecorder()
		h.ServeHTTP(r, authorizedRequest(http.MethodPost, "/api/containers/web123/actions/check-update"))
		if r.Code != http.StatusInternalServerError || !strings.Contains(r.Body.String(), "dry run failed") {
			t.Fatalf("status=%d body=%s", r.Code, r.Body.String())
		}
	})

	t.Run("service deploy command failure", func(t *testing.T) {
		exec := &fakeExecutor{
			err: errors.New("pull failed"), failCall: 1,
		}
		h := New(config.Config{AdminToken: "secret"}, &fakeInventory{projects: []core.Project{project}}, fakeScanner{}, exec).Handler()
		r := httptest.NewRecorder()
		h.ServeHTTP(r, authorizedRequest(http.MethodPost, "/api/containers/web123/actions/deploy"))
		if r.Code != http.StatusInternalServerError || !strings.Contains(r.Body.String(), "pull failed") {
			t.Fatalf("status=%d body=%s", r.Code, r.Body.String())
		}
	})

	t.Run("inventory refresh failure", func(t *testing.T) {
		exec := &fakeExecutor{}
		h := New(config.Config{AdminToken: "secret"}, &fakeInventory{projects: []core.Project{project}}, fakeScanner{err: errors.New("refresh failed")}, exec).Handler()
		r := httptest.NewRecorder()
		h.ServeHTTP(r, authorizedRequest(http.MethodPost, "/api/containers/web123/actions/deploy"))
		if r.Code != http.StatusInternalServerError || !strings.Contains(r.Body.String(), "refresh failed") {
			t.Fatalf("status=%d body=%s", r.Code, r.Body.String())
		}
	})
}

func TestLifecycleRefreshesInventoryAfterSuccess(t *testing.T) {
	inv := &fakeInventory{projects: []core.Project{{
		ID:          "compose:mtp",
		Name:        "mtp",
		Type:        core.ProjectTypeCompose,
		WorkingDir:  "/srv/mtp",
		ConfigFiles: []string{"/srv/mtp/docker-compose.yml"},
	}}}
	scanner := fakeScanner{projects: []core.Project{{ID: "compose:after-restart", Name: "after-restart", Type: core.ProjectTypeCompose}}}
	h := New(config.Config{AdminToken: "secret"}, inv, scanner, &fakeExecutor{}).Handler()

	req := httptest.NewRequest(http.MethodPost, "/api/projects/compose:mtp/actions/restart", nil)
	req.Header.Set("Authorization", "Bearer secret")
	r := httptest.NewRecorder()
	h.ServeHTTP(r, req)

	if r.Code != http.StatusOK {
		t.Fatalf("restart status=%d body=%s", r.Code, r.Body.String())
	}
	if inv.saves != 1 || inv.projects[0].ID != "compose:after-restart" {
		t.Fatalf("inventory was not refreshed after lifecycle action: saves=%d projects=%#v", inv.saves, inv.projects)
	}
}

func TestRefreshInventoryAfterLifecyclePreservesMetadata(t *testing.T) {
	inv := &fakeInventory{projects: []core.Project{{
		ID:          "compose:app",
		Name:        "app",
		Type:        core.ProjectTypeCompose,
		WorkingDir:  "/srv/app",
		ConfigFiles: []string{"/srv/app/compose.yml"},
		Tags:        []string{"critical"},
		Favorite:    true,
	}}}
	scanner := fakeScanner{projects: []core.Project{{
		ID:          "compose:app:newid",
		Name:        "app",
		Type:        core.ProjectTypeCompose,
		WorkingDir:  "/srv/app",
		ConfigFiles: []string{"/srv/app/compose.yml"},
		Status:      "running",
	}}}
	h := New(config.Config{AdminToken: "secret"}, inv, scanner, &fakeExecutor{}).Handler()

	req := httptest.NewRequest(http.MethodPost, "/api/projects/compose:app/actions/restart", nil)
	req.Header.Set("Authorization", "Bearer secret")
	r := httptest.NewRecorder()
	h.ServeHTTP(r, req)

	if r.Code != http.StatusOK {
		t.Fatalf("status=%d body=%s", r.Code, r.Body.String())
	}
	if len(inv.projects) != 1 || !inv.projects[0].Favorite || !reflect.DeepEqual(inv.projects[0].Tags, []string{"critical"}) || inv.projects[0].ID != "compose:app:newid" {
		t.Fatalf("metadata was not preserved after refresh: %#v", inv.projects)
	}
}

func TestDeleteEndpointsUseConservativeDockerCommands(t *testing.T) {
	inv := &fakeInventory{}
	exec := &fakeExecutor{}
	scanner := fakeScanner{projects: []core.Project{{ID: "compose:after-delete", Name: "after-delete", Type: core.ProjectTypeCompose}}}
	h := New(config.Config{AdminToken: "secret"}, inv, scanner, exec).Handler()

	req := httptest.NewRequest(http.MethodDelete, "/api/containers/abc123", nil)
	req.Header.Set("Authorization", "Bearer secret")
	r := httptest.NewRecorder()
	h.ServeHTTP(r, req)
	if r.Code != http.StatusOK || exec.commands[0] != "docker rm -f abc123" {
		t.Fatalf("container delete status=%d commands=%#v body=%s", r.Code, exec.commands, r.Body.String())
	}
	if inv.saves != 1 || inv.projects[0].ID != "compose:after-delete" {
		t.Fatalf("inventory was not refreshed after container delete: saves=%d projects=%#v", inv.saves, inv.projects)
	}

	req = httptest.NewRequest(http.MethodDelete, "/api/images", strings.NewReader(`{"ref":"redis:7"}`))
	req.Header.Set("Authorization", "Bearer secret")
	r = httptest.NewRecorder()
	h.ServeHTTP(r, req)
	if r.Code != http.StatusOK || exec.commands[1] != "docker rmi redis:7" {
		t.Fatalf("image delete status=%d commands=%#v body=%s", r.Code, exec.commands, r.Body.String())
	}

	req = httptest.NewRequest(http.MethodDelete, "/api/images", strings.NewReader(`{"ref":"redis:7","force":true}`))
	req.Header.Set("Authorization", "Bearer secret")
	r = httptest.NewRecorder()
	h.ServeHTTP(r, req)
	if r.Code != http.StatusOK || exec.commands[2] != "docker rmi -f redis:7" {
		t.Fatalf("force image delete status=%d commands=%#v body=%s", r.Code, exec.commands, r.Body.String())
	}
}

func TestDeleteProjectRefreshesInventory(t *testing.T) {
	inv := &fakeInventory{projects: []core.Project{{
		ID:          "compose:mtp",
		Name:        "mtp",
		Type:        core.ProjectTypeCompose,
		WorkingDir:  "/srv/mtp",
		ConfigFiles: []string{"/srv/mtp/docker-compose.yml"},
	}}}
	exec := &fakeExecutor{}
	scanner := fakeScanner{projects: []core.Project{{ID: "compose:after-project-delete", Name: "after-project-delete", Type: core.ProjectTypeCompose}}}
	h := New(config.Config{AdminToken: "secret"}, inv, scanner, exec).Handler()

	req := httptest.NewRequest(http.MethodDelete, "/api/projects/compose:mtp", nil)
	req.Header.Set("Authorization", "Bearer secret")
	r := httptest.NewRecorder()
	h.ServeHTTP(r, req)
	if r.Code != http.StatusOK || exec.commands[0] != "docker compose -f /srv/mtp/docker-compose.yml down" {
		t.Fatalf("project delete status=%d commands=%#v body=%s", r.Code, exec.commands, r.Body.String())
	}
	if inv.saves != 1 || inv.projects[0].ID != "compose:after-project-delete" {
		t.Fatalf("inventory was not refreshed after project delete: saves=%d projects=%#v", inv.saves, inv.projects)
	}
}

func TestImageSearchAndContainerDeployEndpoints(t *testing.T) {
	exec := &fakeExecutor{search: []docker.SearchResult{{Name: "redis", Description: "Redis server", Stars: 1000, Official: true}}}
	h := New(config.Config{AdminToken: "secret"}, &fakeInventory{}, fakeScanner{}, exec).Handler()

	req := httptest.NewRequest(http.MethodGet, "/api/images/search?q=redis", nil)
	req.Header.Set("Authorization", "Bearer secret")
	r := httptest.NewRecorder()
	h.ServeHTTP(r, req)
	if r.Code != http.StatusOK || !strings.Contains(r.Body.String(), "Redis server") {
		t.Fatalf("search status=%d body=%s", r.Code, r.Body.String())
	}

	req = httptest.NewRequest(http.MethodPost, "/api/deploy/container", strings.NewReader(`{"name":"redis-local","image":"redis:7"}`))
	req.Header.Set("Authorization", "Bearer secret")
	r = httptest.NewRecorder()
	h.ServeHTTP(r, req)
	if r.Code != http.StatusOK || exec.commands[0] != "docker run -d --name redis-local redis:7" {
		t.Fatalf("deploy status=%d commands=%#v body=%s", r.Code, exec.commands, r.Body.String())
	}
}

func TestContainerDeployRefreshesInventoryAfterSuccess(t *testing.T) {
	inv := &fakeInventory{}
	scanner := fakeScanner{projects: []core.Project{{ID: "container:redis-local", Name: "redis-local", Type: core.ProjectTypeStandalone}}}
	h := New(config.Config{AdminToken: "secret"}, inv, scanner, &fakeExecutor{}).Handler()

	req := httptest.NewRequest(http.MethodPost, "/api/deploy/container", strings.NewReader(`{"name":"redis-local","image":"redis:7"}`))
	req.Header.Set("Authorization", "Bearer secret")
	r := httptest.NewRecorder()
	h.ServeHTTP(r, req)

	if r.Code != http.StatusOK {
		t.Fatalf("deploy status=%d body=%s", r.Code, r.Body.String())
	}
	if inv.saves != 1 || inv.projects[0].ID != "container:redis-local" {
		t.Fatalf("inventory was not refreshed after container deploy: saves=%d projects=%#v", inv.saves, inv.projects)
	}
}

func TestLocalImagesEndpoint(t *testing.T) {
	exec := &fakeExecutor{images: []docker.LocalImage{{Repository: "redis", Tag: "7", ID: "sha256:abc", Created: "2 days ago", Size: "120MB"}}}
	h := New(config.Config{AdminToken: "secret"}, &fakeInventory{}, fakeScanner{}, exec).Handler()

	req := httptest.NewRequest(http.MethodGet, "/api/images/local", nil)
	req.Header.Set("Authorization", "Bearer secret")
	r := httptest.NewRecorder()
	h.ServeHTTP(r, req)

	if r.Code != http.StatusOK || !strings.Contains(r.Body.String(), "redis") || !strings.Contains(r.Body.String(), "120MB") {
		t.Fatalf("status=%d body=%s", r.Code, r.Body.String())
	}
}

func TestContainerDeployPreviewDoesNotExecute(t *testing.T) {
	exec := &fakeExecutor{}
	h := New(config.Config{AdminToken: "secret"}, &fakeInventory{}, fakeScanner{}, exec).Handler()

	req := httptest.NewRequest(http.MethodPost, "/api/deploy/container/preview", strings.NewReader(`{"name":"redis-local","image":"redis:7"}`))
	req.Header.Set("Authorization", "Bearer secret")
	r := httptest.NewRecorder()
	h.ServeHTTP(r, req)
	if r.Code != http.StatusOK || !strings.Contains(r.Body.String(), "docker run -d --name redis-local redis:7") {
		t.Fatalf("preview status=%d body=%s", r.Code, r.Body.String())
	}
	if len(exec.commands) != 0 {
		t.Fatalf("preview executed commands: %#v", exec.commands)
	}
}

func TestContainerDeployPreviewCanDeriveContainerName(t *testing.T) {
	exec := &fakeExecutor{}
	h := New(config.Config{AdminToken: "secret"}, &fakeInventory{}, fakeScanner{}, exec).Handler()

	req := httptest.NewRequest(http.MethodPost, "/api/deploy/container/preview", strings.NewReader(`{"image":"linuxserver/jellyfin:latest"}`))
	req.Header.Set("Authorization", "Bearer secret")
	r := httptest.NewRecorder()
	h.ServeHTTP(r, req)
	if r.Code != http.StatusOK || !strings.Contains(r.Body.String(), "--name jellyfin linuxserver/jellyfin:latest") {
		t.Fatalf("preview status=%d body=%s", r.Code, r.Body.String())
	}
	if len(exec.commands) != 0 {
		t.Fatalf("preview executed commands: %#v", exec.commands)
	}
}

func TestComposeDeployWritesProvidedPathAndExecutes(t *testing.T) {
	dir := t.TempDir()
	composePath := dir + "/compose.yml"
	exec := &fakeExecutor{}
	h := New(config.Config{AdminToken: "secret"}, &fakeInventory{}, fakeScanner{}, exec).Handler()
	body := `{"name":"demo","composePath":"` + composePath + `","composeContent":"services:\n  web:\n    image: nginx\n"}`

	req := httptest.NewRequest(http.MethodPost, "/api/deploy/compose", strings.NewReader(body))
	req.Header.Set("Authorization", "Bearer secret")
	r := httptest.NewRecorder()
	h.ServeHTTP(r, req)
	if r.Code != http.StatusOK {
		t.Fatalf("deploy status=%d body=%s", r.Code, r.Body.String())
	}
	if len(exec.commands) != 2 || !strings.Contains(exec.commands[0], "docker compose -f ") || !strings.HasSuffix(exec.commands[0], " config") || exec.commands[1] != "docker compose -f "+composePath+" up -d" {
		t.Fatalf("commands=%#v", exec.commands)
	}
}

func TestComposeDeployValidationFailurePreservesExistingFile(t *testing.T) {
	dir := t.TempDir()
	composePath := dir + "/compose.yml"
	original := []byte("services:\n  stable:\n    image: nginx:1\n")
	if err := os.WriteFile(composePath, original, 0o600); err != nil {
		t.Fatal(err)
	}
	exec := &fakeExecutor{}
	h := New(config.Config{AdminToken: "secret"}, &fakeInventory{}, fakeScanner{}, exec).Handler()
	body := `{"name":"demo","composePath":"` + composePath + `","composeContent":"services:\n  broken: [\n"}`

	req := httptest.NewRequest(http.MethodPost, "/api/deploy/compose", strings.NewReader(body))
	req.Header.Set("Authorization", "Bearer secret")
	r := httptest.NewRecorder()
	h.ServeHTTP(r, req)

	if r.Code != http.StatusBadRequest || !strings.Contains(r.Body.String(), "invalid compose yaml") {
		t.Fatalf("status=%d body=%s", r.Code, r.Body.String())
	}
	got, err := os.ReadFile(composePath)
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != string(original) {
		t.Fatalf("compose file changed after validation failure:\n%s", got)
	}
	if len(exec.commands) != 0 {
		t.Fatalf("commands=%#v", exec.commands)
	}
}

func TestComposeFormatNormalizesValidYAML(t *testing.T) {
	exec := &fakeExecutor{}
	h := New(config.Config{AdminToken: "secret"}, &fakeInventory{}, fakeScanner{}, exec).Handler()
	body := `{"composeContent":"services:\n web:\n  image: nginx:latest\n"}`
	req := httptest.NewRequest(http.MethodPost, "/api/deploy/compose/format", strings.NewReader(body))
	req.Header.Set("Authorization", "Bearer secret")
	r := httptest.NewRecorder()
	h.ServeHTTP(r, req)
	if r.Code != http.StatusOK {
		t.Fatalf("format status = %d body=%s", r.Code, r.Body.String())
	}
	var response struct {
		NormalizedContent string `json:"normalizedContent"`
	}
	if err := json.NewDecoder(r.Body).Decode(&response); err != nil {
		t.Fatal(err)
	}
	want, err := docker.NormalizeComposeContent("services:\n web:\n  image: nginx:latest\n")
	if err != nil {
		t.Fatal(err)
	}
	if response.NormalizedContent != want {
		t.Fatalf("normalized content = %q, want %q", response.NormalizedContent, want)
	}
	if len(exec.commands) != 0 {
		t.Fatalf("format must not execute Docker commands: %#v", exec.commands)
	}
}

func TestComposeFormatRejectsInvalidYAML(t *testing.T) {
	h := New(config.Config{AdminToken: "secret"}, &fakeInventory{}, fakeScanner{}, &fakeExecutor{}).Handler()
	req := httptest.NewRequest(http.MethodPost, "/api/deploy/compose/format", strings.NewReader(`{"composeContent":"services: ["}`))
	req.Header.Set("Authorization", "Bearer secret")
	r := httptest.NewRecorder()
	h.ServeHTTP(r, req)
	if r.Code != http.StatusBadRequest || !strings.Contains(r.Body.String(), "invalid compose yaml") {
		t.Fatalf("format status = %d body=%s", r.Code, r.Body.String())
	}
}

func TestComposeDeployFailureRestoresExistingFile(t *testing.T) {
	dir := t.TempDir()
	composePath := dir + "/compose.yml"
	original := []byte("services:\n  stable:\n    image: nginx:1\n")
	if err := os.WriteFile(composePath, original, 0o600); err != nil {
		t.Fatal(err)
	}
	exec := &fakeExecutor{err: errors.New("compose up failed"), failCall: 2}
	h := New(config.Config{AdminToken: "secret"}, &fakeInventory{}, fakeScanner{}, exec).Handler()
	body := `{"name":"demo","composePath":"` + composePath + `","composeContent":"services:\n  web:\n    image: nginx:2\n"}`

	req := httptest.NewRequest(http.MethodPost, "/api/deploy/compose", strings.NewReader(body))
	req.Header.Set("Authorization", "Bearer secret")
	r := httptest.NewRecorder()
	h.ServeHTTP(r, req)

	if r.Code != http.StatusInternalServerError {
		t.Fatalf("status=%d body=%s", r.Code, r.Body.String())
	}
	got, err := os.ReadFile(composePath)
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != string(original) {
		t.Fatalf("compose file was not restored after deploy failure:\n%s", got)
	}
	if len(exec.commands) != 2 || !strings.HasSuffix(exec.commands[0], " config") || exec.commands[1] != "docker compose -f "+composePath+" up -d" {
		t.Fatalf("commands=%#v", exec.commands)
	}
}

func TestComposeFileHelpersHandleNewFilesAndInvalidDirectories(t *testing.T) {
	dir := t.TempDir()
	stagedPath, err := stageComposeFile(dir, "services: {}\n")
	if err != nil {
		t.Fatal(err)
	}
	target := dir + "/compose.yml"
	restore, discardBackup, err := replaceComposeFile(target, stagedPath, "")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(target); err != nil {
		t.Fatalf("target was not installed: %v", err)
	}
	if err := discardBackup(); err != nil {
		t.Fatalf("discardBackup() error = %v", err)
	}
	if err := restore(); err != nil {
		t.Fatalf("restore() error = %v", err)
	}
	if _, err := os.Stat(target); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("new target should be removed during restore, stat error=%v", err)
	}

	notDir := dir + "/not-a-directory"
	if err := os.WriteFile(notDir, []byte("file"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := stageComposeFile(notDir, "services: {}\n"); err == nil {
		t.Fatal("stageComposeFile should fail when dir is a file")
	}
}

func TestSameProjectMatchesStableComposeAndContainerIdentity(t *testing.T) {
	tests := []struct {
		name     string
		existing core.Project
		scanned  core.Project
		want     bool
	}{
		{
			name:     "same id",
			existing: core.Project{ID: "compose:app", Type: core.ProjectTypeCompose},
			scanned:  core.Project{ID: "compose:app", Type: core.ProjectTypeCompose},
			want:     true,
		},
		{
			name:     "compose working directory",
			existing: core.Project{ID: "old", Type: core.ProjectTypeCompose, WorkingDir: "/srv/app"},
			scanned:  core.Project{ID: "new", Type: core.ProjectTypeCompose, WorkingDir: "/srv/app"},
			want:     true,
		},
		{
			name:     "compose config file",
			existing: core.Project{ID: "old", Type: core.ProjectTypeCompose, ConfigFiles: []string{"/srv/app/compose.yml"}},
			scanned:  core.Project{ID: "new", Type: core.ProjectTypeCompose, ConfigFiles: []string{"/srv/app/compose.yml"}},
			want:     true,
		},
		{
			name:     "standalone container",
			existing: core.Project{ID: "old", Type: core.ProjectTypeStandalone, Services: []core.Service{{ContainerID: "abc123"}}},
			scanned:  core.Project{ID: "new", Type: core.ProjectTypeStandalone, Services: []core.Service{{ContainerID: "abc123"}}},
			want:     true,
		},
		{
			name:     "different compose locations",
			existing: core.Project{ID: "old", Name: "app", Type: core.ProjectTypeCompose, WorkingDir: "/srv/one/app"},
			scanned:  core.Project{ID: "new", Name: "app", Type: core.ProjectTypeCompose, WorkingDir: "/srv/two/app"},
			want:     false,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := sameProject(tt.existing, tt.scanned); got != tt.want {
				t.Fatalf("sameProject()=%v want %v", got, tt.want)
			}
		})
	}
}

func TestComposeDeployPreviewDoesNotWriteOrExecute(t *testing.T) {
	dir := t.TempDir()
	composePath := dir + "/compose.yml"
	exec := &fakeExecutor{}
	h := New(config.Config{AdminToken: "secret"}, &fakeInventory{}, fakeScanner{}, exec).Handler()
	body := `{"name":"demo","composePath":"` + composePath + `","composeContent":"services:\n  web:\n    image: nginx\n"}`

	req := httptest.NewRequest(http.MethodPost, "/api/deploy/compose/preview", strings.NewReader(body))
	req.Header.Set("Authorization", "Bearer secret")
	r := httptest.NewRecorder()
	h.ServeHTTP(r, req)

	if r.Code != http.StatusOK || !strings.Contains(r.Body.String(), "docker compose -f "+composePath+" up -d") {
		t.Fatalf("preview status=%d body=%s", r.Code, r.Body.String())
	}
	if len(exec.commands) != 0 {
		t.Fatalf("preview executed commands: %#v", exec.commands)
	}
	if _, err := os.Stat(composePath); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("preview should not write compose file, stat error=%v", err)
	}
}

func TestComposeDeployRefreshesInventoryAfterSuccess(t *testing.T) {
	dir := t.TempDir()
	composePath := dir + "/compose.yml"
	inv := &fakeInventory{}
	scanner := fakeScanner{projects: []core.Project{{ID: "compose:demo", Name: "demo", Type: core.ProjectTypeCompose}}}
	h := New(config.Config{AdminToken: "secret"}, inv, scanner, &fakeExecutor{}).Handler()
	body := `{"name":"demo","composePath":"` + composePath + `","composeContent":"services:\n  web:\n    image: nginx\n"}`

	req := httptest.NewRequest(http.MethodPost, "/api/deploy/compose", strings.NewReader(body))
	req.Header.Set("Authorization", "Bearer secret")
	r := httptest.NewRecorder()
	h.ServeHTTP(r, req)

	if r.Code != http.StatusOK {
		t.Fatalf("deploy status=%d body=%s", r.Code, r.Body.String())
	}
	if inv.saves != 1 || inv.projects[0].ID != "compose:demo" {
		t.Fatalf("inventory was not refreshed after compose deploy: saves=%d projects=%#v", inv.saves, inv.projects)
	}
}

func TestDeployReturnsCommandResultWhenRefreshFails(t *testing.T) {
	exec := &fakeExecutor{}
	scanner := fakeScanner{err: errors.New("scan failed after deploy")}
	h := New(config.Config{AdminToken: "secret"}, &fakeInventory{}, scanner, exec).Handler()

	req := httptest.NewRequest(http.MethodPost, "/api/deploy/container", strings.NewReader(`{"name":"redis-local","image":"redis:7"}`))
	req.Header.Set("Authorization", "Bearer secret")
	r := httptest.NewRecorder()
	h.ServeHTTP(r, req)

	if r.Code != http.StatusInternalServerError {
		t.Fatalf("status = %d body=%s", r.Code, r.Body.String())
	}
	for _, want := range []string{"scan failed after deploy", "docker run -d --name redis-local redis:7", `"error"`} {
		if !strings.Contains(r.Body.String(), want) {
			t.Fatalf("body missing %q: %s", want, r.Body.String())
		}
	}
}

func TestDeployContainerReturnsDockerFailureDetails(t *testing.T) {
	exec := &fakeExecutor{
		result: docker.Result{Command: "docker run -d --name redis redis:7", Output: "docker: Error response from daemon: Conflict. The container name \"/redis\" is already in use.\n", ExitCode: 125, Error: "exit status 125"},
		err:    errors.New("exit status 125"),
	}
	h := New(config.Config{AdminToken: "secret"}, &fakeInventory{}, fakeScanner{}, exec).Handler()

	req := httptest.NewRequest(http.MethodPost, "/api/deploy/container", strings.NewReader(`{"image":"redis:7"}`))
	req.Header.Set("Authorization", "Bearer secret")
	r := httptest.NewRecorder()
	h.ServeHTTP(r, req)

	if r.Code != http.StatusInternalServerError {
		t.Fatalf("status = %d body=%s", r.Code, r.Body.String())
	}
	for _, want := range []string{"exit status 125", "Conflict", "docker run -d --name redis redis:7", `"exitCode":125`} {
		if !strings.Contains(r.Body.String(), want) {
			t.Fatalf("body missing %q: %s", want, r.Body.String())
		}
	}
}

func TestDeployContainerExplainsMissingImageFailures(t *testing.T) {
	exec := &fakeExecutor{
		result: docker.Result{
			Command:  "docker run -d --name mediatree mediatree",
			Output:   "Unable to find image 'mediatree:latest' locally\ndocker: Error response from daemon: pull access denied for mediatree, repository does not exist or may require 'docker login'\n\nRun 'docker run --help' for more information\n",
			ExitCode: 125,
			Error:    "exit status 125",
		},
		err: errors.New("exit status 125"),
	}
	h := New(config.Config{AdminToken: "secret"}, &fakeInventory{}, fakeScanner{}, exec).Handler()

	req := httptest.NewRequest(http.MethodPost, "/api/deploy/container", strings.NewReader(`{"image":"mediatree"}`))
	req.Header.Set("Authorization", "Bearer secret")
	r := httptest.NewRecorder()
	h.ServeHTTP(r, req)

	if r.Code != http.StatusInternalServerError {
		t.Fatalf("status = %d body=%s", r.Code, r.Body.String())
	}
	for _, want := range []string{"Docker 未能拉取当前镜像引用 mediatree:latest", "Docker Hub 官方/library", "用户名", "/mediatree:latest", "docker build -t mediatree .", "Unable to find image", "exit status 125"} {
		if !strings.Contains(r.Body.String(), want) {
			t.Fatalf("body missing %q: %s", want, r.Body.String())
		}
	}
}
