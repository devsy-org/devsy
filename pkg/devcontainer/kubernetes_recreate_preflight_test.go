package devcontainer

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"sync"
	"testing"

	"github.com/devsy-org/devsy/pkg/devcontainer/config"
	"github.com/devsy-org/devsy/pkg/devcontainer/metadata"
	kubernetesdriver "github.com/devsy-org/devsy/pkg/driver/kubernetes"
	"github.com/devsy-org/devsy/pkg/provider"
	"github.com/devsy-org/devsy/pkg/status"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	kubescheme "k8s.io/client-go/kubernetes/scheme"
	"k8s.io/client-go/tools/clientcmd"
	clientcmdapi "k8s.io/client-go/tools/clientcmd/api"
)

const (
	kubeRecreateWorkspace    = "policy-workspace"
	kubeRecreateNamespace    = "policy-test"
	kubeRecreateUID          = "policy-owner"
	kubeRecreateContext      = "fixture"
	kubeRecreateArchitecture = "amd64"
	regularSidecarTemplate   = "spec:\n  containers:\n  - name: helper\n    image: example/helper:1\n"
)

type kubeTemplatePolicyCase struct {
	name          string
	template      string
	timeout       string
	metadataNone  bool
	disableDaemon bool
	reject        bool
}

func TestKubernetesRecreateTemplatePreflightPreservesExistingPod(t *testing.T) {
	for _, tc := range []kubeTemplatePolicyCase{
		{name: "positive regular sidecar rejected", template: regularSidecarTemplate, timeout: "1m", reject: true},
		{name: "zero regular sidecar allowed", template: regularSidecarTemplate},
		{
			name:     "resolved image shutdown none allows sidecar",
			template: regularSidecarTemplate, timeout: "1m", metadataNone: true,
		},
		{name: "current disable daemon allows sidecar", template: regularSidecarTemplate, timeout: "1m", disableDaemon: true},
		{
			name: "positive normal init allowed", timeout: "1m",
			template: "spec:\n  initContainers:\n  - name: prepare\n    image: example/init:1\n",
		},
		{
			name: "positive native sidecar allowed", timeout: "1m",
			template: "spec:\n  initContainers:\n  - name: helper\n" +
				"    image: example/helper:1\n    restartPolicy: Always\n",
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			r, params, api := newKubernetesTemplateRecreateFixture(t, tc)
			before, _, fixtureErr := api.snapshot()
			require.NoError(t, fixtureErr)

			_, _, err := r.buildNewContainerConfig(t.Context(), params)

			after, writes, fixtureErr := api.snapshot()
			require.NoError(t, fixtureErr)
			if tc.reject {
				assert.Error(t, err)
				assert.Equal(
					t,
					before,
					after,
					"unsupported template must not stop the existing pod",
				)
				assert.Empty(t, writes, "preflight must not change API state")
				return
			}
			require.NoError(t, err)
			assert.Nil(
				t,
				after,
				"supported explicit recreation reaches the real Kubernetes stop boundary",
			)
			assert.Equal(t, []string{http.MethodDelete + " " + api.podPath}, writes)
		})
	}
}

func newKubernetesTemplateRecreateFixture(
	t *testing.T,
	tc kubeTemplatePolicyCase,
) (*runner, *resolveParams, *kubeRecreateAPI) {
	t.Helper()
	api := newKubeRecreateAPI()
	server := httptest.NewServer(api)
	t.Cleanup(server.Close)
	info := newKubeRecreateWorkspaceInfo(t, server.URL, tc)
	d, err := kubernetesdriver.NewKubernetesDriver(info)
	require.NoError(t, err)
	r := newTestRunner(d)
	r.id, r.workspaceConfig, r.reporter = kubeRecreateWorkspace, info, status.Nop()
	imageMetadata := &config.ImageMetadataConfig{}
	if tc.metadataNone {
		imageConfig := &config.DevContainerConfig{}
		imageConfig.ShutdownAction = config.ShutdownActionNone
		config.AddConfigToImageMetadata(imageConfig, imageMetadata)
	}
	labels, err := json.Marshal(imageMetadata.Raw)
	require.NoError(t, err)
	image := &config.ImageDetails{ID: "fixture-image", Config: config.ImageDetailsConfig{
		Labels: map[string]string{metadata.ImageMetadataLabel: string(labels)},
	}}
	r.imageBackend = &separateImages{
		details: image,
		buildInfo: &config.BuildInfo{
			ImageName:     "fixture-image",
			ImageDetails:  image,
			ImageMetadata: imageMetadata,
		},
	}
	params := recreateResolveParams()
	params.options.Recreate = true
	params.parsedConfig.Config.Image = "example/workspace:1"
	params.parsedConfig.Raw = config.CloneDevContainerConfig(params.parsedConfig.Config)
	params.substitutionContext = &config.SubstitutionContext{
		ContainerWorkspaceFolder: managedWorkspaceFolder,
	}
	return r, params, api
}

func newKubeRecreateWorkspaceInfo(
	t *testing.T,
	serverURL string,
	tc kubeTemplatePolicyCase,
) *provider.AgentWorkspaceInfo {
	t.Helper()
	dir := t.TempDir()
	kubeconfig := filepath.Join(dir, "kubeconfig")
	data, err := clientcmd.Write(clientcmdapi.Config{
		Clusters: map[string]*clientcmdapi.Cluster{
			kubeRecreateContext: {Server: serverURL},
		},
		AuthInfos: map[string]*clientcmdapi.AuthInfo{kubeRecreateContext: {}},
		Contexts: map[string]*clientcmdapi.Context{kubeRecreateContext: {
			Cluster: kubeRecreateContext, AuthInfo: kubeRecreateContext,
			Namespace: kubeRecreateNamespace,
		}},
		CurrentContext: kubeRecreateContext,
	})
	require.NoError(t, err)
	require.NoError(t, os.WriteFile(kubeconfig, data, 0o600))
	template := filepath.Join(dir, "pod.yaml")
	require.NoError(t, os.WriteFile(template, []byte(tc.template), 0o600))
	return &provider.AgentWorkspaceInfo{
		Workspace: &provider.Workspace{ID: kubeRecreateWorkspace, UID: kubeRecreateUID},
		Agent: provider.ProviderAgentConfig{
			Driver:           provider.KubernetesDriver,
			ContainerTimeout: tc.timeout,
			Kubernetes: provider.ProviderKubernetesDriverConfig{
				KubernetesConfig:    kubeconfig,
				KubernetesNamespace: kubeRecreateNamespace,
				PodManifestTemplate: template,
				Architecture:        kubeRecreateArchitecture,
			},
		},
		CLIOptions: provider.CLIOptions{DisableDaemon: tc.disableDaemon},
	}
}

type kubeRecreateAPI struct {
	mu      sync.Mutex
	podPath string
	pod     *corev1.Pod
	writes  []string
	err     error
}

func newKubeRecreateAPI() *kubeRecreateAPI {
	name := "devsy-" + kubeRecreateWorkspace
	return &kubeRecreateAPI{
		podPath: "/api/v1/namespaces/" + kubeRecreateNamespace + "/pods/" + name,
		pod: &corev1.Pod{
			TypeMeta: metav1.TypeMeta{APIVersion: "v1", Kind: "Pod"},
			ObjectMeta: metav1.ObjectMeta{
				Name:            name,
				Namespace:       kubeRecreateNamespace,
				UID:             "existing-pod",
				ResourceVersion: "7",
				Labels: map[string]string{
					kubernetesdriver.DevsyCreatedLabel:      stringTrue,
					kubernetesdriver.DevsyWorkspaceUIDLabel: kubeRecreateUID,
				},
			},
			Spec: corev1.PodSpec{
				Containers: []corev1.Container{
					{Name: kubernetesdriver.DevContainerName, Image: "example/old:1"},
				},
			},
			Status: corev1.PodStatus{Phase: corev1.PodRunning},
		},
	}
}

func (s *kubeRecreateAPI) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if r.Method != http.MethodGet && r.Method != http.MethodHead {
		s.writes = append(s.writes, r.Method+" "+r.URL.Path)
	}
	if r.URL.Path != s.podPath || s.pod == nil {
		s.writeStatus(w, http.StatusNotFound, metav1.StatusReasonNotFound)
		return
	}
	s.servePod(w, r)
}

func (s *kubeRecreateAPI) servePod(w http.ResponseWriter, r *http.Request) {
	switch r.Method {
	case http.MethodGet:
		s.writeObject(w, http.StatusOK, s.pod)
	case http.MethodDelete:
		if !s.deleteMatches(w, r) {
			return
		}
		s.pod = nil
		s.writeObject(w, http.StatusOK, &metav1.Status{
			TypeMeta: metav1.TypeMeta{
				APIVersion: "v1",
				Kind:       "Status",
			},
			Status: metav1.StatusSuccess,
		})
	default:
		s.writeStatus(w, http.StatusMethodNotAllowed, metav1.StatusReasonMethodNotAllowed)
	}
}

func (s *kubeRecreateAPI) deleteMatches(w http.ResponseWriter, r *http.Request) bool {
	body, err := io.ReadAll(r.Body)
	if err != nil {
		s.err = err
		s.writeStatus(w, http.StatusBadRequest, metav1.StatusReasonBadRequest)
		return false
	}
	options := &metav1.DeleteOptions{}
	if _, _, err := kubescheme.Codecs.UniversalDeserializer().
		Decode(body, nil, options); err != nil {
		s.err = err
		s.writeStatus(w, http.StatusBadRequest, metav1.StatusReasonBadRequest)
		return false
	}
	if preconditions := options.Preconditions; preconditions != nil {
		if preconditions.UID != nil && *preconditions.UID != s.pod.UID {
			s.writeStatus(w, http.StatusConflict, metav1.StatusReasonConflict)
			return false
		}
		if preconditions.ResourceVersion != nil &&
			*preconditions.ResourceVersion != s.pod.ResourceVersion {
			s.writeStatus(w, http.StatusConflict, metav1.StatusReasonConflict)
			return false
		}
	}
	return true
}

func (s *kubeRecreateAPI) writeStatus(
	w http.ResponseWriter,
	code int32,
	reason metav1.StatusReason,
) {
	s.writeObject(w, code, &metav1.Status{
		TypeMeta: metav1.TypeMeta{APIVersion: "v1", Kind: "Status"},
		Status:   metav1.StatusFailure, Reason: reason, Code: code,
	})
}

func (s *kubeRecreateAPI) writeObject(w http.ResponseWriter, code int32, object runtime.Object) {
	w.Header().Set("Content-Type", runtime.ContentTypeJSON)
	w.WriteHeader(int(code))
	if err := json.NewEncoder(w).Encode(object); err != nil {
		s.err = err
	}
}

func (s *kubeRecreateAPI) snapshot() (*corev1.Pod, []string, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.pod.DeepCopy(), append([]string(nil), s.writes...), s.err
}
