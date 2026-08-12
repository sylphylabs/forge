// Package forge manages the lifecycle of a set of transport servers as one
// application.
//
// [New] builds an [App] from options: identity ([WithID], [WithName],
// [WithVersion], [WithMetadata]), the servers to run ([WithServer]), an
// optional service registry ([WithRegistrar]), and lifecycle hooks
// ([WithBeforeStart], [WithAfterStart], [WithBeforeStop], [WithAfterStop]).
// [App.Run] starts every server, registers the instance, and blocks until a
// stop signal arrives or [App.Stop] is called; on the way out it deregisters,
// drains the servers within [WithStopTimeout], and runs the AfterStop hooks
// within [WithAfterStopTimeout]. Run joins every error it observed rather
// than reporting only the first.
//
// A [Suite] bundles options that belong together — an integration and its
// hooks — so an application adopts them with a single [WithSuite] call.
// Handlers and hooks recover the application's identity from the context via
// [FromContext].
//
// The package dictates no project layout and ships no scaffolding CLI. See
// docs/agent/application.md for the usage contract and
// docs/design/application-lifecycle.md for the lifecycle rationale.
package forge
