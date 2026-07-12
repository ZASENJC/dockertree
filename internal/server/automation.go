package server

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"time"

	"dockertree/internal/config"
	"dockertree/internal/core"
)

type Notification struct {
	WebhookURL  string
	WebhookType string
	Title       string
	Message     string
}

type Notifier interface {
	Notify(context.Context, Notification) error
}

type HTTPNotifier struct {
	Client *http.Client
}

func (n HTTPNotifier) Notify(ctx context.Context, notification Notification) error {
	if strings.TrimSpace(notification.WebhookURL) == "" {
		return errors.New("webhook URL is required")
	}
	payload := map[string]any{
		"event":     "dockertree",
		"title":     notification.Title,
		"message":   notification.Message,
		"timestamp": time.Now().UTC(),
	}
	if notification.WebhookType == "ntfy" {
		payload = map[string]any{"title": notification.Title, "message": notification.Message, "tags": []string{"package"}}
	}
	body, err := json.Marshal(payload)
	if err != nil {
		return err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, notification.WebhookURL, bytes.NewReader(body))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")
	client := n.Client
	if client == nil {
		client = &http.Client{Timeout: 10 * time.Second}
	}
	res, err := client.Do(req)
	if err != nil {
		return err
	}
	defer res.Body.Close()
	if res.StatusCode < 200 || res.StatusCode >= 300 {
		return fmt.Errorf("webhook returned %s", res.Status)
	}
	return nil
}

func (s *Server) automationSettings(w http.ResponseWriter, _ *http.Request) {
	respond(w, s.currentConfig().Automation, nil)
}

func (s *Server) updateAutomationSettings(w http.ResponseWriter, r *http.Request) {
	var automation config.AutomationConfig
	if !decodeJSON(w, r, &automation) {
		return
	}
	if strings.TrimSpace(automation.WebhookType) == "" {
		automation.WebhookType = "generic"
	}
	if err := config.ValidateAutomation(automation); err != nil {
		badRequest(w, err)
		return
	}
	unlock := s.lockConfigUpdates()
	defer unlock()
	cfg := s.currentConfig()
	cfg.Automation = automation
	if cfg.Dir != "" {
		if err := config.Save(cfg); err != nil {
			respond(w, nil, err)
			return
		}
	}
	s.setConfig(cfg)
	respond(w, automation, nil)
}

func (s *Server) testNotification(w http.ResponseWriter, r *http.Request) {
	cfg := s.currentConfig()
	if strings.TrimSpace(cfg.Automation.WebhookURL) == "" {
		badRequest(w, errText("webhook URL is not configured"))
		return
	}
	err := s.notifier.Notify(r.Context(), Notification{
		WebhookURL: cfg.Automation.WebhookURL, WebhookType: cfg.Automation.WebhookType,
		Title: "Dockertree 测试通知", Message: "Webhook/ntfy 通知配置可用。",
	})
	respond(w, map[string]bool{"sent": err == nil}, err)
}

func (s *Server) checkProjectUpdate(w http.ResponseWriter, r *http.Request) {
	project, ok, err := s.findProject(r.PathValue("id"))
	if err != nil {
		respond(w, nil, err)
		return
	}
	if !ok {
		http.NotFound(w, r)
		return
	}
	check, err := s.checkUpdate(r.Context(), project)
	s.recordUpdateCheck(check, err)
	respond(w, check, err)
}

func (s *Server) checkAllUpdates(w http.ResponseWriter, r *http.Request) {
	checks, err := s.runUpdateChecks(r.Context(), false)
	respond(w, checks, err)
}

func (s *Server) runUpdateChecks(ctx context.Context, notify bool) ([]core.UpdateCheck, error) {
	projects, err := s.store.LoadInventory()
	if err != nil {
		return nil, err
	}
	checks := make([]core.UpdateCheck, 0)
	available := make([]string, 0)
	for _, project := range projects {
		if project.Type != core.ProjectTypeCompose || len(project.ConfigFiles) == 0 {
			continue
		}
		timeout := s.updateCheckTimeout
		if timeout <= 0 {
			timeout = defaultUpdateCheckTimeout
		}
		checkCtx, cancel := context.WithTimeout(ctx, timeout)
		check, checkErr := s.checkUpdate(checkCtx, project)
		cancel()
		s.recordUpdateCheck(check, checkErr)
		checks = append(checks, check)
		if check.Status == "available" {
			available = append(available, project.Name)
		}
	}
	if notify && len(available) > 0 {
		cfg := s.currentConfig()
		if cfg.Automation.NotifyOnUpdates && strings.TrimSpace(cfg.Automation.WebhookURL) != "" {
			err = s.notifier.Notify(ctx, Notification{
				WebhookURL: cfg.Automation.WebhookURL, WebhookType: cfg.Automation.WebhookType,
				Title: "Dockertree 发现可用更新", Message: strings.Join(available, "、"),
			})
		}
	}
	return checks, err
}

func (s *Server) recordUpdateCheck(check core.UpdateCheck, err error) {
	s.recordOperation(core.OperationRecord{
		ID: fmt.Sprintf("%d", time.Now().UnixNano()), Timestamp: time.Now(), Action: "check-update",
		TargetType: "project", TargetID: check.ProjectID, TargetName: check.ProjectName,
		Command: check.Command, Output: truncateOperationText(check.Output), Success: err == nil, Error: check.Error,
	})
}

func (s *Server) StartAutomation(ctx context.Context) {
	s.automationMu.Lock()
	s.lastAutomationCheck = time.Now()
	s.automationMu.Unlock()
	go func() {
		ticker := time.NewTicker(time.Minute)
		defer ticker.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case now := <-ticker.C:
				s.runScheduledUpdateCheck(ctx, now)
			}
		}
	}()
}

func (s *Server) runScheduledUpdateCheck(ctx context.Context, now time.Time) {
	cfg := s.currentConfig()
	interval := time.Duration(cfg.Automation.UpdateCheckIntervalMinutes) * time.Minute
	if interval <= 0 {
		return
	}
	s.automationMu.Lock()
	if !s.lastAutomationCheck.IsZero() && now.Sub(s.lastAutomationCheck) < interval {
		s.automationMu.Unlock()
		return
	}
	s.lastAutomationCheck = now
	s.automationMu.Unlock()
	_, _ = s.runUpdateChecks(ctx, true)
}
