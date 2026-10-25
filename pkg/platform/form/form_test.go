package form

import (
	"testing"

	managementv1 "github.com/devsy-org/api/pkg/apis/management/v1"
	storagev1 "github.com/devsy-org/api/pkg/apis/storage/v1"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

const (
	testInstanceName = "ws"
	testNamespace    = "p-default"
	templateCurrent  = "tpl"
	templateOld      = "old"
	templateNew      = "new"
	versionV1        = "v1"
	versionV2        = "v2"
	paramsA          = "a"
	paramsB          = "b"
)

type applyCase struct {
	name        string
	templateRef *storagev1.TemplateRef
	parameters  string
	template    string
	version     string
	newParams   string
	wantRef     *storagev1.TemplateRef
	wantParams  string
}

func templateRefOf(name, version string) *storagev1.TemplateRef {
	return &storagev1.TemplateRef{Name: name, Version: version}
}

func newTestInstance(
	templateRef *storagev1.TemplateRef,
	parameters string,
) *managementv1.DevsyWorkspaceInstance {
	instance := &managementv1.DevsyWorkspaceInstance{
		ObjectMeta: metav1.ObjectMeta{Name: testInstanceName, Namespace: testNamespace},
	}
	instance.Spec.TemplateRef = templateRef
	instance.Spec.Parameters = parameters

	return instance
}

func newTestTemplate(name string) *managementv1.DevsyWorkspaceTemplate {
	return &managementv1.DevsyWorkspaceTemplate{
		ObjectMeta: metav1.ObjectMeta{Name: name},
	}
}

func runApplyCase(t *testing.T, tt applyCase) {
	t.Helper()

	instance := newTestInstance(tt.templateRef, tt.parameters)
	original := instance.DeepCopy()

	got := applyInstanceChanges(instance, newTestTemplate(tt.template), tt.version, tt.newParams)

	require.NotNil(t, got)
	assert.NotSame(t, instance, got)
	assert.Equal(t, tt.wantRef, got.Spec.TemplateRef)
	assert.Equal(t, tt.wantParams, got.Spec.Parameters)
	assert.Equal(t, instance.ObjectMeta, got.ObjectMeta)
	assert.Equal(t, original, instance, "original instance must not be mutated")
}

func TestApplyInstanceChangesNilTemplateRef(t *testing.T) {
	t.Parallel()

	tests := []applyCase{
		{
			name:       "parameters change without synthesizing a ref",
			parameters: paramsA,
			template:   templateCurrent,
			version:    versionV2,
			newParams:  paramsB,
			wantRef:    nil,
			wantParams: paramsB,
		},
		{
			name:       "unchanged parameters keep nil ref",
			parameters: paramsA,
			template:   templateCurrent,
			version:    versionV2,
			newParams:  paramsA,
			wantRef:    nil,
			wantParams: paramsA,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			runApplyCase(t, tt)
		})
	}
}

func TestApplyInstanceChangesTemplateRefUpdates(t *testing.T) {
	t.Parallel()

	tests := []applyCase{
		{
			name:        "template name change",
			templateRef: templateRefOf(templateOld, versionV1),
			parameters:  paramsA,
			template:    templateNew,
			version:     versionV1,
			newParams:   paramsA,
			wantRef:     templateRefOf(templateNew, versionV1),
			wantParams:  paramsA,
		},
		{
			name:        "version change",
			templateRef: templateRefOf(templateCurrent, versionV1),
			parameters:  paramsA,
			template:    templateCurrent,
			version:     versionV2,
			newParams:   paramsA,
			wantRef:     templateRefOf(templateCurrent, versionV2),
			wantParams:  paramsA,
		},
		{
			name:        "template name and version change",
			templateRef: templateRefOf(templateOld, versionV1),
			parameters:  paramsA,
			template:    templateNew,
			version:     versionV2,
			newParams:   paramsA,
			wantRef:     templateRefOf(templateNew, versionV2),
			wantParams:  paramsA,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			runApplyCase(t, tt)
		})
	}
}

func TestApplyInstanceChangesParameters(t *testing.T) {
	t.Parallel()

	tests := []applyCase{
		{
			name:        "parameters change keeps template ref",
			templateRef: templateRefOf(templateCurrent, versionV1),
			parameters:  paramsA,
			template:    templateCurrent,
			version:     versionV1,
			newParams:   paramsB,
			wantRef:     templateRefOf(templateCurrent, versionV1),
			wantParams:  paramsB,
		},
		{
			name:        "all values unchanged",
			templateRef: templateRefOf(templateCurrent, versionV1),
			parameters:  paramsA,
			template:    templateCurrent,
			version:     versionV1,
			newParams:   paramsA,
			wantRef:     templateRefOf(templateCurrent, versionV1),
			wantParams:  paramsA,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			runApplyCase(t, tt)
		})
	}
}

func TestApplyInstanceChangesIsolatesOriginalNestedData(t *testing.T) {
	t.Parallel()

	instance := newTestInstance(
		&storagev1.TemplateRef{Name: templateOld, Version: versionV1, SyncOnce: true},
		paramsA,
	)

	got := applyInstanceChanges(instance, newTestTemplate(templateNew), versionV2, paramsB)
	require.NotNil(t, got.Spec.TemplateRef)
	require.NotNil(t, instance.Spec.TemplateRef)
	assert.NotSame(t, instance.Spec.TemplateRef, got.Spec.TemplateRef)

	got.Spec.TemplateRef.SyncOnce = false
	got.Spec.TemplateRef.Name = "mutated"

	assert.Equal(t, templateOld, instance.Spec.TemplateRef.Name)
	assert.Equal(t, versionV1, instance.Spec.TemplateRef.Version)
	assert.True(t, instance.Spec.TemplateRef.SyncOnce)
	assert.Equal(t, paramsA, instance.Spec.Parameters)
}
