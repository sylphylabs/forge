package main

import (
	"strconv"
	"strings"
	"testing"

	"google.golang.org/protobuf/compiler/protogen"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/reflect/protodesc"
	"google.golang.org/protobuf/types/descriptorpb"
	"google.golang.org/protobuf/types/known/emptypb"
	"google.golang.org/protobuf/types/pluginpb"

	messageapi "github.com/sylphylabs/forge/api/message/v1"
)

// subscription builds MethodOptions carrying the subscribe annotation. A nil
// destination declares the option without a destination, which is the case the
// generator must reject.
func subscription(destination *string) *descriptorpb.MethodOptions {
	options := new(descriptorpb.MethodOptions)
	value := new(messageapi.Subscription)
	if destination != nil {
		value.Destination = *destination
	}
	proto.SetExtension(options, messageapi.E_Subscribe, value)
	return options
}

func stringPointer(value string) *string { return &value }

type methodSpec struct {
	name            string
	input           string // request message name; defaults to OrderCreated
	output          string // fully qualified response type; defaults to .google.protobuf.Empty
	options         *descriptorpb.MethodOptions
	streamingClient bool
	streamingServer bool
}

func newMessagePlugin(t *testing.T, methods []methodSpec) (*protogen.Plugin, *descriptorpb.FileDescriptorProto) {
	t.Helper()
	descriptors := make([]*descriptorpb.MethodDescriptorProto, 0, len(methods))
	for _, method := range methods {
		input := method.input
		if input == "" {
			input = "OrderCreated"
		}
		output := method.output
		if output == "" {
			output = ".google.protobuf.Empty"
		}
		descriptor := &descriptorpb.MethodDescriptorProto{
			Name:       proto.String(method.name),
			InputType:  proto.String(".test.v1." + input),
			OutputType: proto.String(output),
			Options:    method.options,
		}
		if method.streamingClient {
			descriptor.ClientStreaming = proto.Bool(true)
		}
		if method.streamingServer {
			descriptor.ServerStreaming = proto.Bool(true)
		}
		descriptors = append(descriptors, descriptor)
	}

	file := &descriptorpb.FileDescriptorProto{
		Name:    proto.String("test/v1/orders.proto"),
		Package: proto.String("test.v1"),
		Syntax:  proto.String("proto3"),
		Dependency: []string{
			"google/protobuf/empty.proto",
			"sylphy/message/v1/message.proto",
		},
		Options: &descriptorpb.FileOptions{
			GoPackage: proto.String("example.com/test/v1;testv1"),
		},
		MessageType: []*descriptorpb.DescriptorProto{
			{
				Name: proto.String("OrderCreated"),
				Field: []*descriptorpb.FieldDescriptorProto{{
					Name:   proto.String("id"),
					Number: proto.Int32(1),
					Type:   descriptorpb.FieldDescriptorProto_TYPE_STRING.Enum(),
					Label:  descriptorpb.FieldDescriptorProto_LABEL_OPTIONAL.Enum(),
				}},
			},
			{
				Name: proto.String("OrderShipped"),
				Field: []*descriptorpb.FieldDescriptorProto{{
					Name:   proto.String("id"),
					Number: proto.Int32(1),
					Type:   descriptorpb.FieldDescriptorProto_TYPE_STRING.Enum(),
					Label:  descriptorpb.FieldDescriptorProto_LABEL_OPTIONAL.Enum(),
				}},
			},
		},
		Service: []*descriptorpb.ServiceDescriptorProto{{
			Name:   proto.String("OrderEvents"),
			Method: descriptors,
		}},
	}
	request := &pluginpb.CodeGeneratorRequest{
		FileToGenerate: []string{file.GetName()},
		ProtoFile: []*descriptorpb.FileDescriptorProto{
			protodesc.ToFileDescriptorProto(descriptorpb.File_google_protobuf_descriptor_proto),
			protodesc.ToFileDescriptorProto(emptypb.File_google_protobuf_empty_proto),
			protodesc.ToFileDescriptorProto(messageapi.File_sylphy_message_v1_message_proto),
			file,
		},
	}
	plugin, err := (protogen.Options{}).New(request)
	if err != nil {
		t.Fatalf("protogen.Options.New() error = %v", err)
	}
	return plugin, file
}

func TestGenerateForgeMessageAnnotations(t *testing.T) {
	plugin, file := newMessagePlugin(t, []methodSpec{
		{name: "OnOrderCreated", options: subscription(stringPointer("order.created"))},
		{name: "OnOrderShipped", input: "OrderShipped", options: subscription(stringPointer("order.shipped"))},
		// An unannotated method is not part of the message contract.
		{name: "OnOrderAudited"},
	})
	generated, err := generateMessageFile(plugin, plugin.FilesByPath[file.GetName()])
	if err != nil {
		t.Fatalf("generateMessageFile() error = %v", err)
	}
	if generated == nil {
		t.Fatal("generateMessageFile() returned nil")
	}

	response := plugin.Response()
	if response.GetError() != "" {
		t.Fatalf("generation error = %s", response.GetError())
	}
	if len(response.File) != 1 {
		t.Fatalf("generated files = %d, want 1", len(response.File))
	}
	if got := response.File[0].GetName(); got != "example.com/test/v1/orders_message.pb.go" {
		t.Fatalf("generated file = %q", got)
	}
	content := response.File[0].GetContent()
	for _, want := range []string{
		`type OrderEventsMessageServer interface {`,
		`OnOrderCreated(context.Context, *OrderCreated) error`,
		`OnOrderShipped(context.Context, *OrderShipped) error`,
		`func RegisterOrderEventsMessageServer(s *message.Server, srv OrderEventsMessageServer, opts ...OrderEventsMessageRegisterOption) error {`,
		`const DestinationOrderEventsOnOrderCreated = "order.created"`,
		`const DestinationOrderEventsOnOrderShipped = "order.shipped"`,
		`func WithOrderEventsMessageDestination(operation, destination string) OrderEventsMessageRegisterOption {`,
		`func WithOrderEventsMessageDestinationPrefix(prefix string) OrderEventsMessageRegisterOption {`,
		`o.resolve("OnOrderCreated", DestinationOrderEventsOnOrderCreated)`,
		// The handler is a middleware.UnaryHandler so that a message consumer
		// composes with the same middleware as HTTP and gRPC. The destination
		// is not a parameter: a handler reads it from the transport in context.
		`func _OrderEvents_OnOrderCreated_Message_Handler(srv OrderEventsMessageServer) middleware.UnaryHandler {`,
		`return func(ctx context.Context, req any) (any, error) {`,
		`github.com/sylphylabs/forge/transport/message`,
		`github.com/sylphylabs/forge/middleware`,
	} {
		if !strings.Contains(content, want) {
			t.Errorf("generated output is missing %q:\n%s", want, content)
		}
	}
	// An unannotated method must not reach the generated contract.
	for _, unwanted := range []string{"OnOrderAudited"} {
		if strings.Contains(content, unwanted) {
			t.Errorf("generated output unexpectedly contains %q:\n%s", unwanted, content)
		}
	}
}

func TestGenerateSkipsFilesWithoutSubscriptions(t *testing.T) {
	plugin, file := newMessagePlugin(t, []methodSpec{{name: "OnOrderAudited"}})
	generated, err := generateMessageFile(plugin, plugin.FilesByPath[file.GetName()])
	if err != nil {
		t.Fatalf("generateMessageFile() error = %v", err)
	}
	if generated != nil {
		t.Fatal("generateMessageFile() emitted a file for an unannotated service")
	}
	if files := plugin.Response().File; len(files) != 0 {
		t.Fatalf("generated files = %d, want 0", len(files))
	}
}

func TestAnalyzeMessageFileRejectsInvalidSubscriptions(t *testing.T) {
	tests := []struct {
		name    string
		methods []methodSpec
		want    []string
	}{
		{
			name:    "missing destination",
			methods: []methodSpec{{name: "OnOrderCreated", options: subscription(nil)}},
			want: []string{
				`proto "test/v1/orders.proto"`,
				"RPC test.v1.OrderEvents.OnOrderCreated",
				"requires a non-empty destination",
			},
		},
		{
			name:    "blank destination",
			methods: []methodSpec{{name: "OnOrderCreated", options: subscription(stringPointer("   "))}},
			want:    []string{"requires a non-empty destination"},
		},
		{
			name: "server streaming",
			methods: []methodSpec{{
				name:            "OnOrderCreated",
				options:         subscription(stringPointer("order.created")),
				streamingServer: true,
			}},
			want: []string{"does not support streaming methods"},
		},
		{
			name: "client streaming",
			methods: []methodSpec{{
				name:            "OnOrderCreated",
				options:         subscription(stringPointer("order.created")),
				streamingClient: true,
			}},
			want: []string{"does not support streaming methods"},
		},
		{
			name: "non-empty response type",
			methods: []methodSpec{{
				name:    "OnOrderCreated",
				output:  ".test.v1.OrderShipped",
				options: subscription(stringPointer("order.created")),
			}},
			want: []string{
				"RPC test.v1.OrderEvents.OnOrderCreated",
				"requires the response type google.protobuf.Empty, not test.v1.OrderShipped",
				"no reply channel",
			},
		},
		{
			name: "duplicate destination",
			methods: []methodSpec{
				{name: "OnOrderCreated", options: subscription(stringPointer("order.created"))},
				{name: "OnOrderShipped", options: subscription(stringPointer("order.created"))},
			},
			want: []string{
				"RPC test.v1.OrderEvents.OnOrderShipped",
				`destination "order.created" is already bound by test.v1.OrderEvents.OnOrderCreated`,
			},
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			plugin, file := newMessagePlugin(t, test.methods)
			_, err := analyzeMessageFile(plugin.FilesByPath[file.GetName()])
			if err == nil {
				t.Fatal("analyzeMessageFile() error = nil")
			}
			for _, want := range test.want {
				if !strings.Contains(err.Error(), want) {
					t.Errorf("error %q is missing %q", err, want)
				}
			}
		})
	}
}

func TestSubscriptionOfIgnoresUnannotatedMethods(t *testing.T) {
	plugin, file := newMessagePlugin(t, []methodSpec{
		{name: "OnOrderAudited"},
		{name: "OnOrderCreated", options: subscription(stringPointer("order.created"))},
	})
	service := plugin.FilesByPath[file.GetName()].Services[0]
	if _, ok := subscriptionOf(service.Methods[0]); ok {
		t.Error("subscriptionOf() reported an annotation on an unannotated method")
	}
	got, ok := subscriptionOf(service.Methods[1])
	if !ok {
		t.Fatal("subscriptionOf() reported no annotation on an annotated method")
	}
	if got.GetDestination() != "order.created" {
		t.Errorf("destination = %q, want %q", got.GetDestination(), "order.created")
	}
}

func TestDeprecatedServiceRendersMarker(t *testing.T) {
	sd := &serviceDesc{
		ServiceType: "OrderEvents",
		ServiceName: "test.v1.OrderEvents",
		Deprecated:  true,
		Methods: []*methodDesc{{
			Name:         "OnOrderCreated",
			OriginalName: "OnOrderCreated",
			Request:      "OrderCreated",
			Destination:  "order.created",
		}},
	}
	rendered, err := sd.execute()
	if err != nil {
		t.Fatalf("execute() error = %v", err)
	}
	want := deprecationComment + "\ntype OrderEventsMessageServer interface {"
	if !strings.Contains(rendered, want) {
		t.Fatalf("rendered output is missing %q:\n%s", want, rendered)
	}
}

func Test_lowerFirst(t *testing.T) {
	tests := []struct {
		name string
		want string
	}{
		{"OrderEvents", "orderEvents"},
		{"A", "a"},
		{"orderEvents", "orderEvents"},
		{"", ""},
	}
	for _, test := range tests {
		if got := lowerFirst(test.name); got != test.want {
			t.Errorf("lowerFirst(%q) = %q, want %q", test.name, got, test.want)
		}
	}
}

// A destination is an adapter-defined string, not a restricted charset, so it
// reaches the generated constant through %q rather than raw interpolation.
//
// Interpolating it raw fails two ways: a quote or newline makes the file
// unparsable, and a backslash silently changes the value — a service that
// compiles and subscribes to a destination the contract never named.
func TestDestinationSurvivesGoQuoting(t *testing.T) {
	for _, destination := range []string{
		`order.created`,
		`a"b`,
		"a\nb",
		`a\b`,
		`orders.\d+`,
		"tab\there",
	} {
		t.Run(destination, func(t *testing.T) {
			plugin, file := newMessagePlugin(t, []methodSpec{
				{name: "OnOrderCreated", options: subscription(stringPointer(destination))},
			})
			if _, err := generateMessageFile(plugin, plugin.FilesByPath[file.GetName()]); err != nil {
				t.Fatalf("generateMessageFile() error = %v", err)
			}
			response := plugin.Response()
			if response.GetError() != "" {
				t.Fatalf("generation error = %s", response.GetError())
			}

			content := response.File[0].GetContent()
			_, literal, found := strings.Cut(content, "const DestinationOrderEventsOnOrderCreated = ")
			if !found {
				t.Fatalf("no destination constant in:\n%s", content)
			}
			literal, _, _ = strings.Cut(literal, "\n")

			got, err := strconv.Unquote(strings.TrimSpace(literal))
			if err != nil {
				t.Fatalf("emitted literal %s is not a valid Go string: %v", literal, err)
			}
			if got != destination {
				t.Errorf("destination round-tripped as %q, want %q", got, destination)
			}
		})
	}
}
