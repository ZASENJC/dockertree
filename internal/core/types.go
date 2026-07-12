package core

import "time"

type ProjectType string

const (
	ProjectTypeCompose    ProjectType = "compose"
	ProjectTypeStandalone ProjectType = "standalone"
)

type Project struct {
	ID           string        `json:"id" yaml:"id"`
	Name         string        `json:"name" yaml:"name"`
	Type         ProjectType   `json:"type" yaml:"type"`
	Status       string        `json:"status" yaml:"status"`
	WorkingDir   string        `json:"workingDir" yaml:"workingDir"`
	ConfigFiles  []string      `json:"configFiles" yaml:"configFiles"`
	Services     []Service     `json:"services" yaml:"services"`
	Ports        []string      `json:"ports" yaml:"ports"`
	Aliases      []string      `json:"aliases,omitempty" yaml:"aliases,omitempty"`
	Tags         []string      `json:"tags,omitempty" yaml:"tags,omitempty"`
	Links        []ServiceLink `json:"links,omitempty" yaml:"links,omitempty"`
	Favorite     bool          `json:"favorite" yaml:"favorite"`
	LastScanned  time.Time     `json:"lastScanned" yaml:"lastScanned"`
	LastAction   string        `json:"lastAction,omitempty" yaml:"lastAction,omitempty"`
	LastExitCode int           `json:"lastExitCode,omitempty" yaml:"lastExitCode,omitempty"`
}

type ServiceLink struct {
	Name string `json:"name" yaml:"name"`
	URL  string `json:"url" yaml:"url"`
}

type Service struct {
	Name        string            `json:"name" yaml:"name"`
	ContainerID string            `json:"containerId" yaml:"containerId"`
	Image       string            `json:"image" yaml:"image"`
	State       string            `json:"state" yaml:"state"`
	Status      string            `json:"status" yaml:"status"`
	Health      string            `json:"health" yaml:"health"`
	Ports       []string          `json:"ports" yaml:"ports"`
	Mounts      []string          `json:"mounts" yaml:"mounts"`
	Labels      map[string]string `json:"labels,omitempty" yaml:"labels,omitempty"`
}

type ContainerStats struct {
	ContainerID   string  `json:"containerId"`
	Name          string  `json:"name"`
	CPUPercent    float64 `json:"cpuPercent"`
	MemoryUsage   string  `json:"memoryUsage"`
	MemoryPercent float64 `json:"memoryPercent"`
	NetworkIO     string  `json:"networkIO"`
	BlockIO       string  `json:"blockIO"`
	PIDs          int     `json:"pids"`
}

type InspectMount struct {
	Type        string `json:"type"`
	Source      string `json:"source"`
	Destination string `json:"destination"`
	Mode        string `json:"mode"`
	RW          bool   `json:"rw"`
}

type ContainerInspect struct {
	ContainerID   string         `json:"containerId"`
	Name          string         `json:"name"`
	Created       string         `json:"created"`
	Status        string         `json:"status"`
	Health        string         `json:"health"`
	RestartPolicy string         `json:"restartPolicy"`
	Networks      []string       `json:"networks"`
	Mounts        []InspectMount `json:"mounts"`
}

type OperationRecord struct {
	ID         string    `json:"id"`
	Timestamp  time.Time `json:"timestamp"`
	Action     string    `json:"action"`
	TargetType string    `json:"targetType"`
	TargetID   string    `json:"targetId"`
	TargetName string    `json:"targetName"`
	Command    string    `json:"command"`
	Output     string    `json:"output,omitempty"`
	ExitCode   int       `json:"exitCode"`
	Success    bool      `json:"success"`
	Error      string    `json:"error,omitempty"`
}

type UpdateCheck struct {
	ProjectID   string    `json:"projectId"`
	ProjectName string    `json:"projectName"`
	Status      string    `json:"status"`
	CheckedAt   time.Time `json:"checkedAt"`
	Command     string    `json:"command"`
	Output      string    `json:"output,omitempty"`
	Error       string    `json:"error,omitempty"`
}

type CleanupCandidate struct {
	Type   string `json:"type"`
	ID     string `json:"id"`
	Name   string `json:"name"`
	Detail string `json:"detail"`
}

type CleanupPreview struct {
	Containers []CleanupCandidate `json:"containers"`
	Images     []CleanupCandidate `json:"images"`
	Networks   []CleanupCandidate `json:"networks"`
}

type ContainerDeploySpec struct {
	Name          string   `json:"name" yaml:"name"`
	Image         string   `json:"image" yaml:"image"`
	Ports         []string `json:"ports,omitempty" yaml:"ports,omitempty"`
	Env           []string `json:"env,omitempty" yaml:"env,omitempty"`
	Volumes       []string `json:"volumes,omitempty" yaml:"volumes,omitempty"`
	Network       string   `json:"network,omitempty" yaml:"network,omitempty"`
	RestartPolicy string   `json:"restartPolicy,omitempty" yaml:"restartPolicy,omitempty"`
}

type ComposeDeploySpec struct {
	Name                 string `json:"name" yaml:"name"`
	ComposePath          string `json:"composePath" yaml:"composePath"`
	ComposeContent       string `json:"composeContent" yaml:"composeContent"`
	ExpectedExistingHash string `json:"expectedExistingHash,omitempty" yaml:"-"`
}

type ComposeDeployPreview struct {
	Plan              UpdatePlan `json:"plan"`
	ComposePath       string     `json:"composePath"`
	NormalizedContent string     `json:"normalizedContent"`
	ExistingContent   string     `json:"existingContent,omitempty"`
	ExistingHash      string     `json:"existingHash,omitempty"`
	Overwrites        bool       `json:"overwrites"`
}

type DeployTemplate struct {
	ID        string               `json:"id" yaml:"id"`
	Name      string               `json:"name" yaml:"name"`
	Mode      string               `json:"mode" yaml:"mode"`
	Container *ContainerDeploySpec `json:"container,omitempty" yaml:"container,omitempty"`
	Compose   *ComposeDeploySpec   `json:"compose,omitempty" yaml:"compose,omitempty"`
}

type UpdatePlan struct {
	ProjectID     string   `json:"projectId"`
	ProjectName   string   `json:"projectName"`
	WorkingDir    string   `json:"workingDir"`
	Commands      []string `json:"commands"`
	Services      []string `json:"services"`
	Warnings      []string `json:"warnings"`
	CanDeploy     bool     `json:"canDeploy"`
	RequiresBuild bool     `json:"requiresBuild"`
}
