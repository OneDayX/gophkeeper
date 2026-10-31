package luhn

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestValid(t *testing.T) {
	tests := []struct {
		number string
		want   bool
	}{
		{"4111111111111111", true},  // Visa test card
		{"5555555555554444", true},  // Mastercard test card
		{"2200000000000004", true},  // Mir-like number
		{"4111111111111112", false}, // wrong check digit
		{"0", true},
		{"", false},
		{"4111 1111 1111 1111", false}, // spaces are not digits
		{"41111111111111a1", false},
	}

	for _, tt := range tests {
		assert.Equal(t, tt.want, Valid(tt.number), tt.number)
	}
}
