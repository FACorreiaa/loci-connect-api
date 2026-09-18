//go:build integration

package placeintel

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

// The submitter is the first voice, so a fresh submission needs exactly one
// more person.
func TestConfirmationsNeeded(t *testing.T) {
	t.Parallel()
	assert.Equal(t, int32(1), confirmationsNeeded(0), "submitter counts as one")
	assert.Equal(t, int32(0), confirmationsNeeded(1), "one confirmation is enough")
	assert.Equal(t, int32(0), confirmationsNeeded(5), "never negative")
}
