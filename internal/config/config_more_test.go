package config

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestLoadAllowsLANWhenExplicit(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("DOCKERTREE_CONFIG_DIR", dir)
	data := []byte("listenAddr: 0.0.0.0:27680\nadminToken: token\nallowLan: true\n")
	if err := os.WriteFile(filepath.Join(dir, "config.yaml"), data, 0o600); err != nil {
		t.Fatal(err)
	}

	cfg, err := Load()
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}
	if !cfg.AllowLAN || cfg.ListenAddr != "0.0.0.0:27680" {
		t.Fatalf("unexpected config: %#v", cfg)
	}
}

func TestSaveUsesConfiguredDirectory(t *testing.T) {
	dir := t.TempDir()
	cfg := Config{Dir: dir, ListenAddr: "127.0.0.1:27680", AdminToken: "token", UI: UIConfig{Theme: "minimal-square"}}
	if err := Save(cfg); err != nil {
		t.Fatalf("Save() error = %v", err)
	}
	if _, err := os.Stat(filepath.Join(dir, "config.yaml")); err != nil {
		t.Fatalf("expected config file: %v", err)
	}
}

func TestConfigDirDefaultsUnderUserConfigDir(t *testing.T) {
	t.Setenv("DOCKERTREE_CONFIG_DIR", "")

	dir, err := ConfigDir()
	if err != nil {
		t.Fatalf("ConfigDir() error = %v", err)
	}
	if !strings.HasSuffix(dir, filepath.Join(".config", "dockertree")) {
		t.Fatalf("ConfigDir() = %q", dir)
	}
}

func TestSaveUsesEnvDirectoryWhenDirIsEmpty(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("DOCKERTREE_CONFIG_DIR", dir)

	cfg := Config{ListenAddr: "127.0.0.1:27680", AdminToken: "token"}
	if err := Save(cfg); err != nil {
		t.Fatalf("Save() error = %v", err)
	}
	if _, err := os.Stat(filepath.Join(dir, "config.yaml")); err != nil {
		t.Fatalf("expected config file in env dir: %v", err)
	}
}

func TestSaveReturnsDirectoryErrors(t *testing.T) {
	dir := t.TempDir()
	filePath := filepath.Join(dir, "config-file")
	if err := os.WriteFile(filePath, []byte("not a directory"), 0o600); err != nil {
		t.Fatal(err)
	}

	err := Save(Config{Dir: filePath, ListenAddr: "127.0.0.1:27680", AdminToken: "token"})
	if err == nil {
		t.Fatal("expected Save() to fail when config dir path is a file")
	}
}

func TestLoadRejectsInvalidConfigFiles(t *testing.T) {
	t.Run("invalid yaml", func(t *testing.T) {
		dir := t.TempDir()
		t.Setenv("DOCKERTREE_CONFIG_DIR", dir)
		if err := os.WriteFile(filepath.Join(dir, "config.yaml"), []byte("listenAddr: ["), 0o600); err != nil {
			t.Fatal(err)
		}
		if _, err := Load(); err == nil {
			t.Fatal("expected invalid YAML error")
		}
	})

	t.Run("invalid listen address", func(t *testing.T) {
		dir := t.TempDir()
		t.Setenv("DOCKERTREE_CONFIG_DIR", dir)
		if err := os.WriteFile(filepath.Join(dir, "config.yaml"), []byte("listenAddr: nope\nadminToken: token\n"), 0o600); err != nil {
			t.Fatal(err)
		}
		if _, err := Load(); err == nil {
			t.Fatal("expected invalid listenAddr error")
		}
	})
}
