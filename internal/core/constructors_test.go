package core

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestNewBaseRequiresNameChannelAndArchitecture(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name        string
		base        Base
		errContains string
	}{
		{
			name:        "missing name",
			base:        Base{Channel: "24.04", Architecture: "amd64"},
			errContains: "base name is required",
		},
		{
			name:        "missing channel",
			base:        Base{Name: "ubuntu", Architecture: "amd64"},
			errContains: "base channel is required",
		},
		{
			name:        "missing architecture",
			base:        Base{Name: "ubuntu", Channel: "24.04"},
			errContains: "base architecture is required",
		},
		{
			name:        "blank fields are missing after trimming",
			base:        Base{Name: " \t", Channel: "24.04", Architecture: "amd64"},
			errContains: "base name is required",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			_, err := NewBase(tt.base)
			require.Error(t, err)
			assert.Contains(t, err.Error(), tt.errContains)
		})
	}
}

func TestNewBaseReturnsTrimmedValidBase(t *testing.T) {
	t.Parallel()

	base, err := NewBase(Base{
		Name:         " ubuntu ",
		Channel:      " 24.04 ",
		Architecture: " amd64 ",
	})

	require.NoError(t, err)
	assert.Equal(t, Base{Name: "ubuntu", Channel: "24.04", Architecture: "amd64"}, base)
}
