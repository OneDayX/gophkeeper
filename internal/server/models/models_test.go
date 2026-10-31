package models

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestItemType_Valid(t *testing.T) {
	for _, typ := range []ItemType{ItemTypeCredentials, ItemTypeText, ItemTypeBinary, ItemTypeCard, ItemTypeOTP} {
		assert.True(t, typ.Valid(), "type %d", typ)
	}

	assert.False(t, ItemType(0).Valid())
	assert.False(t, ItemType(6).Valid())
	assert.False(t, ItemType(-1).Valid())
}
