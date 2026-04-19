package telemetry

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestMongoCommandMonitor_ReturnsWiredCallbacks(t *testing.T) {
	m := MongoCommandMonitor()
	require.NotNil(t, m)
	assert.NotNil(t, m.Started, "Started callback must be set")
	assert.NotNil(t, m.Succeeded, "Succeeded callback must be set")
	assert.NotNil(t, m.Failed, "Failed callback must be set")
}
