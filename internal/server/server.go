package server

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"strings"

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

type Server struct {
	cfg     config.Config
	store   Inventory
	scanner Scanner
	exec    docker.Executor
}

func New(cfg config.Config, store Inventory, scanner Scanner, exec docker.Executor) *Server {
	return &Server{cfg: cfg, store: store, scanner: scanner, exec: exec}
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
	mux.HandleFunc("DELETE /api/projects/{id}", s.auth(s.deleteProject))
	mux.HandleFunc("DELETE /api/containers/{id}", s.auth(s.deleteContainer))
	mux.HandleFunc("POST /api/containers/{id}/actions/start", s.auth(s.containerLifecycle("start")))
	mux.HandleFunc("POST /api/containers/{id}/actions/stop", s.auth(s.containerLifecycle("stop")))
	mux.HandleFunc("POST /api/containers/{id}/actions/restart", s.auth(s.containerLifecycle("restart")))
	mux.HandleFunc("GET /api/containers/{id}/logs", s.auth(s.containerLogs))
	mux.HandleFunc("DELETE /api/images", s.auth(s.deleteImage))
	mux.HandleFunc("POST /api/scan", s.auth(s.scan))
	mux.HandleFunc("POST /api/projects/{id}/actions/preview-update", s.auth(s.previewUpdate))
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
	return mux
}

func (s *Server) index(w http.ResponseWriter, r *http.Request) {
	data, _ := web.Assets.ReadFile("static/index.html")
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
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
	_, _ = w.Write(data)
}

func (s *Server) auth(next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if s.cfg.AdminToken != "" && r.Header.Get("Authorization") != "Bearer "+s.cfg.AdminToken {
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
	respond(w, docker.PreviewUpdate(project, requiresBuild, s.cfg.Update.RemoveOrphans), nil)
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
	plan := docker.PreviewUpdate(project, requiresBuild, s.cfg.Update.RemoveOrphans)
	if !plan.CanDeploy {
		w.WriteHeader(http.StatusBadRequest)
		_ = json.NewEncoder(w).Encode(plan)
		return
	}
	results := []docker.Result{}
	for _, cmd := range docker.UpdateCommands(project, requiresBuild, s.cfg.Update.RemoveOrphans) {
		result, err := s.exec.Execute(r.Context(), cmd)
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
		result, err := s.exec.Execute(r.Context(), docker.LifecycleCommand(project, action))
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
	logs, err := s.exec.Logs(r.Context(), project, r.URL.Query().Get("service"))
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
	result, err := s.exec.Execute(r.Context(), docker.DeleteContainerCommand(id))
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
		result, err := s.exec.Execute(r.Context(), docker.ContainerLifecycleCommand(id, action))
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
	result, err := s.exec.Execute(r.Context(), docker.ContainerLogsCommand(id))
	if err != nil {
		respond(w, result, err)
		return
	}
	w.Header().Set("Content-Type", "text/plain; charset=utf-8")
	_, _ = w.Write([]byte(result.Output))
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
	result, err := s.exec.Execute(r.Context(), docker.DeleteProjectCommand(project))
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
	result, err := s.exec.Execute(r.Context(), docker.DeleteImageCommand(req.Ref, req.Force))
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
	result, err := s.exec.Execute(r.Context(), cmd)
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
	respond(w, plan, nil)
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
	composeDir := filepath.Dir(composePath)
	if err := os.MkdirAll(composeDir, 0o755); err != nil {
		respond(w, nil, err)
		return
	}
	stagedPath, err := stageComposeFile(composeDir, req.ComposeContent)
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
	restore, discardBackup, err := replaceComposeFile(composePath, stagedPath)
	if err != nil {
		respond(w, nil, err)
		return
	}
	result, err := s.exec.Execute(r.Context(), cmd)
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

func replaceComposeFile(target, staged string) (restore func() error, discardBackup func() error, err error) {
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

type errText string

func (e errText) Error() string { return string(e) }
