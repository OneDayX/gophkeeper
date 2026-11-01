package grpcserver

import (
	"context"

	"github.com/OneDayX/gophkeeper/internal/pb"
	"go.uber.org/zap"
)

// AuthServer implements AuthService on top of the user service.
type AuthServer struct {
	pb.UnimplementedAuthServiceServer

	users userService
	log   *zap.Logger
}

// NewAuthServer returns an AuthServer that registers and authenticates users
// with the service.
func NewAuthServer(users userService, log *zap.Logger) *AuthServer {
	return &AuthServer{users: users, log: log}
}

// Register creates an account and returns a token for it. It fails with
// InvalidArgument for malformed credentials and with AlreadyExists for a
// taken login.
func (s *AuthServer) Register(ctx context.Context, req *pb.RegisterRequest) (*pb.RegisterResponse, error) {
	token, err := s.users.Register(ctx, req.GetLogin(), req.GetPassword())
	if err != nil {
		return nil, statusFromError(s.log, err)
	}

	return &pb.RegisterResponse{Token: token}, nil
}

// Login checks the credentials and returns a token. It fails with
// Unauthenticated for an unknown login and for a wrong password alike.
func (s *AuthServer) Login(ctx context.Context, req *pb.LoginRequest) (*pb.LoginResponse, error) {
	token, err := s.users.Login(ctx, req.GetLogin(), req.GetPassword())
	if err != nil {
		return nil, statusFromError(s.log, err)
	}

	return &pb.LoginResponse{Token: token}, nil
}
