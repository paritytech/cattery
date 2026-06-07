package config

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
)

func TestCoordinationConfigWithDefaults(t *testing.T) {
	// empty config gets the memory backend and the standard cadence
	d := CoordinationConfig{}.WithDefaults()
	assert.Equal(t, CoordinationBackendMemory, d.Backend)
	assert.Equal(t, DefaultLeaseTTL, d.Lease.TTL)
	assert.Equal(t, DefaultLeaseTTL/3, d.Lease.RenewInterval, "renew defaults to TTL/3")
	assert.Equal(t, DefaultLeaseRetryInterval, d.Lease.RetryInterval)

	// explicit values are preserved; renew is derived from the custom TTL
	c := CoordinationConfig{
		Backend: CoordinationBackendMongo,
		Lease:   LeaseConfig{TTL: 60 * time.Second},
	}.WithDefaults()
	assert.Equal(t, CoordinationBackendMongo, c.Backend)
	assert.Equal(t, 60*time.Second, c.Lease.TTL)
	assert.Equal(t, 20*time.Second, c.Lease.RenewInterval)
	assert.Equal(t, DefaultLeaseRetryInterval, c.Lease.RetryInterval)
}
