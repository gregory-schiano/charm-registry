package repo

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestToInt32(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name    string
		value   int
		want    int32
		wantErr bool
	}{
		{name: "zero", value: 0, want: 0},
		{name: "max", value: 1<<31 - 1, want: 1<<31 - 1},
		{name: "min", value: -1 << 31, want: -1 << 31},
		{name: "overflow", value: 1 << 31, wantErr: true},
		{name: "underflow", value: -1<<31 - 1, wantErr: true},
	}

	for _, tt := range tests {
		tt := tt
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			got, err := toInt32(tt.value)

			if tt.wantErr {
				require.Error(t, err)
				return
			}
			require.NoError(t, err)
			assert.Equal(t, tt.want, got)
		})
	}
}

func TestInt32Ptr(t *testing.T) {
	t.Parallel()

	// Arrange
	value := 42

	// Act
	ptr, err := int32Ptr(&value)

	// Assert
	require.NoError(t, err)
	require.NotNil(t, ptr)
	assert.Equal(t, int32(42), *ptr)
}
