package local

import (
	"context"
	"encoding/json"
	"fmt"
	"net"
	"net/http"
	"os"
	"sync"
	"time"

	"github.com/devsy-org/devsy/pkg/config"
	"github.com/devsy-org/devsy/pkg/log"
	providerpkg "github.com/devsy-org/devsy/pkg/provider"
	"github.com/devsy-org/devsy/pkg/workspace"
)

const (
	idleTimeout  = 5 * time.Minute
	cacheTTL     = 3 * time.Second
	socketSuffix = "devsy-local.sock"
)

type Daemon struct {
	listener net.Listener
	server   *http.Server
	config   *config.Config

	mu           sync.RWMutex
	lastActivity time.Time

	cacheMu      sync.RWMutex
	workspaces   []*providerpkg.Workspace
	workspacesAt time.Time
	providers    map[string]*workspace.ProviderWithOptions
	providersAt  time.Time
	machines     []*providerpkg.Machine
	machinesAt   time.Time
	contexts     []contextEntry
	contextsAt   time.Time
}

type contextEntry struct {
	Name    string `json:"name"`
	Default bool   `json:"default,omitempty"`
}

func New(devsyConfig *config.Config) (*Daemon, error) {
	socketPath := GetSocketPath()
	ln, err := listen(socketPath)
	if err != nil {
		return nil, fmt.Errorf("listen: %w", err)
	}

	d := &Daemon{
		listener:     ln,
		config:       devsyConfig,
		lastActivity: time.Now(),
	}

	mux := http.NewServeMux()
	mux.HandleFunc("GET /health", d.handleHealth)
	mux.HandleFunc("GET /list", d.handleList)
	mux.HandleFunc("GET /provider/list", d.handleProviderList)
	mux.HandleFunc("GET /machine/list", d.handleMachineList)
	mux.HandleFunc("GET /context/list", d.handleContextList)

	d.server = &http.Server{Handler: mux} //nolint:gosec // local Unix socket, no internet exposure
	return d, nil
}

func (d *Daemon) Run(ctx context.Context) error {
	log.Infof("local daemon listening on %s", GetSocketPath())

	errCh := make(chan error, 1)
	go func() { errCh <- d.server.Serve(d.listener) }()

	ticker := time.NewTicker(30 * time.Second)
	defer ticker.Stop()

	for {
		select {
		case err := <-errCh:
			return err
		case <-ctx.Done():
			return d.shutdown()
		case <-ticker.C:
			d.mu.RLock()
			idle := time.Since(d.lastActivity) > idleTimeout
			d.mu.RUnlock()
			if idle {
				log.Infof("idle timeout reached, shutting down")
				return d.shutdown()
			}
		}
	}
}

func (d *Daemon) shutdown() error {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	_ = os.Remove(GetSocketPath())
	return d.server.Shutdown(ctx)
}

func (d *Daemon) touch() {
	d.mu.Lock()
	d.lastActivity = time.Now()
	d.mu.Unlock()
}

func (d *Daemon) handleHealth(w http.ResponseWriter, _ *http.Request) {
	d.touch()
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write([]byte(`{"status":"ok"}`))
}

func (d *Daemon) handleList(w http.ResponseWriter, _ *http.Request) {
	d.touch()

	d.cacheMu.RLock()
	if time.Since(d.workspacesAt) < cacheTTL && d.workspaces != nil {
		data := d.workspaces
		d.cacheMu.RUnlock()
		writeJSON(w, data)
		return
	}
	d.cacheMu.RUnlock()

	workspaces, err := workspace.ListLocalWorkspaces(d.config.DefaultContext, true)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}

	d.cacheMu.Lock()
	d.workspaces = workspaces
	d.workspacesAt = time.Now()
	d.cacheMu.Unlock()

	writeJSON(w, workspaces)
}

// providerWithDefault mirrors cmd/provider/list.ProviderWithDefault so the
// daemon's JSON shape matches the CLI's. The desktop UI relies on the
// top-level `default` field to flag which provider is current.
type providerWithDefault struct {
	*workspace.ProviderWithOptions

	Default bool `json:"default,omitempty"`
}

func (d *Daemon) handleProviderList(w http.ResponseWriter, _ *http.Request) {
	d.touch()

	d.cacheMu.RLock()
	if time.Since(d.providersAt) < cacheTTL && d.providers != nil {
		data := d.providers
		d.cacheMu.RUnlock()
		writeJSON(w, withDefaults(d.config, data))
		return
	}
	d.cacheMu.RUnlock()

	providers, err := workspace.LoadAllProviders(d.config)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}

	d.cacheMu.Lock()
	d.providers = providers
	d.providersAt = time.Now()
	d.cacheMu.Unlock()

	writeJSON(w, withDefaults(d.config, providers))
}

func withDefaults(
	cfg *config.Config,
	providers map[string]*workspace.ProviderWithOptions,
) map[string]providerWithDefault {
	defaultName := cfg.Current().DefaultProvider
	out := make(map[string]providerWithDefault, len(providers))
	for k, p := range providers {
		out[k] = providerWithDefault{
			ProviderWithOptions: p,
			Default:             p.Config.Name == defaultName,
		}
	}
	return out
}

func (d *Daemon) handleMachineList(w http.ResponseWriter, _ *http.Request) {
	d.touch()

	d.cacheMu.RLock()
	if time.Since(d.machinesAt) < cacheTTL && d.machines != nil {
		data := d.machines
		d.cacheMu.RUnlock()
		writeJSON(w, data)
		return
	}
	d.cacheMu.RUnlock()

	machines, err := workspace.ListMachines(d.config)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}

	d.cacheMu.Lock()
	d.machines = machines
	d.machinesAt = time.Now()
	d.cacheMu.Unlock()

	writeJSON(w, machines)
}

func (d *Daemon) handleContextList(w http.ResponseWriter, _ *http.Request) {
	d.touch()

	d.cacheMu.RLock()
	if time.Since(d.contextsAt) < cacheTTL && d.contexts != nil {
		data := d.contexts
		d.cacheMu.RUnlock()
		writeJSON(w, data)
		return
	}
	d.cacheMu.RUnlock()

	devsyConfig, err := config.LoadConfig("", "")
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}

	entries := make([]contextEntry, 0, len(devsyConfig.Contexts))
	for name := range devsyConfig.Contexts {
		entries = append(entries, contextEntry{
			Name:    name,
			Default: devsyConfig.DefaultContext == name,
		})
	}

	d.cacheMu.Lock()
	d.contexts = entries
	d.contextsAt = time.Now()
	d.config = devsyConfig
	d.cacheMu.Unlock()

	writeJSON(w, entries)
}

func writeJSON(w http.ResponseWriter, v any) {
	w.Header().Set("Content-Type", "application/json")
	if err := json.NewEncoder(w).Encode(v); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
	}
}
