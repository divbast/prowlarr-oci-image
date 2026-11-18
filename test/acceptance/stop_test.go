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
// half again, with the definitions download refused and analytics off,
// as serveProwlarr runs every container. Once its host had started,
// Prowlarr stopped in 0.16 to 0.23 s on its own, 0.40 to 1.10 s within
// this suite, and 0.60 to 2.81 s with three suites running at once on one
// host, as on a busy CI runner. 2.81 s and half again rounds up to 4.3 s,
// well inside the 10 s after which an engine kills a PID 1 that has not
// stopped.
const stopWithin = 4300 * time.Millisecond

// hostStarted is what .NET logs once Prowlarr's host has finished
// starting. Prowlarr answers /ping just before that, while its host runs
// what it runs on starting, and a stop in that window waits for it to
// finish. With the definitions download refused that is 0.1 s; when the
// download went ahead, it was 0.5 to 5.8 s, as the network allowed.
// Waiting for this line keeps the stop measured a stop alone, whatever
// starting comes to run.
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
