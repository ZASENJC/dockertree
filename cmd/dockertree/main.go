package main

import (
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
	scanner := dockerd.NewScanner(dockerd.CLIRunner{}, cfg.ScanPaths)
	handler := server.New(cfg, st, scanner, dockerd.CLIExecutor{}).Handler()

	fmt.Printf("Dockertree listening on http://%s\n", cfg.ListenAddr)
	fmt.Printf("Config dir: %s\n", cfg.Dir)
	fmt.Printf("Admin token: %s\n", cfg.AdminToken)
	log.Fatal(http.ListenAndServe(cfg.ListenAddr, handler))
}
