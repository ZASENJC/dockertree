package config

import (
	"os"
	"path/filepath"
	"testing"
)

func TestLoadCreatesMigratableDefaults(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("DOCKERTREE_CONFIG_DIR", dir)

	cfg, err := Load()
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}

	if cfg.ListenAddr != "127.0.0.1:27680" {
		t.Fatalf("ListenAddr = %q", cfg.ListenAddr)
	}
	if cfg.AdminToken == "" {
		t.Fatal("AdminToken should be generated")
	}
	if cfg.Update.RemoveOrphans {
		t.Fatal("RemoveOrphans should default to false")
	}
	if cfg.Automation.UpdateCheckIntervalMinutes != 0 || cfg.Automation.WebhookType != "generic" || !cfg.Automation.NotifyOnUpdates {
		t.Fatalf("unexpected automation defaults: %#v", cfg.Automation)
	}
	if _, err := os.Stat(filepath.Join(dir, "config.yaml")); err != nil {
		t.Fatalf("config.yaml was not created: %v", err)
	}
}

func TestValidateAutomationRejectsUnsafeSettings(t *testing.T) {
	for _, automation := range []AutomationConfig{
		{UpdateCheckIntervalMinutes: 5, WebhookType: "generic"},
		{UpdateCheckIntervalMinutes: 60, WebhookType: "unknown"},
		{UpdateCheckIntervalMinutes: 60, WebhookType: "generic", WebhookURL: "file:///tmp/hook"},
	} {
		if err := ValidateAutomation(automation); err == nil {
			t.Fatalf("expected validation error for %#v", automation)
		}
	}
}

func TestLoadRejectsNonLocalhostWithoutExplicitOptIn(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("DOCKERTREE_CONFIG_DIR", dir)
	if err := os.WriteFile(filepath.Join(dir, "config.yaml"), []byte("listenAddr: 0.0.0.0:27680\nadminToken: token\nallowLan: false\n"), 0o600); err != nil {
		t.Fatal(err)
	}

	_, err := Load()
	if err == nil {
		t.Fatal("Load() should reject LAN binding unless allowLan is true")
	}
}
