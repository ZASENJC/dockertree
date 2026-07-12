package server

import (
	"net/http"

	"dockertree/internal/config"
)

type scanPathUpdater interface {
	SetScanPaths([]string)
}

type projectSettingsResponse struct {
	ProjectRoot string   `json:"projectRoot"`
	ScanPaths   []string `json:"scanPaths"`
}

func (s *Server) projectSettings(w http.ResponseWriter, _ *http.Request) {
	cfg := s.currentConfig()
	projectRoot, scanPaths, err := config.NormalizeProjectPaths(cfg.ProjectRoot, cfg.ScanPaths)
	if err != nil {
		respond(w, nil, err)
		return
	}
	respond(w, projectSettingsResponse{ProjectRoot: projectRoot, ScanPaths: scanPaths}, nil)
}

func (s *Server) updateProjectSettings(w http.ResponseWriter, r *http.Request) {
	var req struct {
		ProjectRoot *string   `json:"projectRoot"`
		ScanPaths   *[]string `json:"scanPaths"`
	}
	if !decodeJSON(w, r, &req) {
		return
	}
	unlock := s.lockConfigUpdates()
	defer unlock()
	cfg := s.currentConfig()
	projectRoot := cfg.ProjectRoot
	scanPaths := cfg.ScanPaths
	if req.ProjectRoot != nil {
		projectRoot = *req.ProjectRoot
	}
	if req.ScanPaths != nil {
		scanPaths = *req.ScanPaths
	}
	projectRoot, scanPaths, err := config.NormalizeProjectPaths(projectRoot, scanPaths)
	if err != nil {
		badRequest(w, err)
		return
	}
	cfg.ProjectRoot = projectRoot
	cfg.ScanPaths = scanPaths
	if err := config.Validate(cfg); err != nil {
		badRequest(w, err)
		return
	}
	if err := config.Save(cfg); err != nil {
		respond(w, nil, err)
		return
	}
	s.setConfig(cfg)
	s.updateScannerPaths(cfg)
	respond(w, projectSettingsResponse{ProjectRoot: projectRoot, ScanPaths: scanPaths}, nil)
}

func (s *Server) updateScannerPaths(cfg config.Config) {
	if scanner, ok := s.scanner.(scanPathUpdater); ok {
		scanner.SetScanPaths(config.EffectiveScanPaths(cfg))
	}
}
