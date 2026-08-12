# Dependency Maintenance

Status: active

Last verified: July 23, 2026

## Policy

Forge keeps every module on the latest compatible release within its
selected module path. Stable major-version migrations are evaluated separately;
pre-release majors are not adopted as routine dependency maintenance.

The repository contains 31 Go modules. Dependency updates therefore require a
module-aware pass rather than only running commands at the repository root:

```shell
for mod in $(find . -name go.mod -exec dirname {} \; | sort); do
  (cd "$mod" && GOWORK=off go get -u ./... && GOWORK=off go mod tidy)
done
```

Validation must include all-module compilation, focused tests for changed
providers, root and generator tests, and `go vet`. Providers whose integration
tests require external services are compiled locally and exercised in their
service-backed CI environment.

## Completed Migrations

| Dependency | Previous | Current | Reason |
| --- | --- | --- | --- |
| Apollo client | `agollo/v4` v4.4.0 | `agollo/v5` v5.0.0 | Stable major; fixes shutdown races and repeated-stop panics. |
| Consul API | `consul/api` v1.34.4 | `consul/api/v2` v2.0.0 | Stable major for config and registry providers. |
| Nacos config client | `nacos-sdk-go` v1.1.6 | `nacos-sdk-go/v2` v2.3.5 | Rejoins the maintained SDK line already used by the registry provider. |
| Direct UUID generation | `google/uuid` and `gofrs/uuid` | Go 1.27 standard `uuid` | Removes duplicate direct implementations and uses the common standard type. |
| OpenSergo SDK | 2022 pseudo-version | 2023 latest pseudo-version | Moves generated contracts to `pkg/proto/service_contract/v1`. |
| Kubernetes JSON | direct `json-iterator/go` | standard `encoding/json` | Removes a direct dependency on an archived repository. |
| Nacos test mocks | direct `golang/mock` | `go.uber.org/mock` v0.6.0 | Replaces the archived GoMock repository. |
| Validation fixtures | direct archived PGV module | Protovalidate plus interface fakes | Retains `Validate() error` compatibility without requiring PGV. |
| Discovery errors | `pkg/errors` | standard `errors` and wrapped `fmt.Errorf` | Uses standard error identity and wrapping. |
| Error generator casing | `golang.org/x/text/cases` | standard `strings` | Protobuf identifiers are ASCII, so the generator does not need Unicode language-aware title casing. |
| Discovery HTTP client | `go-resty/resty/v2` | standard `net/http` and `encoding/json` | The provider only needs query parameters, GET/POST, context cancellation, timeout, and JSON decoding. Non-2xx responses are now explicit errors. |

After these migrations, every direct dependency is current within its selected
module path.

## Residual Archived Transitives

The selected module graphs contain archived repositories inherited from
provider SDKs. Most are graph-only requirements from old dependency `go.mod`
files and do not enter package compilation. The following archived repositories
are present in actual `go list -deps` output:

| Provider | Compiled archived transitives | Ownership |
| --- | --- | --- |
| Nacos config and registry | `aliyun/alibaba-cloud-sdk-go`, `json-iterator/go`, `opentracing/opentracing-go`; registry also reaches `golang/mock` through the SDK | Nacos SDK v2.3.5 |
| Kubernetes config and registry | `json-iterator/go` | Kubernetes v0.36.2 dependency stack |
| Consul config and registry | `mitchellh/go-homedir` | Consul API v2.0.0 |
| Polaris config, integration, and registry | `mitchellh/go-homedir` | Polaris Go v1.7.1 |

Top-level version forcing cannot remove these imports. Eliminating them requires
an upstream SDK release or replacing the provider SDK with a narrower client.
Forge should not fork a complete service SDK merely to hide an archived
transitive dependency; migration becomes actionable when a maintained
replacement preserves the provider contract and has executable integration
coverage.
