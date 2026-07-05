package goextractor

import (
	"testing"

	"github.com/enola-labs/enola/internal/facts"
)

// grpcClientRoutes filters to gRPC client-role routes (framework grpc).
func grpcClientRoutes(ff []facts.Fact) []facts.Fact {
	var out []facts.Fact
	for _, f := range ff {
		if f.Kind == facts.KindRoute && f.Props["role"] == "client" && f.Props["framework"] == "grpc" {
			out = append(out, f)
		}
	}
	return out
}

// generatedGRPCStub mimics protoc-gen-go-grpc's users_grpc.pb.go: the exported
// constructor + interface and the unexported concrete client whose methods carry
// the wire path in their .Invoke / .NewStream literal.
const generatedGRPCStub = `package usersv1

import (
	context "context"
	grpc "google.golang.org/grpc"
)

type UserServiceClient interface {
	GetUser(ctx context.Context, in *GetUserRequest, opts ...grpc.CallOption) (*GetUserResponse, error)
	CreateUser(ctx context.Context, in *CreateUserRequest, opts ...grpc.CallOption) (*CreateUserResponse, error)
	Sync(ctx context.Context, opts ...grpc.CallOption) (grpc.ClientStream, error)
}

type userServiceClient struct {
	cc grpc.ClientConnInterface
}

func NewUserServiceClient(cc grpc.ClientConnInterface) UserServiceClient {
	return &userServiceClient{cc}
}

func (c *userServiceClient) GetUser(ctx context.Context, in *GetUserRequest, opts ...grpc.CallOption) (*GetUserResponse, error) {
	out := new(GetUserResponse)
	err := c.cc.Invoke(ctx, "/users.v1.UserService/GetUser", in, out, opts...)
	return out, err
}

func (c *userServiceClient) CreateUser(ctx context.Context, in *CreateUserRequest, opts ...grpc.CallOption) (*CreateUserResponse, error) {
	out := new(CreateUserResponse)
	err := c.cc.Invoke(ctx, "/users.v1.UserService/CreateUser", in, out, opts...)
	return out, err
}

func (c *userServiceClient) Sync(ctx context.Context, opts ...grpc.CallOption) (grpc.ClientStream, error) {
	stream, err := c.cc.NewStream(ctx, &grpc.StreamDesc{}, "/users.v1.UserService/Sync", opts...)
	return stream, err
}
`

// TestGoGRPCClient_EmitsClientRoutes is the versionCoverage[74] anchor test.
func TestGoGRPCClient_EmitsClientRoutes(t *testing.T) {
	consumer := `package app

import (
	"context"

	usersv1 "testmod/gen/users/v1"
	"google.golang.org/grpc"
)

func run(ctx context.Context, conn *grpc.ClientConn) {
	client := usersv1.NewUserServiceClient(conn)
	client.CreateUser(ctx, &usersv1.CreateUserRequest{})
}
`
	ff := extractAll(t, map[string]string{
		"gen/users/v1/users_grpc.pb.go": generatedGRPCStub,
		"app/app.go":                    consumer,
	})

	routes := grpcClientRoutes(ff)
	// The generated stub file itself does not construct a client, so only the
	// consumer's single call produces a route.
	if len(routes) != 1 {
		t.Fatalf("expected 1 grpc client route, got %d: %+v", len(routes), routes)
	}
	r := routes[0]
	if r.Name != "/users.v1.UserService/CreateUser" {
		t.Errorf("name = %q", r.Name)
	}
	if r.Props["source"] != "go-grpc-client" {
		t.Errorf("source = %v, want go-grpc-client", r.Props["source"])
	}
	if r.Props["language"] != "go" || r.Props["method"] != "POST" {
		t.Errorf("language/method = %v/%v", r.Props["language"], r.Props["method"])
	}
	if r.Props["rpc_service"] != "users.v1.UserService" || r.Props["rpc_method"] != "CreateUser" {
		t.Errorf("rpc_service/rpc_method = %v/%v", r.Props["rpc_service"], r.Props["rpc_method"])
	}

	// GetUser is never called by the consumer → must not be emitted.
	for _, x := range routes {
		if x.Name == "/users.v1.UserService/GetUser" {
			t.Error("GetUser is uncalled but was emitted as a client route")
		}
	}
}

func TestGoGRPCClient_StreamingMethodDetected(t *testing.T) {
	consumer := `package app

import (
	"context"

	usersv1 "testmod/gen/users/v1"
	"google.golang.org/grpc"
)

func run(ctx context.Context, conn *grpc.ClientConn) {
	c := usersv1.NewUserServiceClient(conn)
	c.Sync(ctx)
}
`
	ff := extractAll(t, map[string]string{
		"gen/users/v1/users_grpc.pb.go": generatedGRPCStub,
		"app/app.go":                    consumer,
	})
	if _, ok := clientRouteByPath(ff, "/users.v1.UserService/Sync"); !ok {
		t.Fatalf("streaming client route not detected: %+v", grpcClientRoutes(ff))
	}
}

func TestGoGRPCClient_InlineConstruction(t *testing.T) {
	consumer := `package app

import (
	"context"

	usersv1 "testmod/gen/users/v1"
	"google.golang.org/grpc"
)

func run(ctx context.Context, conn *grpc.ClientConn) {
	usersv1.NewUserServiceClient(conn).GetUser(ctx, &usersv1.GetUserRequest{})
}
`
	ff := extractAll(t, map[string]string{
		"gen/users/v1/users_grpc.pb.go": generatedGRPCStub,
		"app/app.go":                    consumer,
	})
	if _, ok := clientRouteByPath(ff, "/users.v1.UserService/GetUser"); !ok {
		t.Fatalf("inline-construction client route not detected: %+v", grpcClientRoutes(ff))
	}
}

func TestGoGRPCClient_NoStubNoRoutes(t *testing.T) {
	// A NewXxxClient-looking call with no generated stub in the repo must not
	// fabricate a route.
	consumer := `package app

func run() {
	c := NewUserServiceClient(nil)
	c.GetUser(nil, nil)
}

func NewUserServiceClient(x any) any { return nil }
`
	ff := extractAll(t, map[string]string{"app/app.go": consumer})
	if got := len(grpcClientRoutes(ff)); got != 0 {
		t.Fatalf("expected 0 grpc client routes without a stub, got %d", got)
	}
}
