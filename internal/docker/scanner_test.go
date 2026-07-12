package docker

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"dockertree/internal/core"
)

type fakeRunner map[string]string

func (f fakeRunner) Run(_ context.Context, _ string, args ...string) ([]byte, error) {
	return []byte(f[strings.Join(args, " ")]), nil
}

func TestScannerIndexesComposeFilesFromConfiguredScanPaths(t *testing.T) {
	dir := t.TempDir()
	appDir := filepath.Join(dir, "myapp")
	if err := os.MkdirAll(appDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(appDir, "compose.yml"), []byte("services: {}\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	runner := fakeRunner{
		"compose ls --format json":  `[]`,
		"ps -a --format {{json .}}": ``,
	}

	projects, err := NewScanner(runner, []string{dir}).Scan(context.Background())
	if err != nil {
		t.Fatalf("Scan() error = %v", err)
	}
	if len(projects) != 1 || projects[0].Name != "myapp" || projects[0].Status != "indexed" {
		t.Fatalf("unexpected indexed project: %#v", projects)
	}
}

func TestScannerAppliesUpdatedScanPathsWithoutRestart(t *testing.T) {
	root := t.TempDir()
	appDir := filepath.Join(root, "group", "myapp")
	if err := os.MkdirAll(appDir, 0o755); err != nil {
		t.Fatal(err)
	}
	composePath := filepath.Join(appDir, "docker-compose.yaml")
	if err := os.WriteFile(composePath, []byte("services: {}\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	runner := fakeRunner{
		"compose ls --format json":  `[]`,
		"ps -a --format {{json .}}": ``,
	}
	scanner := NewScanner(runner, nil)
	scanner.SetScanPaths([]string{root})

	projects, err := scanner.Scan(context.Background())
	if err != nil {
		t.Fatalf("Scan() error = %v", err)
	}
	if len(projects) != 1 || projects[0].Name != "myapp" || projects[0].ConfigFiles[0] != composePath {
		t.Fatalf("updated paths were not used: %#v", projects)
	}
}

func TestScannerKeepsSameNamedComposeDirectoriesSeparate(t *testing.T) {
	root := t.TempDir()
	firstDir := filepath.Join(root, "first", "app")
	secondDir := filepath.Join(root, "second", "app")
	for _, dir := range []string{firstDir, secondDir} {
		if err := os.MkdirAll(dir, 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(dir, "compose.yml"), []byte("services: {}\n"), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	runner := fakeRunner{
		"compose ls --format json":  `[]`,
		"ps -a --format {{json .}}": ``,
	}

	projects, err := NewScanner(runner, []string{filepath.Join(root, "first"), filepath.Join(root, "second")}).Scan(context.Background())
	if err != nil {
		t.Fatalf("Scan() error = %v", err)
	}
	if len(projects) != 2 {
		t.Fatalf("projects len = %d, want 2: %#v", len(projects), projects)
	}
	if projects[0].ID == projects[1].ID {
		t.Fatalf("same-named projects share ID %q", projects[0].ID)
	}
	for _, project := range projects {
		if project.Name != "app" || len(project.ConfigFiles) != 1 || filepath.Dir(project.ConfigFiles[0]) != project.WorkingDir {
			t.Fatalf("project merged unrelated compose paths: %#v", project)
		}
	}
}

func TestScannerAggregatesComposeProjectsFromDockerLabels(t *testing.T) {
	runner := fakeRunner{
		"compose ls --format json": `[{"Name":"phototree","Status":"running(2)","ConfigFiles":"/srv/phototree/docker-compose.yml"}]`,
		"ps -a --format {{json .}}": strings.Join([]string{
			`{"ID":"api123","Image":"phototree-api","Labels":"com.docker.compose.project=phototree,com.docker.compose.project.config_files=/srv/phototree/docker-compose.yml,com.docker.compose.project.working_dir=/srv/phototree,com.docker.compose.service=api","Names":"phototree-api-1","Ports":"0.0.0.0:27582->27582/tcp","State":"running","Status":"Up 1 hour","HealthStatus":"healthy","Mounts":"/srv/data"}`,
			`{"ID":"web123","Image":"phototree-web","Labels":"com.docker.compose.project=phototree,com.docker.compose.project.config_files=/srv/phototree/docker-compose.yml,com.docker.compose.project.working_dir=/srv/phototree,com.docker.compose.service=web","Names":"phototree-web-1","Ports":"0.0.0.0:27590->27590/tcp","State":"running","Status":"Up 1 hour","HealthStatus":"none","Mounts":"/srv/web"}`,
		}, "\n"),
	}

	projects, err := NewScanner(runner, nil).Scan(context.Background())
	if err != nil {
		t.Fatalf("Scan() error = %v", err)
	}

	if len(projects) != 1 {
		t.Fatalf("projects len = %d", len(projects))
	}
	p := projects[0]
	if p.Type != core.ProjectTypeCompose || p.Name != "phototree" || p.WorkingDir != "/srv/phototree" {
		t.Fatalf("unexpected project: %#v", p)
	}
	if len(p.Services) != 2 || len(p.Ports) != 2 {
		t.Fatalf("unexpected services/ports: %#v", p)
	}
}

func TestScannerKeepsStandaloneContainersSeparate(t *testing.T) {
	runner := fakeRunner{
		"compose ls --format json":  `[]`,
		"ps -a --format {{json .}}": `{"ID":"solo123","Image":"nginx:latest","Labels":"maintainer=me","Names":"web-solo","Ports":"8080->80/tcp","State":"exited","Status":"Exited","HealthStatus":"none","Mounts":""}`,
	}

	projects, err := NewScanner(runner, nil).Scan(context.Background())
	if err != nil {
		t.Fatalf("Scan() error = %v", err)
	}

	if len(projects) != 1 || projects[0].Type != core.ProjectTypeStandalone || projects[0].Name != "web-solo" {
		t.Fatalf("unexpected standalone project: %#v", projects)
	}
}
