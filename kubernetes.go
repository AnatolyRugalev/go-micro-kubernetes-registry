package kubernetes

import (
	"encoding/json"
	"fmt"
	"github.com/micro/go-micro/registry"
	v1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	corev1 "k8s.io/client-go/kubernetes/typed/core/v1"
	cmd "k8s.io/client-go/tools/clientcmd"
	"os"
	"strings"
	"time"
)

const (
	serviceLabelName     = "kubernetes.micro.mu/service"
	servicePortLabelName = "kubernetes.micro.mu/service-port"
	statusAnnotationName = "kubernetes.micro.mu/service-status"
)

type kregistry struct {
	client  corev1.CoreV1Interface
	timeout time.Duration
	options registry.Options
}

func configure(k *kregistry, opts ...registry.Option) error {
	for _, o := range opts {
		o(&k.options)
	}

	if k.options.Timeout == 0 {
		k.options.Timeout = time.Second * 1
	}

	rules := cmd.NewDefaultClientConfigLoadingRules()
	rules.DefaultClientConfig = &cmd.DefaultClientConfig
	if kubeconfig := os.Getenv("KUBECONFIG"); kubeconfig != "" {
		rules.ExplicitPath = kubeconfig
	}
	clientConfig := cmd.NewNonInteractiveDeferredLoadingClientConfig(rules, &cmd.ConfigOverrides{})

	config, err := clientConfig.ClientConfig()
	if err != nil {
		return fmt.Errorf("error loading kubernetes config: %v", err)
	}

	c, err := corev1.NewForConfig(config)
	if err != nil {
		return fmt.Errorf("error creating kubernetes client: %v", err)
	}

	k.client = c
	k.timeout = k.options.Timeout

	return nil
}

// Init allows reconfig of options
func (c *kregistry) Init(opts ...registry.Option) error {
	return configure(c, opts...)
}

// Options returns the registry Options
func (c *kregistry) Options() registry.Options {
	return c.options
}

type patchStatusAnnotation struct {
	Op    string `json:"op"`
	Path  string `json:"path"`
	Value string `json:"value,omitempty"`
}

func (c *kregistry) findAllServices() ([]v1.Service, error) {
	selector := serviceLabelName
	svcs, err := c.client.Services("").List(metav1.ListOptions{
		LabelSelector: selector,
	})
	if err != nil {
		return nil, fmt.Errorf("error loading services list: %v", err)
	}

	if len(svcs.Items) == 0 {
		return nil, fmt.Errorf("no services found with selector %s", selector)
	}
	return svcs.Items, nil
}
func (c *kregistry) findService(name string) (*v1.Service, error) {
	selector := serviceLabelName + "=" + name
	svcs, err := c.client.Services("").List(metav1.ListOptions{
		LabelSelector: selector,
	})
	if err != nil {
		return nil, fmt.Errorf("error loading services list: %v", err)
	}

	if len(svcs.Items) == 0 {
		return nil, fmt.Errorf("no services found with selector %s", selector)
	}
	return &svcs.Items[0], nil
}

func (c *kregistry) patchService(name string, patch *patchStatusAnnotation) error {
	// Marshal "patch" request for Kubernetes API
	patchJson, _ := json.Marshal(patch)

	_, err := c.client.Services("").Patch(name, types.JSONPatchType, patchJson)

	if err != nil {
		return fmt.Errorf("error updating service %s list: %v", name, err)
	}
	return nil
}

func (c *kregistry) unmarshalService(k8sService *v1.Service) (*registry.Service, error) {
	svcJson, ok := k8sService.Annotations[statusAnnotationName]
	if !ok {
		return nil, fmt.Errorf("could not obtain service status from k8s service %s annotation %s", k8sService.Name, statusAnnotationName)
	}

	var svc registry.Service
	err := json.Unmarshal([]byte(svcJson), &svc)
	if err != nil {
		return nil, fmt.Errorf("could not unmarshal service from service %s annotation", k8sService.Name)
	}
	return &svc, nil
}

func (c *kregistry) Register(s *registry.Service, opts ...registry.RegisterOption) error {
	k8sService, err := c.findService(s.Name)
	if err != nil {
		return err
	}

	portName, ok := k8sService.Annotations[servicePortLabelName]

	var port int32
	if ok {
		for _, p := range k8sService.Spec.Ports {
			if p.Name == portName {
				port = p.Port
				break
			}
		}
		if port == 0 {
			return fmt.Errorf("k8s service %s has no port %s defined", k8sService.Name, portName)
		}
	} else {
		if len(k8sService.Spec.Ports) == 0 {
			return fmt.Errorf("k8s service %s has no ports", k8sService.Name)
		}
		port = k8sService.Spec.Ports[0].Port
	}
	s.Nodes = []*registry.Node{
		{
			Id:       k8sService.Name,
			Address:  fmt.Sprintf("%s:%d", k8sService.Name, port),
			Metadata: s.Metadata,
		},
	}

	// Marshal micro service
	svcJson, _ := json.Marshal(s)
	return c.patchService(k8sService.Name, &patchStatusAnnotation{
		Op:    "replace",
		Path:  "/metadata/annotations/" + strings.ReplaceAll(statusAnnotationName, "/", "~1"),
		Value: string(svcJson),
	})
}

// Deregister nils out any things set in Register
func (c *kregistry) Deregister(s *registry.Service) error {
	k8sService, err := c.findService(s.Name)
	if err != nil {
		return err
	}

	return c.patchService(k8sService.Name, &patchStatusAnnotation{
		Op:   "delete",
		Path: "/metadata/annotations/" + strings.ReplaceAll(statusAnnotationName, "/", "~1"),
	})
}

func (c *kregistry) GetService(name string) ([]*registry.Service, error) {
	k8sService, err := c.findService(name)
	if err != nil {
		return nil, err
	}

	svc, err := c.unmarshalService(k8sService)
	if err != nil {
		return nil, err
	}
	return []*registry.Service{
		svc,
	}, nil
}

// ListServices will list all the service names
func (c *kregistry) ListServices() ([]*registry.Service, error) {
	k8sServices, err := c.findAllServices()
	if err != nil {
		return nil, err
	}

	var svcs []*registry.Service
	for _, k8sService := range k8sServices {
		svc, err := c.unmarshalService(&k8sService)
		if err != nil {
			continue
		}
		svcs = append(svcs, svc)
	}

	return svcs, nil
}

// Watch returns a kubernetes watcher
func (c *kregistry) Watch(opts ...registry.WatchOption) (registry.Watcher, error) {
	return newWatcher(c, opts...)
}

func (c *kregistry) String() string {
	return "kubernetes"
}

// NewRegistry creates a kubernetes registry
func NewRegistry(opts ...registry.Option) registry.Registry {
	k := &kregistry{
		options: registry.Options{},
	}
	err := configure(k, opts...)
	if err != nil {
		panic(err)
	}
	return k
}
