package server

import (
	"errors"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"unicode/utf8"

	"dockertree/internal/config"
	"dockertree/internal/core"
	"dockertree/internal/docker"
)

const maxComposeFileSize = 2 << 20

type projectComposeResponse struct {
	ProjectID      string `json:"projectId"`
	Name           string `json:"name"`
	ComposePath    string `json:"composePath"`
	ComposeContent string `json:"composeContent"`
}

type composeSaveResponse struct {
	docker.Result
	ComposePath string `json:"composePath"`
	Saved       bool   `json:"saved"`
	Deployed    bool   `json:"deployed"`
}

type composeDeployAPIRequest struct {
	ProjectID            string `json:"projectId"`
	Name                 string `json:"name"`
	ComposePath          string `json:"composePath"`
	ComposeContent       string `json:"composeContent"`
	ExpectedExistingHash string `json:"expectedExistingHash"`
}

func (r composeDeployAPIRequest) deployRequest() docker.ComposeDeployRequest {
	return docker.ComposeDeployRequest{
		Name: r.Name, ComposePath: r.ComposePath, ComposeContent: r.ComposeContent,
		ExpectedExistingHash: r.ExpectedExistingHash,
	}
}

type composeDeployTarget struct {
	derived bool
	project *core.Project
}

func composeDeployPlan(req docker.ComposeDeployRequest, target composeDeployTarget) (core.UpdatePlan, error) {
	if target.project == nil {
		return docker.ComposeDeployPlan(req)
	}
	cmd, err := composeDeployCommand(req, target)
	if err != nil {
		return core.UpdatePlan{}, err
	}
	project := target.project
	return core.UpdatePlan{
		ProjectID: project.ID, ProjectName: project.Name, WorkingDir: project.WorkingDir,
		Commands: []string{cmd.String()}, CanDeploy: true,
	}, nil
}

func composeDeployCommand(req docker.ComposeDeployRequest, target composeDeployTarget) (docker.Command, error) {
	if _, err := docker.ValidatedComposeDeployCommand(req); err != nil {
		return docker.Command{}, err
	}
	if target.project != nil {
		return docker.ComposeUpCommand(*target.project), nil
	}
	return docker.ValidatedComposeDeployCommand(req)
}

func composeValidationCommand(req docker.ComposeDeployRequest, target composeDeployTarget, stagedPath string) docker.Command {
	project := core.Project{
		Type: core.ProjectTypeCompose, WorkingDir: filepath.Dir(req.ComposePath), ConfigFiles: []string{stagedPath},
	}
	if target.project != nil {
		project = *target.project
		project.ConfigFiles = append([]string(nil), target.project.ConfigFiles...)
		for i, configFile := range project.ConfigFiles {
			if filepath.Clean(configFile) == filepath.Clean(req.ComposePath) {
				project.ConfigFiles[i] = stagedPath
				break
			}
		}
	}
	return docker.ComposeValidateCommand(project)
}

func (s *Server) projectCompose(w http.ResponseWriter, r *http.Request) {
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
		badRequest(w, errText("project has no compose file"))
		return
	}
	requestedPath := strings.TrimSpace(r.URL.Query().Get("path"))
	if requestedPath == "" {
		requestedPath = project.ConfigFiles[0]
	}
	composePath := matchedComposePath(project.ConfigFiles, requestedPath)
	if composePath == "" {
		badRequest(w, errText("compose file is not part of this project"))
		return
	}
	info, err := s.validateManagedComposeFile(composePath)
	if err != nil {
		badRequest(w, err)
		return
	}
	if info.Size() > maxComposeFileSize {
		badRequest(w, errText("compose file exceeds 2 MiB"))
		return
	}
	data, err := os.ReadFile(composePath)
	if err != nil {
		respond(w, nil, err)
		return
	}
	if !utf8.Valid(data) {
		badRequest(w, errText("compose file is not valid UTF-8 text"))
		return
	}
	respond(w, projectComposeResponse{
		ProjectID: project.ID, Name: project.Name, ComposePath: composePath, ComposeContent: string(data),
	}, nil)
}

func matchedComposePath(configFiles []string, requestedPath string) string {
	requestedPath = filepath.Clean(requestedPath)
	for _, configFile := range configFiles {
		if strings.TrimSpace(configFile) != "" && filepath.Clean(configFile) == requestedPath {
			return filepath.Clean(configFile)
		}
	}
	return ""
}

func (s *Server) resolveComposeDeployRequest(req docker.ComposeDeployRequest, projectID string) (docker.ComposeDeployRequest, composeDeployTarget, error) {
	req.Name = strings.TrimSpace(req.Name)
	req.ComposePath = strings.TrimSpace(req.ComposePath)
	projectID = strings.TrimSpace(projectID)
	if projectID != "" {
		project, ok, err := s.findProject(projectID)
		if err != nil {
			return req, composeDeployTarget{}, err
		}
		if !ok {
			return req, composeDeployTarget{}, errors.New("compose project was not found")
		}
		if project.Type != core.ProjectTypeCompose || len(project.ConfigFiles) == 0 {
			return req, composeDeployTarget{}, errors.New("project has no compose file")
		}
		if req.ComposePath == "" {
			req.ComposePath = project.ConfigFiles[0]
		}
		req.ComposePath = matchedComposePath(project.ConfigFiles, req.ComposePath)
		if req.ComposePath == "" {
			return req, composeDeployTarget{}, errors.New("compose file is not part of this project")
		}
		for _, configFile := range project.ConfigFiles {
			if _, err := s.validateManagedComposeFile(configFile); err != nil {
				return req, composeDeployTarget{}, err
			}
		}
		req.Name = project.Name
		return req, composeDeployTarget{project: &project}, nil
	}
	if req.ComposePath != "" {
		if !filepath.IsAbs(req.ComposePath) {
			return req, composeDeployTarget{}, errors.New("compose path must be absolute")
		}
		req.ComposePath = filepath.Clean(req.ComposePath)
		if req.Name == "" {
			req.Name = filepath.Base(filepath.Dir(req.ComposePath))
		}
		return req, composeDeployTarget{}, nil
	}
	if err := validateProjectFolderName(req.Name); err != nil {
		return req, composeDeployTarget{}, err
	}
	cfg := s.currentConfig()
	projectRoot, _, err := config.NormalizeProjectPaths(cfg.ProjectRoot, cfg.ScanPaths)
	if err != nil {
		return req, composeDeployTarget{}, err
	}
	req.ComposePath = filepath.Join(projectRoot, req.Name, "compose.yml")
	if err := validateDerivedComposePath(projectRoot, req.ComposePath); err != nil {
		return req, composeDeployTarget{}, err
	}
	return req, composeDeployTarget{derived: true}, nil
}

func (s *Server) validateManagedComposeFile(path string) (os.FileInfo, error) {
	path = filepath.Clean(strings.TrimSpace(path))
	if !filepath.IsAbs(path) {
		return nil, errors.New("compose path must be absolute")
	}
	info, err := os.Lstat(path)
	if err != nil {
		return nil, err
	}
	if info.Mode()&os.ModeSymlink != 0 {
		return nil, errors.New("compose path must not be a symbolic link")
	}
	if !info.Mode().IsRegular() {
		return nil, errors.New("compose path is not a regular file")
	}
	cfg := s.currentConfig()
	for _, root := range config.EffectiveScanPaths(cfg) {
		if ensurePathWithinRoot(root, path) != nil {
			continue
		}
		if err := rejectSymlinksBelowRoot(root, path); err != nil {
			return nil, err
		}
		return info, nil
	}
	return nil, errors.New("compose path is outside the configured project directories")
}

func validateProjectFolderName(name string) error {
	if name == "" {
		return errors.New("project name is required when compose path is omitted")
	}
	if utf8.RuneCountInString(name) > 128 {
		return errors.New("project name must not exceed 128 characters")
	}
	if name == "." || name == ".." || filepath.Base(name) != name || strings.ContainsAny(name, "/\\\x00") {
		return errors.New("project name must be a single folder name")
	}
	return nil
}

func ensurePathWithinRoot(root, target string) error {
	resolvedRoot, err := resolvePathWithExistingPrefix(root)
	if err != nil {
		return err
	}
	resolvedTarget, err := resolvePathWithExistingPrefix(target)
	if err != nil {
		return err
	}
	rel, err := filepath.Rel(resolvedRoot, resolvedTarget)
	if err != nil {
		return err
	}
	if rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
		return fmt.Errorf("compose path escapes projectRoot %q", filepath.Clean(root))
	}
	return nil
}

func validateDerivedComposePath(root, target string) error {
	if err := ensurePathWithinRoot(root, target); err != nil {
		return err
	}
	return rejectSymlinksBelowRoot(root, target)
}

func rejectSymlinksBelowRoot(root, target string) error {
	root = filepath.Clean(root)
	target = filepath.Clean(target)
	rel, err := filepath.Rel(root, target)
	if err != nil {
		return err
	}
	if rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
		return fmt.Errorf("path escapes configured root %q", root)
	}
	current := root
	for _, part := range strings.Split(rel, string(filepath.Separator)) {
		if part == "" || part == "." {
			continue
		}
		current = filepath.Join(current, part)
		info, statErr := os.Lstat(current)
		if errors.Is(statErr, os.ErrNotExist) {
			return nil
		}
		if statErr != nil {
			return statErr
		}
		if info.Mode()&os.ModeSymlink != 0 {
			return fmt.Errorf("path below configured root must not contain symbolic links: %q", current)
		}
	}
	return nil
}

func resolvePathWithExistingPrefix(path string) (string, error) {
	absPath, err := filepath.Abs(path)
	if err != nil {
		return "", err
	}
	current := filepath.Clean(absPath)
	missing := make([]string, 0, 4)
	for {
		resolved, err := filepath.EvalSymlinks(current)
		if err == nil {
			for i := len(missing) - 1; i >= 0; i-- {
				resolved = filepath.Join(resolved, missing[i])
			}
			return filepath.Clean(resolved), nil
		}
		if !errors.Is(err, os.ErrNotExist) {
			return "", err
		}
		parent := filepath.Dir(current)
		if parent == current {
			return "", err
		}
		missing = append(missing, filepath.Base(current))
		current = parent
	}
}
