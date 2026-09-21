// Package main implements localnet-health, an HTTP probe that reports
// container status for the Ethera Labs Console via GET /api/services.
// The docker socket is bind-mounted into this container so sibling
// containers can be inspected directly.
package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"sort"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"time"

	"github.com/containerd/errdefs"
	"github.com/docker/docker/api/types/container"
	"github.com/docker/docker/client"
	"github.com/ethera-labs/local-testnet/internal/logger"
)

const (
	defaultPort       = "8090"
	dockerCallTimeout = 5 * time.Second
	httpReadTimeout   = 10 * time.Second
	httpWriteTimeout  = 10 * time.Second
	shutdownTimeout   = 5 * time.Second
)

type serviceSpec struct {
	ID            string `json:"id"`
	Name          string `json:"name"`
	Kind          string `json:"kind"`
	HostPort      int    `json:"host_port,omitempty"`
	ContainerName string `json:"-"`
}

// Status values: "up" | "starting" | "down" | "missing".
// "missing" distinguishes a disabled optional feature from a container that
// is present but unhealthy.
type serviceStatus struct {
	serviceSpec
	Status string `json:"status"`
}

// catalogue is the closed list of containers the frontend renders. Order is
// preserved in the response so the UI layout stays stable across reloads.
var catalogue = []serviceSpec{
	{ID: "publisher", Name: "Publisher", Kind: "publisher", HostPort: 18081, ContainerName: "publisher"},

	{ID: "op-reth-a", Name: "op-reth A", Kind: "op-reth", HostPort: 18545, ContainerName: "op-reth-a"},
	{ID: "op-reth-b", Name: "op-reth B", Kind: "op-reth", HostPort: 28545, ContainerName: "op-reth-b"},

	{ID: "op-node-a", Name: "op-node A", Kind: "op-node", HostPort: 19545, ContainerName: "op-node-a"},
	{ID: "op-node-b", Name: "op-node B", Kind: "op-node", HostPort: 29545, ContainerName: "op-node-b"},

	{ID: "op-batcher-a", Name: "op-batcher A", Kind: "op-batcher", HostPort: 18548, ContainerName: "op-batcher-a"},
	{ID: "op-batcher-b", Name: "op-batcher B", Kind: "op-batcher", HostPort: 28548, ContainerName: "op-batcher-b"},

	{ID: "op-proposer-a", Name: "op-proposer A", Kind: "op-proposer", HostPort: 18560, ContainerName: "op-proposer-a"},
	{ID: "op-proposer-b", Name: "op-proposer B", Kind: "op-proposer", HostPort: 28560, ContainerName: "op-proposer-b"},

	{ID: "rollup-boost-a", Name: "rollup-boost A", Kind: "rollup-boost", HostPort: 17551, ContainerName: "rollup-boost-a"},
	{ID: "rollup-boost-b", Name: "rollup-boost B", Kind: "rollup-boost", HostPort: 27551, ContainerName: "rollup-boost-b"},

	{ID: "op-rbuilder-a", Name: "op-rbuilder A", Kind: "op-rbuilder", HostPort: 17545, ContainerName: "op-rbuilder-a"},
	{ID: "op-rbuilder-b", Name: "op-rbuilder B", Kind: "op-rbuilder", HostPort: 27545, ContainerName: "op-rbuilder-b"},

	{ID: "sidecar-a", Name: "Sidecar A", Kind: "sidecar", HostPort: 17090, ContainerName: "sidecar-a"},
	{ID: "sidecar-b", Name: "Sidecar B", Kind: "sidecar", HostPort: 27090, ContainerName: "sidecar-b"},

	{ID: "op-alt-da-a", Name: "AltDA Server A", Kind: "op-alt-da", HostPort: 3100, ContainerName: "op-alt-da-a"},
	{ID: "op-alt-da-b", Name: "AltDA Server B", Kind: "op-alt-da", HostPort: 3101, ContainerName: "op-alt-da-b"},

	{ID: "op-succinct-a", Name: "op-succinct A", Kind: "op-succinct", HostPort: 18082, ContainerName: "op-succinct-a"},
	{ID: "op-succinct-b", Name: "op-succinct B", Kind: "op-succinct", HostPort: 28082, ContainerName: "op-succinct-b"},
	{ID: "op-succinct-postgres", Name: "op-succinct Postgres", Kind: "op-succinct-postgres", ContainerName: "op-succinct-postgres"},

	{ID: "ethera-console", Name: "Ethera Console", Kind: "frontend", HostPort: 3000, ContainerName: "ethera-console"},
}

type prober struct {
	cli    *client.Client
	logger *slog.Logger
}

func newProber() (*prober, error) {
	cli, err := client.NewClientWithOpts(client.FromEnv, client.WithAPIVersionNegotiation())
	if err != nil {
		return nil, fmt.Errorf("docker client: %w", err)
	}
	return &prober{cli: cli, logger: logger.Named("prober")}, nil
}

func (p *prober) close() error {
	return p.cli.Close()
}

// probe inspects every catalogued container in parallel; a single failure
// is reported as "down" rather than aborting the whole snapshot.
func (p *prober) probe(ctx context.Context) []serviceStatus {
	out := make([]serviceStatus, len(catalogue))

	var wg sync.WaitGroup
	wg.Add(len(catalogue))
	for i, spec := range catalogue {
		go func(idx int, s serviceSpec) {
			defer wg.Done()

			callCtx, cancel := context.WithTimeout(ctx, dockerCallTimeout)
			defer cancel()

			status, image := p.resolveStatus(callCtx, s.ContainerName)
			// The op-besu overlay reuses the op-reth slot (same rollup-boost
			// L2_URL wiring) but renames the container to op-besu-{a,b} and runs
			// a besu image. If op-reth-{a,b} is absent, fall back to the op-besu
			// container name so the console still shows it as up.
			if s.Kind == "op-reth" && status == "missing" {
				if alt := opBesuContainerName(s.ContainerName); alt != "" {
					status, image = p.resolveStatus(callCtx, alt)
				}
			}
			// Relabel the EL slot when it is actually running op-besu, so the
			// console and /api/services report the true execution client.
			if s.Kind == "op-reth" && isBesuImage(image) {
				s.Kind = "op-besu"
				s.Name = "op-besu " + strings.TrimPrefix(s.Name, "op-reth ")
			}

			out[idx] = serviceStatus{serviceSpec: s, Status: status}
		}(i, spec)
	}
	wg.Wait()

	return out
}

// opBesuContainerName maps an op-reth EL container name to its op-besu overlay
// equivalent (op-reth-a -> op-besu-a). Returns "" for non-EL names.
func opBesuContainerName(opRethName string) string {
	if suffix, ok := strings.CutPrefix(opRethName, "op-reth-"); ok {
		return "op-besu-" + suffix
	}
	return ""
}

func isBesuImage(image string) bool {
	return strings.Contains(strings.ToLower(image), "besu")
}

// resolveStatus inspects a container and returns its status plus the image it
// runs (empty when the container is missing or inspection fails).
func (p *prober) resolveStatus(ctx context.Context, name string) (status, image string) {
	info, err := p.cli.ContainerInspect(ctx, name)
	if err != nil {
		if errdefs.IsNotFound(err) {
			return "missing", ""
		}
		p.logger.With("container", name, "err", err.Error()).Debug("container inspect failed")
		return "down", ""
	}
	image = ""
	if info.Config != nil {
		image = info.Config.Image
	}
	return statusFromState(info.State), image
}

func statusFromState(state *container.State) string {
	if state == nil {
		return "down"
	}
	if !state.Running {
		return "down"
	}
	// Mirror healthcheck verdict when defined: "running" alone overstates
	// readiness during the boot window.
	if state.Health != nil {
		switch state.Health.Status {
		case "healthy":
			return "up"
		case "starting":
			return "starting"
		case "unhealthy":
			return "starting"
		}
	}
	return "up"
}

type handler struct {
	prober *prober
}

func (h *handler) services(w http.ResponseWriter, r *http.Request) {
	enableCORS(w)
	if r.Method == http.MethodOptions {
		w.WriteHeader(http.StatusNoContent)
		return
	}
	if r.Method != http.MethodGet {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}

	statuses := h.prober.probe(r.Context())

	sort.SliceStable(statuses, func(i, j int) bool {
		return statuses[i].ID < statuses[j].ID
	})

	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Cache-Control", "no-store")
	if err := json.NewEncoder(w).Encode(struct {
		Services []serviceStatus `json:"services"`
	}{Services: statuses}); err != nil {
		slog.With("err", err.Error()).Warn("encode services response")
	}
}

func (h *handler) health(w http.ResponseWriter, r *http.Request) {
	enableCORS(w)
	if r.Method == http.MethodOptions {
		w.WriteHeader(http.StatusNoContent)
		return
	}
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write([]byte("ok"))
}

func enableCORS(w http.ResponseWriter) {
	w.Header().Set("Access-Control-Allow-Origin", "*")
	w.Header().Set("Access-Control-Allow-Methods", "GET, OPTIONS")
	w.Header().Set("Access-Control-Allow-Headers", "Content-Type")
}

func main() {
	logger.Initialize(slog.LevelInfo)
	log := logger.Named("localnet-health")

	port := os.Getenv("HEALTH_API_PORT")
	if port == "" {
		port = defaultPort
	}
	if _, err := strconv.Atoi(port); err != nil {
		log.With("port", port).Error("HEALTH_API_PORT must be numeric")
		os.Exit(1)
	}

	p, err := newProber()
	if err != nil {
		log.With("err", err.Error()).Error("failed to create docker prober")
		os.Exit(1)
	}
	defer func() {
		if err := p.close(); err != nil {
			log.With("err", err.Error()).Warn("docker client close failed")
		}
	}()

	h := &handler{prober: p}

	mux := http.NewServeMux()
	mux.HandleFunc("/api/services", h.services)
	mux.HandleFunc("/health", h.health)

	server := &http.Server{
		Addr:         ":" + port,
		Handler:      mux,
		ReadTimeout:  httpReadTimeout,
		WriteTimeout: httpWriteTimeout,
	}

	errCh := make(chan error, 1)
	go func() {
		log.With("addr", server.Addr).Info("listening")
		if err := server.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
			errCh <- err
		}
	}()

	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, syscall.SIGINT, syscall.SIGTERM)

	select {
	case err := <-errCh:
		log.With("err", err.Error()).Error("http server failed")
		os.Exit(1)
	case sig := <-sigCh:
		log.With("signal", sig.String()).Info("shutting down")
	}

	shutCtx, cancel := context.WithTimeout(context.Background(), shutdownTimeout)
	defer cancel()
	if err := server.Shutdown(shutCtx); err != nil {
		log.With("err", err.Error()).Warn("graceful shutdown failed")
	}
}
