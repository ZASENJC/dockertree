package store

import (
	"path/filepath"
	"testing"

	"dockertree/internal/core"
)

func TestInventoryRoundTripPreservesProjectMetadata(t *testing.T) {
	s := New(filepath.Join(t.TempDir(), "inventory.yaml"))
	projects := []core.Project{{ID: "compose:mtp", Name: "mtp", Type: core.ProjectTypeCompose, WorkingDir: "/srv/mtp", ConfigFiles: []string{"/srv/mtp/docker-compose.yml"}, Favorite: true, Tags: []string{"photos"}}}

	if err := s.SaveInventory(projects); err != nil {
		t.Fatalf("SaveInventory() error = %v", err)
	}
	got, err := s.LoadInventory()
	if err != nil {
		t.Fatalf("LoadInventory() error = %v", err)
	}

	if len(got) != 1 || got[0].Name != "mtp" || !got[0].Favorite || got[0].Tags[0] != "photos" {
		t.Fatalf("round trip lost data: %#v", got)
	}
}
