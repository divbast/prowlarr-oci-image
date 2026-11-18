// SPDX-License-Identifier: GPL-3.0-only
// SPDX-FileCopyrightText: 2026 Aryan Ameri <github@aryan.ameri.coffee>

package acceptance

import (
	"context"
	"fmt"
	"net/http"
	"slices"
	"testing"
	"time"

	"github.com/divbast/ci/contract"
	"github.com/divbast/ci/harness"
)

// What the image's contract says of every container here.
const (
	// appPort is the one port Prowlarr listens on: the UI, the API and
	// /ping, its health route.
	appPort = 8080
	// tmpfsSize is the size of /tmp the README states. Every container
	// that runs Prowlarr gets exactly this, so no check passes on more
	// room than the contract grants. Prowlarr stages a backup there: the database and
	// config.xml, copied out before it zips them into /config. A backup of
	// a database holding 50 tags staged 229,962 bytes, which failed on a
	// 200 KiB /tmp and fitted 512 KiB: twice it rounds up to 512 KiB,
	// below the 64 MiB floor, so the floor it is.
	tmpfsSize = "64m"
)

// binary is the image's probe, which its HEALTHCHECK and the contract's
// Exec run.
const binary = "/usr/local/bin/divbast-prowlarr"

// serverArgv is PID 1's argv: the image's ENTRYPOINT, since nothing runs
// before Prowlarr.
var serverArgv = []string{"/app/bin/Prowlarr", "-nobrowser", "-exitimmediately", "-data=/config"}

// healthCmd is the shipped health probe, as the image's HEALTHCHECK runs
// it and a Podman user passes it with --health-cmd.
var healthCmd = []string{binary, "health"}

// TestContract runs divbast's confinement contract against the image, on a
// network with no route out: no capability, no new privileges, a seccomp
// filter, a read-only root and no writable mount that can execute but
// /config, uid 65532, Prowlarr as PID 1, no shell and no package manager,
// one listener, no egress, the HEALTHCHECK, and /ping on the app port, as
// Prowlarr has no health port.
func TestContract(t *testing.T) {
	t.Parallel()
	e, image := underTest(t)
	contract.Run(t, &contract.Config{
		Image:   image,
		AppPort: appPort,
		// No health port: Prowlarr serves its health route on the app port
		HealthPort: 0,
		HealthPath: "/ping",
		TmpfsSize:  tmpfsSize,
		PID1:       serverArgv,
		Exec:       []string{binary, "version"},
		HealthCmd:  healthCmd,
		Healthcheck: contract.Healthcheck{
			Test:        slices.Concat([]string{"CMD"}, healthCmd),
			Interval:    30 * time.Second,
			Timeout:     10 * time.Second,
			StartPeriod: 300 * time.Second,
			Retries:     3,
		},
		// No Env: the container runs as shipped, so the probe the engine
		// runs, and health-probe, read the key Prowlarr wrote to config.xml
		// on the fresh volume, as in a plain run
		Mounts: func(tb testing.TB) []harness.Mount {
			tb.Helper()
			return []harness.Mount{configVolume(tb, e)}
		},
		ReadyProbe: pingOK,
	})
}

// pingOK returns once GET /ping, sent from inside c through the probe,
// answers 200, which Prowlarr does once its database is migrated and its
// host is up, or an error with the last answer when ctx ends first.
func pingOK(ctx context.Context, _ *harness.Container, p *harness.Probe) error {
	url := fmt.Sprintf("http://127.0.0.1:%d/ping", appPort)
	for {
		resp, err := p.HTTP(ctx, http.MethodGet, url, nil, "")
		if err == nil && resp.Status == http.StatusOK {
			return nil
		}
		last := fmt.Sprintf("status %d", resp.Status)
		if err != nil {
			last = err.Error()
		}
		if err := harness.Pause(ctx, time.Second); err != nil {
			return fmt.Errorf("GET %s never answered 200, last %s: %w", url, last, err)
		}
	}
}
