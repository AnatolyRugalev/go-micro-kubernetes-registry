# Kubernetes go-micro registry

## Registry design

1. Each service should come with Kubernetes `Service` defined
2. Kubernetes `Service` should have label of this format: `kubernetes.micro.mu/service: "<service name>"` which should match running service name
3. Registration process adds dynamic annotation to such `Service`: `kubernetes.micro.mu/service-status: "<service status json>"`
4. Deregistration process drops annotation `kubernetes.micro.mu/service-status` of `Service`
5. ListServices finds all `Service` resources inside current Kubernetes namespace with label: `kubernetes.micro.mu/service`. You can list those services with this command: `kubectl get svc -l 'kubernetes.micro.mu/service'`
6. Watcher watches for changes of all `Service` resource which has label: `kubernetes.micro.mu/service` 

