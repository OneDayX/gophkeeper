package grpcserver

import (
	"context"
	"time"

	"github.com/OneDayX/gophkeeper/internal/pb"
	"github.com/OneDayX/gophkeeper/internal/server/auth"
	"go.uber.org/zap"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/metadata"
	"google.golang.org/grpc/peer"
	"google.golang.org/grpc/status"
)

// publicMethods are the calls that need no token: they are how one gets it.
var publicMethods = map[string]bool{
	pb.AuthService_Register_FullMethodName: true,
	pb.AuthService_Login_FullMethodName:    true,
}

// UnaryAuth returns an interceptor that lets a unary call through only with a
// valid token in the authorization metadata and puts the user ID into the
// context of the call. The methods of AuthService need no token.
func UnaryAuth(tokens tokenParser) grpc.UnaryServerInterceptor {
	return func(ctx context.Context, req any, info *grpc.UnaryServerInfo, handler grpc.UnaryHandler) (any, error) {
		if publicMethods[info.FullMethod] {
			return handler(ctx, req)
		}

		ctx, err := authenticate(ctx, tokens)
		if err != nil {
			return nil, err
		}

		return handler(ctx, req)
	}
}

// StreamAuth is UnaryAuth for streaming calls.
func StreamAuth(tokens tokenParser) grpc.StreamServerInterceptor {
	return func(srv any, ss grpc.ServerStream, info *grpc.StreamServerInfo, handler grpc.StreamHandler) error {
		if publicMethods[info.FullMethod] {
			return handler(srv, ss)
		}

		ctx, err := authenticate(ss.Context(), tokens)
		if err != nil {
			return err
		}

		return handler(srv, &authenticatedStream{ServerStream: ss, ctx: ctx})
	}
}

// authenticate checks the token of the call and returns its context with the
// user ID. A missing or bad token gives Unauthenticated.
func authenticate(ctx context.Context, tokens tokenParser) (context.Context, error) {
	values := metadata.ValueFromIncomingContext(ctx, auth.MetadataKey)
	if len(values) == 0 {
		return nil, status.Error(codes.Unauthenticated, "missing token")
	}

	userID, err := tokens.Parse(auth.TokenFromHeader(values[0]))
	if err != nil {
		return nil, status.Error(codes.Unauthenticated, "invalid token")
	}

	return auth.WithUserID(ctx, userID), nil
}

// authenticatedStream is a server stream with the context of an
// authenticated call: grpc.ServerStream gives no other way to replace it.
type authenticatedStream struct {
	grpc.ServerStream

	ctx context.Context
}

// Context returns the context with the user ID.
func (s *authenticatedStream) Context() context.Context {
	return s.ctx
}

// UnaryLogger returns an interceptor that logs every unary call: the method,
// the caller, the status code and how long it took. The messages are not
// logged: they carry passwords and secrets.
func UnaryLogger(log *zap.Logger) grpc.UnaryServerInterceptor {
	return func(ctx context.Context, req any, info *grpc.UnaryServerInfo, handler grpc.UnaryHandler) (any, error) {
		start := time.Now()
		resp, err := handler(ctx, req)
		logCall(ctx, log, info.FullMethod, start, err)

		return resp, err
	}
}

// StreamLogger is UnaryLogger for streaming calls.
func StreamLogger(log *zap.Logger) grpc.StreamServerInterceptor {
	return func(srv any, ss grpc.ServerStream, info *grpc.StreamServerInfo, handler grpc.StreamHandler) error {
		start := time.Now()
		err := handler(srv, ss)
		logCall(ss.Context(), log, info.FullMethod, start, err)

		return err
	}
}

// logCall writes one line about a finished call.
func logCall(ctx context.Context, log *zap.Logger, method string, start time.Time, err error) {
	fields := []zap.Field{
		zap.String("method", method),
		zap.String("code", status.Code(err).String()),
		zap.Duration("duration", time.Since(start)),
	}
	if p, ok := peer.FromContext(ctx); ok {
		fields = append(fields, zap.String("peer", p.Addr.String()))
	}

	log.Info("gRPC call", fields...)
}
