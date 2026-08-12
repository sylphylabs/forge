## Discovery Registry

This module implements a `registry.Registrar` and `registry.Discovery` interface in forge based `bilibili/discovery`.

[![go.dev reference](https://img.shields.io/badge/go.dev-reference-007d9c?logo=go&logoColor=white&style=flat-square)](https://pkg.go.dev/github.com/sylphylabs/forge/contrib/registry/discovery)

### Quick Start

**_Register a service_**

```go
import (
	"github.com/sylphylabs/forge/contrib/registry/discovery"
)

func main() {
	// initialize a registry
	r := discovery.New(&discovery.Config{
		Nodes:  []string{"0.0.0.0:7171"},
		Env:    "dev",
		Region: "sh1",
		Zone:   "zone1",
		Host:   "hostname",
	})

	// construct srv instance
	// ...

	app := forge.New(
		forge.WithName("helloworld"),
		forge.WithServer(
			httpSrv,
			grpcSrv,
		),
		forge.WithMetadata(map[string]string{"color": "gray"}),
		// use Registrar
		forge.WithRegistrar(r),
	)

	if err := app.Run(); err != nil {
		log.Fatal(err)
	}
}
```

**_Discover a service_**

```go
import (
	"github.com/sylphylabs/forge/contrib/registry/discovery"
	"github.com/sylphylabs/forge/transport/grpc"
)

func main() {
	// initialize a discovery
	r := discovery.New(&discovery.Config{
		Nodes:  []string{"0.0.0.0:7171"},
		Env:    "dev",
		Region: "sh1",
		Zone:   "zone1",
		Host:   "localhost",
	}, nil)

	conn, err := grpc.NewClient(
		context.Background(),
		grpc.WithTarget("discovery:///appid"),
		// use discovery
		grpc.WithDiscovery(r),
	)
	if err != nil {
		log.Fatal(err)
	}
	defer conn.Close()

	// request and log
}
```

### Config explain

```go
type Config struct {
	Nodes  []string // discovery nodes address
	Region string   // region of the service, sh
	Zone   string   // zone of region, sh001
	Env    string   // env of service, dev, prod and etc
	Host   string   // hostname of service
}
```

### References

- [bilibili/discovery](https://github.com/bilibili/discovery)
