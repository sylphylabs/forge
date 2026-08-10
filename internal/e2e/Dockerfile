# The end-to-end service, built from the repository root so the whole module is
# available. Kept minimal on purpose: the test is about Forge's own behaviour
# across a real network, not about image construction.
FROM golang:1.27rc2 AS build
WORKDIR /src
COPY go.mod go.sum ./
COPY api/go.mod api/go.sum ./api/
RUN go mod download
COPY . .
RUN CGO_ENABLED=0 go build -o /out/service ./internal/e2e/service

FROM gcr.io/distroless/static-debian12
COPY --from=build /out/service /service
EXPOSE 8000 9000
ENTRYPOINT ["/service"]
