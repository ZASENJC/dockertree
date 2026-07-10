package config

import (
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"net"
	"os"
	"path/filepath"
	"strings"

	"gopkg.in/yaml.v3"
)

type Config struct {
	ListenAddr string       `json:"listenAddr" yaml:"listenAddr"`
	AdminToken string       `json:"adminToken" yaml:"adminToken"`
	AllowLAN   bool         `json:"allowLan" yaml:"allowLan"`
	ScanPaths  []string     `json:"scanPaths" yaml:"scanPaths"`
	Update     UpdateConfig `json:"update" yaml:"update"`
	UI         UIConfig     `json:"ui" yaml:"ui"`
	Dir        string       `json:"-" yaml:"-"`
}

type UpdateConfig struct {
	RemoveOrphans bool `json:"removeOrphans" yaml:"removeOrphans"`
}

type UIConfig struct {
	Theme string `json:"theme" yaml:"theme"`
}

func Load() (Config, error) {
	dir, err := ConfigDir()
	if err != nil {
		return Config{}, err
	}
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return Config{}, err
	}

	path := filepath.Join(dir, "config.yaml")
	if _, err := os.Stat(path); errors.Is(err, os.ErrNotExist) {
		cfg := defaultConfig(dir)
		if err := Save(cfg); err != nil {
			return Config{}, err
		}
		return cfg, nil
	}

	data, err := os.ReadFile(path)
	if err != nil {
		return Config{}, err
	}
	cfg := defaultConfig(dir)
	if err := yaml.Unmarshal(data, &cfg); err != nil {
		return Config{}, err
	}
	cfg.Dir = dir
	if cfg.AdminToken == "" {
		cfg.AdminToken = randomToken()
	}
	if err := validate(cfg); err != nil {
		return Config{}, err
	}
	return cfg, nil
}

func Save(cfg Config) error {
	if cfg.Dir == "" {
		dir, err := ConfigDir()
		if err != nil {
			return err
		}
		cfg.Dir = dir
	}
	if err := os.MkdirAll(cfg.Dir, 0o700); err != nil {
		return err
	}
	data, err := yaml.Marshal(cfg)
	if err != nil {
		return err
	}
	return os.WriteFile(filepath.Join(cfg.Dir, "config.yaml"), data, 0o600)
}

func ConfigDir() (string, error) {
	if dir := os.Getenv("DOCKERTREE_CONFIG_DIR"); dir != "" {
		return dir, nil
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(home, ".config", "dockertree"), nil
}

func defaultConfig(dir string) Config {
	return Config{
		ListenAddr: "127.0.0.1:27680",
		AdminToken: randomToken(),
		AllowLAN:   false,
		ScanPaths:  []string{},
		Update:     UpdateConfig{RemoveOrphans: false},
		UI:         UIConfig{Theme: "minimal-square"},
		Dir:        dir,
	}
}

func validate(cfg Config) error {
	host, _, err := net.SplitHostPort(cfg.ListenAddr)
	if err != nil {
		return fmt.Errorf("invalid listenAddr: %w", err)
	}
	if cfg.AllowLAN {
		return nil
	}
	if host == "localhost" || strings.HasPrefix(host, "127.") || host == "::1" || host == "[::1]" {
		return nil
	}
	return fmt.Errorf("listenAddr %q is not localhost; set allowLan: true to bind outside localhost", cfg.ListenAddr)
}

func randomToken() string {
	b := make([]byte, 24)
	if _, err := rand.Read(b); err != nil {
		return "change-me"
	}
	return hex.EncodeToString(b)
}
