package kubernetes

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"sync"
	"testing"

	pkgconfig "github.com/devsy-org/devsy/pkg/config"
	"github.com/devsy-org/devsy/pkg/devcontainer/config"
	"github.com/devsy-org/devsy/pkg/driver"
	provider2 "github.com/devsy-org/devsy/pkg/provider"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/serializer"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/kubernetes/scheme"
	"k8s.io/client-go/rest"
)

func TestBuildPersistentVolumeClaimLabels(t *testing.T) {
	k := &KubernetesDriver{
		options: &provider2.ProviderKubernetesDriverConfig{
			DiskSize: "10Gi",
		},
	}

	pvc, err := k.buildPersistentVolumeClaim("devsy-ws-123", &driver.RunOptions{
		UID: "ws-123",
		WorkspaceMount: &config.Mount{
			Type:   pkgconfig.ResourceVolume,
			Target: "/workspace",
		},
	})
	if err != nil {
		t.Fatalf("buildPersistentVolumeClaim: %v", err)
	}

	labels := pvc.Labels
	want := map[string]string{
		pkgconfig.K8sManagedLabel:      pkgconfig.LabelValueTrue,
		pkgconfig.K8sResourceLabel:     pkgconfig.ResourceVolume,
		pkgconfig.K8sWorkspaceUIDLabel: "ws-123",
		pkgconfig.K8sVolumeRoleLabel:   pkgconfig.VolumeRoleWorkspace,
		DevsyCreatedLabel:              pkgconfig.LabelValueTrue,
	}
	for k, v := range want {
		if labels[k] != v {
			t.Errorf("label %s = %q, want %q", k, labels[k], v)
		}
	}
}

const (
	testWorkspaceID = "ws-1"
	testSavedImage  = "saved-image"
)

type mockPvcServer struct {
	mu   sync.Mutex
	pvcs map[string]*corev1.PersistentVolumeClaim
}

func (m *mockPvcServer) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	codecs := serializer.NewCodecFactory(scheme.Scheme)
	info, ok := runtime.SerializerInfoForMediaType(
		codecs.SupportedMediaTypes(),
		runtime.ContentTypeJSON,
	)
	if !ok {
		w.WriteHeader(http.StatusInternalServerError)
		return
	}
	if m.dispatch(w, r, info.Serializer, codecs.UniversalDeserializer()) {
		return
	}
	w.WriteHeader(http.StatusNotFound)
}

func (m *mockPvcServer) dispatch(
	w http.ResponseWriter,
	r *http.Request,
	encoder runtime.Encoder,
	decoder runtime.Decoder,
) bool {
	parts := strings.Split(strings.Trim(r.URL.Path, "/"), "/")
	if !isPvcPath(parts) {
		return false
	}
	if r.Method == http.MethodGet && len(parts) == 6 {
		m.handleGet(w, encoder, parts[5])
		return true
	}
	if r.Method == http.MethodPost && len(parts) == 5 {
		m.handlePost(w, r, encoder, decoder)
		return true
	}
	if r.Method == http.MethodPut && len(parts) == 6 {
		m.handlePut(w, r, encoder, decoder)
		return true
	}
	return false
}

func isPvcPath(parts []string) bool {
	return len(parts) >= 5 && parts[0] == "api" && parts[1] == "v1" &&
		parts[2] == "namespaces" && parts[4] == "persistentvolumeclaims"
}

func (m *mockPvcServer) handleGet(w http.ResponseWriter, encoder runtime.Encoder, name string) {
	m.mu.Lock()
	pvc, exists := m.pvcs[name]
	m.mu.Unlock()
	if !exists {
		w.Header().Set("Content-Type", runtime.ContentTypeJSON)
		w.WriteHeader(http.StatusNotFound)
		_ = encoder.Encode(&metav1.Status{
			TypeMeta: metav1.TypeMeta{Kind: "Status", APIVersion: "v1"},
			Status:   metav1.StatusFailure,
			Reason:   metav1.StatusReasonNotFound,
			Code:     http.StatusNotFound,
			Message:  "persistentvolumeclaims not found",
		}, w)
		return
	}
	w.Header().Set("Content-Type", runtime.ContentTypeJSON)
	w.WriteHeader(http.StatusOK)
	_ = encoder.Encode(pvc, w)
}

func (m *mockPvcServer) handlePost(
	w http.ResponseWriter,
	r *http.Request,
	encoder runtime.Encoder,
	decoder runtime.Decoder,
) {
	body, err := io.ReadAll(r.Body)
	if err != nil {
		w.WriteHeader(http.StatusBadRequest)
		return
	}
	obj, _, err := decoder.Decode(body, nil, nil)
	if err != nil {
		w.WriteHeader(http.StatusBadRequest)
		return
	}
	pvc, ok := obj.(*corev1.PersistentVolumeClaim)
	if !ok {
		w.WriteHeader(http.StatusBadRequest)
		return
	}
	pvc.UID = types.UID("pvc-" + pvc.Name)
	pvc.ResourceVersion = "1"
	m.mu.Lock()
	m.pvcs[pvc.Name] = pvc
	m.mu.Unlock()
	w.Header().Set("Content-Type", runtime.ContentTypeJSON)
	w.WriteHeader(http.StatusCreated)
	_ = encoder.Encode(pvc, w)
}

func (m *mockPvcServer) handlePut(
	w http.ResponseWriter,
	r *http.Request,
	encoder runtime.Encoder,
	decoder runtime.Decoder,
) {
	body, err := io.ReadAll(r.Body)
	if err != nil {
		w.WriteHeader(http.StatusBadRequest)
		return
	}
	obj, _, err := decoder.Decode(body, nil, nil)
	if err != nil {
		w.WriteHeader(http.StatusBadRequest)
		return
	}
	pvc, ok := obj.(*corev1.PersistentVolumeClaim)
	if !ok {
		w.WriteHeader(http.StatusBadRequest)
		return
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	current := m.pvcs[pvc.Name]
	w.Header().Set("Content-Type", runtime.ContentTypeJSON)
	if current == nil || current.UID != pvc.UID || current.ResourceVersion != pvc.ResourceVersion {
		w.WriteHeader(http.StatusConflict)
		_ = encoder.Encode(&metav1.Status{
			TypeMeta: metav1.TypeMeta{Kind: "Status", APIVersion: "v1"},
			Status:   metav1.StatusFailure, Reason: metav1.StatusReasonConflict,
			Code: http.StatusConflict,
		}, w)
		return
	}
	version, err := strconv.Atoi(current.ResourceVersion)
	if err != nil {
		w.WriteHeader(http.StatusInternalServerError)
		return
	}
	pvc.ResourceVersion = strconv.Itoa(version + 1)
	m.pvcs[pvc.Name] = pvc
	w.WriteHeader(http.StatusOK)
	_ = encoder.Encode(pvc, w)
}

func newMockPvcDriver(
	t *testing.T,
	initialPvcs map[string]*corev1.PersistentVolumeClaim,
) (*KubernetesDriver, *mockPvcServer) {
	t.Helper()
	mock := &mockPvcServer{
		pvcs: make(map[string]*corev1.PersistentVolumeClaim),
	}
	for k, v := range initialPvcs {
		copy := v.DeepCopy()
		copy.UID = types.UID("pvc-" + copy.Name)
		copy.ResourceVersion = "1"
		mock.pvcs[k] = copy
	}
	srv := httptest.NewServer(mock)
	t.Cleanup(srv.Close)

	cs, err := kubernetes.NewForConfig(&rest.Config{Host: srv.URL})
	if err != nil {
		t.Fatalf("NewForConfig: %v", err)
	}

	driver := &KubernetesDriver{
		client:    &Client{client: cs, config: &rest.Config{Host: srv.URL}},
		namespace: "test-ns",
		options: &provider2.ProviderKubernetesDriverConfig{
			DiskSize: "10Gi",
		},
	}
	return driver, mock
}

func makePvcWithContainerInfo(
	t *testing.T,
	name string,
	info *DevContainerInfo,
) *corev1.PersistentVolumeClaim {
	t.Helper()
	raw, err := json.Marshal(info)
	if err != nil {
		t.Fatalf("marshal DevContainerInfo: %v", err)
	}
	return &corev1.PersistentVolumeClaim{
		ObjectMeta: metav1.ObjectMeta{
			Name: name,
			Annotations: map[string]string{
				DevsyInfoAnnotation: string(raw),
			},
		},
	}
}

func testEnsureMissingPvcWithoutOptions(t *testing.T) {
	k, _ := newMockPvcDriver(t, nil)
	initialize, opts, err := k.ensureDevContainerPvc(context.Background(), testWorkspaceID, nil)
	if err == nil {
		t.Fatal("expected error when options are nil and PVC does not exist")
	}
	if initialize {
		t.Errorf("initialize = %v, want false", initialize)
	}
	if opts != nil {
		t.Errorf("opts = %v, want nil", opts)
	}
}

func testEnsureMissingPvcWithOptions(t *testing.T) {
	k, mock := newMockPvcDriver(t, nil)
	options := &driver.RunOptions{
		UID:   testWorkspaceID,
		Image: "golang:1.24",
	}
	initialize, _, err := k.ensureDevContainerPvc(context.Background(), testWorkspaceID, options)
	if err != nil {
		t.Fatalf("ensureDevContainerPvc: %v", err)
	}
	if !initialize {
		t.Errorf("initialize = false, want true")
	}
	mock.mu.Lock()
	created, exists := mock.pvcs[testWorkspaceID]
	mock.mu.Unlock()
	if !exists {
		t.Fatal("expected PVC to be created")
	}
	if created.Annotations[DevsyInfoAnnotation] == "" {
		t.Errorf("expected DevsyInfoAnnotation to be set on created PVC")
	}
}

func runExistingPvcTest(
	t *testing.T,
	savedOptions *driver.RunOptions,
	callerOptions *driver.RunOptions,
) (*driver.RunOptions, error) {
	t.Helper()
	existing := makePvcWithContainerInfo(t, testWorkspaceID, &DevContainerInfo{
		WorkspaceID: testWorkspaceID,
		Options:     savedOptions,
	})
	k, _ := newMockPvcDriver(t, map[string]*corev1.PersistentVolumeClaim{testWorkspaceID: existing})
	initialize, opts, err := k.ensureDevContainerPvc(
		context.Background(),
		testWorkspaceID,
		callerOptions,
	)
	if initialize {
		t.Errorf("initialize = true, want false")
	}
	return opts, err
}

func testEnsureExistingPvcWithoutCallerOptions(t *testing.T) {
	savedOptions := &driver.RunOptions{UID: testWorkspaceID, Image: testSavedImage}
	opts, err := runExistingPvcTest(t, savedOptions, nil)
	if err != nil {
		t.Fatalf("ensureDevContainerPvc: %v", err)
	}
	if opts == nil || opts.Image != testSavedImage {
		t.Errorf("opts = %v, want saved options with image %s", opts, testSavedImage)
	}
}

func TestEnsureDevContainerPvc(t *testing.T) {
	t.Run("missing PVC without options errors", testEnsureMissingPvcWithoutOptions)
	t.Run("missing PVC with options creates PVC and initializes", testEnsureMissingPvcWithOptions)
	t.Run(
		"existing PVC without caller options reuses saved options",
		testEnsureExistingPvcWithoutCallerOptions,
	)
}
