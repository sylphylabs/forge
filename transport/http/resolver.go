package http

import (
	"context"
	"errors"
	"net/url"
	"strings"
	"time"

	"github.com/google/uuid"

	"github.com/openkratos/kratos/internal/endpoint"
	"github.com/openkratos/kratos/internal/subset"
	"github.com/openkratos/kratos/log"
	"github.com/openkratos/kratos/registry"
	"github.com/openkratos/kratos/selector"
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
}

func newResolver(ctx context.Context, discovery registry.Discovery, target *Target,
	rebalancer selector.Rebalancer, block, insecure bool, subsetSize int,
) (*resolver, error) {
	// this is new resolver
	watcher, err := discovery.Watch(ctx, target.Endpoint)
	if err != nil {
		return nil, err
	}
	r := &resolver{
		target:      target,
		watcher:     watcher,
		rebalancer:  rebalancer,
		insecure:    insecure,
		selectorKey: uuid.New().String(),
		subsetSize:  subsetSize,
	}
	if block {
		done := make(chan error, 1)
		go func() {
			for {
				services, err := watcher.Next()
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
				stopErr := watcher.Stop()
				if stopErr != nil {
					log.Error("failed to stop http client watcher", "target", target, "error", stopErr)
				}
				return nil, err
			}
		case <-ctx.Done():
			log.Error("http client watch service reached context deadline", "target", target)
			stopErr := watcher.Stop()
			if stopErr != nil {
				log.Error("failed to stop http client watcher", "target", target, "error", stopErr)
			}
			return nil, ctx.Err()
		}
	}
	go func() {
		for {
			services, err := watcher.Next()
			if err != nil {
				if errors.Is(err, context.Canceled) {
					return
				}
				log.Error("http client watch service got unexpected error", "target", target, "error", err)
				time.Sleep(time.Second)
				continue
			}
			r.update(services)
		}
	}()
	return r, nil
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
	nodes := make([]selector.Node, 0, len(filtered))
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

func (r *resolver) Close() error {
	return r.watcher.Stop()
}
