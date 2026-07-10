package docker

import (
	"bufio"
	"context"
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"io/fs"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"dockertree/internal/core"
)

type Scanner struct {
	runner    Runner
	scanPaths []string
}

func NewScanner(runner Runner, scanPaths []string) *Scanner {
	return &Scanner{runner: runner, scanPaths: scanPaths}
}

type composeLS struct {
	Name        string `json:"Name"`
	Status      string `json:"Status"`
	ConfigFiles string `json:"ConfigFiles"`
}

type psRow struct {
	ID           string `json:"ID"`
	Image        string `json:"Image"`
	Labels       string `json:"Labels"`
	Names        string `json:"Names"`
	Ports        string `json:"Ports"`
	State        string `json:"State"`
	Status       string `json:"Status"`
	HealthStatus string `json:"HealthStatus"`
	Mounts       string `json:"Mounts"`
}

func (s *Scanner) Scan(ctx context.Context) ([]core.Project, error) {
	now := time.Now().UTC()
	projects := map[string]*core.Project{}

	if out, err := s.runner.Run(ctx, "docker", "compose", "ls", "--format", "json"); err == nil && strings.TrimSpace(string(out)) != "" {
		var rows []composeLS
		if json.Unmarshal(out, &rows) == nil {
			for _, row := range rows {
				configFiles := splitConfigFiles(row.ConfigFiles)
				workingDir := composeWorkingDir("", configFiles)
				id := composeID(row.Name, workingDir, configFiles)
				projects[id] = &core.Project{ID: id, Name: row.Name, Type: core.ProjectTypeCompose, Status: row.Status, WorkingDir: workingDir, ConfigFiles: configFiles, LastScanned: now}
			}
		}
	}

	out, err := s.runner.Run(ctx, "docker", "ps", "-a", "--format", "{{json .}}")
	if err != nil {
		return nil, err
	}
	scanner := bufio.NewScanner(strings.NewReader(string(out)))
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if line == "" {
			continue
		}
		var row psRow
		if err := json.Unmarshal([]byte(line), &row); err != nil {
			continue
		}
		labels := parseLabels(row.Labels)
		projectName := labels["com.docker.compose.project"]
		serviceName := labels["com.docker.compose.service"]
		if projectName == "" {
			id := "container:" + row.ID
			projects[id] = &core.Project{ID: id, Name: row.Names, Type: core.ProjectTypeStandalone, Status: row.State, Services: []core.Service{serviceFromRow(row, labels, row.Names)}, Ports: splitCSVish(row.Ports), LastScanned: now}
			continue
		}
		workingDir := labels["com.docker.compose.project.working_dir"]
		configFiles := splitConfigFiles(labels["com.docker.compose.project.config_files"])
		id := composeID(projectName, workingDir, configFiles)
		p, ok := projects[id]
		if !ok {
			var existingID string
			existingID, p = findComposeProject(projects, projectName, workingDir, configFiles)
			if p == nil {
				p = &core.Project{ID: id, Name: projectName, Type: core.ProjectTypeCompose, LastScanned: now}
				projects[id] = p
			} else if existingID != id && p.WorkingDir == "" && len(p.ConfigFiles) == 0 {
				delete(projects, existingID)
				p.ID = id
				projects[id] = p
			}
		}
		if p.WorkingDir == "" {
			p.WorkingDir = workingDir
		}
		if len(p.ConfigFiles) == 0 {
			p.ConfigFiles = configFiles
		}
		p.Services = append(p.Services, serviceFromRow(row, labels, serviceName))
		p.Ports = appendUnique(p.Ports, splitCSVish(row.Ports)...)
		p.Status = aggregateStatus(p.Status, row.State)
	}

	for _, path := range s.scanPaths {
		discoverComposeFiles(path, projects, now)
	}

	result := make([]core.Project, 0, len(projects))
	for _, p := range projects {
		sort.Slice(p.Services, func(i, j int) bool { return p.Services[i].Name < p.Services[j].Name })
		result = append(result, *p)
	}
	sort.Slice(result, func(i, j int) bool { return result[i].Name < result[j].Name })
	return result, scanner.Err()
}

func serviceFromRow(row psRow, labels map[string]string, name string) core.Service {
	if name == "" {
		name = row.Names
	}
	return core.Service{Name: name, ContainerID: row.ID, Image: row.Image, State: row.State, Status: row.Status, Health: row.HealthStatus, Ports: splitCSVish(row.Ports), Mounts: splitCSVish(row.Mounts), Labels: labels}
}

func parseLabels(raw string) map[string]string {
	labels := map[string]string{}
	if raw == "" {
		return labels
	}
	for _, part := range strings.Split(raw, ",") {
		key, value, ok := strings.Cut(part, "=")
		if ok {
			labels[strings.TrimSpace(key)] = strings.TrimSpace(value)
		}
	}
	return labels
}

func splitConfigFiles(raw string) []string {
	if raw == "" {
		return nil
	}
	return splitCSVish(raw)
}

func splitCSVish(raw string) []string {
	if raw == "" {
		return nil
	}
	parts := strings.Split(raw, ",")
	out := make([]string, 0, len(parts))
	for _, part := range parts {
		part = strings.TrimSpace(part)
		if part != "" {
			out = append(out, part)
		}
	}
	return out
}

func appendUnique(base []string, values ...string) []string {
	seen := map[string]bool{}
	for _, item := range base {
		seen[item] = true
	}
	for _, item := range values {
		if !seen[item] {
			base = append(base, item)
			seen[item] = true
		}
	}
	return base
}

func composeID(name, workingDir string, configFiles []string) string {
	workingDir = composeWorkingDir(workingDir, configFiles)
	locations := make([]string, 0, len(configFiles)+1)
	if workingDir != "" {
		locations = append(locations, filepath.Clean(workingDir))
	}
	for _, file := range configFiles {
		if strings.TrimSpace(file) != "" {
			locations = append(locations, filepath.Clean(file))
		}
	}
	if len(locations) == 0 {
		return "compose:" + name
	}
	sum := sha256.Sum256([]byte(strings.Join(locations, "\x00")))
	return "compose:" + name + ":" + fmt.Sprintf("%x", sum[:6])
}

func composeWorkingDir(workingDir string, configFiles []string) string {
	if strings.TrimSpace(workingDir) != "" {
		return filepath.Clean(workingDir)
	}
	if len(configFiles) > 0 && strings.TrimSpace(configFiles[0]) != "" {
		return filepath.Dir(filepath.Clean(configFiles[0]))
	}
	return ""
}

func findComposeProject(projects map[string]*core.Project, name, workingDir string, configFiles []string) (string, *core.Project) {
	workingDir = composeWorkingDir(workingDir, configFiles)
	for id, project := range projects {
		if project.Type != core.ProjectTypeCompose || project.Name != name {
			continue
		}
		if workingDir != "" && composeWorkingDir(project.WorkingDir, project.ConfigFiles) == workingDir {
			return id, project
		}
		for _, existingFile := range project.ConfigFiles {
			for _, configFile := range configFiles {
				if filepath.Clean(existingFile) == filepath.Clean(configFile) {
					return id, project
				}
			}
		}
		if project.WorkingDir == "" && len(project.ConfigFiles) == 0 {
			return id, project
		}
	}
	return "", nil
}

func aggregateStatus(existing, state string) string {
	if existing == "" {
		return state
	}
	if strings.Contains(existing, "running") || state == "running" {
		return "running"
	}
	return existing
}

func discoverComposeFiles(root string, projects map[string]*core.Project, now time.Time) {
	_ = filepath.WalkDir(root, func(path string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() || !isComposeFileName(d.Name()) {
			return nil
		}
		workingDir := filepath.Dir(path)
		name := filepath.Base(workingDir)
		id := composeID(name, workingDir, []string{path})
		existingID, existing := findComposeProject(projects, name, workingDir, []string{path})
		if existing != nil {
			existing.ConfigFiles = appendUnique(existing.ConfigFiles, path)
			if existing.WorkingDir == "" {
				existing.WorkingDir = workingDir
			}
			if existing.ID == "" {
				existing.ID = existingID
			}
			return nil
		}
		projects[id] = &core.Project{
			ID:          id,
			Name:        name,
			Type:        core.ProjectTypeCompose,
			Status:      "indexed",
			WorkingDir:  workingDir,
			ConfigFiles: []string{path},
			LastScanned: now,
		}
		return nil
	})
}

func isComposeFileName(name string) bool {
	switch name {
	case "compose.yaml", "compose.yml", "docker-compose.yaml", "docker-compose.yml":
		return true
	default:
		return false
	}
}
