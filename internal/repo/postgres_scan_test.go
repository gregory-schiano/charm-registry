package repo

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestMarshalJSONReturnsErrorInsteadOfPanicking(t *testing.T) {
	t.Parallel()

	payload, err := marshalJSON(make(chan int))

	require.Error(t, err)
	assert.Nil(t, payload)
}

func TestUnmarshalJSONReturnsError(t *testing.T) {
	t.Parallel()

	var target map[string]any

	err := unmarshalJSON([]byte("{"), &target)

	require.Error(t, err)
}
