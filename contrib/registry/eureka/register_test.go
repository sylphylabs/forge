package eureka

import (
	"context"
	"fmt"
	"log"
	"sync"
	"testing"
	"time"

	"github.com/openkratos/kratos/registry"
)

func TestRegistry(_ *testing.T) {
	ctx := context.Background()
	ctx, cancel := context.WithCancel(ctx)
	s1 := &registry.ServiceInstance{
		ID:        "0",
		Name:      "helloworld",
		Endpoints: []string{"http://127.0.0.1:1111"},
	}
	s2 := &registry.ServiceInstance{
		ID:        "0",
		Name:      "helloworld2",
		Endpoints: []string{"http://127.0.0.1:222"},
	}

	r, _ := New([]string{"https://127.0.0.1:18761"}, WithContext(ctx), WithHeartbeat(time.Second), WithRefresh(time.Second), WithEurekaPath("eureka"))

	go do(r, s1)
	go do(r, s2)

	time.Sleep(time.Second * 20)
	cancel()
	time.Sleep(time.Second * 1)
}

func do(r *Registry, s *registry.ServiceInstance) {
	w, err := r.Watch(context.Background(), s.Name)
	if err != nil {
		log.Fatalf("Failed to watch service %q: %v", s.Name, err)
	}
	defer func() {
		_ = w.Stop()
	}()
	go func() {
		for {
			res, nextErr := w.Next()
			if nextErr != nil {
				return
			}
			log.Printf("watch: %d", len(res))
			for _, r := range res {
				log.Printf("next: %+v", r)
			}
		}
	}()

	ctx, cancel := context.WithCancel(context.Background())
	if err = r.Register(ctx, s); err != nil {
		log.Fatalf("Failed to register service %q: %v", s.Name, err)
	}

	time.Sleep(time.Second * 10)
	res, err := r.GetService(ctx, s.Name)
	if err != nil {
		log.Fatalf("Failed to get service %q: %v", s.Name, err)
	}
	for i, re := range res {
		log.Printf("first %d re:%v\n", i, re)
	}

	if len(res) != 1 && res[0].Name != s.Name {
		log.Fatalf("not expected: %+v", res)
	}

	if err = r.Deregister(ctx, s); err != nil {
		log.Fatalf("Failed to deregister service %q: %v", s.Name, err)
	}
	cancel()
	time.Sleep(time.Second * 10)

	res, err = r.GetService(ctx, s.Name)
	if err != nil {
		log.Fatalf("Failed to get service %q after deregister: %v", s.Name, err)
	}
	for i, re := range res {
		log.Printf("second %d re:%v\n", i, re)
	}
	if len(res) != 0 {
		log.Fatalf("not expected empty")
	}
}

func TestEndpointsCloneMetadata(t *testing.T) {
	r := new(Registry)
	service := &registry.ServiceInstance{
		ID:        "instance",
		Name:      "greeter",
		Version:   "v1",
		Endpoints: []string{"grpc://127.0.0.1:9000", "http://127.0.0.1:8000"},
		Metadata:  map[string]string{"zone": "a"},
	}

	endpoints := r.Endpoints(service)
	if len(endpoints) != 2 {
		t.Fatalf("Endpoints() returned %d endpoints, want 2", len(endpoints))
	}
	for i, endpoint := range endpoints {
		if got, want := endpoint.MetaData["Endpoints"], service.Endpoints[i]; got != want {
			t.Errorf("endpoint %d metadata = %q, want %q", i, got, want)
		}
	}
	endpoints[0].MetaData["zone"] = "b"
	if got := endpoints[1].MetaData["zone"]; got != "a" {
		t.Errorf("second endpoint metadata changed to %q", got)
	}
	if got := service.Metadata["zone"]; got != "a" {
		t.Errorf("service metadata changed to %q", got)
	}
}

func TestLock(_ *testing.T) {
	type me struct {
		lock sync.Mutex
	}

	a := &me{}
	go func() {
		defer a.lock.Unlock()
		a.lock.Lock()
		fmt.Println("This is fmt first.")
		time.Sleep(time.Second * 5)
	}()
	go func() {
		defer a.lock.Unlock()
		a.lock.Lock()
		fmt.Println("This is fmt second.")
		time.Sleep(time.Second * 5)
	}()
	time.Sleep(time.Second * 10)
}
