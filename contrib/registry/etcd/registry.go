package etcd

import (
	"context"
	"errors"
	"fmt"
	"math/rand/v2"
	"time"

	clientv3 "go.etcd.io/etcd/client/v3"

	"github.com/openkratos/kratos/registry"
)

var (
	_ registry.Registrar = (*Registry)(nil)
	_ registry.Discovery = (*Registry)(nil)
)

// Option is etcd registry option.
type Option func(o *options)

type options struct {
	ctx       context.Context
	namespace string
	ttl       time.Duration
	maxRetry  int
}

// Context with registry context.
func Context(ctx context.Context) Option {
	return func(o *options) { o.ctx = ctx }
}

// Namespace with registry namespace.
func Namespace(ns string) Option {
	return func(o *options) { o.namespace = ns }
}

// RegisterTTL with register ttl.
func RegisterTTL(ttl time.Duration) Option {
	return func(o *options) { o.ttl = ttl }
}

func MaxRetry(num int) Option {
	return func(o *options) { o.maxRetry = num }
}

// Registry is etcd registry.
type Registry struct {
	opts   *options
	client *clientv3.Client
	kv     clientv3.KV
	lease  clientv3.Lease
	/*
		ctxMap is used to store the context cancel function of each service instance.
		When the service instance is deregistered, the corresponding context cancel function is called to stop the heartbeat.
	*/
	ctxMap map[string]*serviceCancel

	registerFn  func(context.Context, string, string) (clientv3.LeaseID, error)
	keepAliveFn func(context.Context, clientv3.LeaseID) (<-chan *clientv3.LeaseKeepAliveResponse, error)
	retryDelay  func(int) time.Duration
}

type serviceCancel struct {
	service *registry.ServiceInstance
	cancel  context.CancelFunc
}

// New creates etcd registry
func New(client *clientv3.Client, opts ...Option) (r *Registry) {
	op := &options{
		ctx:       context.Background(),
		namespace: "/microservices",
		ttl:       time.Second * 15,
		maxRetry:  5,
	}
	for _, o := range opts {
		o(op)
	}
	r = &Registry{
		opts:   op,
		client: client,
		kv:     clientv3.NewKV(client),
		ctxMap: make(map[string]*serviceCancel),
	}
	r.registerFn = r.registerWithKV
	r.keepAliveFn = client.KeepAlive
	r.retryDelay = func(attempt int) time.Duration {
		return rand.N(time.Second << min(attempt, 5))
	}
	return r
}

// Register the registration.
func (r *Registry) Register(ctx context.Context, service *registry.ServiceInstance) error {
	key := r.registerKey(service)
	value, err := marshal(service)
	if err != nil {
		return err
	}
	if r.lease != nil {
		r.lease.Close()
	}
	r.lease = clientv3.NewLease(r.client)
	leaseID, err := r.registerWithKV(ctx, key, value)
	if err != nil {
		return err
	}

	hctx, cancel := context.WithCancel(r.opts.ctx)
	r.ctxMap[key] = &serviceCancel{
		service: service,
		cancel:  cancel,
	}
	go r.heartBeat(hctx, leaseID, key, value)
	return nil
}

func (r *Registry) registerKey(service *registry.ServiceInstance) string {
	return fmt.Sprintf("%s/%s/%s", r.opts.namespace, service.Name, service.ID)
}

// Deregister the registration.
func (r *Registry) Deregister(ctx context.Context, service *registry.ServiceInstance) error {
	defer func() {
		if r.lease != nil {
			r.lease.Close()
		}
	}()
	// cancel heartbeat
	key := r.registerKey(service)
	if serviceCancel, ok := r.ctxMap[key]; ok {
		serviceCancel.cancel()
		delete(r.ctxMap, key)
	}
	_, err := r.client.Delete(ctx, key)
	return err
}

// GetService return the service instances in memory according to the service name.
func (r *Registry) GetService(ctx context.Context, name string) ([]*registry.ServiceInstance, error) {
	key := r.serviceKey(name)
	resp, err := r.kv.Get(ctx, key, clientv3.WithPrefix())
	if err != nil {
		return nil, err
	}
	items := make([]*registry.ServiceInstance, 0, len(resp.Kvs))
	for _, kv := range resp.Kvs {
		si, err := unmarshal(kv.Value)
		if err != nil {
			return nil, err
		}
		if si.Name != name {
			continue
		}
		items = append(items, si)
	}
	return items, nil
}

func (r *Registry) serviceKey(name string) string {
	return fmt.Sprintf("%s/%s", r.opts.namespace, name)
}

// Watch creates a watcher according to the service name.
func (r *Registry) Watch(ctx context.Context, name string) (registry.Watcher, error) {
	key := r.serviceKey(name)
	return newWatcher(ctx, key, name, r.client)
}

// registerWithKV create a new lease, return current leaseID
func (r *Registry) registerWithKV(ctx context.Context, key string, value string) (clientv3.LeaseID, error) {
	grant, err := r.lease.Grant(ctx, int64(r.opts.ttl.Seconds()))
	if err != nil {
		return 0, err
	}
	_, err = r.client.Put(ctx, key, value, clientv3.WithLease(grant.ID))
	if err != nil {
		return 0, err
	}
	return grant.ID, nil
}

func (r *Registry) heartBeat(ctx context.Context, leaseID clientv3.LeaseID, key string, value string) {
	keepAlive, err := r.keepAliveFn(ctx, leaseID)
	if err != nil {
		keepAlive = nil
	}
	for {
		if keepAlive == nil {
			keepAlive, err = r.recoverKeepAlive(ctx, key, value)
			if err != nil {
				return
			}
		}

		select {
		case _, ok := <-keepAlive:
			if !ok {
				keepAlive = nil
			}
		case <-ctx.Done():
			return
		}
	}
}

func (r *Registry) recoverKeepAlive(ctx context.Context, key, value string) (<-chan *clientv3.LeaseKeepAliveResponse, error) {
	var lastErr error
	for attempt := 0; attempt < r.opts.maxRetry; attempt++ {
		attemptCtx, cancel := context.WithTimeout(ctx, 3*time.Second)
		leaseID, err := r.registerFn(attemptCtx, key, value)
		cancel()
		if err == nil {
			keepAlive, keepAliveErr := r.keepAliveFn(ctx, leaseID)
			if keepAliveErr == nil && keepAlive != nil {
				return keepAlive, nil
			}
			err = keepAliveErr
			if err == nil {
				err = errors.New("etcd keepalive returned a nil channel")
			}
		}
		lastErr = err
		if attempt+1 == r.opts.maxRetry {
			break
		}
		if err := sleepContext(ctx, r.retryDelay(attempt)); err != nil {
			return nil, err
		}
	}
	if lastErr == nil {
		lastErr = errors.New("etcd heartbeat recovery disabled")
	}
	return nil, fmt.Errorf("recover etcd registration after %d attempts: %w", r.opts.maxRetry, lastErr)
}

func sleepContext(ctx context.Context, delay time.Duration) error {
	timer := time.NewTimer(delay)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-timer.C:
		return nil
	}
}
