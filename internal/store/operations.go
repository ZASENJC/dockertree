package store

import (
	"bufio"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"sync"

	"dockertree/internal/core"
	"gopkg.in/yaml.v3"
)

type OperationStore struct {
	Path string
	mu   sync.Mutex
}

func NewOperationStore(path string) *OperationStore {
	return &OperationStore{Path: path}
}

func (s *OperationStore) Append(record core.OperationRecord) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if err := os.MkdirAll(filepath.Dir(s.Path), 0o700); err != nil {
		return err
	}
	file, err := os.OpenFile(s.Path, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o600)
	if err != nil {
		return err
	}
	defer file.Close()
	return json.NewEncoder(file).Encode(record)
}

func (s *OperationStore) List(limit int, targetID string, failedOnly bool) ([]core.OperationRecord, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	file, err := os.Open(s.Path)
	if errors.Is(err, os.ErrNotExist) {
		return []core.OperationRecord{}, nil
	}
	if err != nil {
		return nil, err
	}
	defer file.Close()
	if limit <= 0 || limit > 500 {
		limit = 100
	}
	records := make([]core.OperationRecord, 0)
	scanner := bufio.NewScanner(file)
	scanner.Buffer(make([]byte, 64*1024), 1024*1024)
	for scanner.Scan() {
		var record core.OperationRecord
		if err := json.Unmarshal(scanner.Bytes(), &record); err != nil {
			return nil, err
		}
		if targetID != "" && record.TargetID != targetID {
			continue
		}
		if failedOnly && record.Success {
			continue
		}
		records = append(records, record)
	}
	if err := scanner.Err(); err != nil {
		return nil, err
	}
	for left, right := 0, len(records)-1; left < right; left, right = left+1, right-1 {
		records[left], records[right] = records[right], records[left]
	}
	if len(records) > limit {
		records = records[:limit]
	}
	return records, nil
}

type TemplateStore struct {
	Path string
	mu   sync.Mutex
}

func NewTemplateStore(path string) *TemplateStore {
	return &TemplateStore{Path: path}
}

func (s *TemplateStore) LoadTemplates() ([]core.DeployTemplate, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	data, err := os.ReadFile(s.Path)
	if errors.Is(err, os.ErrNotExist) {
		return []core.DeployTemplate{}, nil
	}
	if err != nil {
		return nil, err
	}
	var templates []core.DeployTemplate
	if err := yaml.Unmarshal(data, &templates); err != nil {
		return nil, err
	}
	return templates, nil
}

func (s *TemplateStore) SaveTemplates(templates []core.DeployTemplate) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if err := os.MkdirAll(filepath.Dir(s.Path), 0o700); err != nil {
		return err
	}
	data, err := yaml.Marshal(templates)
	if err != nil {
		return err
	}
	return os.WriteFile(s.Path, data, 0o600)
}
