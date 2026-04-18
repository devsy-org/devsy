package watch

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"sync"
	"time"

	managementv1 "github.com/devsy-org/api/pkg/apis/management/v1"
	storagev1 "github.com/devsy-org/api/pkg/apis/storage/v1"
	loftclient "github.com/devsy-org/api/pkg/clientset/versioned"
	informers "github.com/devsy-org/api/pkg/informers/externalversions"
	informermanagementv1 "github.com/devsy-org/api/pkg/informers/externalversions/management/v1"
	"github.com/devsy-org/devsy/cmd/pro/flags"
	"github.com/devsy-org/devsy/pkg/config"
	"github.com/devsy-org/devsy/pkg/platform"
	"github.com/devsy-org/devsy/pkg/platform/client"
	"github.com/devsy-org/devsy/pkg/platform/project"
	"github.com/devsy-org/devsy/pkg/provider"
	"github.com/devsy-org/devsy/pkg/workspace"
	"github.com/devsy-org/log"
	"github.com/spf13/cobra"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/tools/cache"
)

// WorkspacesCmd holds the cmd flags.
type WorkspacesCmd struct {
	*flags.GlobalFlags

	Log log.Logger
}

// NewWorkspacesCmd creates a new command.
func NewWorkspacesCmd(globalFlags *flags.GlobalFlags) *cobra.Command {
	cmd := &WorkspacesCmd{
		GlobalFlags: globalFlags,
		Log:         log.Default.ErrorStreamOnly(),
	}
	c := &cobra.Command{
		Use:    "workspaces",
		Short:  "Watches all workspaces for a project",
		Hidden: true,
		Args:   cobra.NoArgs,
		RunE: func(cobraCmd *cobra.Command, args []string) error {
			return cmd.Run(cobraCmd.Context(), os.Stdin, os.Stdout, os.Stderr)
		},
	}

	return c
}

type ProWorkspaceInstance struct {
	metav1.TypeMeta   `json:",inline"`
	metav1.ObjectMeta `json:"metadata"`

	Spec   managementv1.DevsyWorkspaceInstanceSpec `json:"spec"`
	Status ProWorkspaceInstanceStatus              `json:"status"`
}

type ProWorkspaceInstanceStatus struct {
	managementv1.DevsyWorkspaceInstanceStatus `json:",inline"`

	Source *provider.WorkspaceSource    `json:"source,omitempty"`
	IDE    *provider.WorkspaceIDEConfig `json:"ide,omitempty"`
}

func (cmd *WorkspacesCmd) Run(
	ctx context.Context,
	stdin io.Reader,
	stdout io.Writer,
	stderr io.Writer,
) error {
	if cmd.Context == "" {
		cmd.Context = config.DefaultContext
	}

	projectName := os.Getenv(config.EnvLoftProject)
	if projectName == "" {
		return fmt.Errorf("project name not found")
	}

	baseClient, err := client.InitClientFromPath(ctx, cmd.Config)
	if err != nil {
		return err
	}

	managementConfig, err := baseClient.ManagementConfig()
	if err != nil {
		return err
	}

	clientset, err := loftclient.NewForConfig(managementConfig)
	if err != nil {
		return err
	}

	factory := informers.NewSharedInformerFactoryWithOptions(clientset, time.Second*60,
		informers.WithNamespace(project.ProjectNamespace(projectName)),
	)
	workspaceInformer := factory.Management().V1().DevsyWorkspaceInstances()

	self := baseClient.Self()
	filterByOwner := os.Getenv(config.EnvLoftFilterByOwner) == config.BoolTrue
	instanceStore := newStore(workspaceInformer, self, cmd.Context, filterByOwner, cmd.Log)

	_, err = workspaceInformer.Informer().AddEventHandler(cache.ResourceEventHandlerFuncs{
		AddFunc: func(obj any) {
			instance, ok := obj.(*managementv1.DevsyWorkspaceInstance)
			if !ok {
				return
			}
			instanceStore.Add(instance)
			printInstances(stdout, instanceStore.List())
		},
		UpdateFunc: func(oldObj any, newObj any) {
			oldInstance, ok := oldObj.(*managementv1.DevsyWorkspaceInstance)
			if !ok {
				return
			}
			newInstance, ok := newObj.(*managementv1.DevsyWorkspaceInstance)
			if !ok {
				return
			}
			instanceStore.Update(oldInstance, newInstance)
			printInstances(stdout, instanceStore.List())
		},
		DeleteFunc: func(obj any) {
			instance, ok := obj.(*managementv1.DevsyWorkspaceInstance)
			if !ok {
				// check for DeletedFinalStateUnknown. Can happen if the informer misses the delete event
				u, ok := obj.(cache.DeletedFinalStateUnknown)
				if !ok {
					return
				}
				instance, ok = u.Obj.(*managementv1.DevsyWorkspaceInstance)
				if !ok {
					return
				}
			}
			instanceStore.Delete(instance)
			printInstances(stdout, instanceStore.List())
		},
	})
	if err != nil {
		return err
	}

	stopCh := make(chan struct{})
	defer close(stopCh)
	go func() {
		factory.Start(stopCh)
		factory.WaitForCacheSync(stopCh)

		// Kick off initial message
		printInstances(stdout, instanceStore.List())
	}()

	<-stopCh

	return nil
}

type instanceStore struct {
	informer      informermanagementv1.DevsyWorkspaceInstanceInformer
	self          *managementv1.Self
	context       string
	filterByOwner bool

	m         sync.Mutex
	instances map[string]*ProWorkspaceInstance

	log log.Logger
}

func newStore(
	informer informermanagementv1.DevsyWorkspaceInstanceInformer,
	self *managementv1.Self,
	context string,
	filterByOwner bool,
	log log.Logger,
) *instanceStore {
	return &instanceStore{
		informer:      informer,
		self:          self,
		context:       context,
		filterByOwner: filterByOwner,
		instances:     map[string]*ProWorkspaceInstance{},
		log:           log,
	}
}

func (s *instanceStore) key(meta metav1.ObjectMeta) string {
	return fmt.Sprintf("%s/%s", meta.Namespace, meta.Name)
}

func (s *instanceStore) Add(instance *managementv1.DevsyWorkspaceInstance) {
	if s.filterByOwner && !platform.IsOwner(s.self, instance.Spec.Owner) {
		return
	}
	var source *provider.WorkspaceSource
	if instance.GetAnnotations() != nil &&
		instance.GetAnnotations()[storagev1.DevsyWorkspaceSourceAnnotation] != "" {
		source = provider.ParseWorkspaceSource(
			instance.GetAnnotations()[storagev1.DevsyWorkspaceSourceAnnotation],
		)
	}

	var ideConfig *provider.WorkspaceIDEConfig
	if instance.GetLabels() != nil && instance.GetLabels()[storagev1.DevsyWorkspaceIDLabel] != "" {
		id := instance.GetLabels()[storagev1.DevsyWorkspaceIDLabel]
		workspaceConfig, err := provider.LoadWorkspaceConfig(s.context, id)
		if err == nil {
			ideConfig = &workspaceConfig.IDE
		}
	}

	proInstance := &ProWorkspaceInstance{
		TypeMeta:   instance.TypeMeta,
		ObjectMeta: instance.ObjectMeta,
		Spec:       instance.Spec,
		Status: ProWorkspaceInstanceStatus{
			DevsyWorkspaceInstanceStatus: instance.Status,
			Source:                       source,
			IDE:                          ideConfig,
		},
	}

	key := s.key(instance.ObjectMeta)
	s.m.Lock()
	s.instances[key] = proInstance
	s.m.Unlock()
}

func (s *instanceStore) Update(
	oldInstance *managementv1.DevsyWorkspaceInstance,
	newInstance *managementv1.DevsyWorkspaceInstance,
) {
	s.Add(newInstance)
}

func (s *instanceStore) Delete(instance *managementv1.DevsyWorkspaceInstance) {
	if s.filterByOwner && !platform.IsOwner(s.self, instance.Spec.Owner) {
		return
	}

	s.m.Lock()
	defer s.m.Unlock()
	key := s.key(instance.ObjectMeta)
	delete(s.instances, key)
}

func (s *instanceStore) List() []*ProWorkspaceInstance {
	instanceList := []*ProWorkspaceInstance{}
	// Check local imported workspaces
	// Eventually this should be implemented by filtering based on ownership and access on the CRD, for now we're stuck with this approach...
	localWorkspaces, err := workspace.ListLocalWorkspaces(s.context, false, s.log)
	if err == nil {
		for _, workspace := range localWorkspaces {
			if workspace.Imported && workspace.Pro != nil {
				// get instance for imported workspace
				selector, err := metav1.LabelSelectorAsSelector(&metav1.LabelSelector{
					MatchLabels: map[string]string{
						storagev1.DevsyWorkspaceUIDLabel: workspace.UID,
					},
				})
				if err != nil {
					continue
				}

				l, err := s.informer.Lister().
					DevsyWorkspaceInstances(project.ProjectFromNamespace(workspace.Pro.Project)).
					List(selector)
				if err != nil {
					continue
				}
				if len(l) == 0 {
					continue
				}
				instance := l[0]
				s.m.Lock()
				if _, ok := s.instances[s.key(instance.ObjectMeta)]; ok {
					continue
				}
				s.m.Unlock()

				var source *provider.WorkspaceSource
				if instance.GetAnnotations() != nil &&
					instance.GetAnnotations()[storagev1.DevsyWorkspaceSourceAnnotation] != "" {
					source = provider.ParseWorkspaceSource(
						instance.GetAnnotations()[storagev1.DevsyWorkspaceSourceAnnotation],
					)
				}

				var ideConfig *provider.WorkspaceIDEConfig
				if instance.GetLabels() != nil &&
					instance.GetLabels()[storagev1.DevsyWorkspaceIDLabel] != "" {
					id := instance.GetLabels()[storagev1.DevsyWorkspaceIDLabel]
					workspaceConfig, err := provider.LoadWorkspaceConfig(s.context, id)
					if err == nil {
						ideConfig = &workspaceConfig.IDE
					}
				}

				proInstance := &ProWorkspaceInstance{
					TypeMeta:   instance.TypeMeta,
					ObjectMeta: instance.ObjectMeta,
					Spec:       instance.Spec,
					Status: ProWorkspaceInstanceStatus{
						DevsyWorkspaceInstanceStatus: instance.Status,
						Source:                       source,
						IDE:                          ideConfig,
					},
				}
				instanceList = append(instanceList, proInstance)
			}
		}
	}

	s.m.Lock()
	for _, instance := range s.instances {
		instanceList = append(instanceList, instance)
	}
	s.m.Unlock()

	return instanceList
}

func printInstances(w io.Writer, instances []*ProWorkspaceInstance) {
	out, err := json.Marshal(instances)
	if err != nil {
		return
	}

	_, _ = fmt.Fprintln(w, string(out))
}
