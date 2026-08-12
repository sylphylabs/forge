package registry

import (
	"maps"
	"slices"
	"sync"
	"sync/atomic"
	"testing"
)

func TestServiceInstanceEqual(t *testing.T) {
	tests := []struct {
		name   string
		change func(*ServiceInstance)
		want   bool
	}{
		{
			name: "equivalent with endpoints in a different order",
			change: func(instance *ServiceInstance) {
				instance.Endpoints = []string{
					instance.Endpoints[2],
					instance.Endpoints[0],
					instance.Endpoints[3],
					instance.Endpoints[1],
				}
			},
			want: true,
		},
		{
			name: "different ID",
			change: func(instance *ServiceInstance) {
				instance.ID = "instance-2"
			},
		},
		{
			name: "different name",
			change: func(instance *ServiceInstance) {
				instance.Name = "payments"
			},
		},
		{
			name: "different version",
			change: func(instance *ServiceInstance) {
				instance.Version = "v2"
			},
		},
		{
			name: "different endpoint",
			change: func(instance *ServiceInstance) {
				instance.Endpoints[0] = "grpc://127.0.0.1:9100"
			},
		},
		{
			name: "different endpoint multiplicity",
			change: func(instance *ServiceInstance) {
				instance.Endpoints[0] = instance.Endpoints[1]
			},
		},
		{
			name: "fewer endpoints",
			change: func(instance *ServiceInstance) {
				instance.Endpoints = instance.Endpoints[:len(instance.Endpoints)-1]
			},
		},
		{
			name: "different metadata value",
			change: func(instance *ServiceInstance) {
				instance.Metadata["region"] = "us-east"
			},
		},
		{
			name: "different metadata key",
			change: func(instance *ServiceInstance) {
				delete(instance.Metadata, "region")
				instance.Metadata["zone"] = "tokyo-1"
			},
		},
		{
			name: "additional metadata",
			change: func(instance *ServiceInstance) {
				instance.Metadata["protocol"] = "grpc"
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			left := newTestServiceInstance()
			right := cloneServiceInstance(left)
			tt.change(right)

			if got := left.Equal(right); got != tt.want {
				t.Fatalf("Equal() = %t, want %t", got, tt.want)
			}
		})
	}
}

func TestServiceInstanceEqualNil(t *testing.T) {
	var nilInstance *ServiceInstance
	tests := []struct {
		name     string
		receiver *ServiceInstance
		other    *ServiceInstance
		want     bool
	}{
		{
			name: "nil receiver and nil argument",
			want: true,
		},
		{
			name:  "nil receiver and typed nil",
			other: nilInstance,
			want:  true,
		},
		{
			name:     "instance and nil argument",
			receiver: &ServiceInstance{},
		},
		{
			name:  "nil receiver and instance",
			other: &ServiceInstance{},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := tt.receiver.Equal(tt.other); got != tt.want {
				t.Fatalf("Equal() = %t, want %t", got, tt.want)
			}
		})
	}
}

func TestServiceInstanceEqualEmptyCollections(t *testing.T) {
	left := &ServiceInstance{}
	right := &ServiceInstance{
		Metadata:  map[string]string{},
		Endpoints: []string{},
	}
	if !left.Equal(right) {
		t.Fatal("Equal() = false, want true for nil and empty collections")
	}
}

func TestServiceInstanceEqualDoesNotMutateEndpoints(t *testing.T) {
	left := newTestServiceInstance()
	right := cloneServiceInstance(left)
	slices.Reverse(right.Endpoints)
	leftBefore := slices.Clone(left.Endpoints)
	rightBefore := slices.Clone(right.Endpoints)

	if !left.Equal(right) {
		t.Fatal("Equal() = false, want true")
	}
	if !slices.Equal(left.Endpoints, leftBefore) {
		t.Errorf("left endpoints changed from %v to %v", leftBefore, left.Endpoints)
	}
	if !slices.Equal(right.Endpoints, rightBefore) {
		t.Errorf("right endpoints changed from %v to %v", rightBefore, right.Endpoints)
	}
}

func TestServiceInstanceEqualConcurrent(t *testing.T) {
	left := newTestServiceInstance()
	right := cloneServiceInstance(left)
	slices.Reverse(right.Endpoints)

	start := make(chan struct{})
	var mismatch atomic.Bool
	var wg sync.WaitGroup
	for range 32 {
		wg.Go(func() {
			<-start
			for range 100 {
				if !left.Equal(right) {
					mismatch.Store(true)
					return
				}
			}
		})
	}
	close(start)
	wg.Wait()

	if mismatch.Load() {
		t.Fatal("concurrent Equal() call returned false for equivalent instances")
	}
}

func newTestServiceInstance() *ServiceInstance {
	return &ServiceInstance{
		ID:      "instance-1",
		Name:    "orders",
		Version: "v1",
		Metadata: map[string]string{
			"region": "ap-northeast",
			"weight": "100",
		},
		Endpoints: []string{
			"grpc://127.0.0.1:9000",
			"http://127.0.0.1:8000",
			"metrics://127.0.0.1:7000",
			"grpc://127.0.0.1:9000",
		},
	}
}

func cloneServiceInstance(instance *ServiceInstance) *ServiceInstance {
	clone := *instance
	clone.Metadata = maps.Clone(instance.Metadata)
	clone.Endpoints = slices.Clone(instance.Endpoints)
	return &clone
}
