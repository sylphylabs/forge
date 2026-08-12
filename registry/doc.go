// Package registry defines the service registration and discovery contract.
//
// [Registrar] is the write side: the application lifecycle registers a
// [ServiceInstance] after its servers start and deregisters it before they
// stop. [Discovery] is the read side: clients resolve a service name to its
// current instances with Instances or follow changes with Watch. A
// [ServiceInstance] carries the registered identity — ID, name, version,
// metadata — and its endpoint URLs.
//
// This package holds only the contract. Implementations for Consul, etcd,
// Kubernetes, Nacos, and others live under contrib/registry/, each as its
// own module. Both interfaces take contexts because registry operations are
// remote calls; implementations must honor cancellation.
package registry
