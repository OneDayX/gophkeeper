package grpcserver

import (
	"context"
	"errors"

	"github.com/OneDayX/gophkeeper/internal/server/models"
	"go.uber.org/zap"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

// statusFromError turns a service error into a gRPC status by introspecting
// it with errors.Is. Anything unknown is a server fault: it is logged, and the
// client gets no details about it.
func statusFromError(log *zap.Logger, err error) error {
	switch {
	case errors.Is(err, models.ErrMalformedCredentials),
		errors.Is(err, models.ErrInvalidItem):
		return status.Error(codes.InvalidArgument, err.Error())

	case errors.Is(err, models.ErrInvalidCredentials):
		// The wrapped details tell an unknown login from a wrong password,
		// which the client must not learn.
		return status.Error(codes.Unauthenticated, models.ErrInvalidCredentials.Error())

	case errors.Is(err, models.ErrLoginTaken),
		errors.Is(err, models.ErrItemExists):
		return status.Error(codes.AlreadyExists, err.Error())

	case errors.Is(err, models.ErrItemNotFound):
		return status.Error(codes.NotFound, err.Error())

	case errors.Is(err, models.ErrVersionConflict):
		return status.Error(codes.Aborted, err.Error())

	case errors.Is(err, context.Canceled):
		return status.Error(codes.Canceled, err.Error())

	case errors.Is(err, context.DeadlineExceeded):
		return status.Error(codes.DeadlineExceeded, err.Error())
	}

	// A failed stream send already carries its status.
	if st, ok := status.FromError(err); ok {
		return st.Err()
	}

	log.Error("internal error", zap.Error(err))

	return status.Error(codes.Internal, "internal error")
}
