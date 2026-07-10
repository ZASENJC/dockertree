package docker

import (
	"testing"

	"dockertree/internal/core"
)

func TestPreviewUpdateBuildsConservativeComposePlan(t *testing.T) {
	project := core.Project{
		ID:          "compose:phototree",
		Name:        "phototree",
		Type:        core.ProjectTypeCompose,
		WorkingDir:  "/srv/phototree",
		ConfigFiles: []string{"/srv/phototree/docker-compose.yml"},
		Services:    []core.Service{{Name: "api"}, {Name: "web"}},
	}

	plan := PreviewUpdate(project, true, false)

	if !plan.CanDeploy || !plan.RequiresBuild {
		t.Fatalf("unexpected flags: %#v", plan)
	}
	want := []string{
		"docker compose -f /srv/phototree/docker-compose.yml pull",
		"docker compose -f /srv/phototree/docker-compose.yml build",
		"docker compose -f /srv/phototree/docker-compose.yml up -d",
	}
	for i := range want {
		if plan.Commands[i] != want[i] {
			t.Fatalf("command[%d] = %q, want %q", i, plan.Commands[i], want[i])
		}
	}
}

func TestPreviewUpdateQuotesComposePathsWithSpaces(t *testing.T) {
	project := core.Project{
		ID:          "compose:photo tree",
		Name:        "photo tree",
		Type:        core.ProjectTypeCompose,
		WorkingDir:  "/srv/photo tree",
		ConfigFiles: []string{"/srv/photo tree/docker-compose.yml"},
	}

	plan := PreviewUpdate(project, false, true)
	if plan.Commands[0] != "docker compose -f '/srv/photo tree/docker-compose.yml' pull" {
		t.Fatalf("unexpected quoted command: %q", plan.Commands[0])
	}
	if plan.Commands[1] != "docker compose -f '/srv/photo tree/docker-compose.yml' up -d --remove-orphans" {
		t.Fatalf("unexpected up command: %q", plan.Commands[1])
	}
}

func TestPreviewUpdateWarnsForStandaloneContainers(t *testing.T) {
	plan := PreviewUpdate(core.Project{Name: "solo", Type: core.ProjectTypeStandalone}, false, false)
	if plan.CanDeploy {
		t.Fatal("standalone containers should not be deployable in v1")
	}
	if len(plan.Warnings) == 0 {
		t.Fatal("expected warning")
	}
}
