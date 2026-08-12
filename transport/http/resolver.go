package http

import (
	"context"
	"errors"
	"net/url"
	"strings"
	"sync"
	"time"
	"uuid"

	"github.com/sylphylabs/forge/internal/endpoint"
	"github.com/sylphylabs/forge/internal/subset"
	"github.com/sylphylabs/forge/log"
	"github.com/sylphylabs/forge/registry"
	"github.com/sylphylabs/forge/selector"
)

// Target is resolver target
type Target struct {
	Scheme     string
	Authority  string
	Endpoint   string
	PathPrefix string
}

func parseTarget(endpoint string, insecure bool) (*Target, error) {
	if !strings.Contains(endpoint, "://") {
		if insecure {
			endpoint = schemeHTTP + "://" + endpoint
		} else {
			endpoint = schemeHTTPS + "://" + endpoint
		}
	}
	u, err := url.Parse(endpoint)
	if err != nil {
		return nil, err
	}
	target := &Target{Scheme: u.Scheme, Authority: u.Host}
	path := strings.TrimPrefix(u.Path, "/")
	if u.Scheme == schemeDiscovery {
		target.Endpoint = path
		if target.Endpoint == "" {
			target.Endpoint = u.Host
		}
		return target, nil
	}
	if path != "" {
		target.PathPrefix = "/" + path
	}
	return target, nil
}

func (t *Target) requestURL(requestPath string) (string, error) {
	reference, err := url.Parse(requestPath)
	if err != nil {
		return "", err
	}
	escapedPath := joinEscapedPath(t.PathPrefix, reference.EscapedPath())
	path, err := url.PathUnescape(escapedPath)
	if err != nil {
		return "", err
	}
	requestURL := &url.URL{
		Scheme:   t.Scheme,
		Host:     t.Authority,
		Path:     path,
		RawPath:  escapedPath,
		RawQuery: reference.RawQuery,
		Fragment: reference.Fragment,
	}
	return requestURL.String(), nil
}

func joinEscapedPath(prefix, path string) string {
	prefix = strings.TrimSuffix(prefix, "/")
	path = strings.TrimPrefix(path, "/")
	if prefix == "" {
		return "/" + path
	}
	if path == "" {
		return prefix + "/"
	}
	return prefix + "/" + path
}

type resolver struct {
	rebalancer selector.Rebalancer

	target      *Target
	watcher     registry.Watcher
	selectorKey string
	subsetSize  int

	insecure bool

	ctx       context.Context
	cancel    context.CancelFunc
	closeOnce sync.Once
	closeErr  error
}

// newResolver starts watching target through discovery and applies every
// update to rebalancer until Close is called.
//
// The watch is rooted in context.Background and lives until Close: ctx bounds
// only the construction itself, so a caller may cancel it once newResolver
// returns — the usual `ctx, cancel := context.WithTimeout(...); defer cancel()`
// around client construction — without tearing down discovery. In block mode
// ctx is the deadline for the first successful update.
func newResolver(ctx context.Context, discovery registry.Discovery, target *Target,
	rebalancer selector.Rebalancer, block, insecure bool, subsetSize int,
) (*resolver, error) {
	watchCtx, cancel := context.WithCancel(context.Background())
	watcher, err := discovery.Watch(watchCtx, target.Endpoint)
	if err != nil {
		cancel()
		return nil, err
	}
	r := &resolver{
		target:      target,
		watcher:     watcher,
		rebalancer:  rebalancer,
		insecure:    insecure,
		selectorKey: uuid.NewV4().String(),
		subsetSize:  subsetSize,
		ctx:         watchCtx,
		cancel:      cancel,
	}
	if block {
		if err := ctx.Err(); err != nil {
			log.Error("http client watch service reached context deadline", "target", target)
			if closeErr := r.Close(); closeErr != nil {
				log.Error("failed to stop http client watcher", "target", target, "error", closeErr)
			}
			return nil, err
		}
		done := make(chan error, 1)
		go func() {
			for {
				services, err := watcher.Next(watchCtx)
				if err != nil {
					done <- err
					return
				}
				if r.update(services) {
					done <- nil
					return
				}
			}
		}()
		select {
		case err := <-done:
			if err != nil {
				if closeErr := r.Close(); closeErr != nil {
					log.Error("failed to stop http client watcher", "target", target, "error", closeErr)
				}
				return nil, err
			}
		case <-ctx.Done():
			log.Error("http client watch service reached context deadline", "target", target)
			if closeErr := r.Close(); closeErr != nil {
				log.Error("failed to stop http client watcher", "target", target, "error", closeErr)
			}
			return nil, ctx.Err()
		}
	}
	go r.watch()
	return r, nil
}

func (r *resolver) watch() {
	for {
		select {
		case <-r.ctx.Done():
			return
		default:
		}
		services, err := r.watcher.Next(r.ctx)
		if err != nil {
			if errors.Is(err, context.Canceled) {
				return
			}
			log.Error("http client watch service got unexpected error", "target", r.target, "error", err)
			select {
			case <-r.ctx.Done():
				return
			case <-time.After(time.Second):
			}
			continue
		}
		// Next may have returned an update raced with Close; a canceled
		// resolver must not apply it.
		select {
		case <-r.ctx.Done():
			return
		default:
		}
		r.update(services)
	}
}

func (r *resolver) update(services []*registry.ServiceInstance) bool {
	filtered := make([]*registry.ServiceInstance, 0, len(services))
	for _, ins := range services {
		ept, err := endpoint.ParseEndpoint(ins.Endpoints, endpoint.Scheme(schemeHTTP, !r.insecure))
		if err != nil {
			log.Error("failed to parse discovery endpoint", "target", r.target, "endpoints", ins.Endpoints, "error", err)
			continue
		}
		if ept == "" {
			continue
		}
		filtered = append(filtered, ins)
	}
	if r.subsetSize != 0 {
		filtered = subset.Subset(r.selectorKey, filtered, r.subsetSize)
	}
	nodes := make([]*selector.Node, 0, len(filtered))
	for _, ins := range filtered {
		ept, _ := endpoint.ParseEndpoint(ins.Endpoints, endpoint.Scheme(schemeHTTP, !r.insecure))
		nodes = append(nodes, selector.NewNode(schemeHTTP, ept, ins))
	}

	if len(nodes) == 0 {
		log.Warn("[http resolver] zero endpoint found, refused to write", "endpoint", r.target.Endpoint, "nodes", nodes)
		return false
	}
	r.rebalancer.Apply(nodes)
	return true
}

// Close terminates the watch goroutine and stops the watcher. It is
// idempotent; every call reports the outcome of the first.
func (r *resolver) Close() error {
	r.closeOnce.Do(func() {
		r.cancel()
		r.closeErr = r.watcher.Stop()
	})
	return r.closeErr
}
