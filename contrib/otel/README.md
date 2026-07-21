# OpenTelemetry contrib

This module keeps OpenTelemetry integrations out of the core Kratos module.

## Packages

- `github.com/openkratos/kratos/contrib/otel/log`: slog handler bridge, usually imported as `otel` for `otel.NewHandler`.
- `github.com/openkratos/kratos/contrib/otel/tracing`: tracing middleware and trace slog attributes.
- `github.com/openkratos/kratos/contrib/otel/metrics`: metrics middleware and OTel metric helpers.

## Logger

```go
import (
	otel "github.com/openkratos/kratos/contrib/otel/log"
	"github.com/openkratos/kratos/log"
)

logger := log.NewLogger(otel.NewHandler("helloworld"))
```

Use the core Kratos log builder when the logger also needs fixed attrs or
filtering:

```go
import (
	"log/slog"

	otel "github.com/openkratos/kratos/contrib/otel/log"
	"github.com/openkratos/kratos/log"
)

logger := log.NewLogger(
	otel.NewHandler("helloworld"),
	log.WithFilter(log.FilterKey("password")),
).With(slog.String("service.name", "helloworld"))
```

Log, tracing, and metrics stay as shallow subpackages because they expose common
names such as `NewHandler`, `Server`, `Client`, and `Option`.
