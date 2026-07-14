package docker

import (
	"context"
	"errors"
	"reflect"
	"strings"
	"testing"
	"time"

	"dockertree/internal/core"
)

func TestCLIExecutorExecuteStreamEmitsOutputBeforeCommandCompletes(t *testing.T) {
	exec := CLIExecutor{}
	chunks := make(chan string, 4)
	done := make(chan struct{})
	var result Result
	var execErr error

	go func() {
		result, execErr = exec.ExecuteStream(context.Background(), Command{
			Name: "sh",
			Args: []string{"-c", "printf 'first\\n'; sleep 0.3; printf 'second\\n'"},
		}, func(chunk []byte) {
			chunks <- string(chunk)
		})
		close(done)
	}()

	select {
	case chunk := <-chunks:
		if !strings.Contains(chunk, "first") {
			t.Fatalf("first streamed chunk = %q", chunk)
		}
	case <-time.After(200 * time.Millisecond):
		t.Fatal("first command output was buffered until command completion")
	}

	select {
	case <-done:
		t.Fatal("command completed before the streaming assertion")
	case <-time.After(50 * time.Millisecond):
	}

	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("streaming command did not complete")
	}
	if execErr != nil {
		t.Fatalf("ExecuteStream() error = %v", execErr)
	}
	if result.Output != "first\nsecond\n" {
		t.Fatalf("result output = %q", result.Output)
	}
}

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
		"docker compose -f /srv/app/compose.yml logs --tail 100 --timestamps web": "compose logs",
		"docker logs --tail 100 --timestamps abc123":                              "standalone logs",
	}}
	exec := CLIExecutor{Runner: runner}
	options := LogOptions{Tail: 100, Timestamps: true}

	out, err := exec.Logs(context.Background(), core.Project{
		Type:        core.ProjectTypeCompose,
		WorkingDir:  "/srv/app",
		ConfigFiles: []string{"/srv/app/compose.yml"},
	}, "web", options)
	if err != nil || out != "compose logs" {
		t.Fatalf("compose logs out=%q err=%v", out, err)
	}

	out, err = exec.Logs(context.Background(), core.Project{
		Type:     core.ProjectTypeStandalone,
		Services: []core.Service{{ContainerID: "abc123"}},
	}, "", options)
	if err != nil || out != "standalone logs" {
		t.Fatalf("standalone logs out=%q err=%v", out, err)
	}
	if got := strings.Join(runner.calls, "\n"); !strings.Contains(got, "docker logs --tail 100 --timestamps abc123") {
		t.Fatalf("standalone logs did not use docker logs: %s", got)
	}
}

func TestCLIExecutorStatsParsesDockerJSONLines(t *testing.T) {
	runner := &recordingRunner{outputs: map[string]string{
		"docker stats --no-stream --format {{json .}}": strings.Join([]string{
			`{"Container":"abc123","Name":"redis","CPUPerc":"1.25%","MemUsage":"42MiB / 1GiB","MemPerc":"4.10%","NetIO":"1.2MB / 300kB","BlockIO":"4kB / 8kB","PIDs":"7"}`,
			`not-json`,
			`{"Container":"def456","Name":"web","CPUPerc":"0.00%","MemUsage":"8MiB / 512MiB","MemPerc":"1.56%","NetIO":"0B / 0B","BlockIO":"0B / 0B","PIDs":"3"}`,
		}, "\n"),
	}}

	stats, err := (CLIExecutor{Runner: runner}).Stats(context.Background())
	if err != nil {
		t.Fatalf("Stats() error = %v", err)
	}
	if len(stats) != 2 {
		t.Fatalf("stats = %#v", stats)
	}
	if stats[0].ContainerID != "abc123" || stats[0].CPUPercent != 1.25 || stats[0].MemoryPercent != 4.10 || stats[0].PIDs != 7 {
		t.Fatalf("first stats row = %#v", stats[0])
	}
	if stats[1].Name != "web" || stats[1].MemoryUsage != "8MiB / 512MiB" {
		t.Fatalf("second stats row = %#v", stats[1])
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

	logsCmd := ContainerLogsCommand(" abc123 ", LogOptions{Tail: 1000, Timestamps: true})
	wantLogs := Command{Name: "docker", Args: []string{"logs", "--tail", "1000", "--timestamps", "abc123"}}
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
	commands := UpdateCommands(project, false)
	if len(commands) != 2 {
		t.Fatalf("commands len = %d", len(commands))
	}
	if commands[0].Args[2] != "/srv/photo tree/compose.yml" {
		t.Fatalf("path was split or changed: %#v", commands[0].Args)
	}
	if commands[0].String() != "docker compose -f '/srv/photo tree/compose.yml' --progress json pull" {
		t.Fatalf("display command not quoted: %q", commands[0].String())
	}
}

func TestServiceUpdateCommandsTargetOnlySelectedComposeService(t *testing.T) {
	project := core.Project{Type: core.ProjectTypeCompose, WorkingDir: "/srv/photo tree", ConfigFiles: []string{"/srv/photo tree/compose.yml"}}
	commands := ServiceUpdateCommands(project, " web ")
	want := []Command{
		{Name: "docker", Args: []string{"compose", "-f", "/srv/photo tree/compose.yml", "--progress", "json", "pull", "web"}, Dir: "/srv/photo tree"},
		{Name: "docker", Args: []string{"compose", "-f", "/srv/photo tree/compose.yml", "up", "-d", "--no-deps", "web"}, Dir: "/srv/photo tree"},
	}
	if !reflect.DeepEqual(commands, want) {
		t.Fatalf("commands = %#v, want %#v", commands, want)
	}

	commands = ServiceUpdateCommands(project, "web")
	if len(commands) != 2 || commands[1].String() != "docker compose -f '/srv/photo tree/compose.yml' up -d --no-deps web" {
		t.Fatalf("image-only service commands = %#v", commands)
	}
}

func TestServiceUpdateCheckCommandTargetsOnlySelectedComposeService(t *testing.T) {
	project := core.Project{Type: core.ProjectTypeCompose, WorkingDir: "/srv/app", ConfigFiles: []string{"/srv/app/compose.yml"}}
	cmd := ServiceUpdateCheckCommand(project, " api ")
	want := Command{Name: "docker", Args: []string{"compose", "-f", "/srv/app/compose.yml", "--dry-run", "pull", "api"}, Dir: "/srv/app"}
	if !reflect.DeepEqual(cmd, want) {
		t.Fatalf("cmd = %#v, want %#v", cmd, want)
	}
}

func TestContainerDeployCommands(t *testing.T) {
	req := ContainerDeployRequest{
		Name: "redis-local", Image: "redis:7", Ports: []string{"6379:6379"},
		Env: []string{"REDIS_PASSWORD=secret"}, Volumes: []string{"redis-data:/data"},
		Network: "proxy", RestartPolicy: "unless-stopped",
	}
	cmd, err := ValidatedContainerDeployCommand(req)
	if err != nil {
		t.Fatalf("ValidatedContainerDeployCommand() error = %v", err)
	}
	want := Command{Name: "docker", Args: []string{"run", "-d", "--name", "redis-local", "--restart", "unless-stopped", "-p", "6379:6379", "-e", "REDIS_PASSWORD=secret", "-v", "redis-data:/data", "--network", "proxy", "redis:7"}}
	if !reflect.DeepEqual(cmd, want) {
		t.Fatalf("cmd = %#v, want %#v", cmd, want)
	}
	if redacted := cmd.RedactedString(); strings.Contains(redacted, "secret") || !strings.Contains(redacted, "REDIS_PASSWORD=***") {
		t.Fatalf("redacted command = %q", redacted)
	}
}

func TestContainerDeployPlanRedactsEnvironmentValues(t *testing.T) {
	plan, err := ContainerDeployPlan(ContainerDeployRequest{Image: "redis:7", Env: []string{"PASSWORD=top-secret"}})
	if err != nil {
		t.Fatalf("ContainerDeployPlan() error = %v", err)
	}
	if len(plan.Commands) != 1 || strings.Contains(plan.Commands[0], "top-secret") || !strings.Contains(plan.Commands[0], "PASSWORD=***") {
		t.Fatalf("plan commands = %#v", plan.Commands)
	}
}

func TestContainerDeployValidationRejectsUnsafeOptions(t *testing.T) {
	for _, req := range []ContainerDeployRequest{
		{Image: "redis:7", Ports: []string{"bad-port"}},
		{Image: "redis:7", Env: []string{"NOT AN ENV"}},
		{Image: "redis:7", Volumes: []string{"missing-target"}},
		{Image: "redis:7", Network: "bad network"},
		{Image: "redis:7", RestartPolicy: "sometimes"},
	} {
		if _, err := ValidatedContainerDeployCommand(req); err == nil {
			t.Fatalf("expected validation error for %#v", req)
		}
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

func TestComposeContentValidationAndHash(t *testing.T) {
	normalized, err := NormalizeComposeContent("services:\n  web:\n    image: nginx:latest\n")
	if err != nil || !strings.Contains(normalized, "services:") || !strings.Contains(normalized, "nginx:latest") {
		t.Fatalf("normalized=%q err=%v", normalized, err)
	}
	if _, err := NormalizeComposeContent("name: missing-services\n"); err == nil {
		t.Fatal("compose content without services should fail")
	}
	if ComposeContentHash(normalized) == "" || ComposeContentHash(normalized) != ComposeContentHash(normalized) {
		t.Fatal("compose hash should be stable")
	}
}
