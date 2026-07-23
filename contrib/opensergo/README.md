# OpenSergo

## Usage

```go
osServer, err := opensergo.New(opensergo.WithEndpoint("localhost:9090"))
if err != nil {
	panic("init opensergo error")
}

s := &server{}
grpcSrv := grpc.NewServer(grpc.Address(":9000"))
grpcService, err := helloworld.WrapGreeterGRPCServer(s, helloworld.GreeterMiddleware{
	Unary: []middleware.UnaryMiddleware{recovery.Recovery()},
})
if err != nil {
	panic(err)
}
helloworld.RegisterGreeterServer(grpcSrv, grpcService)

app := kratos.New(
	kratos.Name(Name),
	kratos.Server(
		grpcSrv,
	),
)

osServer.ReportMetadata(context.Background(), app)

if err := app.Run(); err != nil {
	log.Fatal(err)
}
```
