package store

import (
	"path/filepath"
	"testing"
	"time"

	"dockertree/internal/core"
)

func TestOperationStoreAppendsAndFiltersNewestFirst(t *testing.T) {
	path := filepath.Join(t.TempDir(), "logs", "operations.jsonl")
	store := NewOperationStore(path)
	records := []core.OperationRecord{
		{ID: "one", Timestamp: time.Unix(1, 0), TargetID: "compose:app", Action: "start", Success: true},
		{ID: "two", Timestamp: time.Unix(2, 0), TargetID: "compose:app", Action: "deploy", Success: false, Error: "boom"},
		{ID: "three", Timestamp: time.Unix(3, 0), TargetID: "container:solo", Action: "stop", Success: false, Error: "nope"},
	}
	for _, record := range records {
		if err := store.Append(record); err != nil {
			t.Fatalf("Append() error = %v", err)
		}
	}

	got, err := store.List(10, "compose:app", true)
	if err != nil {
		t.Fatalf("List() error = %v", err)
	}
	if len(got) != 1 || got[0].ID != "two" {
		t.Fatalf("filtered records = %#v", got)
	}

	got, err = store.List(2, "", false)
	if err != nil || len(got) != 2 || got[0].ID != "three" || got[1].ID != "two" {
		t.Fatalf("newest records = %#v err=%v", got, err)
	}
}

func TestTemplateStoreRoundTripAndMissingFile(t *testing.T) {
	store := NewTemplateStore(filepath.Join(t.TempDir(), "templates.yaml"))
	got, err := store.LoadTemplates()
	if err != nil || len(got) != 0 {
		t.Fatalf("missing templates = %#v err=%v", got, err)
	}
	templates := []core.DeployTemplate{{ID: "redis", Name: "Redis", Mode: "container", Container: &core.ContainerDeploySpec{Image: "redis:7", Ports: []string{"6379:6379"}}}}
	if err := store.SaveTemplates(templates); err != nil {
		t.Fatalf("SaveTemplates() error = %v", err)
	}
	got, err = store.LoadTemplates()
	if err != nil || len(got) != 1 || got[0].Container.Image != "redis:7" {
		t.Fatalf("templates = %#v err=%v", got, err)
	}
}
