package form

import (
	"testing"

	managementv1 "github.com/devsy-org/api/pkg/apis/management/v1"
	storagev1 "github.com/devsy-org/api/pkg/apis/storage/v1"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

func newTestInstance(
	templateRef *storagev1.TemplateRef,
	parameters string,
) *managementv1.DevsyWorkspaceInstance {
	instance := &managementv1.DevsyWorkspaceInstance{
		ObjectMeta: metav1.ObjectMeta{Name: "ws", Namespace: "p-default"},
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

func TestApplyInstanceChanges(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name        string
		templateRef *storagev1.TemplateRef
		parameters  string
		template    string
		version     string
		newParams   string
		wantRef     *storagev1.TemplateRef
		wantParams  string
	}{
		{
			name:       "nil template ref is never synthesized",
			parameters: "a",
			template:   "tpl",
			version:    "v2",
			newParams:  "b",
			wantRef:    nil,
			wantParams: "b",
		},
		{
			name:       "nil template ref with unchanged parameters stays nil",
			parameters: "a",
			template:   "tpl",
			version:    "v2",
			newParams:  "a",
			wantRef:    nil,
			wantParams: "a",
		},
		{
			name:        "template name change",
			templateRef: &storagev1.TemplateRef{Name: "old", Version: "v1"},
			parameters:  "a",
			template:    "new",
			version:     "v1",
			newParams:   "a",
			wantRef:     &storagev1.TemplateRef{Name: "new", Version: "v1"},
			wantParams:  "a",
		},
		{
			name:        "version change",
			templateRef: &storagev1.TemplateRef{Name: "tpl", Version: "v1"},
			parameters:  "a",
			template:    "tpl",
			version:     "v2",
			newParams:   "a",
			wantRef:     &storagev1.TemplateRef{Name: "tpl", Version: "v2"},
			wantParams:  "a",
		},
		{
			name:        "parameters change",
			templateRef: &storagev1.TemplateRef{Name: "tpl", Version: "v1"},
			parameters:  "a",
			template:    "tpl",
			version:     "v1",
			newParams:   "b",
			wantRef:     &storagev1.TemplateRef{Name: "tpl", Version: "v1"},
			wantParams:  "b",
		},
		{
			name:        "all values unchanged",
			templateRef: &storagev1.TemplateRef{Name: "tpl", Version: "v1"},
			parameters:  "a",
			template:    "tpl",
			version:     "v1",
			newParams:   "a",
			wantRef:     &storagev1.TemplateRef{Name: "tpl", Version: "v1"},
			wantParams:  "a",
		},
		{
			name:        "template, version and parameters change together",
			templateRef: &storagev1.TemplateRef{Name: "old", Version: "v1"},
			parameters:  "a",
			template:    "new",
			version:     "v2",
			newParams:   "b",
			wantRef:     &storagev1.TemplateRef{Name: "new", Version: "v2"},
			wantParams:  "b",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			instance := newTestInstance(tt.templateRef, tt.parameters)
			original := instance.DeepCopy()

			got := applyInstanceChanges(
				instance,
				newTestTemplate(tt.template),
				tt.version,
				tt.newParams,
			)

			require.NotNil(t, got)
			assert.NotSame(t, instance, got)
			assert.Equal(t, tt.wantRef, got.Spec.TemplateRef)
			assert.Equal(t, tt.wantParams, got.Spec.Parameters)
			assert.Equal(t, instance.ObjectMeta, got.ObjectMeta)
			assert.Equal(t, original, instance, "original instance must not be mutated")
		})
	}
}

func TestApplyInstanceChangesIsolatesOriginalNestedData(t *testing.T) {
	t.Parallel()

	instance := newTestInstance(
		&storagev1.TemplateRef{Name: "old", Version: "v1", SyncOnce: true},
		"a",
	)

	got := applyInstanceChanges(instance, newTestTemplate("new"), "v2", "b")
	require.NotNil(t, got.Spec.TemplateRef)
	require.NotNil(t, instance.Spec.TemplateRef)
	assert.NotSame(t, instance.Spec.TemplateRef, got.Spec.TemplateRef)

	got.Spec.TemplateRef.SyncOnce = false
	got.Spec.TemplateRef.Name = "mutated"

	assert.Equal(t, "old", instance.Spec.TemplateRef.Name)
	assert.Equal(t, "v1", instance.Spec.TemplateRef.Version)
	assert.True(t, instance.Spec.TemplateRef.SyncOnce)
	assert.Equal(t, "a", instance.Spec.Parameters)
}
