package server

import (
	"errors"
	"fmt"
	"net/http"
	"strings"
	"time"
	"unicode/utf8"

	"dockertree/internal/core"
	"dockertree/internal/docker"
)

func (s *Server) listTemplates(w http.ResponseWriter, _ *http.Request) {
	if s.templates == nil {
		respond(w, []core.DeployTemplate{}, nil)
		return
	}
	templates, err := s.templates.LoadTemplates()
	respond(w, templates, err)
}

func (s *Server) saveTemplate(w http.ResponseWriter, r *http.Request) {
	if s.templates == nil {
		respond(w, nil, errors.New("template store is not configured"))
		return
	}
	s.templateMu.Lock()
	defer s.templateMu.Unlock()
	var template core.DeployTemplate
	if !decodeJSON(w, r, &template) {
		return
	}
	template.Name = strings.TrimSpace(template.Name)
	if template.Name == "" || utf8.RuneCountInString(template.Name) > 64 {
		badRequest(w, errText("template name is required and must not exceed 64 characters"))
		return
	}
	switch template.Mode {
	case "container":
		if template.Container == nil {
			badRequest(w, errText("container template payload is required"))
			return
		}
		if _, err := docker.ValidatedContainerDeployCommand(*template.Container); err != nil {
			badRequest(w, err)
			return
		}
		template.Compose = nil
	case "compose":
		if template.Compose == nil {
			badRequest(w, errText("compose template payload is required"))
			return
		}
		if _, err := docker.ComposeDeployPlan(*template.Compose); err != nil {
			badRequest(w, err)
			return
		}
		template.Container = nil
	default:
		badRequest(w, errText("template mode must be container or compose"))
		return
	}
	if strings.TrimSpace(template.ID) == "" {
		template.ID = fmt.Sprintf("template-%d", time.Now().UnixNano())
	}
	templates, err := s.templates.LoadTemplates()
	if err != nil {
		respond(w, nil, err)
		return
	}
	replaced := false
	for i := range templates {
		if templates[i].ID == template.ID {
			templates[i] = template
			replaced = true
			break
		}
	}
	if !replaced {
		templates = append(templates, template)
	}
	if err := s.templates.SaveTemplates(templates); err != nil {
		respond(w, nil, err)
		return
	}
	respond(w, template, nil)
}

func (s *Server) deleteTemplate(w http.ResponseWriter, r *http.Request) {
	if s.templates == nil {
		respond(w, nil, errors.New("template store is not configured"))
		return
	}
	s.templateMu.Lock()
	defer s.templateMu.Unlock()
	id := strings.TrimSpace(r.PathValue("id"))
	templates, err := s.templates.LoadTemplates()
	if err != nil {
		respond(w, nil, err)
		return
	}
	kept := templates[:0]
	found := false
	for _, template := range templates {
		if template.ID == id {
			found = true
			continue
		}
		kept = append(kept, template)
	}
	if !found {
		http.NotFound(w, r)
		return
	}
	if err := s.templates.SaveTemplates(kept); err != nil {
		respond(w, nil, err)
		return
	}
	respond(w, map[string]bool{"deleted": true}, nil)
}
