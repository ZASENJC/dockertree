package store

import (
	"errors"
	"os"
	"path/filepath"

	"dockertree/internal/core"
	"gopkg.in/yaml.v3"
)

type Store struct {
	InventoryPath string
}

func New(inventoryPath string) *Store {
	return &Store{InventoryPath: inventoryPath}
}

func (s *Store) LoadInventory() ([]core.Project, error) {
	data, err := os.ReadFile(s.InventoryPath)
	if errors.Is(err, os.ErrNotExist) {
		return []core.Project{}, nil
	}
	if err != nil {
		return nil, err
	}
	var projects []core.Project
	if err := yaml.Unmarshal(data, &projects); err != nil {
		return nil, err
	}
	return projects, nil
}

func (s *Store) SaveInventory(projects []core.Project) error {
	if err := os.MkdirAll(filepath.Dir(s.InventoryPath), 0o700); err != nil {
		return err
	}
	data, err := yaml.Marshal(projects)
	if err != nil {
		return err
	}
	return os.WriteFile(s.InventoryPath, data, 0o600)
}
