package daemon

import (
	"context"
	"encoding/json"
	"fmt"
	"net"
	"net/http"
	"sync"
	"time"

	managementv1 "github.com/devsy-org/api/pkg/apis/management/v1"
	"github.com/devsy-org/devsy/pkg/config"
	"github.com/devsy-org/devsy/pkg/dockercredentials"
	"github.com/devsy-org/devsy/pkg/gitcredentials"
	"github.com/devsy-org/devsy/pkg/log"
	"github.com/devsy-org/devsy/pkg/platform"
	platformclient "github.com/devsy-org/devsy/pkg/platform/client"
	"github.com/devsy-org/devsy/pkg/platform/kube"
	"github.com/devsy-org/devsy/pkg/platform/project"
	"github.com/gorilla/handlers"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/klog/v2"
	"tailscale.com/client/local"
	"tailscale.com/ipn"
	"tailscale.com/ipn/ipnstate"
	"tailscale.com/net/memnet"
)

type localServer struct {
	devsyContext   string
	httpServer     *http.Server
	lc             *local.Client
	listener       *memnet.Listener
	pc             platformclient.Client
	platformStatus *platformStatus
	stopChan       chan struct{}
}

type Status struct {
	State         DaemonState  `json:"state,omitempty"`
	LoginRequired bool         `json:"loginRequired,omitempty"`
	Online        bool         `json:"online,omitempty"`
	Debug         *DebugStatus `json:"debug,omitempty"`
}

type DebugStatus struct {
	Tailscale *ipnstate.Status   `json:"tailscale,omitempty"`
	Self      *managementv1.Self `json:"self,omitempty"`
}

type DaemonState string

var (
	DaemonStateRunning DaemonState = "running"
	DaemonStateStopped DaemonState = "stopped"
	DaemonStatePending DaemonState = "pending"
)

const (
	platformStatusCheckInterval = 10 * time.Second
	readHeaderTimeout           = 10 * time.Second
)

type platformStatus struct {
	mu            sync.RWMutex
	authenticated bool
}

var (
	routeHealth            = "/health"
	routeStatus            = "/status"
	routeVersion           = "/version"
	routeShutdown          = "/shutdown"
	routeSelf              = "/self"
	routeProjects          = "/projects"
	routeProjectTemplates  = "/projects/{project}/templates"
	routeProjectClusters   = "/projects/{project}/clusters"
	routeGetWorkspace      = "/workspace"
	routeWatchWorkspaces   = "/watch-workspaces"
	routeListWorkspaces    = "/list-workspaces"
	routeCreateWorkspace   = "/create-workspace"
	routeUpdateWorkspace   = "/update-workspace"
	routeGetUserProfile    = "/user-profile"
	routeUpdateUserProfile = "/update-user-profile"
	routeGitCredentials    = "/git-credentials"
	routeDockerCredentials = "/docker-credentials" //nolint:gosec // route path, not a credential
)

func newLocalServer(
	lc *local.Client,
	pc platformclient.Client,
	devsyContext string,
) *localServer {
	l := &localServer{
		lc:             lc,
		pc:             pc,
		devsyContext:   devsyContext,
		listener:       memnet.Listen("localclient.devsy:80"),
		platformStatus: &platformStatus{authenticated: true},
		stopChan:       make(chan struct{}, 1),
	}

	mux := http.NewServeMux()
	mux.Handle("GET "+routeHealth, handlerFunc(l.health))
	mux.Handle("GET "+routeStatus, handlerFunc(l.status))
	mux.Handle("GET "+routeVersion, handlerFunc(l.version))
	mux.Handle("GET "+routeShutdown, handlerFunc(l.shutdown))
	mux.Handle("GET "+routeSelf, handlerFunc(l.self))
	mux.Handle("GET "+routeProjects, handlerFunc(l.projects))
	mux.Handle("GET "+routeProjectTemplates, handlerFunc(l.projectTemplates))
	mux.Handle("GET "+routeProjectClusters, handlerFunc(l.projectClusters))
	mux.Handle("GET "+routeGetWorkspace, handlerFunc(l.getWorkspace))
	mux.HandleFunc("GET "+routeWatchWorkspaces, l.watchWorkspaces)
	mux.Handle("GET "+routeListWorkspaces, handlerFunc(l.listWorkspace))
	mux.Handle("POST "+routeCreateWorkspace, handlerFunc(l.createWorkspace))
	mux.Handle("POST "+routeUpdateWorkspace, handlerFunc(l.updateWorkspace))
	mux.Handle("GET "+routeGetUserProfile, handlerFunc(l.userProfile))
	mux.Handle("POST "+routeUpdateUserProfile, handlerFunc(l.updateUserProfile))
	mux.Handle("GET "+routeGitCredentials, handlerFunc(l.getGitCredentials))
	mux.Handle("GET "+routeDockerCredentials, handlerFunc(l.getDockerCredentials))

	handler := handlers.RecoveryHandler(
		handlers.RecoveryLogger(panicLogger{}),
		handlers.PrintRecoveryStack(true),
	)(mux)
	handler = handlers.LoggingHandler(log.Writer(log.LevelDebug), handler)
	l.httpServer = &http.Server{Handler: handler, ReadHeaderTimeout: readHeaderTimeout}

	return l
}

type panicLogger struct{}

func (r panicLogger) Println(args ...any) {
	log.Error(args...)
}

func (l *localServer) ListenAndServe() error {
	errChan := make(chan error, 1)
	go func() {
		log.Info("Start config watcher")
		l.watchPlatform(l.stopChan)
		errChan <- nil
	}()
	go func() {
		err := l.httpServer.Serve(l.listener)
		errChan <- err
	}()

	select {
	case err := <-errChan:
		return err
	case <-l.stopChan:
		return nil
	}
}

func (l *localServer) Close() error {
	log.Info("shutting down local server")
	l.stopChan <- struct{}{}
	_ = l.listener.Close()
	return nil
}

func (l *localServer) Addr() string {
	return l.listener.Addr().String()
}

func (l *localServer) Dial(ctx context.Context, network, addr string) (net.Conn, error) {
	return l.listener.Dial(ctx, network, addr)
}

func (l *localServer) watchPlatform(stopChan <-chan struct{}) {
	for {
		log.Debug("Check platform status")

		managementClient, err := l.pc.Management()
		if err != nil {
			log.Error(fmt.Errorf("create mangement client: %w", err))
		} else {
			_, err = managementClient.Devsy().
				ManagementV1().
				Selves().
				Create(context.Background(), &managementv1.Self{}, metav1.CreateOptions{})
			l.updatePlatformAuthStatus(err)
		}

		select {
		case <-stopChan:
			return
		case <-time.After(platformStatusCheckInterval):
		}
	}
}

// updatePlatformAuthStatus records the platform authentication status from a
// self-creation attempt's result.
func (l *localServer) updatePlatformAuthStatus(err error) {
	l.platformStatus.mu.Lock()
	defer l.platformStatus.mu.Unlock()

	if err == nil {
		l.platformStatus.authenticated = true
		return
	}
	if IsAccessKeyNotFound(err) {
		log.Warnf("client not authenticated: %s", err)
		l.platformStatus.authenticated = false
		return
	}
	log.Errorf("failed to create self: %v", err)
}

func (l *localServer) health(w http.ResponseWriter, r *http.Request) error {
	w.WriteHeader(http.StatusOK)
	return nil
}

func daemonStateFromBackend(backendState string) DaemonState {
	switch backendState {
	case ipn.Starting.String():
		return DaemonStatePending
	case ipn.Running.String():
		return DaemonStateRunning
	default:
		return DaemonStateStopped
	}
}

func (l *localServer) status(w http.ResponseWriter, r *http.Request) error {
	st, err := l.lc.Status(r.Context())
	if err != nil {
		return err
	}

	status := &Status{
		State: daemonStateFromBackend(st.BackendState),
	}

	// authentication info
	l.platformStatus.mu.RLock()
	status.LoginRequired = !l.platformStatus.authenticated
	l.platformStatus.mu.RUnlock()

	// set online status
	status.Online = st.Self != nil && st.Self.Online && st.Self.InNetworkMap

	// debug info
	if r.URL.Query().Has("debug") {
		self := l.pc.Self()
		self.Status.AccessKey = "*********" // redact access key
		status.Debug = &DebugStatus{
			Tailscale: st,
			Self:      self,
		}
	}

	return writeJSON(w, status)
}

func (l *localServer) shutdown(w http.ResponseWriter, r *http.Request) error {
	err := l.Close()
	if err != nil {
		return fmt.Errorf("shut down daemon server: %w", err)
	}

	w.WriteHeader(http.StatusOK)
	return nil
}

type VersionInfo struct {
	ServerVersion string `json:"serverVersion,omitempty"`
}

func (l *localServer) version(w http.ResponseWriter, r *http.Request) error {
	platformVersion, err := platform.GetPlatformVersion(l.pc.Config().Host)
	if err != nil {
		return fmt.Errorf("get platform version: %w", err)
	}

	return writeJSON(w, VersionInfo{
		ServerVersion: platformVersion.Version,
	})
}

func (l *localServer) self(w http.ResponseWriter, r *http.Request) error {
	return writeJSON(w, l.pc.Self())
}

func (l *localServer) userProfile(
	w http.ResponseWriter,
	r *http.Request,
) error {
	managementClient, err := l.pc.Management()
	if err != nil {
		return err
	}
	userName := l.pc.Self().Status.User.Name

	profile, err := managementClient.Devsy().
		ManagementV1().
		Users().
		GetProfile(r.Context(), userName, metav1.GetOptions{})
	if err != nil {
		return fmt.Errorf("get user profile: %w", err)
	}

	return writeJSON(w, profile)
}

func (l *localServer) updateUserProfile(
	w http.ResponseWriter,
	r *http.Request,
) error {
	profile := &managementv1.UserProfile{}
	err := json.NewDecoder(r.Body).Decode(profile)
	if err != nil {
		return badRequest(err)
	}
	profile.TypeMeta = metav1.TypeMeta{}

	managementClient, err := l.pc.Management()
	if err != nil {
		return err
	}
	userName := l.pc.Self().Status.User.Name
	updatedProfile, err := managementClient.Devsy().
		ManagementV1().
		Users().
		UpdateProfile(r.Context(), userName, profile, metav1.CreateOptions{})
	if err != nil {
		return fmt.Errorf("update user profile: %w", err)
	}

	return writeJSON(w, updatedProfile)
}

func (l *localServer) projects(w http.ResponseWriter, r *http.Request) error {
	managementClient, err := l.pc.Management()
	if err != nil {
		return err
	}

	projectList, err := managementClient.Devsy().
		ManagementV1().
		Projects().
		List(r.Context(), metav1.ListOptions{})
	if err != nil {
		return fmt.Errorf("list projects: %w", err)
	} else if len(projectList.Items) == 0 {
		err := fmt.Errorf(
			"you don't have access to any projects, make sure you have at least access to 1 project",
		)
		return forbidden(err)
	}

	return writeJSON(w, projectList.Items)
}

func (l *localServer) projectTemplates(
	w http.ResponseWriter,
	r *http.Request,
) error {
	projectName := r.PathValue("project")
	managementClient, err := l.pc.Management()
	if err != nil {
		return err
	}

	templateList, err := managementClient.Devsy().
		ManagementV1().
		Projects().
		ListTemplates(r.Context(), projectName, metav1.GetOptions{})
	if err != nil {
		return fmt.Errorf("list templates: %w", err)
	} else if len(templateList.DevsyWorkspaceTemplates) == 0 {
		err := fmt.Errorf(
			"seems like there is no template allowed in project %s, "+
				"make sure to at least have a single template available",
			projectName,
		)
		return err
	}

	return writeJSON(w, templateList)
}

func (l *localServer) projectClusters(
	w http.ResponseWriter,
	r *http.Request,
) error {
	projectName := r.PathValue("project")
	managementClient, err := l.pc.Management()
	if err != nil {
		return err
	}

	clusterList, err := managementClient.Devsy().
		ManagementV1().
		Projects().
		ListClusters(r.Context(), projectName, metav1.GetOptions{})
	if err != nil {
		return fmt.Errorf("list cluster: %w", err)
	} else if len(clusterList.Clusters) == 0 {
		err := fmt.Errorf(
			"seems like there is no cluster allowed in project %s, make sure to at least have a single cluster available",
			projectName,
		)
		return err
	}

	return writeJSON(w, clusterList)
}

type workspaceOwnerFilter struct {
	filter platform.OwnerFilter
	self   *managementv1.Self
}

func collectProjectWorkspaces(
	ctx context.Context,
	managementClient kube.Interface,
	p managementv1.Project,
	ownerFilter workspaceOwnerFilter,
) ([]managementv1.DevsyWorkspaceInstance, error) {
	ns := project.ProjectNamespace(p.GetName())
	workspaceList, err := managementClient.Devsy().
		ManagementV1().
		DevsyWorkspaceInstances(ns).
		List(ctx, metav1.ListOptions{})
	if err != nil {
		return nil, fmt.Errorf("list workspaces in project %s: %w", p.GetName(), err)
	}

	var instances []managementv1.DevsyWorkspaceInstance
	for _, instance := range workspaceList.Items {
		if ownerFilter.filter == platform.SelfOwnerFilter &&
			!platform.IsOwner(ownerFilter.self, instance.GetOwner()) {
			continue
		}
		if instance.GetLabels() == nil {
			instance.Labels = map[string]string{}
		}
		instance.Labels[config.K8sProjectLabel] = p.GetName()
		instances = append(instances, instance)
	}
	return instances, nil
}

func (l *localServer) listWorkspace(
	w http.ResponseWriter,
	r *http.Request,
) error {
	ownerFilter := workspaceOwnerFilter{
		filter: platform.SelfOwnerFilter,
		self:   l.pc.Self(),
	}
	if ownerParam := r.URL.Query().Get("owner"); ownerParam != "" {
		ownerFilter.filter = platform.OwnerFilter(ownerParam)
	}

	managementClient, err := l.pc.Management()
	if err != nil {
		return err
	}

	projectList, err := managementClient.Devsy().
		ManagementV1().
		Projects().
		List(r.Context(), metav1.ListOptions{})
	if err != nil {
		return fmt.Errorf("list projects: %w", err)
	} else if len(projectList.Items) == 0 {
		err := fmt.Errorf(
			"you don't have access to any projects, make sure you have at least access to 1 project",
		)
		return forbidden(err)
	}

	instances := []managementv1.DevsyWorkspaceInstance{}
	for _, p := range projectList.Items {
		projectInstances, err := collectProjectWorkspaces(
			r.Context(), managementClient, p, ownerFilter,
		)
		if err != nil {
			return err
		}
		instances = append(instances, projectInstances...)
	}

	return writeJSON(w, instances)
}

func (l *localServer) getWorkspace(
	w http.ResponseWriter,
	r *http.Request,
) error {
	uid := r.URL.Query().Get("uid")
	if uid == "" {
		return missingQueryParam("uid")
	}

	instance, err := platform.FindInstance(
		r.Context(),
		l.pc,
		platform.FindInstanceOptions{UID: uid},
	)
	if err != nil {
		return fmt.Errorf("failed to get workspace with uid %s: %w", uid, err)
	}
	if instance == nil {
		// send OK but don't try to marshal nil instance
		w.WriteHeader(http.StatusOK)
		return nil
	}

	return writeJSON(w, instance)
}

func (l *localServer) watchWorkspaces(
	w http.ResponseWriter,
	r *http.Request,
) {
	f, ok := w.(http.Flusher)
	if !ok {
		http.Error(w, "not a flusher", http.StatusInternalServerError)
		return
	}

	project := r.URL.Query().Get("project")
	if project == "" {
		http.Error(w, "missing required query parameter \"project\"", http.StatusBadRequest)
		return
	}
	ownerParam := r.URL.Query().Get("owner")
	ownerFilter := platform.SelfOwnerFilter
	if ownerParam != "" {
		ownerFilter = platform.OwnerFilter(ownerParam)
	}

	w.Header().Set("Content-Type", "application/json")
	enc := json.NewEncoder(w)
	err := startWorkspaceWatcher(r.Context(), watchConfig{
		Project:        project,
		Context:        l.devsyContext,
		OwnerFilter:    ownerFilter,
		PlatformClient: l.pc,
		TsClient:       l.lc,
	},
		// we need to debounce events here to avoid spamming the client with too many events
		throttle(func(instanceList []*WorkspaceInstance) {
			if r.Context().Err() != nil {
				return // Client disconnected, stop trying to write
			}

			if instanceList != nil {
				err := enc.Encode(instanceList)
				if err != nil {
					http.Error(w, "decode workspace list", http.StatusInternalServerError)
					return
				}
				f.Flush()
			}
		}, time.Second),
	)
	if err != nil {
		http.Error(
			w,
			fmt.Errorf("failed to watch workspaces: %w", err).Error(),
			http.StatusInternalServerError,
		)
		log.Errorf("watch workspaces: %v", err)
		return
	}
}

// throttle returns a function that wraps f so that f is called at most once
// every interval. If a call happens before the interval has elapsed,
// the latest instanceList is saved and f will be called with it when possible.
func throttle(
	f func(instanceList []*WorkspaceInstance),
	interval time.Duration,
) func(instanceList []*WorkspaceInstance) {
	var mu sync.Mutex
	var lastExecuted time.Time       // Time of the last execution of f.
	var pending []*WorkspaceInstance // Latest instanceList waiting to be processed.
	var timer *time.Timer            // Timer scheduled to call f.

	return func(instanceList []*WorkspaceInstance) {
		mu.Lock()
		defer mu.Unlock()

		now := time.Now()
		// If enough time has passed and there's no pending timer, call immediately.
		if timer == nil && now.Sub(lastExecuted) >= interval {
			lastExecuted = now
			// Call f asynchronously so the caller is not blocked.
			f(instanceList)
			return
		}

		// Otherwise, save the latest instanceList.
		pending = instanceList

		// If no timer is set, schedule one to run when the interval expires.
		if timer == nil {
			remaining := max(interval-now.Sub(lastExecuted), 0)
			timer = time.AfterFunc(remaining, func() {
				mu.Lock()
				// Grab the latest pending instanceList.
				currentPending := pending
				// Clear state.
				pending = nil
				timer = nil
				lastExecuted = time.Now()
				mu.Unlock()
				// Execute the function with the latest instanceList.
				if currentPending != nil {
					f(currentPending)
				}
			})
		}
	}
}

func (l *localServer) createWorkspace(
	w http.ResponseWriter,
	r *http.Request,
) error {
	instance := &managementv1.DevsyWorkspaceInstance{}
	err := json.NewDecoder(r.Body).Decode(instance)
	if err != nil {
		return badRequest(err)
	}
	instance.TypeMeta = metav1.TypeMeta{}

	updatedInstance, err := platform.CreateInstance(r.Context(), l.pc, instance)
	if err != nil {
		return badRequest(fmt.Errorf("create workspace: %w", err))
	}

	return writeJSON(w, updatedInstance)
}

func (l *localServer) updateWorkspace(
	w http.ResponseWriter,
	r *http.Request,
) error {
	newInstance := &managementv1.DevsyWorkspaceInstance{}
	err := json.NewDecoder(r.Body).Decode(newInstance)
	if err != nil {
		return badRequest(err)
	}
	newInstance.TypeMeta = metav1.TypeMeta{}

	projectName := project.ProjectFromNamespace(newInstance.GetNamespace())
	opts := platform.FindInstanceOptions{Name: newInstance.GetName(), ProjectName: projectName}
	oldInstance, err := platform.FindInstance(r.Context(), l.pc, opts)
	if err != nil {
		return badRequest(fmt.Errorf("find old workspace: %w", err))
	}

	updatedInstance, err := platform.UpdateInstanceSyncTemplate(
		r.Context(),
		l.pc,
		oldInstance,
		newInstance,
	)
	if err != nil {
		return badRequest(fmt.Errorf("update workspace: %w", err))
	}

	return writeJSON(w, updatedInstance)
}

func (l *localServer) getGitCredentials(
	w http.ResponseWriter,
	r *http.Request,
) error {
	host := r.URL.Query().Get("host")
	if host == "" {
		return missingQueryParam("host")
	}
	protocol := r.URL.Query().Get("protocol")
	if protocol == "" {
		protocol = "https"
	}

	credentials, err := gitcredentials.GetCredentials(r.Context(), &gitcredentials.GitCredentials{
		Protocol: protocol,
		Host:     host,
	})
	if err != nil {
		return fmt.Errorf("get git credentials: %w", err)
	}

	return writeJSON(w, credentials)
}

func (l *localServer) getDockerCredentials(
	w http.ResponseWriter,
	r *http.Request,
) error {
	host := r.URL.Query().Get("server")
	if host == "" {
		return missingQueryParam("server")
	}

	all, err := dockercredentials.ListCredentials()
	if err != nil {
		klog.Errorf("failed to list docker credentials: %v", err)
		return fmt.Errorf("list docker credentials: %w", err)
	}
	for registry, cred := range all.Registries {
		if registry == host {
			return writeJSON(w, cred)
		}
	}
	return nil
}
