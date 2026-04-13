package repo

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestMarshalJSONReturnsErrorInsteadOfPanicking(t *testing.T) {
	t.Parallel()

	// Act
	payload, err := marshalJSON(make(chan int))

	// Assert
	require.Error(t, err)
	assert.Nil(t, payload)

}

func TestUnmarshalJSONReturnsError(t *testing.T) {
	t.Parallel()

	// Arrange
	var target map[string]any

	// Act
	err := unmarshalJSON([]byte("{"), &target)

	// Assert
	require.Error(t, err)
}
