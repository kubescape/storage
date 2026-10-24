package file

import (
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

func TestWriteGate_BusyWithoutRegisteredHolderIsCaughtByWatchdog(t *testing.T) {
	oldInterval, oldThreshold := gateWatchdogInterval, gateWatchdogThreshold
	gateWatchdogInterval, gateWatchdogThreshold = 10*time.Millisecond, 100*time.Millisecond
	t.Cleanup(func() { gateWatchdogInterval, gateWatchdogThreshold = oldInterval, oldThreshold })

	e := newACG2OnEnv(t)
	require.NoError(t, e.gate.acquire(e.ctx, priorityHigh))
	t.Cleanup(e.gate.release)
	require.True(t, e.gate.held())

	require.Eventually(t, func() bool {
		return e.gate.watchdogFired.Load() >= 1
	}, 2*time.Second, 10*time.Millisecond, "a gate that is busy with no registered holder must be reported by the watchdog")
}
