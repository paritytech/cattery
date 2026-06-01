package election

import (
	"cattery/lib/config"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestNewFromConfig_Memory(t *testing.T) {
	e, err := NewFromConfig(config.CoordinationConfig{Backend: config.CoordinationBackendMemory}, nil)
	require.NoError(t, err)
	assert.IsType(t, MemoryElector{}, e)
}

func TestNewFromConfig_MongoRequiresCollection(t *testing.T) {
	_, err := NewFromConfig(config.CoordinationConfig{Backend: config.CoordinationBackendMongo}, nil)
	assert.Error(t, err, "mongo backend without a collection must error")
}

func TestNewFromConfig_UnknownBackend(t *testing.T) {
	_, err := NewFromConfig(config.CoordinationConfig{Backend: "bogus"}, nil)
	assert.Error(t, err)
}
