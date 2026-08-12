// Package encoding defines the [Codec] contract — Marshal, Unmarshal, and a
// stable Name — and a process-wide registry that resolves a codec by the
// content subtype it serves.
//
// [RegisterCodec] installs a codec, normally from an init function in the
// codec's own package; [GetCodec] resolves one by name. The subpackages
// json, xml, yaml, form, proto, and protojson each provide a codec and
// register it on import, so linking a codec in is a blank import away.
// Additional codecs live under contrib/encoding/ as separate modules.
//
// The registry serves callers for whom a codec name arrives as data: a
// config source's format tag, an HTTP Content-Type subtype. Registration is
// additive and names are stable, so what a name resolves to never depends on
// initialization order.
package encoding
