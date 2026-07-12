package main

import (
	"context"
	"fmt"
	"log"
	"net/http"
	"path/filepath"

	"dockertree/internal/config"
	dockerd "dockertree/internal/docker"
	"dockertree/internal/server"
	"dockertree/internal/store"
)

func main() {
	cfg, err := config.Load()
	if err != nil {
		log.Fatal(err)
	}
	st := store.New(filepath.Join(cfg.Dir, "inventory.yaml"))
	operations := store.NewOperationStore(filepath.Join(cfg.Dir, "logs", "operations.jsonl"))
	templates := store.NewTemplateStore(filepath.Join(cfg.Dir, "templates.yaml"))
	scanner := dockerd.NewScanner(dockerd.CLIRunner{}, cfg.ScanPaths)
	srv := server.New(cfg, st, scanner, dockerd.CLIExecutor{}).WithOperationLog(operations).WithTemplateStore(templates)
	srv.StartAutomation(context.Background())
	handler := srv.Handler()

	fmt.Printf("Dockertree listening on http://%s\n", cfg.ListenAddr)
	fmt.Printf("Config dir: %s\n", cfg.Dir)
	fmt.Printf("Admin token: %s\n", cfg.AdminToken)
	log.Fatal(http.ListenAndServe(cfg.ListenAddr, handler))
}
