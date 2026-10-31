package grpcserver

import (
	"github.com/OneDayX/gophkeeper/internal/pb"
	"github.com/OneDayX/gophkeeper/internal/server/models"
	"google.golang.org/protobuf/types/known/timestamppb"
)

// itemTypeFromProto turns a protocol item type into a models one. An unknown
// type becomes zero, which the item service rejects.
func itemTypeFromProto(t pb.ItemType) models.ItemType {
	switch t {
	case pb.ItemType_ITEM_TYPE_CREDENTIALS:
		return models.ItemTypeCredentials
	case pb.ItemType_ITEM_TYPE_TEXT:
		return models.ItemTypeText
	case pb.ItemType_ITEM_TYPE_BINARY:
		return models.ItemTypeBinary
	case pb.ItemType_ITEM_TYPE_CARD:
		return models.ItemTypeCard
	case pb.ItemType_ITEM_TYPE_OTP:
		return models.ItemTypeOTP
	default:
		return 0
	}
}

// itemTypeToProto turns a models item type into a protocol one.
func itemTypeToProto(t models.ItemType) pb.ItemType {
	switch t {
	case models.ItemTypeCredentials:
		return pb.ItemType_ITEM_TYPE_CREDENTIALS
	case models.ItemTypeText:
		return pb.ItemType_ITEM_TYPE_TEXT
	case models.ItemTypeBinary:
		return pb.ItemType_ITEM_TYPE_BINARY
	case models.ItemTypeCard:
		return pb.ItemType_ITEM_TYPE_CARD
	case models.ItemTypeOTP:
		return pb.ItemType_ITEM_TYPE_OTP
	default:
		return pb.ItemType_ITEM_TYPE_UNSPECIFIED
	}
}

// itemToProto turns a models item into a protocol one.
func itemToProto(item models.Item) *pb.Item {
	return &pb.Item{
		Id:        item.ID,
		Type:      itemTypeToProto(item.Type),
		Data:      item.Data,
		Version:   item.Version,
		Deleted:   item.Deleted,
		UpdatedAt: timestamppb.New(item.UpdatedAt),
	}
}
