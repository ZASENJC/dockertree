package docker

import (
	"context"
	"encoding/json"
	"reflect"
	"strings"
	"testing"

	"dockertree/internal/core"
)

func TestCLIExecutorInspectReturnsSafeFieldsOnly(t *testing.T) {
	runner := &recordingRunner{outputs: map[string]string{
		"docker inspect abc123": `[{
			"Id":"abc123","Name":"/redis","Created":"2026-07-11T00:00:00Z",
			"Config":{"Env":["SECRET=hidden"]},
			"State":{"Status":"running","Health":{"Status":"healthy"}},
			"HostConfig":{"RestartPolicy":{"Name":"unless-stopped"}},
			"NetworkSettings":{"Networks":{"proxy":{},"default":{}}},
			"Mounts":[{"Type":"bind","Source":"/srv/data","Destination":"/data","Mode":"ro","RW":false}]
		}]`,
	}}
	info, err := (CLIExecutor{Runner: runner}).Inspect(context.Background(), "abc123")
	if err != nil {
		t.Fatalf("Inspect() error = %v", err)
	}
	if info.Name != "redis" || info.RestartPolicy != "unless-stopped" || len(info.Networks) != 2 || info.Mounts[0].Destination != "/data" {
		t.Fatalf("inspect = %#v", info)
	}
	data, _ := json.Marshal(info)
	if strings.Contains(string(data), "SECRET") || strings.Contains(string(data), "Env") {
		t.Fatalf("inspect response leaked environment: %s", data)
	}
}

func TestCLIExecutorUpdateCheckUsesComposeDryRun(t *testing.T) {
	project := core.Project{
		ID: "compose:app", Name: "app", Type: core.ProjectTypeCompose, WorkingDir: "/srv/app", ConfigFiles: []string{"/srv/app/compose.yml"},
		Services: []core.Service{{
			Name: "web", Image: "registry/app:latest",
			Labels: map[string]string{"com.docker.compose.image": "sha256:1111111111111111111111111111111111111111111111111111111111111111"},
		}},
	}
	command := "docker compose -f /srv/app/compose.yml --dry-run pull"
	digestCommand := "docker buildx imagetools inspect --format {{json .Manifest.Digest}} registry/app:latest"
	runner := &recordingRunner{outputs: map[string]string{
		command:       "DRY-RUN MODE - web Pulled",
		digestCommand: `"sha256:2222222222222222222222222222222222222222222222222222222222222222"`,
	}}
	check, err := (CLIExecutor{Runner: runner}).CheckUpdate(context.Background(), project)
	if err != nil || check.Status != "available" || check.Command != command {
		t.Fatalf("check = %#v err=%v", check, err)
	}
	if len(check.Versions) != 1 || check.Versions[0].Service != "web" || check.Versions[0].Current == check.Versions[0].Available {
		t.Fatalf("versions = %#v", check.Versions)
	}

	runner.outputs[command] = "Image is up to date"
	check, err = (CLIExecutor{Runner: runner}).CheckUpdate(context.Background(), project)
	if err != nil || check.Status != "current" {
		t.Fatalf("current check = %#v err=%v", check, err)
	}

	runner.outputs[command] = "web Already exists\nworker DRY-RUN MODE - worker Pulled"
	check, err = (CLIExecutor{Runner: runner}).CheckUpdate(context.Background(), project)
	if err != nil || check.Status != "available" {
		t.Fatalf("mixed update check = %#v err=%v", check, err)
	}
}

func TestRemoteImageDigestCommandUsesBuildxImagetools(t *testing.T) {
	cmd := RemoteImageDigestCommand(" registry/app:latest ")
	want := Command{Name: "docker", Args: []string{"buildx", "imagetools", "inspect", "--format", "{{json .Manifest.Digest}}", "registry/app:latest"}}
	if !reflect.DeepEqual(cmd, want) {
		t.Fatalf("cmd = %#v, want %#v", cmd, want)
	}
}

func TestCLIExecutorCleanupPreviewAndCommands(t *testing.T) {
	runner := &recordingRunner{outputs: map[string]string{
		"docker ps -a --filter status=exited --filter status=dead --format {{json .}}":      `{"ID":"c1","Names":"old","Image":"nginx:1","Status":"Exited (0)"}`,
		"docker images --filter dangling=true --format json":                                `{"ID":"i1","Repository":"<none>","Tag":"<none>","Size":"20MB"}`,
		"docker network ls --filter dangling=true --filter type=custom --format {{json .}}": `{"ID":"n1","Name":"old_default","Driver":"bridge"}`,
	}}
	preview, err := (CLIExecutor{Runner: runner}).CleanupPreview(context.Background())
	if err != nil || len(preview.Containers) != 1 || len(preview.Images) != 1 || len(preview.Networks) != 1 {
		t.Fatalf("preview = %#v err=%v", preview, err)
	}
	for _, tc := range []struct {
		item core.CleanupCandidate
		want string
	}{
		{core.CleanupCandidate{Type: "container", ID: "c1"}, "docker rm c1"},
		{core.CleanupCandidate{Type: "image", ID: "i1"}, "docker rmi i1"},
		{core.CleanupCandidate{Type: "network", ID: "n1"}, "docker network rm n1"},
	} {
		cmd, err := CleanupCommand(tc.item)
		if err != nil || cmd.String() != tc.want {
			t.Fatalf("CleanupCommand(%#v) = %q err=%v", tc.item, cmd.String(), err)
		}
	}
}
