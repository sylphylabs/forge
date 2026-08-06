package tracing

import (
	"context"
	"net"
	"net/url"
	"strconv"
	"strings"

	"github.com/sylphylabs/forge/metadata"
	"github.com/sylphylabs/forge/transport"
	"github.com/sylphylabs/forge/transport/http"

	"go.opentelemetry.io/otel/attribute"
	semconv "go.opentelemetry.io/otel/semconv/v1.41.0"
	"go.opentelemetry.io/otel/trace"
	"google.golang.org/grpc/peer"
	"google.golang.org/protobuf/proto"
)

func setClientSpan(ctx context.Context, span trace.Span, m any) {
	var (
		attrs     []attribute.KeyValue
		remote    string
		operation string
	)
	tr, ok := transport.FromClientContext(ctx)
	if ok {
		operation = tr.Operation()
		switch tr.Kind() {
		case transport.KindHTTP:
			if ht, ok := tr.(http.Transporter); ok {
				attrs = append(attrs, httpClientAttrs(ht)...)
				remote = ht.Request().Host
			}
		case transport.KindGRPC:
			remote, _ = parseTarget(tr.Endpoint())
			attrs = append(attrs, semconv.RPCSystemNameGRPC)
			_, methodAttrs := parseFullMethod(operation)
			attrs = append(attrs, methodAttrs...)
		}
	}
	if remote != "" {
		attrs = append(attrs, peerAttr(remote)...)
	}
	if p, ok := m.(proto.Message); ok {
		attrs = append(attrs, attribute.Key("send_msg.size").Int(proto.Size(p)))
	}

	span.SetAttributes(attrs...)
}

func setServerSpan(ctx context.Context, span trace.Span, m any) {
	var (
		attrs     []attribute.KeyValue
		remote    string
		operation string
	)
	tr, ok := transport.FromServerContext(ctx)
	if ok {
		operation = tr.Operation()
		switch tr.Kind() {
		case transport.KindHTTP:
			if ht, ok := tr.(http.Transporter); ok {
				attrs = append(attrs, httpServerAttrs(ht)...)
				remote = ht.Request().RemoteAddr
			}
		case transport.KindGRPC:
			attrs = append(attrs, semconv.RPCSystemNameGRPC)
			_, methodAttrs := parseFullMethod(operation)
			attrs = append(attrs, methodAttrs...)
			if p, ok := peer.FromContext(ctx); ok {
				remote = p.Addr.String()
			}
		}
	}
	attrs = append(attrs, peerAttr(remote)...)
	if p, ok := m.(proto.Message); ok {
		attrs = append(attrs, attribute.Key("recv_msg.size").Int(proto.Size(p)))
	}
	if md, ok := metadata.FromServerContext(ctx); ok {
		if service := md.Get(serviceHeader); service != "" {
			attrs = append(attrs, semconv.ServicePeerName(service))
		}
	}

	span.SetAttributes(attrs...)
}

// parseFullMethod returns a span name following the OpenTelemetry semantic
// conventions as well as all applicable span attribute.KeyValue attributes based
// on a gRPC's FullMethod.
func parseFullMethod(fullMethod string) (string, []attribute.KeyValue) {
	name, ok := strings.CutPrefix(fullMethod, "/")
	service, method, valid := strings.Cut(name, "/")
	if !ok || !valid || service == "" || method == "" || strings.ContainsRune(method, '/') {
		return fullMethod, []attribute.KeyValue{
			semconv.RPCMethod("_OTHER"),
			semconv.RPCMethodOriginal(fullMethod),
		}
	}
	return name, []attribute.KeyValue{semconv.RPCMethod(service + "/" + method)}
}

// peerAttr returns attributes about the peer address.
func peerAttr(addr string) []attribute.KeyValue {
	host, port, err := net.SplitHostPort(addr)
	if err != nil {
		return []attribute.KeyValue(nil)
	}

	if host == "" {
		host = "127.0.0.1"
	}

	attrs := []attribute.KeyValue{semconv.NetworkPeerAddress(host)}
	if port, err := strconv.Atoi(port); err == nil {
		attrs = append(attrs, semconv.NetworkPeerPort(port))
	}
	return attrs
}

func httpClientAttrs(transport http.Transporter) []attribute.KeyValue {
	request := transport.Request()
	attrs := []attribute.KeyValue{
		semconv.HTTPRequestMethodKey.String(request.Method),
		semconv.HTTPRoute(transport.PathTemplate()),
		semconv.URLFull(request.URL.String()),
	}
	if userAgent := request.UserAgent(); userAgent != "" {
		attrs = append(attrs, semconv.UserAgentOriginal(userAgent))
	}
	return attrs
}

func httpServerAttrs(transport http.Transporter) []attribute.KeyValue {
	request := transport.Request()
	attrs := []attribute.KeyValue{
		semconv.HTTPRequestMethodKey.String(request.Method),
		semconv.HTTPRoute(transport.PathTemplate()),
		semconv.URLPath(request.URL.Path),
	}
	if query := request.URL.RawQuery; query != "" {
		attrs = append(attrs, semconv.URLQuery(query))
	}
	if userAgent := request.UserAgent(); userAgent != "" {
		attrs = append(attrs, semconv.UserAgentOriginal(userAgent))
	}
	return attrs
}

func parseTarget(endpoint string) (address string, err error) {
	var u *url.URL
	u, err = url.Parse(endpoint)
	if err != nil {
		if u, err = url.Parse("http://" + endpoint); err != nil {
			return "", err
		}
		return u.Host, nil
	}
	if len(u.Path) > 1 {
		return u.Path[1:], nil
	}
	return endpoint, nil
}
