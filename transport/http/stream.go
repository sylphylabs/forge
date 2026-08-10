package http

import (
	"bufio"
	"bytes"
	"context"
	stderrors "errors"
	"fmt"
	"io"
	stdhttp "net/http"
	"strings"
	"sync"
	"time"

	"github.com/gorilla/websocket"

	"github.com/sylphylabs/forge/encoding"
	kerrors "github.com/sylphylabs/forge/errors"
	"github.com/sylphylabs/forge/internal/httputil"
	"github.com/sylphylabs/forge/middleware"
	"github.com/sylphylabs/forge/selector"
	"github.com/sylphylabs/forge/transport"
)

const (
	sseContentType = "text/event-stream"

	websocketControlPrefix = "\x1e"
	websocketControlEnd    = websocketControlPrefix + "end"
	websocketControlError  = websocketControlPrefix + "error:"
)

type streamMode int

const (
	streamModeSSE streamMode = iota + 1
	streamModeWebSocket
)

// ServerStream is one server-side HTTP stream, carried by SSE or by a
// WebSocket connection.
//
// The metadata methods take an [stdhttp.Header] rather than a gRPC metadata
// map. The two are the same shape, and using the HTTP type keeps a
// pure-HTTP application from linking the gRPC runtime to serve a stream.
type ServerStream interface {
	// SetHeader adds to the header sent with the first message. Calling it
	// after the header has been sent has no effect on the wire.
	SetHeader(stdhttp.Header) error
	// SendHeader adds to and then sends the header.
	SendHeader(stdhttp.Header) error
	// SetTrailer adds to the trailer sent when the stream ends.
	SetTrailer(stdhttp.Header)
	// Context returns the stream's context.
	Context() context.Context
	// SendMsg encodes and sends one message.
	SendMsg(any) error
	// RecvMsg receives and decodes the next message.
	RecvMsg(any) error

	Close(error) error
	SetContext(context.Context)
	SetReadDeadline(t time.Time) error
	SetWriteDeadline(t time.Time) error
}

// ClientStream is one client-side HTTP stream.
//
// It declares only what every client stream can honour, whichever wire
// carries it. Sending is not part of that set: SSE is one-directional by
// definition, so an SSE stream can no more send a message than a listener can
// dial. Streams that do have a sending half implement [SendingClientStream]
// as well, and a caller discovers that by type assertion, the way it
// discovers [transport.Healthzer] on a server.
//
// CloseSend is here rather than on the sending capability because it means
// "no more messages from me" — a statement a receive-only stream can make
// truthfully, and one that releases the underlying response body. Callers can
// therefore close every stream they open without asking what it is.
type ClientStream interface {
	// Header blocks until the response header is available.
	Header() (stdhttp.Header, error)
	// Trailer returns the trailer, valid once the stream has ended.
	Trailer() stdhttp.Header
	// CloseSend closes the sending half.
	CloseSend() error
	// Context returns the stream's context.
	Context() context.Context
	// RecvMsg receives and decodes the next message. It reports io.EOF once
	// the server has ended the stream normally.
	RecvMsg(any) error
}

// SendingClientStream is implemented by client streams that have a sending
// half, currently the WebSocket stream. It is an optional capability
// alongside [ClientStream]: consumers type-assert for it, and a stream that
// does not implement it cannot be asked to send at all.
//
// Splitting it out is what turns "this transport cannot send" from a runtime
// error string into a fact the compiler and a type assertion can both see.
type SendingClientStream interface {
	ClientStream

	// SendMsg encodes and sends one message.
	SendMsg(any) error
	// CloseAndRecv closes the sending half and receives the single reply that
	// terminates a client-streaming call.
	CloseAndRecv(any) error
}

type serverStream struct {
	ctx       context.Context
	req       *stdhttp.Request
	res       stdhttp.ResponseWriter
	mode      streamMode
	conn      *websocket.Conn
	header    stdhttp.Header
	trailer   stdhttp.Header
	encoder   encoding.Codec
	decoder   encoding.Codec
	started   bool
	writeMu   sync.Mutex
	upgrader  websocket.Upgrader
	bodyField string
}

// ServerStreamOption customizes a server stream created by the HTTP transport.
type ServerStreamOption func(*serverStream)

// WithStreamBodyField declares the request message field that carries each streamed
// frame's payload. It is used for client-streaming RPCs whose HTTP rule maps a named
// body field (e.g. body: "data"): every received frame is decoded into that field while
// the remaining fields are bound from the request query and path vars.
func WithStreamBodyField(name string) ServerStreamOption {
	return func(s *serverStream) {
		s.bodyField = name
	}
}

// NewServerSentEventServerStream returns a stream that writes server messages as SSE events.
func NewServerSentEventServerStream(ctx Context) ServerStream {
	s := &serverStream{
		ctx:  detachStreamContext(ctx),
		req:  ctx.Request(),
		res:  ctx.Response(),
		mode: streamModeSSE,
	}
	s.encoder = streamCodecFromHeaders(s.req.Header, "Accept", "Content-Type")
	s.decoder = streamCodecFromHeaders(s.req.Header, "Content-Type", "Accept")
	return s
}

// NewWebSocketServerStream upgrades the current request and returns a WebSocket stream.
func NewWebSocketServerStream(ctx Context, opts ...ServerStreamOption) (ServerStream, error) {
	s := &serverStream{
		ctx:  detachStreamContext(ctx),
		req:  ctx.Request(),
		res:  ctx.Response(),
		mode: streamModeWebSocket,
	}
	for _, opt := range opts {
		opt(s)
	}
	s.encoder = streamCodecFromHeaders(s.req.Header, "Accept", "Content-Type")
	s.decoder = streamCodecFromHeaders(s.req.Header, "Content-Type", "Accept")
	conn, err := s.upgrader.Upgrade(ctx.Response(), ctx.Request(), nil)
	if err != nil {
		return nil, err
	}
	s.conn = conn
	return s, nil
}

// SetContext stores the streaming handler context. The server timeout and
// cancellation are detached so a long-lived stream is not torn down by the
// per-request server timeout; only the context values (tracing, auth, metadata
// injected by middleware) are preserved. The stream lifecycle is instead driven
// by Send/Recv errors and the read/write deadlines set via SetReadDeadline and
// SetWriteDeadline. This mirrors how the gRPC transport leaves streams on the
// connection-scoped context rather than the per-request timeout context.
func (s *serverStream) SetContext(ctx context.Context) {
	s.ctx = detachStreamContext(ctx)
}

// detachStreamContext returns a context that keeps the values of ctx but drops
// its deadline and cancellation, so the per-request server timeout does not
// abort a long-lived stream.
func detachStreamContext(ctx context.Context) context.Context {
	if ctx == nil {
		return context.Background()
	}
	return context.WithoutCancel(ctx)
}

// SetReadDeadline sets the deadline for future Recv calls. A zero value for t
// disables the deadline. For WebSocket streams it is applied to the underlying
// connection; for SSE streams it is applied via http.ResponseController.
func (s *serverStream) SetReadDeadline(t time.Time) error {
	switch s.mode {
	case streamModeWebSocket:
		if s.conn == nil {
			return stderrors.New("http: websocket connection not established")
		}
		return s.conn.SetReadDeadline(t)
	case streamModeSSE:
		return stdhttp.NewResponseController(s.res).SetReadDeadline(t)
	default:
		return stderrors.New("unknown HTTP stream mode")
	}
}

// SetWriteDeadline sets the deadline for future Send calls. A zero value for t
// disables the deadline. For WebSocket streams it is serialized against in-flight
// writes via the stream's write mutex; for SSE streams it is applied via
// http.ResponseController.
func (s *serverStream) SetWriteDeadline(t time.Time) error {
	switch s.mode {
	case streamModeWebSocket:
		if s.conn == nil {
			return stderrors.New("http: websocket connection not established")
		}
		s.writeMu.Lock()
		defer s.writeMu.Unlock()
		return s.conn.SetWriteDeadline(t)
	case streamModeSSE:
		return stdhttp.NewResponseController(s.res).SetWriteDeadline(t)
	default:
		return stderrors.New("unknown HTTP stream mode")
	}
}

func (s *serverStream) SetHeader(md stdhttp.Header) error {
	s.header = joinHeader(s.header, md)
	if s.mode == streamModeSSE && !s.started {
		copyMetadataToHeader(s.res.Header(), md)
	}
	return nil
}

func (s *serverStream) SendHeader(md stdhttp.Header) error {
	if err := s.SetHeader(md); err != nil {
		return err
	}
	if s.mode == streamModeSSE {
		s.startSSE()
	}
	return nil
}

func (s *serverStream) SetTrailer(md stdhttp.Header) {
	s.trailer = joinHeader(s.trailer, md)
}

func (s *serverStream) Context() context.Context {
	if s.ctx == nil {
		return context.Background()
	}
	return s.ctx
}

// recvMessage decodes the next frame. When a named body field is declared the frame
// carries only that field's payload, so it is decoded into a freshly allocated sub-message
// and assigned back onto m; otherwise the frame is decoded into m directly. The generator
// only declares a body field for a singular message-kind field, so a mismatch here is a
// programming error and is reported rather than silently ignored.
func (s *serverStream) recvMessage(m any) error {
	if s.mode != streamModeWebSocket {
		return io.EOF
	}
	if s.bodyField == "" {
		return readWebSocketMessage(s.conn, m, s.decoder)
	}
	if !schemaOwns(m) {
		return fmt.Errorf("http: stream body field %q needs the schema runtime; "+
			"import transport/http/transcoding", s.bodyField)
	}
	return schema.DecodeField(m, s.bodyField, func(target any) error {
		return readWebSocketMessage(s.conn, target, s.decoder)
	})
}

func (s *serverStream) SendMsg(m any) error {
	switch s.mode {
	case streamModeSSE:
		return s.sendSSE("message", m)
	case streamModeWebSocket:
		return s.writeWebSocketMessage(m)
	default:
		return stderrors.New("unknown HTTP stream mode")
	}
}

func (s *serverStream) RecvMsg(m any) error {
	if err := s.recvMessage(m); err != nil {
		return err
	}
	if s.req != nil {
		if err := DefaultRequestQuery(s.req, m); err != nil {
			return err
		}
		if err := DefaultRequestVars(s.req, m); err != nil {
			return err
		}
	}
	return nil
}

func (s *serverStream) Close(err error) error {
	switch s.mode {
	case streamModeSSE:
		if err == nil {
			return nil
		}
		if !s.started {
			return err
		}
		if data, marshalErr := marshalProblem(kerrors.PublicOf(err)); marshalErr == nil {
			_ = s.sendSSEData("error", data)
		}
		return nil
	case streamModeWebSocket:
		if s.conn == nil {
			return err
		}
		if err != nil {
			if data, marshalErr := marshalProblem(kerrors.PublicOf(err)); marshalErr == nil {
				_ = s.writeWebSocketControl(websocketControlError + string(data))
			}
			_ = s.writeWebSocketClose(websocket.CloseInternalServerErr, "")
			_ = s.conn.Close()
			return nil
		}
		_ = s.writeWebSocketClose(websocket.CloseNormalClosure, "")
		return s.conn.Close()
	default:
		return err
	}
}

func (s *serverStream) startSSE() {
	if s.started {
		return
	}
	h := s.res.Header()
	h.Set("Content-Type", sseContentType)
	h.Set("Cache-Control", "no-cache")
	h.Set("Connection", "keep-alive")
	h.Set("X-Accel-Buffering", "no")
	copyMetadataToHeader(h, s.header)
	s.res.WriteHeader(stdhttp.StatusOK)
	s.started = true
}

func (s *serverStream) sendSSE(event string, v any) error {
	data, err := marshalStreamMessage(v, s.encoder)
	if err != nil {
		return err
	}
	return s.sendSSEData(event, data)
}

func (s *serverStream) sendSSEData(event string, data []byte) error {
	s.writeMu.Lock()
	defer s.writeMu.Unlock()
	s.startSSE()
	if _, err := fmt.Fprintf(s.res, "event: %s\n", event); err != nil {
		return err
	}
	for _, line := range bytes.Split(data, []byte("\n")) {
		if _, err := fmt.Fprintf(s.res, "data: %s\n", line); err != nil {
			return err
		}
	}
	if _, err := io.WriteString(s.res, "\n"); err != nil {
		return err
	}
	if flusher, ok := s.res.(stdhttp.Flusher); ok {
		flusher.Flush()
	}
	return nil
}

func (s *serverStream) writeWebSocketMessage(m any) error {
	data, err := marshalStreamMessage(m, s.encoder)
	if err != nil {
		return err
	}
	s.writeMu.Lock()
	defer s.writeMu.Unlock()
	return s.conn.WriteMessage(websocket.TextMessage, data)
}

func (s *serverStream) writeWebSocketControl(message string) error {
	s.writeMu.Lock()
	defer s.writeMu.Unlock()
	return s.conn.WriteMessage(websocket.TextMessage, []byte(message))
}

func (s *serverStream) writeWebSocketClose(code int, text string) error {
	s.writeMu.Lock()
	defer s.writeMu.Unlock()
	msg := websocket.FormatCloseMessage(code, text)
	return s.conn.WriteControl(websocket.CloseMessage, msg, time.Now().Add(time.Second))
}

// The two client streams differ in exactly one capability, and these
// assertions are where that difference is stated: the SSE stream must never
// acquire a sending half, because the wire it rides on has none.
var (
	_ ClientStream        = (*sseClientStream)(nil)
	_ SendingClientStream = (*websocketClientStream)(nil)
)

type sseClientStream struct {
	ctx       context.Context
	res       *stdhttp.Response
	scanner   *bufio.Scanner
	decoder   encoding.Codec
	closeOnce sync.Once
	closeErr  error
}

func newSSEClientStream(ctx context.Context, res *stdhttp.Response, decoder encoding.Codec) ClientStream {
	scanner := bufio.NewScanner(res.Body)
	scanner.Buffer(make([]byte, 0, 64*1024), 1024*1024)
	return &sseClientStream{ctx: ctx, res: res, scanner: scanner, decoder: decoder}
}

func (s *sseClientStream) Header() (stdhttp.Header, error) {
	return s.res.Header.Clone(), nil
}

func (s *sseClientStream) Trailer() stdhttp.Header {
	return s.res.Trailer.Clone()
}

func (s *sseClientStream) CloseSend() error {
	return s.closeBody()
}

func (s *sseClientStream) Context() context.Context {
	if s.ctx == nil {
		return context.Background()
	}
	return s.ctx
}

func (s *sseClientStream) RecvMsg(m any) error {
	for {
		event, data, err := s.readEvent()
		if err != nil {
			_ = s.closeBody()
			return err
		}
		switch event {
		case "", "message":
			if err := unmarshalStreamMessage(data, m, s.decoder); err != nil {
				_ = s.closeBody()
				return err
			}
			return nil
		case "error":
			_ = s.closeBody()
			if se, ok := unmarshalProblem(ProblemContentType, data, NoStatus); ok {
				return se
			}
			return stderrors.New(string(data))
		}
	}
}

func (s *sseClientStream) closeBody() error {
	if s.res == nil || s.res.Body == nil {
		return nil
	}
	s.closeOnce.Do(func() {
		s.closeErr = s.res.Body.Close()
	})
	return s.closeErr
}

func (s *sseClientStream) readEvent() (string, []byte, error) {
	var (
		event string
		data  bytes.Buffer
	)
	for s.scanner.Scan() {
		line := s.scanner.Text()
		if line == "" {
			if event == "" && data.Len() == 0 {
				continue
			}
			return event, bytes.TrimSuffix(data.Bytes(), []byte("\n")), nil
		}
		switch {
		case strings.HasPrefix(line, "event:"):
			event = strings.TrimSpace(strings.TrimPrefix(line, "event:"))
		case strings.HasPrefix(line, "data:"):
			value := strings.TrimPrefix(line, "data:")
			value = strings.TrimPrefix(value, " ")
			data.WriteString(value)
			data.WriteByte('\n')
		}
	}
	if err := s.scanner.Err(); err != nil {
		return "", nil, err
	}
	return "", nil, io.EOF
}

type websocketClientStream struct {
	ctx        context.Context
	conn       *websocket.Conn
	header     stdhttp.Header
	done       func(error)
	encoder    encoding.Codec
	decoder    encoding.Codec
	mu         sync.Mutex
	sendClosed bool
	closed     bool
	closeOnce  sync.Once
	closeErr   error
	writeMu    sync.Mutex
}

func (s *websocketClientStream) Header() (stdhttp.Header, error) {
	return s.header.Clone(), nil
}

func (s *websocketClientStream) Trailer() stdhttp.Header {
	return nil
}

func (s *websocketClientStream) CloseSend() error {
	s.mu.Lock()
	if s.sendClosed || s.closed {
		s.mu.Unlock()
		return nil
	}
	s.sendClosed = true
	s.mu.Unlock()
	return s.writeControl(websocketControlEnd)
}

func (s *websocketClientStream) Context() context.Context {
	if s.ctx == nil {
		return context.Background()
	}
	return s.ctx
}

func (s *websocketClientStream) CloseAndRecv(m any) error {
	if err := s.CloseSend(); err != nil {
		return err
	}
	defer s.close(nil)
	return s.RecvMsg(m)
}

func (s *websocketClientStream) SendMsg(m any) error {
	if err := s.checkSendOpen(); err != nil {
		return err
	}
	data, err := marshalStreamMessage(m, s.encoder)
	if err != nil {
		return err
	}
	s.writeMu.Lock()
	defer s.writeMu.Unlock()
	if err := s.checkSendOpen(); err != nil {
		return err
	}
	return s.conn.WriteMessage(websocket.TextMessage, data)
}

func (s *websocketClientStream) RecvMsg(m any) error {
	if err := readWebSocketMessage(s.conn, m, s.decoder); err != nil {
		doneErr := err
		if stderrors.Is(err, io.EOF) {
			doneErr = nil
		}
		_ = s.close(doneErr)
		return err
	}
	return nil
}

func (s *websocketClientStream) writeControl(message string) error {
	s.writeMu.Lock()
	defer s.writeMu.Unlock()
	return s.conn.WriteMessage(websocket.TextMessage, []byte(message))
}

func (s *websocketClientStream) close(err error) error {
	s.closeOnce.Do(func() {
		s.mu.Lock()
		s.closed = true
		s.sendClosed = true
		s.mu.Unlock()
		if s.done != nil {
			s.done(err)
		}
		s.writeMu.Lock()
		defer s.writeMu.Unlock()
		_ = s.conn.WriteControl(websocket.CloseMessage, websocket.FormatCloseMessage(websocket.CloseNormalClosure, ""), time.Now().Add(time.Second))
		s.closeErr = s.conn.Close()
	})
	return s.closeErr
}

func (s *websocketClientStream) checkSendOpen() error {
	s.mu.Lock()
	defer s.mu.Unlock()
	switch {
	case s.sendClosed:
		return stderrors.New("websocket client stream send side is closed")
	case s.closed:
		return stderrors.New("websocket client stream is closed")
	default:
		return nil
	}
}

// ServerSentEvent opens an HTTP server-streaming call and receives replies as SSE events.
func (client *Client) ServerSentEvent(ctx context.Context, method, path string, args any, opts ...CallOption) (ClientStream, error) {
	var (
		contentType string
		body        io.Reader
	)
	c := defaultCallInfo(path)
	for _, o := range opts {
		if err := o.before(&c); err != nil {
			return nil, err
		}
	}
	if args != nil {
		data, err := client.opts.encoder(ctx, c.contentType, args)
		if err != nil {
			return nil, err
		}
		contentType = c.contentType
		body = bytes.NewReader(data)
	} else if c.contentTypeSet {
		contentType = c.contentType
	}
	url := fmt.Sprintf("%s://%s%s", client.target.Scheme, client.target.Authority, path)
	req, err := stdhttp.NewRequest(method, url, body)
	if err != nil {
		return nil, err
	}
	prepareClientRequest(client, req, contentType, c)
	ctx = transport.NewClientContext(ctx, &Transport{
		endpoint:     client.opts.endpoint,
		reqHeader:    headerCarrier(req.Header),
		operation:    c.operation,
		request:      req,
		pathTemplate: c.pathTemplate,
	})
	h := func(ctx context.Context, _ any) (any, error) {
		res, doErr := client.do(req.WithContext(ctx)) //nolint:bodyclose // newSSEClientStream owns and closes res.Body on success.
		if res != nil {
			cs := csAttempt{res: res}
			for _, o := range opts {
				o.after(&c, &cs)
			}
		}
		if doErr != nil {
			if res != nil {
				_ = res.Body.Close()
			}
			return nil, doErr
		}
		return newSSEClientStream(ctx, res, streamCodecFromCallInfo(c, "Accept", "Content-Type")), nil
	}
	var p selector.Peer
	ctx = selector.NewPeerContext(ctx, &p)
	if len(client.opts.middleware) > 0 {
		h = middleware.ChainUnary(client.opts.middleware...)(h)
	}
	stream, err := h(ctx, args)
	if err != nil {
		return nil, err
	}
	return clientStreamFromHandler(stream)
}

// WebSocket opens an HTTP bidirectional streaming call over WebSocket.
//
// The return type is the sending capability rather than the bare
// [ClientStream], so a caller that opened a WebSocket can send without
// asserting for a capability the wire always has.
func (client *Client) WebSocket(ctx context.Context, path string, opts ...CallOption) (SendingClientStream, error) {
	c := defaultCallInfo(path)
	for _, o := range opts {
		if err := o.before(&c); err != nil {
			return nil, err
		}
	}
	scheme := "ws"
	if client.target.Scheme == schemeHTTPS {
		scheme = "wss"
	}
	url := fmt.Sprintf("%s://%s%s", scheme, client.target.Authority, path)
	header := stdhttp.Header{}
	if c.headerCarrier != nil {
		header = *c.headerCarrier
	}
	if c.accept != "" {
		header.Set("Accept", c.accept)
	}
	if c.contentTypeSet {
		header.Set("Content-Type", c.contentType)
	}
	if client.opts.userAgent != "" {
		header.Set("User-Agent", client.opts.userAgent)
	}
	req, err := stdhttp.NewRequestWithContext(ctx, stdhttp.MethodGet, url, nil)
	if err != nil {
		return nil, err
	}
	req.Header = header
	ctx = transport.NewClientContext(ctx, &Transport{
		endpoint:     client.opts.endpoint,
		reqHeader:    headerCarrier(req.Header),
		operation:    c.operation,
		request:      req,
		pathTemplate: c.pathTemplate,
	})

	h := func(ctx context.Context, _ any) (any, error) {
		var done func(context.Context, selector.DoneInfo)
		dialURL := req.URL.String()
		if client.r != nil {
			node, doneFunc, selectErr := client.selector.Select(ctx, selector.WithNodeFilter(client.opts.nodeFilters...))
			if selectErr != nil {
				// No node was chosen, so no connection was attempted and no
				// application message left this process.
				return nil, transport.MarkNotSent(ErrNodeNotFound.Msg(selectErr.Error()).Wrap(selectErr))
			}
			done = doneFunc
			if client.insecure {
				scheme = "ws"
			} else {
				scheme = "wss"
			}
			req.URL.Scheme = scheme
			req.URL.Host = node.Address()
			req.Host = node.Address()
			dialURL = fmt.Sprintf("%s://%s%s", scheme, node.Address(), path)
		}
		dialer := websocket.Dialer{
			Proxy:            stdhttp.ProxyFromEnvironment,
			HandshakeTimeout: client.opts.timeout,
			TLSClientConfig:  client.opts.tlsConf,
		}
		conn, res, dialErr := dialer.DialContext(ctx, dialURL, req.Header)
		if res != nil {
			cs := csAttempt{res: res}
			for _, o := range opts {
				o.after(&c, &cs)
			}
		}
		if dialErr != nil {
			if res != nil && res.Body != nil {
				_ = res.Body.Close()
			}
			if done != nil {
				done(ctx, selector.DoneInfo{Err: dialErr})
			}
			// The handshake never completed, so the stream carried no
			// application message. This holds however far the handshake got:
			// a rejected upgrade means the peer declined the stream rather
			// than accepting work on it.
			return nil, transport.MarkNotSent(dialErr)
		}
		var resHeader stdhttp.Header
		if res != nil {
			resHeader = res.Header
		}
		if res != nil && res.Body != nil {
			_ = res.Body.Close()
		}
		return &websocketClientStream{
			ctx:     ctx,
			conn:    conn,
			header:  resHeader,
			encoder: streamCodecFromCallInfo(c, "Content-Type", "Accept"),
			decoder: streamCodecFromCallInfo(c, "Accept", "Content-Type"),
			done: func(err error) {
				if done != nil {
					done(ctx, selector.DoneInfo{Err: err})
				}
			},
		}, nil
	}
	var p selector.Peer
	ctx = selector.NewPeerContext(ctx, &p)
	if len(client.opts.middleware) > 0 {
		h = middleware.ChainUnary(client.opts.middleware...)(h)
	}
	stream, err := h(ctx, nil)
	if err != nil {
		return nil, err
	}
	return sendingClientStreamFromHandler(stream)
}

func clientStreamFromHandler(v any) (ClientStream, error) {
	stream, ok := v.(ClientStream)
	if !ok {
		return nil, stderrors.New("http stream middleware returned non-client stream")
	}
	return stream, nil
}

// sendingClientStreamFromHandler is the WebSocket counterpart. Middleware may
// wrap the stream it was handed, so the capability has to be re-checked on the
// way out: a wrapper that dropped the sending half would otherwise be handed
// back to a caller that is entitled to send on it.
func sendingClientStreamFromHandler(v any) (SendingClientStream, error) {
	stream, ok := v.(SendingClientStream)
	if !ok {
		return nil, stderrors.New("http stream middleware returned non-sending client stream")
	}
	return stream, nil
}

func prepareClientRequest(client *Client, req *stdhttp.Request, contentType string, c callInfo) {
	if c.headerCarrier != nil {
		req.Header = *c.headerCarrier
	}
	if contentType != "" {
		req.Header.Set("Content-Type", c.contentType)
	}
	if c.accept != "" {
		req.Header.Set("Accept", c.accept)
	}
	if client.opts.userAgent != "" {
		req.Header.Set("User-Agent", client.opts.userAgent)
	}
}

func marshalStreamMessage(v any, codec encoding.Codec) ([]byte, error) {
	if body, ok := httpBody(v); ok {
		return body.GetData(), nil
	}
	if codec == nil {
		codec = defaultStreamCodec()
	}
	return encodeWithCodec(codec, v)
}

func unmarshalStreamMessage(data []byte, v any, codec encoding.Codec) error {
	if body, ok := httpBody(v); ok {
		body.SetData(data)
		return nil
	}
	if codec == nil {
		codec = defaultStreamCodec()
	}
	return decodeWithCodec(codec, data, v)
}

func readWebSocketMessage(conn *websocket.Conn, m any, codec encoding.Codec) error {
	for {
		messageType, data, err := conn.ReadMessage()
		if err != nil {
			if websocket.IsCloseError(err, websocket.CloseNormalClosure, websocket.CloseGoingAway) {
				return io.EOF
			}
			return err
		}
		if messageType != websocket.TextMessage && messageType != websocket.BinaryMessage {
			continue
		}
		text := string(data)
		switch {
		case text == websocketControlEnd:
			return io.EOF
		case strings.HasPrefix(text, websocketControlError):
			payload := strings.TrimPrefix(text, websocketControlError)
			if se, ok := unmarshalProblem(ProblemContentType, []byte(payload), NoStatus); ok {
				return se
			}
			return stderrors.New(payload)
		default:
			return unmarshalStreamMessage(data, m, codec)
		}
	}
}

func streamCodecFromCallInfo(c callInfo, names ...string) encoding.Codec {
	header := stdhttp.Header{}
	if c.accept != "" {
		header.Set("Accept", c.accept)
	}
	if c.contentTypeSet {
		header.Set("Content-Type", c.contentType)
	}
	return streamCodecFromHeaders(header, names...)
}

func streamCodecFromHeaders(header stdhttp.Header, names ...string) encoding.Codec {
	for _, name := range names {
		for _, values := range header.Values(name) {
			for _, value := range strings.Split(values, ",") {
				contentType := strings.TrimSpace(value)
				if codec := encoding.GetCodec(httputil.ContentSubtype(contentType)); codec != nil {
					return codec
				}
			}
		}
	}
	return defaultStreamCodec()
}

func defaultStreamCodec() encoding.Codec {
	if codec := encoding.GetCodec("protojson"); codec != nil {
		return codec
	}
	return encoding.GetCodec("json")
}

func copyMetadataToHeader(h, md stdhttp.Header) {
	for k, values := range md {
		for _, v := range values {
			h.Add(k, v)
		}
	}
}

// joinHeader returns dst with every entry of src appended. A nil dst is
// allocated, so a zero-valued stream accepts headers without initialization.
func joinHeader(dst, src stdhttp.Header) stdhttp.Header {
	if len(src) == 0 {
		return dst
	}
	if dst == nil {
		dst = make(stdhttp.Header, len(src))
	}
	for k, values := range src {
		for _, v := range values {
			dst.Add(k, v)
		}
	}
	return dst
}
