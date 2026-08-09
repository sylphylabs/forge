package diagnosis

import (
	"encoding/json"
	"net/http"
	"sort"
	"strings"
)

// NewHandler returns an [http.Handler] that serves a [Registry] as JSON, for
// mounting on a mux the application already owns:
//
//	mux.Handle("/debug/probes/", http.StripPrefix("/debug/probes", diagnosis.NewHandler(reg)))
//
// The handler answers GET requests only:
//
//   - "/" runs every probe and responds 200 with an object keyed by probe
//     name, each entry holding {"value": ...} for a snapshot or
//     {"error": "..."} for a probe failure. The status is 200 even when
//     probes fail — a dump reports state, it does not judge it.
//   - "/<name>" runs one probe: 200 with the snapshot value on success,
//     500 with {"error": "..."} when the probe fails, 404 when no probe has
//     that name. The whole remaining path is the name, so slash-containing
//     names like "governance/ratelimit" resolve.
//
// A snapshot that cannot be marshaled — a [ProbeFunc] contract violation —
// is reported as that probe's error; it never breaks the rest of a dump.
//
// The handler opens no port and starts no goroutine. Whether the route is
// exposed at all, on which listener, and behind what authentication is the
// application's decision, which is exactly why this is a handler and not a
// server.
func NewHandler(r *Registry) http.Handler {
	return &handler{registry: r}
}

type handler struct {
	registry *Registry
}

func (h *handler) ServeHTTP(w http.ResponseWriter, req *http.Request) {
	if req.Method != http.MethodGet {
		w.Header().Set("Allow", http.MethodGet)
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	name := strings.TrimPrefix(req.URL.Path, "/")
	if name == "" {
		h.serveAll(w, req)
		return
	}
	h.serveOne(w, req, name)
}

// entry is the wire form of one probe outcome. Exactly one field is set.
type entry struct {
	Value json.RawMessage `json:"value,omitempty"`
	Error string          `json:"error,omitempty"`
}

// encode turns a Result into its wire form, folding an unmarshalable value
// into an error entry so one bad snapshot cannot poison a whole dump.
func encode(res Result) entry {
	if res.Err != nil {
		return entry{Error: res.Err.Error()}
	}
	raw, err := json.Marshal(res.Value)
	if err != nil {
		return entry{Error: "diagnosis: snapshot is not JSON-serializable: " + err.Error()}
	}
	return entry{Value: raw}
}

func (h *handler) serveAll(w http.ResponseWriter, req *http.Request) {
	results := h.registry.Collect(req.Context())
	entries := make(map[string]entry, len(results))
	for name, res := range results {
		entries[name] = encode(res)
	}
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	writeSorted(w, entries)
}

func (h *handler) serveOne(w http.ResponseWriter, req *http.Request, name string) {
	res, ok := h.registry.Probe(req.Context(), name)
	if !ok {
		http.Error(w, "unknown probe: "+name, http.StatusNotFound)
		return
	}
	e := encode(res)
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	if e.Error != "" {
		w.WriteHeader(http.StatusInternalServerError)
		_ = json.NewEncoder(w).Encode(map[string]string{"error": e.Error})
		return
	}
	_, _ = w.Write(append(e.Value, '\n'))
}

// writeSorted emits entries as one JSON object with keys in lexical order,
// so successive dumps of the same state are byte-identical and diffable.
func writeSorted(w http.ResponseWriter, entries map[string]entry) {
	names := make([]string, 0, len(entries))
	for name := range entries {
		names = append(names, name)
	}
	sort.Strings(names)

	var b strings.Builder
	b.WriteByte('{')
	for i, name := range names {
		if i > 0 {
			b.WriteByte(',')
		}
		key, _ := json.Marshal(name)
		b.Write(key)
		b.WriteByte(':')
		raw, err := json.Marshal(entries[name])
		if err != nil {
			// Unreachable: entry holds only a RawMessage and a string.
			raw = []byte(`{"error":"diagnosis: entry not serializable"}`)
		}
		b.Write(raw)
	}
	b.WriteString("}\n")
	_, _ = w.Write([]byte(b.String()))
}
