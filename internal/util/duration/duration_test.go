package duration

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/mechta-market/pulse_agent/internal/errs"
)

func TestParse(t *testing.T) {
	for in, want := range map[string]time.Duration{"": 0, "30m": 30 * time.Minute, " 2h ": 2 * time.Hour, "1d": 24 * time.Hour, "7d": 7 * 24 * time.Hour} {
		got, err := Parse(in)
		require.NoError(t, err, in)
		assert.Equal(t, want, got, in)
	}
	for _, in := range []string{"0d", "-1h", "завтра", "d", "0s"} {
		_, err := Parse(in)
		assert.ErrorIs(t, err, errs.InvalidRequest, in)
	}
}
