package election

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestSanitizeName(t *testing.T) {
	cases := map[string]string{
		"docker-local": "docker-local",
		"Docker_Local": "docker-local",
		"UPPER":        "upper",
		"  a@b!! ":     "a-b",
		"x86_64-large": "x86-64-large",
	}
	for in, want := range cases {
		assert.Equal(t, want, sanitizeName(in), "sanitizeName(%q)", in)
	}
}

func TestResolveNamespace(t *testing.T) {
	// explicit value wins
	assert.Equal(t, "explicit", resolveNamespace("explicit"))

	// falls back to POD_NAMESPACE
	t.Setenv("POD_NAMESPACE", "from-env")
	assert.Equal(t, "from-env", resolveNamespace(""))
}

func TestResolveNamespace_Default(t *testing.T) {
	// empty env + no service-account file ⇒ "default"
	t.Setenv("POD_NAMESPACE", "")
	assert.Equal(t, "default", resolveNamespace(""))
}
