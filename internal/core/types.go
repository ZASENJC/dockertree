package core

import "time"

type ProjectType string

const (
	ProjectTypeCompose    ProjectType = "compose"
	ProjectTypeStandalone ProjectType = "standalone"
)

type Project struct {
	ID           string      `json:"id" yaml:"id"`
	Name         string      `json:"name" yaml:"name"`
	Type         ProjectType `json:"type" yaml:"type"`
	Status       string      `json:"status" yaml:"status"`
	WorkingDir   string      `json:"workingDir" yaml:"workingDir"`
	ConfigFiles  []string    `json:"configFiles" yaml:"configFiles"`
	Services     []Service   `json:"services" yaml:"services"`
	Ports        []string    `json:"ports" yaml:"ports"`
	Aliases      []string    `json:"aliases,omitempty" yaml:"aliases,omitempty"`
	Tags         []string    `json:"tags,omitempty" yaml:"tags,omitempty"`
	Favorite     bool        `json:"favorite" yaml:"favorite"`
	LastScanned  time.Time   `json:"lastScanned" yaml:"lastScanned"`
	LastAction   string      `json:"lastAction,omitempty" yaml:"lastAction,omitempty"`
	LastExitCode int         `json:"lastExitCode,omitempty" yaml:"lastExitCode,omitempty"`
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
