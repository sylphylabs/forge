# Asynchronous Message Transport

`transport/message` contains the broker-neutral contract for asynchronous
messages. It intentionally has no Kafka, NATS, RabbitMQ, or task-queue SDK
dependency.

```go
subscriber := newSubscriber() // an application-owned adapter
server := message.NewServer(subscriber,
	message.WithMiddleware(loggingMiddleware),
)
if err := server.Handle("accounts.created", handleAccountCreated); err != nil {
	return err
}

app := forge.New(forge.WithServer(server))
return app.Run()
```

Adapters implement `message.Publisher` and `message.Subscriber`. A successful
`Publisher.Publish` call must document whether the broker acknowledged the
message or only accepted it into a local asynchronous buffer. A subscriber
must stop delivery when its lifetime context is canceled and must release all
resources from `Subscription.Close`.

`Message.Body` is encoded data and `Message.Headers` uses the same normalized,
multi-value metadata model as the HTTP and gRPC transports. Partition,
offset, acknowledgement handles, retry policy, and raw SDK values remain
adapter-specific.
