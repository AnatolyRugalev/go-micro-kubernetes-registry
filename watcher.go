package kubernetes

import (
	"errors"
	"k8s.io/apimachinery/pkg/watch"
	"k8s.io/client-go/kubernetes/scheme"

	"github.com/micro/go-micro/registry"
	"github.com/micro/go-micro/util/log"
	v1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

type k8sWatcher struct {
	registry *kregistry
	watcher  watch.Interface
	next     chan *registry.Result
}

// handleEvent will taken an event from the k8s pods API and do the correct
// things with the result, based on the local cache.
func (k *k8sWatcher) handleEvent(event watch.Event) {
	var k8sService v1.Service

	err := scheme.Scheme.Convert(event.Object, &k8sService, nil)

	if err != nil {
		log.Log("K8s Watcher: coundn't convert event.Object to service")
		return
	}

	service, err := k.registry.unmarshalService(&k8sService)
	if err != nil {
		log.Logf("K8s Watcher: cannot unmarshal service: %v", err)
		return
	}

	switch event.Type {
	case watch.Added:
		k.next <- &registry.Result{
			Action:  "create",
			Service: service,
		}
	case watch.Modified:
		k.next <- &registry.Result{
			Action:  "update",
			Service: service,
		}
	case watch.Deleted:
		k.next <- &registry.Result{
			Action:  "delete",
			Service: service,
		}
	}
}

// Next will block until a new result comes in
func (k *k8sWatcher) Next() (*registry.Result, error) {
	r, ok := <-k.next
	if !ok {
		return nil, errors.New("result chan closed")
	}
	return r, nil
}

// Stop will cancel any requests, and close channels
func (k *k8sWatcher) Stop() {
	k.watcher.Stop()

	select {
	case <-k.next:
		return
	default:
		close(k.next)
	}
}

func newWatcher(kr *kregistry, opts ...registry.WatchOption) (registry.Watcher, error) {
	var wo registry.WatchOptions
	for _, o := range opts {
		o(&wo)
	}

	watcher, err := kr.client.Services("").Watch(metav1.ListOptions{
		LabelSelector: serviceLabelName,
	})

	if err != nil {
		return nil, err
	}

	k := &k8sWatcher{
		registry: kr,
		watcher:  watcher,
		next:     make(chan *registry.Result),
	}

	// range over watch request changes, and invoke
	// the update event
	go func() {
		for event := range watcher.ResultChan() {
			k.handleEvent(event)
		}
		k.Stop()
	}()

	return k, nil
}
