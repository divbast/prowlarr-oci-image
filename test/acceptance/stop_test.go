// SPDX-License-Identifier: GPL-3.0-only
// SPDX-FileCopyrightText: 2026 Aryan Ameri <github@aryan.ameri.coffee>

package acceptance

import (
	"os/exec"
	"testing"
	"time"

	"github.com/divbast/ci/harness"
)

// stopWithin is how long a stop may take: the slowest stop measured, plus
// half again. Once its host had started, Prowlarr stopped in 0.16 to
// 0.22 s on its own, 0.18 to 0.48 s within this suite, and 0.42 to 2.7 s
// with three suites running at once on one host, as on a busy CI runner.
// 2.7 s and half again rounds up to 4.1 s, well inside the 10 s after
// which an engine kills a PID 1 that has not stopped.
const stopWithin = 4100 * time.Millisecond

// hostStarted is what .NET logs once Prowlarr's host has finished
// starting. Prowlarr answers /ping before that, while it still fetches
// its indexer definitions from indexers.prowlarr.com, and a stop then
// waits for the fetch: from 0.5 to 5.8 s on one host, as the network
// allowed, which says nothing of how Prowlarr stops.
const hostStarted = "Application started."

// TestStopsPromptly stops a serving container as a user does, with
// `<engine> stop`, once Prowlarr has finished starting, and checks it
// exits 0 well before the engine's timeout. The kernel drops a signal to
// PID 1 that it does not handle, so a server that never handles SIGTERM
// keeps running until the engine kills it, ten seconds on, with exit 137.
func TestStopsPromptly(t *testing.T) {
	t.Parallel()
	e, image := underTest(t)
	ctx := t.Context()
	s := serveFresh(t, e, image)
	checks := harness.NewChecks(t)
	err := logged(ctx, s.C, hostStarted, harness.ServeWithin)
	checks.True("Prowlarr's host finishes starting", err == nil, errText(err))
	if err != nil {
		return
	}

	start := time.Now()
	out, err := exec.CommandContext(ctx, e.Name(), "stop", s.C.ID).CombinedOutput() //nolint:gosec // G204: the engine and the container are the harness's own
	took := time.Since(start)
	checks.True(e.Name()+" stop returns", err == nil, string(out))
	checks.True("the container stops within "+stopWithin.String(), took < stopWithin, took.String())

	code, err := s.C.WaitExit(ctx, time.Second)
	if err != nil {
		checks.True("the container exits", false, err.Error())
		return
	}
	harness.Equal(checks, "the container exits 0", 0, code)
}
