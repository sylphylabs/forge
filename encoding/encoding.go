package encoding

import (
	"strings"
	"sync"
)

// Codec defines the interface Transport uses to encode and decode messages. Note
// that implementations of this interface must be thread safe; a Codec's
// methods can be called from concurrent goroutines.
type Codec interface {
	// Marshal returns the wire format of v.
	Marshal(v any) ([]byte, error)
	// Unmarshal parses the wire format into v.
	Unmarshal(data []byte, v any) error
	// Name returns the name of the Codec implementation. The returned string
	// will be used as part of content type in transmission. The result must be
	// static; the result cannot change between calls.
	Name() string
}

var (
	registeredCodecsMu sync.RWMutex
	registeredCodecs   = make(map[string]Codec)
)

// RegisterCodec registers the provided Codec for use with all Transport clients and
// servers. It is safe to call concurrently with itself and with [GetCodec];
// registering a Codec whose name is already registered replaces the earlier one.
func RegisterCodec(codec Codec) {
	if codec == nil {
		panic("cannot register a nil Codec")
	}
	if codec.Name() == "" {
		panic("cannot register Codec with empty string result for Name()")
	}
	contentSubtype := strings.ToLower(codec.Name())
	registeredCodecsMu.Lock()
	defer registeredCodecsMu.Unlock()
	registeredCodecs[contentSubtype] = codec
}

// GetCodec gets a registered Codec by content-subtype, or nil if no Codec is
// registered for the content-subtype. It is safe to call concurrently with
// [RegisterCodec].
//
// The content-subtype is expected to be lowercase.
func GetCodec(contentSubtype string) Codec {
	registeredCodecsMu.RLock()
	defer registeredCodecsMu.RUnlock()
	return registeredCodecs[contentSubtype]
}
