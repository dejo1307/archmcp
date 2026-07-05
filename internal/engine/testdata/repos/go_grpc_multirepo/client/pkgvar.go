package main

import (
	"context"

	usersv1 "grpcclient/gen/users/v1"
)

// userClient is a package-level gRPC client var, exercised by callGetUser below
// — the package-scope binding form (distinct from main.go's local var and
// repo.go's struct field).
var userClient = usersv1.NewUserServiceClient(nil)

func callGetUser(ctx context.Context, id string) {
	userClient.GetUser(ctx, &usersv1.GetUserRequest{UserId: id})
}
