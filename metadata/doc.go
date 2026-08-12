// Package metadata carries request-scoped key-value pairs across process
// boundaries, transport-neutrally.
//
// [Metadata] is a case-insensitive multimap: keys are normalized to lower
// case, and each key holds one or more string values. It travels in the
// context — [NewServerContext]/[FromServerContext] on the serving side,
// [NewClientContext]/[FromClientContext] on the calling side — and the
// metadata middleware (middleware/metadata) moves it on and off the wire,
// deciding by key prefix what propagates to downstream calls.
//
// This is application metadata, not transport headers: handlers and
// middleware read and write it without knowing whether the carrier was an
// HTTP header, a gRPC metadata pair, or a message-broker header.
package metadata
