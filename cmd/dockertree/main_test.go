package main

import (
	"bytes"
	"net"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"dockertree/internal/config"
)

func TestRunConfigCommandInitializesAndPrintsDefaultPort(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("DOCKERTREE_CONFIG_DIR", dir)

	handled, err := runConfigCommand([]string{"config", "init"}, &bytes.Buffer{})
	if err != nil || !handled {
		t.Fatalf("config init handled=%v err=%v", handled, err)
	}
	if _, err := os.Stat(filepath.Join(dir, "config.yaml")); err != nil {
		t.Fatalf("config init did not create config.yaml: %v", err)
	}

	var output bytes.Buffer
	handled, err = runConfigCommand([]string{"config", "port"}, &output)
	if err != nil || !handled || strings.TrimSpace(output.String()) != "27680" {
		t.Fatalf("config port handled=%v err=%v output=%q", handled, err, output.String())
	}
}

func TestRunConfigCommandSetsAndPersistsPort(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("DOCKERTREE_CONFIG_DIR", dir)

	handled, err := runConfigCommand([]string{"config", "set-port", "28680"}, &bytes.Buffer{})
	if err != nil || !handled {
		t.Fatalf("config set-port handled=%v err=%v", handled, err)
	}
	cfg, err := config.Load()
	if err != nil {
		t.Fatal(err)
	}
	if cfg.ListenAddr != "0.0.0.0:28680" || !cfg.AllowLAN {
		t.Fatalf("unexpected saved config: %#v", cfg)
	}
}

func TestRunConfigCommandRejectsInvalidPort(t *testing.T) {
	t.Setenv("DOCKERTREE_CONFIG_DIR", t.TempDir())
	for _, port := range []string{"", "nope", "0", "65536"} {
		if handled, err := runConfigCommand([]string{"config", "set-port", port}, &bytes.Buffer{}); !handled || err == nil {
			t.Fatalf("set-port %q handled=%v err=%v", port, handled, err)
		}
	}
}

func TestRunConfigCommandDetectsOccupiedPort(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("DOCKERTREE_CONFIG_DIR", dir)
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer listener.Close()

	cfg := config.Config{
		Dir:        dir,
		ListenAddr: listener.Addr().String(),
		AdminToken: "token",
	}
	if err := config.Save(cfg); err != nil {
		t.Fatal(err)
	}
	if handled, err := runConfigCommand([]string{"config", "check-port"}, &bytes.Buffer{}); !handled || err == nil {
		t.Fatalf("config check-port handled=%v err=%v", handled, err)
	}
}

