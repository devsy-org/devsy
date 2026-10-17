package platform

import (
	"context"
	"errors"
	"testing"

	managementv1 "github.com/devsy-org/api/pkg/apis/management/v1"
	storagev1 "github.com/devsy-org/api/pkg/apis/storage/v1"
	loftclient "github.com/devsy-org/api/pkg/clientset/versioned"
	loftfake "github.com/devsy-org/api/pkg/clientset/versioned/fake"
	"github.com/devsy-org/devsy/pkg/platform/client"
	"github.com/devsy-org/devsy/pkg/platform/kube"
	"github.com/stretchr/testify/suite"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	k8stesting "k8s.io/client-go/testing"
)

type instanceOpsSuite struct {
	suite.Suite
}

func TestInstanceOpsSuite(t *testing.T) {
	suite.Run(t, new(instanceOpsSuite))
}

func (s *instanceOpsSuite) TestSetTemplateSyncOnceSetsFlagWhenTemplateRefPresent() {
	instance := &managementv1.DevsyWorkspaceInstance{}
	instance.Spec.TemplateRef = &storagev1.TemplateRef{}

	setTemplateSyncOnce(instance)

	s.True(instance.Spec.TemplateRef.SyncOnce)
}

func (s *instanceOpsSuite) TestSetTemplateSyncOnceLeavesInstanceWithoutTemplateRefUnchanged() {
	instance := &managementv1.DevsyWorkspaceInstance{}

	setTemplateSyncOnce(instance)

	s.Nil(instance.Spec.TemplateRef)
}

func (s *instanceOpsSuite) TestCreateInstanceWrapsCreateError() {
	createErr := errors.New("boom")
	loft := loftfake.NewSimpleClientset()
	loft.PrependReactor(
		"create",
		"devsyworkspaceinstances",
		func(k8stesting.Action) (bool, runtime.Object, error) {
			return true, nil, createErr
		},
	)
	pc := &instanceOpsPlatformClient{management: &instanceOpsKubeClient{loft: loft}}
	instance := &managementv1.DevsyWorkspaceInstance{
		ObjectMeta: metav1.ObjectMeta{Name: "ws", Namespace: "p-default"},
	}

	_, err := CreateInstance(context.Background(), pc, instance)

	s.ErrorIs(err, createErr)
	s.EqualError(err, "create workspace instance: boom")
}

// instanceOpsKubeClient provides only Loft(); other kube.Interface methods are unused by these tests.
type instanceOpsKubeClient struct {
	kube.Interface
	loft loftclient.Interface
}

func (c *instanceOpsKubeClient) Loft() loftclient.Interface {
	return c.loft
}

// instanceOpsPlatformClient provides only Management(); other client.Client methods are unused by these tests.
type instanceOpsPlatformClient struct {
	client.Client
	management kube.Interface
}

func (c *instanceOpsPlatformClient) Management() (kube.Interface, error) {
	return c.management, nil
}
