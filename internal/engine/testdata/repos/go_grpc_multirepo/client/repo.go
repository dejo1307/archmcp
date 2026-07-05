package main

import (
	"context"

	usersv1 "grpcclient/gen/users/v1"
)

// UserRepo holds the gRPC client as a struct field (dependency injection). The
// call goes through s.users, exercising field-type resolution.
type UserRepo struct {
	users usersv1.UserServiceClient
}

func (r *UserRepo) Fetch(ctx context.Context, id string) (*usersv1.GetUserResponse, error) {
	return r.users.GetUser(ctx, &usersv1.GetUserRequest{UserId: id})
}
