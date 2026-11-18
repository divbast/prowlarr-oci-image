// SPDX-License-Identifier: GPL-3.0-only
// SPDX-FileCopyrightText: 2026 Aryan Ameri <github@aryan.ameri.coffee>

package acceptance

import (
	"context"
	"fmt"
	"net/http"
	"testing"
	"time"

	"github.com/divbast/ci/harness"
)

// restarting is what Prowlarr logs once its host has stopped for a
// restart and it builds the host again, in the same process.
const restarting = "Application restart requested, reinitializing host"

// restartWithin bounds a restart from the UI: the host stopped and served
// /ping again in about 1.3 s when it was measured.
const restartWithin = time.Minute

// TestRestartsInProcess restarts Prowlarr as its UI does, with POST
// /api/v1/system/restart. Prowlarr stops its host and builds it again
// inside the one process, which, as PID 1, must not exit: the container
// stops with it, and no engine starts it again unless told to. So the
// container must still be running, its PID 1 the process the engine
// started, with the same argv, and /ping must answer 200 again, after
// Prowlarr has said it is restarting, within a minute.
func TestRestartsInProcess(t *testing.T) {
	t.Parallel()
	e, image := underTest(t)
	ctx := t.Context()
	s := serveFresh(t, e, image)
	checks := harness.NewChecks(t)

	before, err := s.C.Inspect(ctx)
	if err != nil {
		t.Fatal(err)
	}
	harness.RunRecords(t, s.Probe, "assert-pid1", serverArgv...)

	resp, err := api(ctx, s, http.MethodPost, "/api/v1/system/restart", nil)
	harness.Answers(t, "POST /api/v1/system/restart answers 200", http.StatusOK, resp, err)
	err = servesAgain(ctx, s)
	checks.True("Prowlarr says it is restarting, and /ping answers 200 again within "+restartWithin.String(), err == nil, errText(err))

	after, err := s.C.Inspect(ctx)
	if err != nil {
		t.Fatal(err)
	}
	checks.True("the container is still running", after.State.Running, after.State.Status)
	checks.True("PID 1 is still the process the engine started", after.State.StartedAt.Equal(before.State.StartedAt),
		fmt.Sprintf("started at %s, and at %s before the restart", after.State.StartedAt, before.State.StartedAt))
	harness.RunRecords(t, s.Probe, "assert-pid1", serverArgv...)
}

// servesAgain waits until s's log says Prowlarr is restarting, and then
// until /ping answers 200, which only the host built after that can. It
// returns an error at once if the container stops, and once restartWithin
// has passed.
func servesAgain(ctx context.Context, s *harness.Serving) error {
	deadline := time.Now().Add(restartWithin)
	if err := logged(ctx, s.C, restarting, restartWithin); err != nil {
		return err
	}
	for {
		resp, err := s.Get(ctx, pingPath, nil)
		if err == nil && resp.Status == http.StatusOK {
			return nil
		}
		last := fmt.Sprintf("status %d", resp.Status)
		if err != nil {
			last = err.Error()
		}
		ins, err := s.C.Inspect(ctx)
		switch {
		case err != nil:
			return err
		case !ins.State.Running:
			return harness.WithLog(ctx, s.C, fmt.Errorf("the container stopped, exit status %d", ins.State.ExitCode))
		case !time.Now().Before(deadline):
			return harness.WithLog(ctx, s.C, fmt.Errorf("GET /ping did not answer 200 within %s: %s", restartWithin, last))
		}
		if err := harness.Pause(ctx, 250*time.Millisecond); err != nil {
			return err
		}
	}
}
