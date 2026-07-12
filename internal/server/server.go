package server

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"time"
	"unicode/utf8"

	"dockertree/internal/config"
	"dockertree/internal/core"
	"dockertree/internal/docker"
	"dockertree/internal/web"
)

type Inventory interface {
	LoadInventory() ([]core.Project, error)
	SaveInventory([]core.Project) error
}

type Scanner interface {
	Scan(context.Context) ([]core.Project, error)
}

type OperationLog interface {
	Append(core.OperationRecord) error
	List(limit int, targetID string, failedOnly bool) ([]core.OperationRecord, error)
}

type TemplateStore interface {
	LoadTemplates() ([]core.DeployTemplate, error)
	SaveTemplates([]core.DeployTemplate) error
}

const defaultUpdateCheckTimeout = 2 * time.Minute

type Server struct {
	cfgMu               sync.RWMutex
	cfg                 config.Config
	store               Inventory
	scanner             Scanner
	exec                docker.Executor
	operations          OperationLog
	templates           TemplateStore
	templateMu          sync.Mutex
	notifier            Notifier
	automationMu        sync.Mutex
	lastAutomationCheck time.Time
	updateCheckTimeout  time.Duration
}

func New(cfg config.Config, store Inventory, scanner Scanner, exec docker.Executor) *Server {
	return &Server{
		cfg: cfg, store: store, scanner: scanner, exec: exec, notifier: HTTPNotifier{},
		updateCheckTimeout: defaultUpdateCheckTimeout,
	}
}

func (s *Server) WithOperationLog(log OperationLog) *Server {
	s.operations = log
	return s
}

func (s *Server) WithTemplateStore(templates TemplateStore) *Server {
	s.templates = templates
	return s
}

func (s *Server) WithNotifier(notifier Notifier) *Server {
	s.notifier = notifier
	return s
}

func (s *Server) currentConfig() config.Config {
	s.cfgMu.RLock()
	defer s.cfgMu.RUnlock()
	return s.cfg
}

func (s *Server) setConfig(cfg config.Config) {
	s.cfgMu.Lock()
	s.cfg = cfg
	s.cfgMu.Unlock()
}

func (s *Server) Handler() http.Handler {
	if s.exec == nil {
		s.exec = docker.CLIExecutor{}
	}
	mux := http.NewServeMux()
	mux.HandleFunc("GET /", s.index)
	mux.HandleFunc("GET /app.js", s.asset)
	mux.HandleFunc("GET /styles.css", s.asset)
	mux.HandleFunc("GET /api/projects", s.auth(s.projects))
	mux.HandleFunc("GET /api/projects/{id}", s.auth(s.project))
	mux.HandleFunc("PATCH /api/projects/{id}/metadata", s.auth(s.updateProjectMetadata))
	mux.HandleFunc("DELETE /api/projects/{id}", s.auth(s.deleteProject))
	mux.HandleFunc("GET /api/containers/stats", s.auth(s.containerStats))
	mux.HandleFunc("GET /api/containers/{id}/inspect", s.auth(s.containerInspect))
	mux.HandleFunc("DELETE /api/containers/{id}", s.auth(s.deleteContainer))
	mux.HandleFunc("POST /api/containers/{id}/actions/start", s.auth(s.containerLifecycle("start")))
	mux.HandleFunc("POST /api/containers/{id}/actions/stop", s.auth(s.containerLifecycle("stop")))
	mux.HandleFunc("POST /api/containers/{id}/actions/restart", s.auth(s.containerLifecycle("restart")))
	mux.HandleFunc("GET /api/containers/{id}/logs", s.auth(s.containerLogs))
	mux.HandleFunc("DELETE /api/images", s.auth(s.deleteImage))
	mux.HandleFunc("POST /api/scan", s.auth(s.scan))
	mux.HandleFunc("POST /api/projects/{id}/actions/preview-update", s.auth(s.previewUpdate))
	mux.HandleFunc("POST /api/projects/{id}/actions/check-update", s.auth(s.checkProjectUpdate))
	mux.HandleFunc("POST /api/projects/{id}/actions/deploy", s.auth(s.deploy))
	mux.HandleFunc("POST /api/projects/{id}/actions/start", s.auth(s.lifecycle("start")))
	mux.HandleFunc("POST /api/projects/{id}/actions/stop", s.auth(s.lifecycle("stop")))
	mux.HandleFunc("POST /api/projects/{id}/actions/restart", s.auth(s.lifecycle("restart")))
	mux.HandleFunc("GET /api/projects/{id}/logs", s.auth(s.logs))
	mux.HandleFunc("GET /api/images/search", s.auth(s.searchImages))
	mux.HandleFunc("GET /api/images/local", s.auth(s.localImages))
	mux.HandleFunc("POST /api/deploy/container/preview", s.auth(s.previewContainerDeploy))
	mux.HandleFunc("POST /api/deploy/container", s.auth(s.deployContainer))
	mux.HandleFunc("POST /api/deploy/compose/preview", s.auth(s.previewComposeDeploy))
	mux.HandleFunc("POST /api/deploy/compose", s.auth(s.deployCompose))
	mux.HandleFunc("GET /api/operations", s.auth(s.operationHistory))
	mux.HandleFunc("POST /api/updates/check", s.auth(s.checkAllUpdates))
	mux.HandleFunc("GET /api/settings/automation", s.auth(s.automationSettings))
	mux.HandleFunc("PATCH /api/settings/automation", s.auth(s.updateAutomationSettings))
	mux.HandleFunc("POST /api/notifications/test", s.auth(s.testNotification))
	mux.HandleFunc("GET /api/cleanup/preview", s.auth(s.cleanupPreview))
	mux.HandleFunc("POST /api/cleanup", s.auth(s.cleanup))
	mux.HandleFunc("GET /api/config/export", s.auth(s.exportConfig))
	mux.HandleFunc("POST /api/config/restore", s.auth(s.restoreConfig))
	mux.HandleFunc("GET /api/templates", s.auth(s.listTemplates))
	mux.HandleFunc("POST /api/templates", s.auth(s.saveTemplate))
	mux.HandleFunc("DELETE /api/templates/{id}", s.auth(s.deleteTemplate))
	return mux
}

func (s *Server) index(w http.ResponseWriter, r *http.Request) {
	data, _ := web.Assets.ReadFile("static/index.html")
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.Header().Set("Cache-Control", "no-store")
	_, _ = w.Write(data)
}

func (s *Server) asset(w http.ResponseWriter, r *http.Request) {
	name := "static" + r.URL.Path
	data, err := web.Assets.ReadFile(name)
	if err != nil {
		http.NotFound(w, r)
		return
	}
	if strings.HasSuffix(name, ".css") {
		w.Header().Set("Content-Type", "text/css; charset=utf-8")
	} else {
		w.Header().Set("Content-Type", "application/javascript; charset=utf-8")
	}
	w.Header().Set("Cache-Control", "no-store")
	_, _ = w.Write(data)
}

func (s *Server) auth(next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		cfg := s.currentConfig()
		if cfg.AdminToken != "" && r.Header.Get("Authorization") != "Bearer "+cfg.AdminToken {
			http.Error(w, "unauthorized", http.StatusUnauthorized)
			return
		}
		next(w, r)
	}
}

func (s *Server) projects(w http.ResponseWriter, _ *http.Request) {
	projects, err := s.store.LoadInventory()
	respond(w, projects, err)
}

func (s *Server) project(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	projects, err := s.store.LoadInventory()
	if err != nil {
		respond(w, nil, err)
		return
	}
	for _, p := range projects {
		if p.ID == id {
			respond(w, p, nil)
			return
		}
	}
	http.NotFound(w, r)
}

func (s *Server) updateProjectMetadata(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Favorite *bool               `json:"favorite"`
		Tags     *[]string           `json:"tags"`
		Aliases  *[]string           `json:"aliases"`
		Links    *[]core.ServiceLink `json:"links"`
	}
	if !decodeJSON(w, r, &req) {
		return
	}
	var tags []string
	var aliases []string
	var links []core.ServiceLink
	var err error
	if req.Tags != nil {
		tags, err = normalizeMetadataList(*req.Tags)
		if err != nil {
			badRequest(w, err)
			return
		}
	}
	if req.Aliases != nil {
		aliases, err = normalizeMetadataList(*req.Aliases)
		if err != nil {
			badRequest(w, err)
			return
		}
	}
	if req.Links != nil {
		links, err = normalizeServiceLinks(*req.Links)
		if err != nil {
			badRequest(w, err)
			return
		}
	}
	projects, err := s.store.LoadInventory()
	if err != nil {
		respond(w, nil, err)
		return
	}
	for i := range projects {
		if projects[i].ID != r.PathValue("id") {
			continue
		}
		if req.Favorite != nil {
			projects[i].Favorite = *req.Favorite
		}
		if req.Tags != nil {
			projects[i].Tags = tags
		}
		if req.Aliases != nil {
			projects[i].Aliases = aliases
		}
		if req.Links != nil {
			projects[i].Links = links
		}
		if err := s.store.SaveInventory(projects); err != nil {
			respond(w, nil, err)
			return
		}
		respond(w, projects[i], nil)
		return
	}
	http.NotFound(w, r)
}

func normalizeServiceLinks(values []core.ServiceLink) ([]core.ServiceLink, error) {
	seen := make(map[string]struct{}, len(values))
	links := make([]core.ServiceLink, 0, len(values))
	for _, link := range values {
		link.Name = strings.TrimSpace(link.Name)
		link.URL = strings.TrimSpace(link.URL)
		if link.Name == "" || link.URL == "" {
			continue
		}
		if utf8.RuneCountInString(link.Name) > 64 || len(link.URL) > 2048 {
			return nil, errors.New("service link name or URL is too long")
		}
		parsed, err := url.ParseRequestURI(link.URL)
		if err != nil || (parsed.Scheme != "http" && parsed.Scheme != "https") || parsed.Host == "" {
			return nil, errors.New("service links must use http or https URLs")
		}
		key := strings.ToLower(link.Name) + "\x00" + link.URL
		if _, ok := seen[key]; ok {
			continue
		}
		seen[key] = struct{}{}
		links = append(links, link)
	}
	return links, nil
}

func normalizeMetadataList(values []string) ([]string, error) {
	const maxMetadataItemLength = 64
	seen := make(map[string]struct{}, len(values))
	normalized := make([]string, 0, len(values))
	for _, value := range values {
		value = strings.TrimSpace(value)
		if value == "" {
			continue
		}
		if utf8.RuneCountInString(value) > maxMetadataItemLength {
			return nil, fmt.Errorf("metadata items must not exceed %d characters", maxMetadataItemLength)
		}
		key := strings.ToLower(value)
		if _, ok := seen[key]; ok {
			continue
		}
		seen[key] = struct{}{}
		normalized = append(normalized, value)
	}
	return normalized, nil
}

func (s *Server) scan(w http.ResponseWriter, r *http.Request) {
	projects, err := s.scanInventory(r.Context())
	respond(w, projects, err)
}

func (s *Server) previewUpdate(w http.ResponseWriter, r *http.Request) {
	project, ok, err := s.findProject(r.PathValue("id"))
	if err != nil {
		respond(w, nil, err)
		return
	}
	if !ok {
		http.NotFound(w, r)
		return
	}
	requiresBuild, inspection, err := s.composeRequiresBuild(r.Context(), project)
	if err != nil {
		respond(w, inspection, err)
		return
	}
	cfg := s.currentConfig()
	respond(w, docker.PreviewUpdate(project, requiresBuild, cfg.Update.RemoveOrphans), nil)
}

func (s *Server) deploy(w http.ResponseWriter, r *http.Request) {
	project, ok, err := s.findProject(r.PathValue("id"))
	if err != nil {
		respond(w, nil, err)
		return
	}
	if !ok {
		http.NotFound(w, r)
		return
	}
	requiresBuild, inspection, err := s.composeRequiresBuild(r.Context(), project)
	if err != nil {
		respond(w, inspection, err)
		return
	}
	cfg := s.currentConfig()
	plan := docker.PreviewUpdate(project, requiresBuild, cfg.Update.RemoveOrphans)
	if !plan.CanDeploy {
		w.WriteHeader(http.StatusBadRequest)
		_ = json.NewEncoder(w).Encode(plan)
		return
	}
	results := []docker.Result{}
	for _, cmd := range docker.UpdateCommands(project, requiresBuild, cfg.Update.RemoveOrphans) {
		result, err := s.executeRecorded(r.Context(), cmd, "project", project.ID, project.Name, "deploy")
		results = append(results, result)
		if err != nil {
			respond(w, results, err)
			return
		}
	}
	if err := s.refreshInventory(r.Context()); err != nil {
		respond(w, results, err)
		return
	}
	respond(w, results, nil)
}

func (s *Server) lifecycle(action string) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		project, ok, err := s.findProject(r.PathValue("id"))
		if err != nil {
			respond(w, nil, err)
			return
		}
		if !ok {
			http.NotFound(w, r)
			return
		}
		result, err := s.executeRecorded(r.Context(), docker.LifecycleCommand(project, action), "project", project.ID, project.Name, action)
		if err == nil {
			err = s.refreshInventory(r.Context())
		}
		respond(w, result, err)
	}
}

func (s *Server) logs(w http.ResponseWriter, r *http.Request) {
	project, ok, err := s.findProject(r.PathValue("id"))
	if err != nil {
		respond(w, nil, err)
		return
	}
	if !ok {
		http.NotFound(w, r)
		return
	}
	options, err := logOptionsFromRequest(r)
	if err != nil {
		badRequest(w, err)
		return
	}
	logs, err := s.exec.Logs(r.Context(), project, r.URL.Query().Get("service"), options)
	if err != nil {
		respond(w, nil, err)
		return
	}
	w.Header().Set("Content-Type", "text/plain; charset=utf-8")
	_, _ = w.Write([]byte(logs))
}

func (s *Server) deleteContainer(w http.ResponseWriter, r *http.Request) {
	id := strings.TrimSpace(r.PathValue("id"))
	if id == "" {
		badRequest(w, errText("container id is required"))
		return
	}
	result, err := s.executeRecorded(r.Context(), docker.DeleteContainerCommand(id), "container", id, id, "delete")
	if err == nil {
		err = s.refreshInventory(r.Context())
	}
	respond(w, result, err)
}

func (s *Server) containerLifecycle(action string) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		id := strings.TrimSpace(r.PathValue("id"))
		if id == "" {
			badRequest(w, errText("container id is required"))
			return
		}
		result, err := s.executeRecorded(r.Context(), docker.ContainerLifecycleCommand(id, action), "container", id, id, action)
		if err == nil {
			err = s.refreshInventory(r.Context())
		}
		respond(w, result, err)
	}
}

func (s *Server) containerLogs(w http.ResponseWriter, r *http.Request) {
	id := strings.TrimSpace(r.PathValue("id"))
	if id == "" {
		badRequest(w, errText("container id is required"))
		return
	}
	options, err := logOptionsFromRequest(r)
	if err != nil {
		badRequest(w, err)
		return
	}
	result, err := s.exec.Execute(r.Context(), docker.ContainerLogsCommand(id, options))
	if err != nil {
		respond(w, result, err)
		return
	}
	w.Header().Set("Content-Type", "text/plain; charset=utf-8")
	_, _ = w.Write([]byte(result.Output))
}

func logOptionsFromRequest(r *http.Request) (docker.LogOptions, error) {
	options := docker.LogOptions{Tail: 300}
	if value := strings.TrimSpace(r.URL.Query().Get("tail")); value != "" {
		tail, err := strconv.Atoi(value)
		if err != nil || tail < 1 || tail > 5000 {
			return docker.LogOptions{}, errors.New("tail must be between 1 and 5000")
		}
		options.Tail = tail
	}
	if value := strings.TrimSpace(r.URL.Query().Get("timestamps")); value != "" {
		timestamps, err := strconv.ParseBool(value)
		if err != nil {
			return docker.LogOptions{}, errors.New("timestamps must be true or false")
		}
		options.Timestamps = timestamps
	}
	return options, nil
}

func (s *Server) containerStats(w http.ResponseWriter, r *http.Request) {
	stats, err := s.exec.Stats(r.Context())
	respond(w, stats, err)
}

func (s *Server) deleteProject(w http.ResponseWriter, r *http.Request) {
	project, ok, err := s.findProject(r.PathValue("id"))
	if err != nil {
		respond(w, nil, err)
		return
	}
	if !ok {
		http.NotFound(w, r)
		return
	}
	if project.Type != core.ProjectTypeCompose || len(project.ConfigFiles) == 0 {
		badRequest(w, errText("project has no compose file for docker compose down"))
		return
	}
	result, err := s.executeRecorded(r.Context(), docker.DeleteProjectCommand(project), "project", project.ID, project.Name, "delete")
	if err == nil {
		err = s.refreshInventory(r.Context())
	}
	respond(w, result, err)
}

func (s *Server) deleteImage(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Ref   string `json:"ref"`
		Force bool   `json:"force"`
	}
	if !decodeJSON(w, r, &req) {
		return
	}
	if strings.TrimSpace(req.Ref) == "" {
		badRequest(w, errText("image ref is required"))
		return
	}
	result, err := s.executeRecorded(r.Context(), docker.DeleteImageCommand(req.Ref, req.Force), "image", req.Ref, req.Ref, "delete")
	respond(w, result, err)
}

func (s *Server) refreshInventory(ctx context.Context) error {
	_, err := s.scanInventory(ctx)
	return err
}

func (s *Server) scanInventory(ctx context.Context) ([]core.Project, error) {
	projects, err := s.scanner.Scan(ctx)
	if err != nil {
		return projects, err
	}
	existing, err := s.store.LoadInventory()
	if err != nil {
		return projects, err
	}
	projects = preserveProjectMetadata(existing, projects)
	if err := s.store.SaveInventory(projects); err != nil {
		return projects, err
	}
	return projects, nil
}

func preserveProjectMetadata(existing, scanned []core.Project) []core.Project {
	for i := range scanned {
		for _, previous := range existing {
			if !sameProject(previous, scanned[i]) {
				continue
			}
			scanned[i].Aliases = append([]string(nil), previous.Aliases...)
			scanned[i].Tags = append([]string(nil), previous.Tags...)
			scanned[i].Links = append([]core.ServiceLink(nil), previous.Links...)
			scanned[i].Favorite = previous.Favorite
			scanned[i].LastAction = previous.LastAction
			scanned[i].LastExitCode = previous.LastExitCode
			break
		}
	}
	return scanned
}

func sameProject(existing, scanned core.Project) bool {
	if existing.ID != "" && existing.ID == scanned.ID {
		return true
	}
	if existing.Type != scanned.Type {
		return false
	}
	if existing.Type == core.ProjectTypeCompose {
		if existing.WorkingDir != "" && scanned.WorkingDir != "" && filepath.Clean(existing.WorkingDir) == filepath.Clean(scanned.WorkingDir) {
			return true
		}
		for _, existingFile := range existing.ConfigFiles {
			for _, scannedFile := range scanned.ConfigFiles {
				if existingFile != "" && scannedFile != "" && filepath.Clean(existingFile) == filepath.Clean(scannedFile) {
					return true
				}
			}
		}
		if existing.WorkingDir == "" && scanned.WorkingDir == "" && len(existing.ConfigFiles) == 0 && len(scanned.ConfigFiles) == 0 {
			return existing.Name == scanned.Name
		}
		return false
	}
	for _, existingService := range existing.Services {
		for _, scannedService := range scanned.Services {
			if existingService.ContainerID != "" && existingService.ContainerID == scannedService.ContainerID {
				return true
			}
		}
	}
	return existing.Name != "" && existing.Name == scanned.Name
}

func (s *Server) searchImages(w http.ResponseWriter, r *http.Request) {
	results, err := s.exec.SearchImages(r.Context(), r.URL.Query().Get("q"))
	respond(w, results, err)
}

func (s *Server) localImages(w http.ResponseWriter, r *http.Request) {
	images, err := s.exec.LocalImages(r.Context())
	respond(w, images, err)
}

func (s *Server) previewContainerDeploy(w http.ResponseWriter, r *http.Request) {
	var req docker.ContainerDeployRequest
	if !decodeJSON(w, r, &req) {
		return
	}
	plan, err := docker.ContainerDeployPlan(req)
	if err != nil {
		badRequest(w, err)
		return
	}
	respond(w, plan, nil)
}

func (s *Server) deployContainer(w http.ResponseWriter, r *http.Request) {
	var req docker.ContainerDeployRequest
	if !decodeJSON(w, r, &req) {
		return
	}
	cmd, err := docker.ValidatedContainerDeployCommand(req)
	if err != nil {
		badRequest(w, err)
		return
	}
	name := strings.TrimSpace(req.Name)
	if name == "" {
		name = docker.DeriveContainerName(req.Image)
	}
	result, err := s.executeRecorded(r.Context(), cmd, "container", "container:"+name, name, "deploy")
	if err != nil {
		result = explainContainerDeployFailure(result, req.Image)
	}
	if err == nil {
		err = s.refreshInventory(r.Context())
	}
	respond(w, result, err)
}

func (s *Server) previewComposeDeploy(w http.ResponseWriter, r *http.Request) {
	var req docker.ComposeDeployRequest
	if !decodeJSON(w, r, &req) {
		return
	}
	plan, err := docker.ComposeDeployPlan(req)
	if err != nil {
		badRequest(w, err)
		return
	}
	normalized, err := docker.NormalizeComposeContent(req.ComposeContent)
	if err != nil {
		badRequest(w, err)
		return
	}
	existing := ""
	existingFound := false
	if data, readErr := os.ReadFile(strings.TrimSpace(req.ComposePath)); readErr == nil {
		existing = string(data)
		existingFound = true
	} else if !errors.Is(readErr, os.ErrNotExist) {
		respond(w, nil, readErr)
		return
	}
	preview := core.ComposeDeployPreview{
		Plan: plan, NormalizedContent: normalized, ExistingContent: existing,
		Overwrites: existingFound && existing != normalized,
	}
	preview.ExistingHash = "absent"
	if existingFound {
		preview.ExistingHash = docker.ComposeContentHash(existing)
	}
	respond(w, preview, nil)
}

func (s *Server) deployCompose(w http.ResponseWriter, r *http.Request) {
	var req docker.ComposeDeployRequest
	if !decodeJSON(w, r, &req) {
		return
	}
	cmd, err := docker.ValidatedComposeDeployCommand(req)
	if err != nil {
		badRequest(w, err)
		return
	}
	composePath := strings.TrimSpace(req.ComposePath)
	normalized, err := docker.NormalizeComposeContent(req.ComposeContent)
	if err != nil {
		badRequest(w, err)
		return
	}
	if req.ExpectedExistingHash != "" {
		actualHash, hashErr := composeFileHash(composePath)
		if hashErr != nil {
			respond(w, nil, hashErr)
			return
		}
		if actualHash != req.ExpectedExistingHash {
			conflict(w, errComposeFileChanged)
			return
		}
	}
	composeDir := filepath.Dir(composePath)
	if err := os.MkdirAll(composeDir, 0o755); err != nil {
		respond(w, nil, err)
		return
	}
	stagedPath, err := stageComposeFile(composeDir, normalized)
	if err != nil {
		respond(w, nil, err)
		return
	}
	defer os.Remove(stagedPath)
	validationResult, err := s.exec.Execute(r.Context(), docker.Command{
		Name: "docker",
		Args: []string{"compose", "-f", stagedPath, "config"},
		Dir:  composeDir,
	})
	if err != nil {
		respond(w, validationResult, err)
		return
	}
	restore, discardBackup, err := replaceComposeFile(composePath, stagedPath, req.ExpectedExistingHash)
	if err != nil {
		if errors.Is(err, errComposeFileChanged) {
			conflict(w, err)
			return
		}
		respond(w, nil, err)
		return
	}
	projectName := strings.TrimSpace(req.Name)
	if projectName == "" {
		projectName = filepath.Base(composeDir)
	}
	result, err := s.executeRecorded(r.Context(), cmd, "project", "compose:"+projectName, projectName, "deploy")
	if err != nil {
		if restoreErr := restore(); restoreErr != nil {
			err = errors.Join(err, fmt.Errorf("restore original compose file: %w", restoreErr))
		}
		respond(w, result, err)
		return
	}
	if err := discardBackup(); err != nil {
		respond(w, result, fmt.Errorf("remove compose backup: %w", err))
		return
	}
	if err == nil {
		err = s.refreshInventory(r.Context())
	}
	respond(w, result, err)
}

func stageComposeFile(dir, content string) (string, error) {
	file, err := os.CreateTemp(dir, ".dockertree-compose-*.yaml")
	if err != nil {
		return "", err
	}
	path := file.Name()
	if _, err := file.WriteString(content); err != nil {
		_ = file.Close()
		_ = os.Remove(path)
		return "", err
	}
	if err := file.Close(); err != nil {
		_ = os.Remove(path)
		return "", err
	}
	return path, nil
}

var errComposeFileChanged = errText("compose file changed after preview; preview again before deploying")

func composeFileHash(path string) (string, error) {
	data, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		return "absent", nil
	}
	if err != nil {
		return "", err
	}
	return docker.ComposeContentHash(string(data)), nil
}

func replaceComposeFile(target, staged, expectedExistingHash string) (restore func() error, discardBackup func() error, err error) {
	if expectedExistingHash != "" {
		actualHash, hashErr := composeFileHash(target)
		if hashErr != nil {
			return nil, nil, hashErr
		}
		if actualHash != expectedExistingHash {
			return nil, nil, errComposeFileChanged
		}
	}
	backup := ""
	if _, statErr := os.Stat(target); statErr == nil {
		placeholder, createErr := os.CreateTemp(filepath.Dir(target), ".dockertree-compose-backup-*")
		if createErr != nil {
			return nil, nil, createErr
		}
		backup = placeholder.Name()
		if closeErr := placeholder.Close(); closeErr != nil {
			_ = os.Remove(backup)
			return nil, nil, closeErr
		}
		if removeErr := os.Remove(backup); removeErr != nil {
			return nil, nil, removeErr
		}
		if renameErr := os.Rename(target, backup); renameErr != nil {
			return nil, nil, renameErr
		}
	} else if !errors.Is(statErr, os.ErrNotExist) {
		return nil, nil, statErr
	}

	if renameErr := os.Rename(staged, target); renameErr != nil {
		if backup != "" {
			_ = os.Rename(backup, target)
		}
		return nil, nil, renameErr
	}

	restore = func() error {
		if removeErr := os.Remove(target); removeErr != nil && !errors.Is(removeErr, os.ErrNotExist) {
			return removeErr
		}
		if backup != "" {
			return os.Rename(backup, target)
		}
		return nil
	}
	discardBackup = func() error {
		if backup == "" {
			return nil
		}
		return os.Remove(backup)
	}
	return restore, discardBackup, nil
}

func (s *Server) findProject(id string) (core.Project, bool, error) {
	projects, err := s.store.LoadInventory()
	if err != nil {
		return core.Project{}, false, err
	}
	for _, p := range projects {
		if p.ID == id {
			return p, true, nil
		}
	}
	return core.Project{}, false, nil
}

func (s *Server) composeRequiresBuild(ctx context.Context, project core.Project) (bool, docker.Result, error) {
	if project.Type != core.ProjectTypeCompose || len(project.ConfigFiles) == 0 {
		return false, docker.Result{}, nil
	}
	result, err := s.exec.Execute(ctx, docker.ComposeConfigCommand(project))
	if err != nil {
		return false, result, err
	}
	requiresBuild, err := docker.ComposeConfigRequiresBuild(result.Output)
	if err != nil {
		return false, result, fmt.Errorf("parse compose config: %w", err)
	}
	return requiresBuild, result, nil
}

func respond(w http.ResponseWriter, value any, err error) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	if err != nil {
		w.WriteHeader(http.StatusInternalServerError)
		if value != nil {
			_ = json.NewEncoder(w).Encode(withError(value, err))
			return
		}
		_ = json.NewEncoder(w).Encode(map[string]string{"error": err.Error()})
		return
	}
	_ = json.NewEncoder(w).Encode(value)
}

func withError(value any, err error) any {
	switch v := value.(type) {
	case docker.Result:
		if v.Error == "" {
			v.Error = err.Error()
		}
		return v
	case []docker.Result:
		if len(v) > 0 && v[len(v)-1].Error == "" {
			v[len(v)-1].Error = err.Error()
		}
		return v
	default:
		return map[string]any{"error": err.Error(), "result": value}
	}
}

func explainContainerDeployFailure(result docker.Result, image string) docker.Result {
	if result.ExitCode != 125 || !strings.Contains(result.Output, "Unable to find image") {
		return result
	}
	if !strings.Contains(result.Output, "pull access denied") && !strings.Contains(result.Output, "repository does not exist") {
		return result
	}
	ref := missingImageRef(result.Output)
	if ref == "" {
		if implicitRef, ok := docker.ImplicitLatestRef(image); ok {
			ref = implicitRef
		} else {
			ref = strings.TrimSpace(image)
		}
	}
	buildRef := strings.TrimSuffix(ref, ":latest")
	message := "Docker 未能拉取当前镜像引用 " + ref + "。请确认镜像名、标签和登录权限；如果这是本地项目镜像，请先在项目目录运行 docker build -t " + buildRef + " .。"
	if warnings := docker.ContainerImageWarnings(image); len(warnings) > 0 {
		message += "\n" + strings.Join(warnings, "\n")
	}
	if result.Error != "" {
		result.Error = message + "\n" + result.Error
	} else {
		result.Error = message
	}
	return result
}

func missingImageRef(output string) string {
	const prefix = "Unable to find image '"
	start := strings.Index(output, prefix)
	if start == -1 {
		return ""
	}
	start += len(prefix)
	end := strings.Index(output[start:], "'")
	if end == -1 {
		return ""
	}
	return output[start : start+end]
}

func decodeJSON(w http.ResponseWriter, r *http.Request, value any) bool {
	if err := json.NewDecoder(r.Body).Decode(value); err != nil {
		badRequest(w, err)
		return false
	}
	return true
}

func badRequest(w http.ResponseWriter, err error) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(http.StatusBadRequest)
	_ = json.NewEncoder(w).Encode(map[string]string{"error": err.Error()})
}

func conflict(w http.ResponseWriter, err error) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(http.StatusConflict)
	_ = json.NewEncoder(w).Encode(map[string]string{"error": err.Error()})
}

type errText string

func (e errText) Error() string { return string(e) }
