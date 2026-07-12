package server

import (
	"errors"
	"net/http"
	"strings"
	"time"

	"dockertree/internal/config"
	"dockertree/internal/core"
	"dockertree/internal/docker"
	"gopkg.in/yaml.v3"
)

type backupBundle struct {
	Version    int            `yaml:"version"`
	ExportedAt time.Time      `yaml:"exportedAt"`
	Config     config.Config  `yaml:"config"`
	Inventory  []core.Project `yaml:"inventory"`
}

func (s *Server) cleanupPreview(w http.ResponseWriter, r *http.Request) {
	preview, err := s.exec.CleanupPreview(r.Context())
	respond(w, preview, err)
}

func (s *Server) cleanup(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Items []core.CleanupCandidate `json:"items"`
	}
	if !decodeJSON(w, r, &req) {
		return
	}
	if len(req.Items) == 0 {
		badRequest(w, errText("at least one cleanup item is required"))
		return
	}
	preview, err := s.exec.CleanupPreview(r.Context())
	if err != nil {
		respond(w, nil, err)
		return
	}
	allowed := make(map[string]core.CleanupCandidate)
	for _, item := range append(append(preview.Containers, preview.Images...), preview.Networks...) {
		allowed[item.Type+"\x00"+item.ID] = item
	}
	selected := make([]core.CleanupCandidate, 0, len(req.Items))
	seen := make(map[string]struct{}, len(req.Items))
	for _, item := range req.Items {
		key := item.Type + "\x00" + strings.TrimSpace(item.ID)
		candidate, ok := allowed[key]
		if !ok {
			badRequest(w, errText("cleanup selection is stale or invalid"))
			return
		}
		if _, duplicate := seen[key]; duplicate {
			continue
		}
		seen[key] = struct{}{}
		selected = append(selected, candidate)
	}
	results := make([]docker.Result, 0, len(selected))
	refresh := false
	for _, item := range selected {
		cmd, err := docker.CleanupCommand(item)
		if err != nil {
			badRequest(w, err)
			return
		}
		result, execErr := s.executeRecorded(r.Context(), cmd, item.Type, item.ID, item.Name, "cleanup")
		results = append(results, result)
		if execErr != nil {
			respond(w, results, execErr)
			return
		}
		refresh = refresh || item.Type == "container"
	}
	if refresh {
		if err := s.refreshInventory(r.Context()); err != nil {
			respond(w, results, err)
			return
		}
	}
	respond(w, results, nil)
}

func (s *Server) exportConfig(w http.ResponseWriter, _ *http.Request) {
	cfg := s.currentConfig()
	cfg.AdminToken = ""
	cfg.Dir = ""
	projects, err := s.store.LoadInventory()
	if err != nil {
		respond(w, nil, err)
		return
	}
	data, err := yaml.Marshal(backupBundle{Version: 1, ExportedAt: time.Now().UTC(), Config: cfg, Inventory: projects})
	if err != nil {
		respond(w, nil, err)
		return
	}
	w.Header().Set("Content-Type", "application/yaml; charset=utf-8")
	w.Header().Set("Content-Disposition", `attachment; filename="dockertree-backup.yaml"`)
	w.Header().Set("Cache-Control", "no-store")
	_, _ = w.Write(data)
}

func (s *Server) restoreConfig(w http.ResponseWriter, r *http.Request) {
	r.Body = http.MaxBytesReader(w, r.Body, 5<<20)
	var bundle backupBundle
	if err := yaml.NewDecoder(r.Body).Decode(&bundle); err != nil {
		badRequest(w, err)
		return
	}
	if bundle.Version != 1 {
		badRequest(w, errors.New("unsupported backup version"))
		return
	}
	unlock := s.lockConfigUpdates()
	defer unlock()
	current := s.currentConfig()
	restored := bundle.Config
	restored.AdminToken = current.AdminToken
	restored.ListenAddr = current.ListenAddr
	restored.AllowLAN = current.AllowLAN
	restored.Dir = current.Dir
	if strings.TrimSpace(restored.Automation.WebhookType) == "" {
		restored.Automation.WebhookType = "generic"
	}
	projectRoot, scanPaths, err := config.NormalizeProjectPaths(restored.ProjectRoot, restored.ScanPaths)
	if err != nil {
		badRequest(w, err)
		return
	}
	restored.ProjectRoot = projectRoot
	restored.ScanPaths = scanPaths
	if err := config.Validate(restored); err != nil {
		badRequest(w, err)
		return
	}
	if err := s.store.SaveInventory(bundle.Inventory); err != nil {
		respond(w, nil, err)
		return
	}
	if restored.Dir != "" {
		if err := config.Save(restored); err != nil {
			respond(w, nil, err)
			return
		}
	}
	s.setConfig(restored)
	s.updateScannerPaths(restored)
	respond(w, map[string]any{"restored": true, "restartRecommended": true}, nil)
}
