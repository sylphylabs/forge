package forge

import (
	"context"
	"maps"
	"slices"

	"github.com/sylphylabs/forge/diagnosis"
)

// AppSnapshot is the value reported by an [AppProbe]: the application's
// identity as it stands at the moment the probe runs.
type AppSnapshot struct {
	// ID is the application instance id.
	ID string `json:"id"`
	// Name is the service name.
	Name string `json:"name"`
	// Version is the application version.
	Version string `json:"version"`
	// Metadata is the service metadata, if any.
	Metadata map[string]string `json:"metadata,omitempty"`
	// Endpoints lists the endpoints the application currently advertises.
	// Empty before the application has started its servers.
	Endpoints []string `json:"endpoints,omitempty"`
}

// AppProbe returns a probe that reports info's identity as an [AppSnapshot].
//
// The probe reads info when it runs, not when it is built, so a snapshot
// taken after [App.Run] has started the servers includes the endpoints they
// bound. Register it under a name of your choosing — "app" by convention:
//
//	reg := diagnosis.NewRegistry()
//	app := forge.New(forge.WithName("checkout"), ...)
//	reg.Register("app", forge.AppProbe(app))
//
// AppProbe panics if info is nil; a probe wired to nothing is a construction
// bug, surfaced at the offending line rather than as an error in every dump.
func AppProbe(info AppInfo) diagnosis.ProbeFunc {
	if info == nil {
		panic("forge: AppProbe called with a nil AppInfo")
	}
	return func(context.Context) (any, error) {
		return AppSnapshot{
			ID:        info.ID(),
			Name:      info.Name(),
			Version:   info.Version(),
			Metadata:  maps.Clone(info.Metadata()),
			Endpoints: slices.Clone(info.Endpoints()),
		}, nil
	}
}
