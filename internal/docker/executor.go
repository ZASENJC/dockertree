package docker

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os/exec"
	"strconv"
	"strings"
	"sync"

	"dockertree/internal/core"
)

type Command struct {
	Name string
	Args []string
	Dir  string
}

func (c Command) String() string {
	parts := make([]string, 0, len(c.Args)+1)
	parts = append(parts, c.Name)
	for _, arg := range c.Args {
		parts = append(parts, quoteArg(arg))
	}
	return strings.TrimSpace(strings.Join(parts, " "))
}

func (c Command) RedactedString() string {
	redacted := Command{Name: c.Name, Dir: c.Dir, Args: append([]string(nil), c.Args...)}
	for i := 0; i < len(redacted.Args); i++ {
		if redacted.Args[i] == "-e" || redacted.Args[i] == "--env" {
			if i+1 < len(redacted.Args) {
				redacted.Args[i+1] = redactEnv(redacted.Args[i+1])
				i++
			}
			continue
		}
		if strings.HasPrefix(redacted.Args[i], "--env=") {
			redacted.Args[i] = "--env=" + redactEnv(strings.TrimPrefix(redacted.Args[i], "--env="))
		}
	}
	return redacted.String()
}

func redactEnv(value string) string {
	key, _, ok := strings.Cut(value, "=")
	if !ok {
		return value
	}
	return key + "=***"
}

type Result struct {
	Command  string `json:"command"`
	Output   string `json:"output"`
	ExitCode int    `json:"exitCode"`
	Error    string `json:"error,omitempty"`
}

type Executor interface {
	Execute(ctx context.Context, cmd Command) (Result, error)
	Logs(ctx context.Context, project core.Project, service string, options LogOptions) (string, error)
	SearchImages(ctx context.Context, term string) ([]SearchResult, error)
	LocalImages(ctx context.Context) ([]LocalImage, error)
	Stats(ctx context.Context) ([]core.ContainerStats, error)
	Inspect(ctx context.Context, containerID string) (core.ContainerInspect, error)
	CheckUpdate(ctx context.Context, project core.Project) (core.UpdateCheck, error)
	CleanupPreview(ctx context.Context) (core.CleanupPreview, error)
}

type StreamingExecutor interface {
	ExecuteStream(ctx context.Context, cmd Command, emit func([]byte)) (Result, error)
}

type StreamingUpdateChecker interface {
	CheckUpdateStream(ctx context.Context, project core.Project, onCommand func(Command), emit func([]byte)) (core.UpdateCheck, error)
}

type LogOptions struct {
	Tail       int
	Timestamps bool
}

type CLIExecutor struct {
	Runner Runner
}

func (e CLIExecutor) Execute(ctx context.Context, cmd Command) (Result, error) {
	return e.execute(ctx, cmd, nil)
}

func (e CLIExecutor) ExecuteStream(ctx context.Context, cmd Command, emit func([]byte)) (Result, error) {
	return e.execute(ctx, cmd, emit)
}

func (e CLIExecutor) execute(ctx context.Context, cmd Command, emit func([]byte)) (Result, error) {
	var out []byte
	var err error
	if e.Runner != nil {
		out, err = e.Runner.Run(ctx, cmd.Name, cmd.Args...)
		if emit != nil && len(out) > 0 {
			emit(out)
		}
	} else {
		process := exec.CommandContext(ctx, cmd.Name, cmd.Args...)
		process.Dir = cmd.Dir
		collector := &streamCollector{emit: emit}
		process.Stdout = collector
		process.Stderr = collector
		err = process.Run()
		out = collector.Bytes()
	}
	result := Result{Command: cmd.String(), Output: string(out)}
	if err != nil {
		result.Error = err.Error()
		var exitErr *exec.ExitError
		if errors.As(err, &exitErr) {
			result.ExitCode = exitErr.ExitCode()
		} else {
			result.ExitCode = 1
		}
		return result, err
	}
	return result, nil
}

type streamCollector struct {
	mu     sync.Mutex
	buffer bytes.Buffer
	emit   func([]byte)
}

func (w *streamCollector) Write(chunk []byte) (int, error) {
	w.mu.Lock()
	defer w.mu.Unlock()
	written, err := w.buffer.Write(chunk)
	if written > 0 && w.emit != nil {
		copyOfChunk := append([]byte(nil), chunk[:written]...)
		w.emit(copyOfChunk)
	}
	return written, err
}

func (w *streamCollector) Bytes() []byte {
	w.mu.Lock()
	defer w.mu.Unlock()
	return append([]byte(nil), w.buffer.Bytes()...)
}

func (e CLIExecutor) Logs(ctx context.Context, project core.Project, service string, options LogOptions) (string, error) {
	logArgs := logOptionArgs(options)
	if project.Type == core.ProjectTypeStandalone {
		containerID := ""
		if service != "" {
			for _, svc := range project.Services {
				if svc.Name == service || svc.ContainerID == service {
					containerID = svc.ContainerID
					break
				}
			}
		}
		if containerID == "" && len(project.Services) > 0 {
			containerID = project.Services[0].ContainerID
		}
		if containerID == "" {
			return "", errors.New("standalone project has no container for logs")
		}
		result, err := e.Execute(ctx, Command{Name: "docker", Args: append(append([]string{"logs"}, logArgs...), containerID)})
		return result.Output, err
	}
	args := composeArgs(project.ConfigFiles)
	args = append(args, "logs")
	args = append(args, logArgs...)
	if service != "" {
		args = append(args, service)
	}
	result, err := e.Execute(ctx, Command{Name: "docker", Args: args, Dir: project.WorkingDir})
	return result.Output, err
}

func (e CLIExecutor) Stats(ctx context.Context) ([]core.ContainerStats, error) {
	result, err := e.Execute(ctx, Command{Name: "docker", Args: []string{"stats", "--no-stream", "--format", "{{json .}}"}})
	if err != nil {
		return nil, err
	}
	rows := make([]core.ContainerStats, 0)
	for _, line := range strings.Split(result.Output, "\n") {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		var row struct {
			Container string `json:"Container"`
			Name      string `json:"Name"`
			CPUPerc   string `json:"CPUPerc"`
			MemUsage  string `json:"MemUsage"`
			MemPerc   string `json:"MemPerc"`
			NetIO     string `json:"NetIO"`
			BlockIO   string `json:"BlockIO"`
			PIDs      string `json:"PIDs"`
		}
		if err := json.Unmarshal([]byte(line), &row); err != nil {
			continue
		}
		pids, _ := strconv.Atoi(strings.TrimSpace(row.PIDs))
		rows = append(rows, core.ContainerStats{
			ContainerID:   row.Container,
			Name:          row.Name,
			CPUPercent:    parsePercent(row.CPUPerc),
			MemoryUsage:   row.MemUsage,
			MemoryPercent: parsePercent(row.MemPerc),
			NetworkIO:     row.NetIO,
			BlockIO:       row.BlockIO,
			PIDs:          pids,
		})
	}
	return rows, nil
}

func parsePercent(value string) float64 {
	parsed, _ := strconv.ParseFloat(strings.TrimSpace(strings.TrimSuffix(value, "%")), 64)
	return parsed
}

func (e CLIExecutor) SearchImages(ctx context.Context, term string) ([]SearchResult, error) {
	term = strings.TrimSpace(term)
	if term == "" {
		return []SearchResult{}, nil
	}
	result, err := e.Execute(ctx, Command{Name: "docker", Args: []string{"search", "--limit", "10", "--format", "json", term}})
	if err != nil {
		return nil, err
	}
	lines := strings.Split(result.Output, "\n")
	results := make([]SearchResult, 0, len(lines))
	for _, line := range lines {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		var row struct {
			Name        string `json:"Name"`
			Description string `json:"Description"`
			StarCount   string `json:"StarCount"`
			Official    string `json:"IsOfficial"`
		}
		if err := json.Unmarshal([]byte(line), &row); err != nil {
			continue
		}
		stars, _ := strconv.Atoi(row.StarCount)
		results = append(results, SearchResult{Name: row.Name, Description: row.Description, Stars: stars, Official: row.Official == "[OK]" || strings.EqualFold(row.Official, "true")})
	}
	return results, nil
}

func (e CLIExecutor) LocalImages(ctx context.Context) ([]LocalImage, error) {
	result, err := e.Execute(ctx, Command{Name: "docker", Args: []string{"images", "--format", "json"}})
	if err != nil {
		return nil, err
	}
	lines := strings.Split(result.Output, "\n")
	images := make([]LocalImage, 0, len(lines))
	for _, line := range lines {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		var row struct {
			Repository   string `json:"Repository"`
			Tag          string `json:"Tag"`
			ID           string `json:"ID"`
			CreatedAt    string `json:"CreatedAt"`
			CreatedSince string `json:"CreatedSince"`
			Size         string `json:"Size"`
		}
		if err := json.Unmarshal([]byte(line), &row); err != nil {
			continue
		}
		created := row.CreatedSince
		if created == "" {
			created = row.CreatedAt
		}
		images = append(images, LocalImage{Repository: row.Repository, Tag: row.Tag, ID: row.ID, Created: created, Size: row.Size})
	}
	return images, nil
}

func CommandsForPlan(plan core.UpdatePlan) []Command {
	commands := make([]Command, 0, len(plan.Commands))
	for _, line := range plan.Commands {
		commands = append(commands, parseDockerCommand(line, plan.WorkingDir))
	}
	return commands
}

func UpdateCommands(project core.Project, requiresBuild bool, removeOrphans bool) []Command {
	if project.Type != core.ProjectTypeCompose || len(project.ConfigFiles) == 0 {
		return nil
	}
	base := composeArgs(project.ConfigFiles)
	pullArgs := append(append([]string{}, base...), "--progress", "json", "pull")
	commands := []Command{{Name: "docker", Args: pullArgs, Dir: project.WorkingDir}}
	if requiresBuild {
		commands = append(commands, Command{Name: "docker", Args: append(append([]string{}, base...), "build"), Dir: project.WorkingDir})
	}
	upArgs := append(append([]string{}, base...), "up", "-d")
	if removeOrphans {
		upArgs = append(upArgs, "--remove-orphans")
	}
	commands = append(commands, Command{Name: "docker", Args: upArgs, Dir: project.WorkingDir})
	return commands
}

func ServiceUpdateCheckCommand(project core.Project, service string) Command {
	args := composeArgs(project.ConfigFiles)
	args = append(args, "--dry-run", "pull", strings.TrimSpace(service))
	return Command{Name: "docker", Args: args, Dir: project.WorkingDir}
}

func RemoteImageDigestCommand(image string) Command {
	return Command{
		Name: "docker",
		Args: []string{"buildx", "imagetools", "inspect", "--format", "{{json .Manifest.Digest}}", strings.TrimSpace(image)},
	}
}

func ServiceUpdateCommands(project core.Project, service string, requiresBuild bool) []Command {
	service = strings.TrimSpace(service)
	if project.Type != core.ProjectTypeCompose || len(project.ConfigFiles) == 0 || service == "" {
		return nil
	}
	base := composeArgs(project.ConfigFiles)
	pullArgs := append(append([]string{}, base...), "--progress", "json", "pull", service)
	commands := []Command{{Name: "docker", Args: pullArgs, Dir: project.WorkingDir}}
	if requiresBuild {
		commands = append(commands, Command{Name: "docker", Args: append(append([]string{}, base...), "build", service), Dir: project.WorkingDir})
	}
	commands = append(commands, Command{
		Name: "docker", Args: append(append([]string{}, base...), "up", "-d", "--no-deps", service), Dir: project.WorkingDir,
	})
	return commands
}

func ComposeConfigCommand(project core.Project) Command {
	args := composeArgs(project.ConfigFiles)
	args = append(args, "config", "--format", "json")
	return Command{Name: "docker", Args: args, Dir: project.WorkingDir}
}

func ComposeValidateCommand(project core.Project) Command {
	args := composeArgs(project.ConfigFiles)
	args = append(args, "config")
	return Command{Name: "docker", Args: args, Dir: project.WorkingDir}
}

func ComposeUpCommand(project core.Project) Command {
	args := composeArgs(project.ConfigFiles)
	args = append(args, "up", "-d")
	return Command{Name: "docker", Args: args, Dir: project.WorkingDir}
}

func ComposeConfigRequiresBuild(output string) (bool, error) {
	config, err := parseComposeBuildConfig(output)
	if err != nil {
		return false, err
	}
	for _, service := range config.Services {
		if composeServiceHasBuild(service) {
			return true, nil
		}
	}
	return false, nil
}

func ComposeServiceRequiresBuild(output, serviceName string) (bool, error) {
	config, err := parseComposeBuildConfig(output)
	if err != nil {
		return false, err
	}
	serviceName = strings.TrimSpace(serviceName)
	service, ok := config.Services[serviceName]
	if !ok {
		return false, fmt.Errorf("compose service %q was not found", serviceName)
	}
	return composeServiceHasBuild(service), nil
}

type composeBuildConfig struct {
	Services map[string]composeBuildService `json:"services"`
}

type composeBuildService struct {
	Build json.RawMessage `json:"build"`
}

func parseComposeBuildConfig(output string) (composeBuildConfig, error) {
	var config composeBuildConfig
	if err := json.Unmarshal([]byte(output), &config); err != nil {
		return composeBuildConfig{}, err
	}
	return config, nil
}

func composeServiceHasBuild(service composeBuildService) bool {
	build := strings.TrimSpace(string(service.Build))
	return build != "" && build != "null"
}

func LifecycleCommand(project core.Project, action string) Command {
	if project.Type == core.ProjectTypeStandalone && len(project.Services) > 0 {
		return Command{Name: "docker", Args: []string{action, project.Services[0].ContainerID}}
	}
	args := composeArgs(project.ConfigFiles)
	args = append(args, action)
	return Command{Name: "docker", Args: args, Dir: project.WorkingDir}
}

func ContainerLifecycleCommand(containerID string, action string) Command {
	return Command{Name: "docker", Args: []string{action, strings.TrimSpace(containerID)}}
}

func ContainerLogsCommand(containerID string, options LogOptions) Command {
	args := append([]string{"logs"}, logOptionArgs(options)...)
	args = append(args, strings.TrimSpace(containerID))
	return Command{Name: "docker", Args: args}
}

func logOptionArgs(options LogOptions) []string {
	tail := options.Tail
	if tail <= 0 {
		tail = 300
	}
	args := []string{"--tail", strconv.Itoa(tail)}
	if options.Timestamps {
		args = append(args, "--timestamps")
	}
	return args
}

func DeleteContainerCommand(containerID string) Command {
	return Command{Name: "docker", Args: []string{"rm", "-f", strings.TrimSpace(containerID)}}
}

func DeleteProjectCommand(project core.Project) Command {
	args := composeArgs(project.ConfigFiles)
	args = append(args, "down")
	return Command{Name: "docker", Args: args, Dir: project.WorkingDir}
}

func DeleteImageCommand(ref string, force bool) Command {
	args := []string{"rmi"}
	if force {
		args = append(args, "-f")
	}
	args = append(args, strings.TrimSpace(ref))
	return Command{Name: "docker", Args: args}
}

func composeArgs(files []string) []string {
	args := []string{"compose"}
	for _, file := range files {
		args = append(args, "-f", file)
	}
	return args
}

func parseDockerCommand(line, dir string) Command {
	fields := strings.Fields(line)
	if len(fields) == 0 {
		return Command{}
	}
	return Command{Name: fields[0], Args: fields[1:], Dir: dir}
}

func quoteArg(arg string) string {
	if arg == "" {
		return "''"
	}
	if !strings.ContainsAny(arg, " \t\n'\"") {
		return arg
	}
	return "'" + strings.ReplaceAll(arg, "'", "'\\''") + "'"
}
