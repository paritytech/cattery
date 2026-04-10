package scaleSetPoller

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestNewManager(t *testing.T) {
	m := NewManager()
	assert.NotNil(t, m)
}

func TestManagerRegisterAndGet(t *testing.T) {
	m := NewManager()

	// A nil poller is fine for testing the registry itself
	m.Register("type-a", &Poller{})
	m.Register("type-b", &Poller{})

	assert.NotNil(t, m.GetPoller("type-a"))
	assert.NotNil(t, m.GetPoller("type-b"))
}

func TestManagerGetNonExistent(t *testing.T) {
	m := NewManager()
	assert.Nil(t, m.GetPoller("does-not-exist"))
}

func TestManagerRegisterOverwrite(t *testing.T) {
	m := NewManager()

	poller1 := &Poller{}
	poller2 := &Poller{}

	m.Register("type-a", poller1)
	m.Register("type-a", poller2)

	result := m.GetPoller("type-a")
	assert.Same(t, poller2, result)
}
