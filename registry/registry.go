package registry

import (
	"context"
	"fmt"
	"maps"
	"slices"
)

// Registrar is service registrar.
type Registrar interface {
	// Register the registration.
	Register(ctx context.Context, service *ServiceInstance) error
	// Deregister the registration.
	Deregister(ctx context.Context, service *ServiceInstance) error
}

// Discovery is service discovery.
type Discovery interface {
	// GetService return the service instances in memory according to the service name.
	GetService(ctx context.Context, serviceName string) ([]*ServiceInstance, error)
	// Watch creates a watcher according to the service name.
	Watch(ctx context.Context, serviceName string) (Watcher, error)
}

// Watcher is service watcher.
type Watcher interface {
	// Next returns services in the following two cases:
	// 1.the first time to watch and the service instance list is not empty.
	// 2.any service instance changes found.
	// if the above two conditions are not met, it will block until context deadline exceeded or canceled
	Next() ([]*ServiceInstance, error)
	// Stop close the watcher.
	Stop() error
}

// ServiceInstance is an instance of a service in a discovery system.
type ServiceInstance struct {
	// ID is the unique instance ID as registered.
	ID string `json:"id"`
	// Name is the service name as registered.
	Name string `json:"name"`
	// Version is the version of the compiled.
	Version string `json:"version"`
	// Metadata is the kv pair metadata associated with the service instance.
	Metadata map[string]string `json:"metadata"`
	// Endpoints are endpoint addresses of the service instance.
	// schema:
	//   http://127.0.0.1:8000?isSecure=false
	//   grpc://127.0.0.1:9000?isSecure=false
	Endpoints []string `json:"endpoints"`
}

func (i *ServiceInstance) String() string {
	return fmt.Sprintf("%s-%s", i.Name, i.ID)
}

// Equal returns whether i and o are equivalent.
func (i *ServiceInstance) Equal(o any) bool {
	if o == nil {
		return i == nil
	}
	t, ok := o.(*ServiceInstance)
	if !ok {
		return false
	}
	if i == nil || t == nil {
		return i == t
	}

	if i.ID != t.ID || i.Name != t.Name || i.Version != t.Version {
		return false
	}
	if !maps.Equal(i.Metadata, t.Metadata) {
		return false
	}
	if len(i.Endpoints) != len(t.Endpoints) {
		return false
	}

	iEndpoints := slices.Clone(i.Endpoints)
	tEndpoints := slices.Clone(t.Endpoints)
	slices.Sort(iEndpoints)
	slices.Sort(tEndpoints)
	return slices.Equal(iEndpoints, tEndpoints)
}
