package grpcserver

import (
	"context"

	"github.com/OneDayX/gophkeeper/internal/pb"
	"github.com/OneDayX/gophkeeper/internal/server/auth"
	"github.com/OneDayX/gophkeeper/internal/server/models"
	"go.uber.org/zap"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/types/known/timestamppb"
)

// ItemServer implements ItemService on top of the item service. Every call
// works on the items of the user the auth interceptor has let through.
type ItemServer struct {
	pb.UnimplementedItemServiceServer

	items itemService
	log   *zap.Logger
}

// NewItemServer returns an ItemServer that keeps the items with the service.
func NewItemServer(items itemService, log *zap.Logger) *ItemServer {
	return &ItemServer{items: items, log: log}
}

// CreateItem stores a new item. It fails with InvalidArgument for a malformed
// item and with AlreadyExists for a taken ID.
func (s *ItemServer) CreateItem(ctx context.Context, req *pb.CreateItemRequest) (*pb.CreateItemResponse, error) {
	userID, err := authenticatedUser(ctx)
	if err != nil {
		return nil, err
	}

	item, err := s.items.Create(ctx, userID, models.Item{
		ID:   req.GetId(),
		Type: itemTypeFromProto(req.GetType()),
		Data: req.GetData(),
	})
	if err != nil {
		return nil, statusFromError(s.log, err)
	}

	return &pb.CreateItemResponse{
		Id:        item.ID,
		Version:   item.Version,
		UpdatedAt: timestamppb.New(item.UpdatedAt),
	}, nil
}

// UpdateItem replaces the data of an item. It fails with Aborted if the
// version in the request is not the current one and with NotFound if there
// is no such item.
func (s *ItemServer) UpdateItem(ctx context.Context, req *pb.UpdateItemRequest) (*pb.UpdateItemResponse, error) {
	userID, err := authenticatedUser(ctx)
	if err != nil {
		return nil, err
	}

	item, err := s.items.Update(ctx, userID, models.Item{
		ID:      req.GetId(),
		Data:    req.GetData(),
		Version: req.GetVersion(),
	})
	if err != nil {
		return nil, statusFromError(s.log, err)
	}

	return &pb.UpdateItemResponse{
		Version:   item.Version,
		UpdatedAt: timestamppb.New(item.UpdatedAt),
	}, nil
}

// DeleteItem turns an item into a tombstone. It fails like UpdateItem.
func (s *ItemServer) DeleteItem(ctx context.Context, req *pb.DeleteItemRequest) (*pb.DeleteItemResponse, error) {
	userID, err := authenticatedUser(ctx)
	if err != nil {
		return nil, err
	}

	version, err := s.items.Delete(ctx, userID, req.GetId(), req.GetVersion())
	if err != nil {
		return nil, statusFromError(s.log, err)
	}

	return &pb.DeleteItemResponse{Version: version}, nil
}

// GetItem returns one item. It fails with NotFound if there is no such item
// or it has been deleted.
func (s *ItemServer) GetItem(ctx context.Context, req *pb.GetItemRequest) (*pb.GetItemResponse, error) {
	userID, err := authenticatedUser(ctx)
	if err != nil {
		return nil, err
	}

	item, err := s.items.Get(ctx, userID, req.GetId())
	if err != nil {
		return nil, statusFromError(s.log, err)
	}

	return &pb.GetItemResponse{Item: itemToProto(item)}, nil
}

// ListItems streams the items changed after the version in the request, one
// per message, in the order of versions.
func (s *ItemServer) ListItems(req *pb.ListItemsRequest, stream grpc.ServerStreamingServer[pb.ListItemsResponse]) error {
	ctx := stream.Context()

	userID, err := authenticatedUser(ctx)
	if err != nil {
		return err
	}

	err = s.items.List(ctx, userID, req.GetSinceVersion(), func(item models.Item) error {
		return stream.Send(&pb.ListItemsResponse{Item: itemToProto(item)})
	})
	if err != nil {
		return statusFromError(s.log, err)
	}

	return nil
}

// authenticatedUser returns the user the auth interceptor has let through. A
// handler reached without the interceptor refuses the call rather than
// serving it for nobody.
func authenticatedUser(ctx context.Context) (int64, error) {
	userID, ok := auth.UserID(ctx)
	if !ok {
		return 0, status.Error(codes.Unauthenticated, "not authenticated")
	}

	return userID, nil
}
