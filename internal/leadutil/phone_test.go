package leadutil

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestNormalizePhone(t *testing.T) {
	tests := []struct {
		name      string
		raw       string
		defaultCC string
		expected  string
		expectErr bool
	}{
		{
			name:      "+91 formatted with spaces",
			raw:       "+91 98765 43210",
			defaultCC: "91",
			expected:  "919876543210",
			expectErr: false,
		},
		{
			name:      "10-digit Indian number without cc",
			raw:       "9876543210",
			defaultCC: "91",
			expected:  "919876543210",
			expectErr: false,
		},
		{
			name:      "11-digit with leading 0",
			raw:       "09876543210",
			defaultCC: "91",
			expected:  "919876543210",
			expectErr: false,
		},
		{
			name:      "Full 12-digit Indian number already with cc",
			raw:       "919876543210",
			defaultCC: "91",
			expected:  "919876543210",
			expectErr: false,
		},
		{
			name:      "UAE number with spaces and +971",
			raw:       "+971 50 123 4567",
			defaultCC: "91",
			expected:  "971501234567",
			expectErr: false,
		},
		{
			name:      "Too short number",
			raw:       "12345",
			defaultCC: "91",
			expected:  "",
			expectErr: true,
		},
		{
			name:      "Empty string",
			raw:       "",
			defaultCC: "91",
			expected:  "",
			expectErr: true,
		},
		{
			name:      "Letters and symbols only",
			raw:       "hello world",
			defaultCC: "91",
			expected:  "",
			expectErr: true,
		},
		{
			name:      "More than 15 digits",
			raw:       "911234567890123456",
			defaultCC: "91",
			expected:  "",
			expectErr: true,
		},
		{
			name:      "91 with invalid first national digit (e.g. 5)",
			raw:       "+91 58765 43210",
			defaultCC: "91",
			expected:  "",
			expectErr: true,
		},
		{
			name:      "00 prefix for international format",
			raw:       "00919876543210",
			defaultCC: "91",
			expected:  "919876543210",
			expectErr: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			res, err := NormalizePhone(tt.raw, tt.defaultCC)
			if tt.expectErr {
				assert.Error(t, err)
			} else {
				require.NoError(t, err)
				assert.Equal(t, tt.expected, res)
			}
		})
	}
}
