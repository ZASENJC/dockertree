package store

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"dockertree/internal/core"
)

func TestLoadInventoryMissingFileReturnsEmptyList(t *testing.T) {
	s := New(filepath.Join(t.TempDir(), "missing", "inventory.yaml"))
	projects, err := s.LoadInventory()
	if err != nil {
		t.Fatalf("LoadInventory() error = %v", err)
	}
	if len(projects) != 0 {
		t.Fatalf("expected empty inventory, got %#v", projects)
	}
}

func TestLoadInventoryRejectsInvalidYAML(t *testing.T) {
	path := filepath.Join(t.TempDir(), "inventory.yaml")
	if err := os.WriteFile(path, []byte("id: ["), 0o600); err != nil {
		t.Fatal(err)
	}

	_, err := New(path).LoadInventory()
	if err == nil {
		t.Fatal("expected invalid YAML error")
	}
}

func TestSaveInventoryReturnsDirectoryErrors(t *testing.T) {
	dir := t.TempDir()
	filePath := filepath.Join(dir, "not-a-directory")
	if err := os.WriteFile(filePath, []byte("x"), 0o600); err != nil {
		t.Fatal(err)
	}
	s := New(filepath.Join(filePath, "inventory.yaml"))

	err := s.SaveInventory([]core.Project{{ID: "compose:mtp", Name: "mtp"}})
	if err == nil || !strings.Contains(err.Error(), "not a directory") {
		t.Fatalf("expected directory error, got %v", err)
	}
}
