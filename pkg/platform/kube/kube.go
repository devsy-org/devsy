package kube

import (
	"fmt"

	devsyclient "github.com/devsy-org/api/pkg/clientset/versioned"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/rest"
)

type Interface interface {
	kubernetes.Interface
	Devsy() devsyclient.Interface
}

func NewForConfig(c *rest.Config) (Interface, error) {
	kubeClient, err := kubernetes.NewForConfig(c)
	if err != nil {
		return nil, fmt.Errorf("create kube client: %w", err)
	}

	devsyClient, err := devsyclient.NewForConfig(c)
	if err != nil {
		return nil, fmt.Errorf("create devsy client: %w", err)
	}

	return &client{
		Interface:   kubeClient,
		devsyClient: devsyClient,
	}, nil
}

type client struct {
	kubernetes.Interface
	devsyClient devsyclient.Interface
}

func (c *client) Devsy() devsyclient.Interface {
	return c.devsyClient
}
