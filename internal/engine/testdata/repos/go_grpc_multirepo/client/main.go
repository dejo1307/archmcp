package main

import (
	"context"

	usersv1 "grpcclient/gen/users/v1"

	"google.golang.org/grpc"
)

// main dials the user service and creates a user. It calls only CreateUser —
// GetUser is never invoked, so the server's GetUser RPC stays unmatched.
func main() {
	conn, err := grpc.Dial("localhost:8080", grpc.WithInsecure())
	if err != nil {
		panic(err)
	}
	defer conn.Close()

	client := usersv1.NewUserServiceClient(conn)
	client.CreateUser(context.Background(), &usersv1.CreateUserRequest{Name: "ada"})
}
