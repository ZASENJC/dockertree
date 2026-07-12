package docker

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"strings"
	"time"

	"dockertree/internal/core"
)

func (e CLIExecutor) Inspect(ctx context.Context, containerID string) (core.ContainerInspect, error) {
	containerID = strings.TrimSpace(containerID)
	if containerID == "" {
		return core.ContainerInspect{}, errors.New("container id is required")
	}
	result, err := e.Execute(ctx, Command{Name: "docker", Args: []string{"inspect", containerID}})
	if err != nil {
		return core.ContainerInspect{}, err
	}
	var rows []struct {
		ID      string `json:"Id"`
		Name    string `json:"Name"`
		Created string `json:"Created"`
		State   struct {
			Status string `json:"Status"`
			Health *struct {
				Status string `json:"Status"`
			} `json:"Health"`
		} `json:"State"`
		HostConfig struct {
			RestartPolicy struct {
				Name string `json:"Name"`
			} `json:"RestartPolicy"`
		} `json:"HostConfig"`
		NetworkSettings struct {
			Networks map[string]json.RawMessage `json:"Networks"`
		} `json:"NetworkSettings"`
		Mounts []struct {
			Type        string `json:"Type"`
			Source      string `json:"Source"`
			Destination string `json:"Destination"`
			Mode        string `json:"Mode"`
			RW          bool   `json:"RW"`
		} `json:"Mounts"`
	}
	if err := json.Unmarshal([]byte(result.Output), &rows); err != nil {
		return core.ContainerInspect{}, err
	}
	if len(rows) == 0 {
		return core.ContainerInspect{}, errors.New("docker inspect returned no containers")
	}
	row := rows[0]
	info := core.ContainerInspect{
		ContainerID: row.ID, Name: strings.TrimPrefix(row.Name, "/"), Created: row.Created,
		Status: row.State.Status, RestartPolicy: row.HostConfig.RestartPolicy.Name,
	}
	if row.State.Health != nil {
		info.Health = row.State.Health.Status
	}
	for name := range row.NetworkSettings.Networks {
		info.Networks = append(info.Networks, name)
	}
	sort.Strings(info.Networks)
	for _, mount := range row.Mounts {
		info.Mounts = append(info.Mounts, core.InspectMount{Type: mount.Type, Source: mount.Source, Destination: mount.Destination, Mode: mount.Mode, RW: mount.RW})
	}
	return info, nil
}

func (e CLIExecutor) CheckUpdate(ctx context.Context, project core.Project) (core.UpdateCheck, error) {
	return e.checkUpdate(ctx, project, func(cmd Command) (Result, error) {
		return e.Execute(ctx, cmd)
	})
}

func (e CLIExecutor) CheckUpdateStream(ctx context.Context, project core.Project, onCommand func(Command), emit func([]byte)) (core.UpdateCheck, error) {
	return e.checkUpdate(ctx, project, func(cmd Command) (Result, error) {
		if onCommand != nil {
			onCommand(cmd)
		}
		return e.ExecuteStream(ctx, cmd, emit)
	})
}

func (e CLIExecutor) checkUpdate(ctx context.Context, project core.Project, execute func(Command) (Result, error)) (core.UpdateCheck, error) {
	check := core.UpdateCheck{ProjectID: project.ID, ProjectName: project.Name, CheckedAt: time.Now(), Status: "unknown"}
	if project.Type != core.ProjectTypeCompose || len(project.ConfigFiles) == 0 {
		check.Error = "project has no compose file"
		return check, errors.New(check.Error)
	}
	args := composeArgs(project.ConfigFiles)
	args = append(args, "--dry-run", "pull")
	cmd := Command{Name: "docker", Args: args, Dir: project.WorkingDir}
	check.Command = cmd.String()
	result, err := execute(cmd)
	check.Output = result.Output
	if err != nil {
		check.Error = result.Error
		if check.Error == "" {
			check.Error = err.Error()
		}
		return check, err
	}
	check.Status = ClassifyUpdateOutput(result.Output)
	check.Versions = e.updateVersions(project, execute)
	allDigestsComparable := len(check.Versions) > 0
	for _, version := range check.Versions {
		if !isImageDigest(version.Current) || !isImageDigest(version.Available) {
			allDigestsComparable = false
			continue
		}
		if isDifferentDigest(version.Current, version.Available) {
			check.Status = "available"
			return check, nil
		}
	}
	if allDigestsComparable {
		check.Status = "current"
	}
	return check, nil
}

func (e CLIExecutor) updateVersions(project core.Project, execute func(Command) (Result, error)) []core.UpdateVersion {
	versions := make([]core.UpdateVersion, 0, len(project.Services))
	for _, service := range project.Services {
		image := strings.TrimSpace(service.Image)
		if image == "" {
			continue
		}
		current := strings.TrimSpace(service.Labels["com.docker.compose.image"])
		if current == "" {
			current = image
		}
		available := image
		if result, err := execute(RemoteImageDigestCommand(image)); err == nil {
			if digest := parseRemoteImageDigest(result.Output); digest != "" {
				available = digest
			}
		}
		versions = append(versions, core.UpdateVersion{
			Service: service.Name, Image: image, Current: current, Available: available,
		})
	}
	return versions
}

func parseRemoteImageDigest(output string) string {
	output = strings.TrimSpace(output)
	if output == "" || output == "null" {
		return ""
	}
	var digest string
	if json.Unmarshal([]byte(output), &digest) == nil {
		return strings.TrimSpace(digest)
	}
	return strings.Trim(output, "\"")
}

func isDifferentDigest(current, available string) bool {
	current = strings.TrimSpace(current)
	available = strings.TrimSpace(available)
	return isImageDigest(current) && isImageDigest(available) && current != available
}

func isImageDigest(value string) bool {
	return strings.HasPrefix(strings.TrimSpace(value), "sha256:")
}

func ClassifyUpdateOutput(output string) string {
	text := strings.ToLower(output)
	switch {
	case strings.Contains(text, "pulling"), strings.Contains(text, "pulled"), strings.Contains(text, "download"):
		return "available"
	case strings.Contains(text, "up to date"), strings.Contains(text, "already exists"), strings.Contains(text, "no resource to pull"):
		return "current"
	default:
		return "unknown"
	}
}

func (e CLIExecutor) CleanupPreview(ctx context.Context) (core.CleanupPreview, error) {
	preview := core.CleanupPreview{}
	containerResult, err := e.Execute(ctx, Command{Name: "docker", Args: []string{"ps", "-a", "--filter", "status=exited", "--filter", "status=dead", "--format", "{{json .}}"}})
	if err != nil {
		return preview, err
	}
	preview.Containers = parseCleanupRows(containerResult.Output, "container")
	imageResult, err := e.Execute(ctx, Command{Name: "docker", Args: []string{"images", "--filter", "dangling=true", "--format", "json"}})
	if err != nil {
		return preview, err
	}
	preview.Images = parseCleanupRows(imageResult.Output, "image")
	networkResult, err := e.Execute(ctx, Command{Name: "docker", Args: []string{"network", "ls", "--filter", "dangling=true", "--filter", "type=custom", "--format", "{{json .}}"}})
	if err != nil {
		return preview, err
	}
	preview.Networks = parseCleanupRows(networkResult.Output, "network")
	return preview, nil
}

func parseCleanupRows(output, candidateType string) []core.CleanupCandidate {
	items := []core.CleanupCandidate{}
	for _, line := range strings.Split(output, "\n") {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		var row map[string]string
		if err := json.Unmarshal([]byte(line), &row); err != nil {
			continue
		}
		item := core.CleanupCandidate{Type: candidateType, ID: row["ID"]}
		switch candidateType {
		case "container":
			item.Name = row["Names"]
			item.Detail = strings.TrimSpace(row["Image"] + " " + row["Status"])
		case "image":
			item.Name = row["Repository"] + ":" + row["Tag"]
			if row["Repository"] == "<none>" {
				item.Name = row["ID"]
			}
			item.Detail = row["Size"]
		case "network":
			item.Name = row["Name"]
			item.Detail = row["Driver"]
		}
		if item.ID != "" {
			items = append(items, item)
		}
	}
	return items
}

func CleanupCommand(item core.CleanupCandidate) (Command, error) {
	id := strings.TrimSpace(item.ID)
	if id == "" {
		return Command{}, errors.New("cleanup item id is required")
	}
	switch item.Type {
	case "container":
		return Command{Name: "docker", Args: []string{"rm", id}}, nil
	case "image":
		return Command{Name: "docker", Args: []string{"rmi", id}}, nil
	case "network":
		return Command{Name: "docker", Args: []string{"network", "rm", id}}, nil
	default:
		return Command{}, fmt.Errorf("unsupported cleanup item type %q", item.Type)
	}
}
