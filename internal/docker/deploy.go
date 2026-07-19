package docker

import (
	"crypto/sha256"
	"errors"
	"fmt"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"

	"dockertree/internal/core"
	"gopkg.in/yaml.v3"
)

type SearchResult struct {
	Name        string `json:"name"`
	Description string `json:"description"`
	Stars       int    `json:"stars"`
	Official    bool   `json:"official"`
}

type LocalImage struct {
	Repository string `json:"repository"`
	Tag        string `json:"tag"`
	ID         string `json:"id"`
	Created    string `json:"created"`
	Size       string `json:"size"`
}

func (i LocalImage) Ref() string {
	if i.Repository == "" || i.Repository == "<none>" {
		return i.ID
	}
	if i.Tag == "" || i.Tag == "<none>" {
		return i.Repository
	}
	return i.Repository + ":" + i.Tag
}

type ContainerDeployRequest = core.ContainerDeploySpec

type ComposeDeployRequest = core.ComposeDeploySpec

func ContainerDeployPlan(req ContainerDeployRequest) (core.UpdatePlan, error) {
	cmd, err := ValidatedContainerDeployCommand(req)
	if err != nil {
		return core.UpdatePlan{}, err
	}
	name := ""
	if len(cmd.Args) >= 4 {
		name = cmd.Args[3]
	}
	plan := core.UpdatePlan{ProjectID: "container:" + name, ProjectName: name, Commands: []string{cmd.RedactedString()}, CanDeploy: true}
	plan.Warnings = append(plan.Warnings, ContainerImageWarnings(req.Image)...)
	return plan, nil
}

func ValidatedContainerDeployCommand(req ContainerDeployRequest) (Command, error) {
	req.Name = strings.TrimSpace(req.Name)
	req.Image = strings.TrimSpace(req.Image)
	if req.Image == "" {
		return Command{}, errors.New("image name is required")
	}
	if req.Name == "" {
		req.Name = DeriveContainerName(req.Image)
	}
	if !regexp.MustCompile(`^[a-zA-Z0-9][a-zA-Z0-9_.-]*$`).MatchString(req.Name) {
		return Command{}, errors.New("container name contains unsupported characters")
	}
	for _, port := range req.Ports {
		if err := validatePortMapping(port); err != nil {
			return Command{}, err
		}
	}
	for _, env := range req.Env {
		key, _, ok := strings.Cut(env, "=")
		if !ok || !regexp.MustCompile(`^[A-Za-z_][A-Za-z0-9_]*$`).MatchString(key) || strings.ContainsAny(env, "\r\n\x00") {
			return Command{}, fmt.Errorf("invalid environment entry %q", env)
		}
	}
	for _, volume := range req.Volumes {
		if err := validateVolumeMapping(volume); err != nil {
			return Command{}, err
		}
	}
	if req.Network != "" && !regexp.MustCompile(`^[a-zA-Z0-9][a-zA-Z0-9_.-]*$`).MatchString(req.Network) {
		return Command{}, errors.New("network contains unsupported characters")
	}
	allowedRestarts := map[string]bool{"": true, "no": true, "always": true, "unless-stopped": true, "on-failure": true}
	if !allowedRestarts[req.RestartPolicy] {
		return Command{}, errors.New("restartPolicy must be no, always, unless-stopped, or on-failure")
	}
	return ContainerDeployCommand(req), nil
}

func ContainerDeployCommand(req ContainerDeployRequest) Command {
	args := []string{"run", "-d", "--name", strings.TrimSpace(req.Name)}
	if req.RestartPolicy != "" {
		args = append(args, "--restart", req.RestartPolicy)
	}
	for _, port := range req.Ports {
		args = append(args, "-p", strings.TrimSpace(port))
	}
	for _, env := range req.Env {
		args = append(args, "-e", strings.TrimSpace(env))
	}
	for _, volume := range req.Volumes {
		args = append(args, "-v", strings.TrimSpace(volume))
	}
	if req.Network != "" {
		args = append(args, "--network", strings.TrimSpace(req.Network))
	}
	args = append(args, strings.TrimSpace(req.Image))
	return Command{Name: "docker", Args: args}
}

func validatePortMapping(value string) error {
	value = strings.TrimSpace(value)
	base, protocol, hasProtocol := strings.Cut(value, "/")
	if hasProtocol && protocol != "tcp" && protocol != "udp" && protocol != "sctp" {
		return fmt.Errorf("invalid port mapping %q", value)
	}
	parts := strings.Split(base, ":")
	if len(parts) != 2 && len(parts) != 3 {
		return fmt.Errorf("invalid port mapping %q", value)
	}
	for _, port := range parts[len(parts)-2:] {
		number, err := strconv.Atoi(port)
		if err != nil || number < 1 || number > 65535 {
			return fmt.Errorf("invalid port mapping %q", value)
		}
	}
	if len(parts) == 3 && strings.TrimSpace(parts[0]) == "" {
		return fmt.Errorf("invalid port mapping %q", value)
	}
	return nil
}

func validateVolumeMapping(value string) error {
	value = strings.TrimSpace(value)
	if strings.ContainsAny(value, "\r\n\x00") {
		return fmt.Errorf("invalid volume mapping %q", value)
	}
	parts := strings.Split(value, ":")
	if len(parts) < 2 || len(parts) > 3 || strings.TrimSpace(parts[0]) == "" || !strings.HasPrefix(parts[1], "/") {
		return fmt.Errorf("invalid volume mapping %q", value)
	}
	if len(parts) == 3 && parts[2] != "ro" && parts[2] != "rw" {
		return fmt.Errorf("invalid volume mapping %q", value)
	}
	return nil
}

func DeriveContainerName(image string) string {
	image = strings.TrimSpace(image)
	if digestBase, _, ok := strings.Cut(image, "@"); ok {
		image = digestBase
	}
	parts := strings.Split(strings.Trim(image, "/"), "/")
	base := ""
	if len(parts) > 0 {
		base = parts[len(parts)-1]
	}
	if beforeTag, _, ok := strings.Cut(base, ":"); ok {
		base = beforeTag
	}
	base = strings.ToLower(base)
	invalid := regexp.MustCompile(`[^a-z0-9.-]+`)
	base = invalid.ReplaceAllString(base, "-")
	base = strings.Trim(base, "-.")
	if base == "" {
		return "container"
	}
	return base
}

func ImplicitLatestRef(image string) (string, bool) {
	image = strings.TrimSpace(image)
	if image == "" || strings.Contains(image, "@") {
		return "", false
	}
	lastSlash := strings.LastIndex(image, "/")
	lastColon := strings.LastIndex(image, ":")
	if lastColon > lastSlash {
		return "", false
	}
	return image + ":latest", true
}

func ContainerImageWarnings(image string) []string {
	image = strings.TrimSpace(image)
	if image == "" {
		return nil
	}
	warnings := []string{}
	if ref, ok := ImplicitLatestRef(image); ok {
		warnings = append(warnings, "镜像未指定标签，Docker 会按 "+ref+" 处理。")
	}
	if isBareDockerHubRef(image) {
		name := imageBaseName(image)
		warnings = append(warnings, "裸镜像名会解析到 Docker Hub 官方/library 命名空间，不会自动使用你的用户名或组织名；如果远端仓库在账号下，请输入 <用户名>/"+name+":latest。")
	}
	if len(warnings) > 0 {
		warnings = append(warnings, "如果这是本地项目镜像，请先运行 docker build -t "+imageBuildRef(image)+" .，或改用本地镜像列表中的完整镜像名。")
	}
	return warnings
}

func isBareDockerHubRef(image string) bool {
	image = strings.TrimSpace(image)
	if image == "" || strings.Contains(image, "/") || strings.Contains(image, "@") {
		return false
	}
	return true
}

func imageBaseName(image string) string {
	image = strings.TrimSpace(image)
	if digestBase, _, ok := strings.Cut(image, "@"); ok {
		image = digestBase
	}
	parts := strings.Split(strings.Trim(image, "/"), "/")
	base := image
	if len(parts) > 0 {
		base = parts[len(parts)-1]
	}
	if beforeTag, _, ok := strings.Cut(base, ":"); ok {
		base = beforeTag
	}
	return base
}

func imageBuildRef(image string) string {
	image = strings.TrimSpace(image)
	if image == "" {
		return "container"
	}
	if strings.Contains(image, "@") {
		return image
	}
	return strings.TrimSuffix(image, ":latest")
}

func ComposeDeployPlan(req ComposeDeployRequest) (core.UpdatePlan, error) {
	cmd, err := ValidatedComposeDeployCommand(req)
	if err != nil {
		return core.UpdatePlan{}, err
	}
	name := strings.TrimSpace(req.Name)
	if name == "" {
		name = filepath.Base(filepath.Dir(req.ComposePath))
	}
	return core.UpdatePlan{ProjectID: "compose:" + name, ProjectName: name, WorkingDir: filepath.Dir(req.ComposePath), Commands: []string{cmd.String()}, CanDeploy: true}, nil
}

func ValidatedComposeDeployCommand(req ComposeDeployRequest) (Command, error) {
	path := strings.TrimSpace(req.ComposePath)
	content := strings.TrimSpace(req.ComposeContent)
	if path == "" {
		return Command{}, errors.New("compose path is required")
	}
	if content == "" {
		return Command{}, errors.New("compose content is required")
	}
	if _, err := NormalizeComposeContent(content); err != nil {
		return Command{}, err
	}
	return Command{Name: "docker", Args: []string{"compose", "-f", path, "--progress", "json", "up", "-d"}, Dir: filepath.Dir(path)}, nil
}

func NormalizeComposeContent(content string) (string, error) {
	content = strings.TrimSpace(content)
	if content == "" {
		return "", errors.New("compose content is required")
	}
	var document yaml.Node
	if err := yaml.Unmarshal([]byte(content), &document); err != nil {
		return "", fmt.Errorf("invalid compose yaml: %w", err)
	}
	if len(document.Content) == 0 || document.Content[0].Kind != yaml.MappingNode {
		return "", errors.New("compose content must be a mapping")
	}
	root := document.Content[0]
	servicesFound := false
	for i := 0; i+1 < len(root.Content); i += 2 {
		if root.Content[i].Value == "services" && root.Content[i+1].Kind == yaml.MappingNode && len(root.Content[i+1].Content) > 0 {
			servicesFound = true
			break
		}
	}
	if !servicesFound {
		return "", errors.New("compose content must define at least one service")
	}
	data, err := yaml.Marshal(&document)
	if err != nil {
		return "", err
	}
	return string(data), nil
}

func ComposeContentHash(content string) string {
	digest := sha256.Sum256([]byte(content))
	return fmt.Sprintf("%x", digest[:])
}
