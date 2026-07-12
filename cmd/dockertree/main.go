package main

import (
	"context"
	"fmt"
	"io"
	"log"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"strconv"

	"dockertree/internal/config"
	dockerd "dockertree/internal/docker"
	"dockertree/internal/server"
	"dockertree/internal/store"
)

func main() {
	if handled, err := runConfigCommand(os.Args[1:], os.Stdout); handled {
		if err != nil {
			log.Fatal(err)
		}
		return
	}
	if len(os.Args) > 1 {
		log.Fatalf("unknown command %q", os.Args[1])
	}

	cfg, err := config.Load()
	if err != nil {
		log.Fatal(err)
	}
	st := store.New(filepath.Join(cfg.Dir, "inventory.yaml"))
	operations := store.NewOperationStore(filepath.Join(cfg.Dir, "logs", "operations.jsonl"))
	templates := store.NewTemplateStore(filepath.Join(cfg.Dir, "templates.yaml"))
	scanner := dockerd.NewScanner(dockerd.CLIRunner{}, config.EffectiveScanPaths(cfg))
	srv := server.New(cfg, st, scanner, dockerd.CLIExecutor{}).WithOperationLog(operations).WithTemplateStore(templates)
	srv.StartAutomation(context.Background())
	handler := srv.Handler()

	fmt.Printf("Dockertree listening on http://%s\n", cfg.ListenAddr)
	fmt.Printf("Config dir: %s\n", cfg.Dir)
	fmt.Printf("Admin token: %s\n", cfg.AdminToken)
	log.Fatal(http.ListenAndServe(cfg.ListenAddr, handler))
}

func runConfigCommand(args []string, output io.Writer) (bool, error) {
	if len(args) == 0 || args[0] != "config" {
		return false, nil
	}
	if len(args) < 2 {
		return true, fmt.Errorf("config command requires init, port, set-port, or check-port")
	}

	switch args[1] {
	case "init":
		if len(args) != 2 {
			return true, fmt.Errorf("config init does not accept arguments")
		}
		_, err := config.Load()
		return true, err
	case "port":
		if len(args) != 2 {
			return true, fmt.Errorf("config port does not accept arguments")
		}
		cfg, err := config.Load()
		if err != nil {
			return true, err
		}
		_, port, err := net.SplitHostPort(cfg.ListenAddr)
		if err != nil {
			return true, err
		}
		_, err = fmt.Fprintln(output, port)
		return true, err
	case "set-port":
		if len(args) != 3 {
			return true, fmt.Errorf("config set-port requires one port")
		}
		port, err := strconv.Atoi(args[2])
		if err != nil || port < 1 || port > 65535 {
			return true, fmt.Errorf("port must be an integer from 1 to 65535")
		}
		cfg, err := config.Load()
		if err != nil {
			return true, err
		}
		host, _, err := net.SplitHostPort(cfg.ListenAddr)
		if err != nil {
			return true, err
		}
		cfg.ListenAddr = net.JoinHostPort(host, strconv.Itoa(port))
		return true, config.Save(cfg)
	case "check-port":
		if len(args) != 2 {
			return true, fmt.Errorf("config check-port does not accept arguments")
		}
		cfg, err := config.Load()
		if err != nil {
			return true, err
		}
		listener, err := net.Listen("tcp", cfg.ListenAddr)
		if err != nil {
			return true, fmt.Errorf("listen address %s is unavailable: %w", cfg.ListenAddr, err)
		}
		return true, listener.Close()
	default:
		return true, fmt.Errorf("unknown config command %q", args[1])
	}
}
