package docker

import (
	"context"
	"errors"
	"reflect"
	"strings"
	"testing"

	"dockertree/internal/core"
)

type recordingRunner struct {
	outputs map[string]string
	errs    map[string]error
	calls   []string
}

func (r *recordingRunner) Run(_ context.Context, name string, args ...string) ([]byte, error) {
	call := name + " " + strings.Join(args, " ")
	r.calls = append(r.calls, call)
	if err := r.errs[call]; err != nil {
		return []byte(r.outputs[call]), err
	}
	return []byte(r.outputs[call]), nil
}

func TestCLIExecutorExecuteReturnsFailureDetails(t *testing.T) {
	runner := &recordingRunner{
		outputs: map[string]string{"docker run nope": "daemon says no"},
		errs:    map[string]error{"docker run nope": errors.New("exit status 125")},
	}
	exec := CLIExecutor{Runner: runner}

	result, err := exec.Execute(context.Background(), Command{Name: "docker", Args: []string{"run", "nope"}})

	if err == nil {
		t.Fatal("expected command error")
	}
	if result.Command != "docker run nope" || result.Output != "daemon says no" || result.ExitCode != 1 || result.Error != "exit status 125" {
		t.Fatalf("unexpected result: %#v", result)
	}
}

func TestCLIExecutorParsesImageSearchAndLocalImages(t *testing.T) {
	runner := &recordingRunner{outputs: map[string]string{
		"docker search --limit 10 --format json redis": strings.Join([]string{
			`{"Name":"redis","Description":"Redis server","StarCount":"12000","IsOfficial":"[OK]"}`,
			`{"Name":"bitnami/redis","Description":"Bitnami Redis","StarCount":"42","IsOfficial":"false"}`,
		}, "\n"),
		"docker images --format json": strings.Join([]string{
			`{"Repository":"redis","Tag":"7","ID":"sha256:abc","CreatedSince":"2 days ago","Size":"120MB"}`,
			`{"Repository":"<none>","Tag":"<none>","ID":"sha256:def","CreatedAt":"2026-07-09","Size":"10MB"}`,
		}, "\n"),
	}}
	exec := CLIExecutor{Runner: runner}

	search, err := exec.SearchImages(context.Background(), " redis ")
	if err != nil {
		t.Fatalf("SearchImages() error = %v", err)
	}
	if len(search) != 2 || search[0].Name != "redis" || !search[0].Official || search[1].Stars != 42 {
		t.Fatalf("unexpected search results: %#v", search)
	}

	images, err := exec.LocalImages(context.Background())
	if err != nil {
		t.Fatalf("LocalImages() error = %v", err)
	}
	if len(images) != 2 || images[0].Ref() != "redis:7" || images[1].Created != "2026-07-09" {
		t.Fatalf("unexpected local images: %#v", images)
	}
}

func TestCLIExecutorLogsUsesComposeOrStandaloneCommand(t *testing.T) {
	runner := &recordingRunner{outputs: map[string]string{
		"docker compose -f /srv/app/compose.yml logs --tail 300 web": "compose logs",
		"docker logs --tail 300 abc123":                              "standalone logs",
	}}
	exec := CLIExecutor{Runner: runner}

	out, err := exec.Logs(context.Background(), core.Project{
		Type:        core.ProjectTypeCompose,
		WorkingDir:  "/srv/app",
		ConfigFiles: []string{"/srv/app/compose.yml"},
	}, "web")
	if err != nil || out != "compose logs" {
		t.Fatalf("compose logs out=%q err=%v", out, err)
	}

	out, err = exec.Logs(context.Background(), core.Project{
		Type:     core.ProjectTypeStandalone,
		Services: []core.Service{{ContainerID: "abc123"}},
	}, "")
	if err != nil || out != "standalone logs" {
		t.Fatalf("standalone logs out=%q err=%v", out, err)
	}
	if got := strings.Join(runner.calls, "\n"); !strings.Contains(got, "docker logs --tail 300 abc123") {
		t.Fatalf("standalone logs did not use docker logs: %s", got)
	}
}

func TestLifecycleCommandForCompose(t *testing.T) {
	cmd := LifecycleCommand(core.Project{Type: core.ProjectTypeCompose, WorkingDir: "/srv/app", ConfigFiles: []string{"/srv/app/compose.yml"}}, "restart")
	want := Command{Name: "docker", Args: []string{"compose", "-f", "/srv/app/compose.yml", "restart"}, Dir: "/srv/app"}
	if !reflect.DeepEqual(cmd, want) {
		t.Fatalf("cmd = %#v, want %#v", cmd, want)
	}
}

func TestLifecycleCommandForStandalone(t *testing.T) {
	cmd := LifecycleCommand(core.Project{Type: core.ProjectTypeStandalone, Services: []core.Service{{ContainerID: "abc123"}}}, "stop")
	want := Command{Name: "docker", Args: []string{"stop", "abc123"}}
	if !reflect.DeepEqual(cmd, want) {
		t.Fatalf("cmd = %#v, want %#v", cmd, want)
	}
}

func TestContainerLifecycleAndLogsCommands(t *testing.T) {
	lifecycleCmd := ContainerLifecycleCommand(" abc123 ", "restart")
	wantLifecycle := Command{Name: "docker", Args: []string{"restart", "abc123"}}
	if !reflect.DeepEqual(lifecycleCmd, wantLifecycle) {
		t.Fatalf("container lifecycle cmd = %#v, want %#v", lifecycleCmd, wantLifecycle)
	}

	logsCmd := ContainerLogsCommand(" abc123 ")
	wantLogs := Command{Name: "docker", Args: []string{"logs", "--tail", "300", "abc123"}}
	if !reflect.DeepEqual(logsCmd, wantLogs) {
		t.Fatalf("container logs cmd = %#v, want %#v", logsCmd, wantLogs)
	}
}

func TestDeleteCommands(t *testing.T) {
	containerCmd := DeleteContainerCommand("abc123")
	if !reflect.DeepEqual(containerCmd, Command{Name: "docker", Args: []string{"rm", "-f", "abc123"}}) {
		t.Fatalf("container delete cmd = %#v", containerCmd)
	}

	project := core.Project{Type: core.ProjectTypeCompose, WorkingDir: "/srv/app", ConfigFiles: []string{"/srv/app/compose.yml"}}
	projectCmd := DeleteProjectCommand(project)
	wantProject := Command{Name: "docker", Args: []string{"compose", "-f", "/srv/app/compose.yml", "down"}, Dir: "/srv/app"}
	if !reflect.DeepEqual(projectCmd, wantProject) {
		t.Fatalf("project delete cmd = %#v, want %#v", projectCmd, wantProject)
	}

	imageCmd := DeleteImageCommand("redis:7", false)
	if !reflect.DeepEqual(imageCmd, Command{Name: "docker", Args: []string{"rmi", "redis:7"}}) {
		t.Fatalf("image delete cmd = %#v", imageCmd)
	}

	forceImageCmd := DeleteImageCommand("redis:7", true)
	if !reflect.DeepEqual(forceImageCmd, Command{Name: "docker", Args: []string{"rmi", "-f", "redis:7"}}) {
		t.Fatalf("force image delete cmd = %#v", forceImageCmd)
	}
}

func TestCommandsForPlanPreserveWorkingDir(t *testing.T) {
	plan := core.UpdatePlan{WorkingDir: "/srv/app", Commands: []string{"docker compose -f /srv/app/compose.yml up -d"}}
	commands := CommandsForPlan(plan)
	if len(commands) != 1 || commands[0].Dir != "/srv/app" || commands[0].String() != plan.Commands[0] {
		t.Fatalf("unexpected commands: %#v", commands)
	}
}

func TestUpdateCommandsKeepPathWithSpacesAsSingleArg(t *testing.T) {
	project := core.Project{Type: core.ProjectTypeCompose, WorkingDir: "/srv/photo tree", ConfigFiles: []string{"/srv/photo tree/compose.yml"}}
	commands := UpdateCommands(project, false, false)
	if len(commands) != 2 {
		t.Fatalf("commands len = %d", len(commands))
	}
	if commands[0].Args[2] != "/srv/photo tree/compose.yml" {
		t.Fatalf("path was split or changed: %#v", commands[0].Args)
	}
	if commands[0].String() != "docker compose -f '/srv/photo tree/compose.yml' pull" {
		t.Fatalf("display command not quoted: %q", commands[0].String())
	}
}

func TestComposeConfigRequiresBuildUsesBuildField(t *testing.T) {
	project := core.Project{
		Type:        core.ProjectTypeCompose,
		WorkingDir:  "/srv/app",
		ConfigFiles: []string{"/srv/app/compose.yml"},
	}
	cmd := ComposeConfigCommand(project)
	want := Command{Name: "docker", Args: []string{"compose", "-f", "/srv/app/compose.yml", "config", "--format", "json"}, Dir: "/srv/app"}
	if !reflect.DeepEqual(cmd, want) {
		t.Fatalf("cmd = %#v, want %#v", cmd, want)
	}

	requiresBuild, err := ComposeConfigRequiresBuild(`{"services":{"api":{"image":"registry/app:dev","build":{"context":"."}},"db":{"image":"postgres:16"}}}`)
	if err != nil {
		t.Fatalf("ComposeConfigRequiresBuild() error = %v", err)
	}
	if !requiresBuild {
		t.Fatal("explicit image tag must not hide the service build configuration")
	}

	requiresBuild, err = ComposeConfigRequiresBuild(`{"services":{"api":{"image":"registry/app:dev"}}}`)
	if err != nil || requiresBuild {
		t.Fatalf("image-only config requiresBuild=%v err=%v", requiresBuild, err)
	}
	if _, err := ComposeConfigRequiresBuild(`{"services":`); err == nil {
		t.Fatal("invalid compose config JSON should fail")
	}
}

func TestContainerDeployCommands(t *testing.T) {
	cmd := ContainerDeployCommand(ContainerDeployRequest{Name: "redis-local", Image: "redis:7"})
	want := Command{Name: "docker", Args: []string{"run", "-d", "--name", "redis-local", "redis:7"}}
	if !reflect.DeepEqual(cmd, want) {
		t.Fatalf("cmd = %#v, want %#v", cmd, want)
	}
}

func TestContainerDeployDerivesNameFromImageWhenNameIsEmpty(t *testing.T) {
	cmd, err := ValidatedContainerDeployCommand(ContainerDeployRequest{Image: "linuxserver/jellyfin:latest"})
	if err != nil {
		t.Fatalf("ValidatedContainerDeployCommand() error = %v", err)
	}
	want := "docker run -d --name jellyfin linuxserver/jellyfin:latest"
	if cmd.String() != want {
		t.Fatalf("cmd = %q, want %q", cmd.String(), want)
	}
}

func TestContainerDeployPlanWarnsWhenImageUsesImplicitLatest(t *testing.T) {
	plan, err := ContainerDeployPlan(ContainerDeployRequest{Image: "mediatree"})
	if err != nil {
		t.Fatalf("ContainerDeployPlan() error = %v", err)
	}
	warningText := strings.Join(plan.Warnings, "\n")
	for _, want := range []string{"mediatree:latest", "Docker Hub 官方/library", "<用户名>/mediatree:latest", "docker build -t mediatree ."} {
		if !strings.Contains(warningText, want) {
			t.Fatalf("warning missing %q: %q", want, warningText)
		}
	}
}

func TestContainerNameDerivationSanitizesRegistryTagsAndDigests(t *testing.T) {
	cases := map[string]string{
		"redis:7":                       "redis",
		"ghcr.io/example/My_App:latest": "my-app",
		"registry.local:5000/team/app@sha256:abcd": "app",
		"///": "container",
	}
	for image, want := range cases {
		if got := DeriveContainerName(image); got != want {
			t.Fatalf("DeriveContainerName(%q) = %q, want %q", image, got, want)
		}
	}
}

func TestLocalImageRef(t *testing.T) {
	if got := (LocalImage{Repository: "redis", Tag: "7", ID: "sha256:abc"}).Ref(); got != "redis:7" {
		t.Fatalf("Ref() = %q", got)
	}
	if got := (LocalImage{Repository: "<none>", Tag: "<none>", ID: "sha256:abc"}).Ref(); got != "sha256:abc" {
		t.Fatalf("dangling Ref() = %q", got)
	}
}

func TestComposeDeployPlanUsesProvidedPath(t *testing.T) {
	plan, err := ComposeDeployPlan(ComposeDeployRequest{Name: "myapp", ComposePath: "/srv/my app/compose.yml", ComposeContent: "services:\n  web:\n    image: nginx\n"})
	if err != nil {
		t.Fatalf("ComposeDeployPlan() error = %v", err)
	}
	if plan.WorkingDir != "/srv/my app" {
		t.Fatalf("WorkingDir = %q", plan.WorkingDir)
	}
	if len(plan.Commands) != 1 || plan.Commands[0] != "docker compose -f '/srv/my app/compose.yml' up -d" {
		t.Fatalf("unexpected plan: %#v", plan)
	}
}

func TestComposeDeployPlanRequiresPathAndContent(t *testing.T) {
	if _, err := ComposeDeployPlan(ComposeDeployRequest{ComposePath: "/tmp/compose.yml"}); err == nil {
		t.Fatal("expected missing content error")
	}
	if _, err := ComposeDeployPlan(ComposeDeployRequest{ComposeContent: "services: {}"}); err == nil {
		t.Fatal("expected missing path error")
	}
}
