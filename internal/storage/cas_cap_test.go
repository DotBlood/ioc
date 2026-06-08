package storage

import (
	"bytes"
	"context"
	"testing"

	"github.com/stretchr/testify/require"
)

// N2: CAS.Store bounds a streaming reader so an unbounded source can't exhaust memory.
func TestCASStore_BoundsInput(t *testing.T) {
	old := maxCASStoreBytes
	maxCASStoreBytes = 16
	defer func() { maxCASStoreBytes = old }()

	c := NewCAS(t.TempDir(), nil)
	if _, err := c.Store(context.Background(), bytes.NewReader(make([]byte, int(maxCASStoreBytes)+1))); err == nil {
		t.Fatal("expected an error: input over the cap must be rejected")
	}
	_, err := c.Store(context.Background(), bytes.NewReader(make([]byte, int(maxCASStoreBytes))))
	require.NoError(t, err, "input at the cap must be accepted")
}
