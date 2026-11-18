// SPDX-License-Identifier: GPL-3.0-only
// SPDX-FileCopyrightText: 2026 Aryan Ameri <github@aryan.ameri.coffee>

package acceptance

import (
	"context"
	"encoding/json"
	"fmt"
	"maps"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/divbast/ci/harness"
)

// Where Prowlarr keeps its state, and what it reads from its environment.
const (
	// configDir is Prowlarr's data directory, the ENTRYPOINT's -data: the
	// one place it writes but /tmp.
	configDir = "/config"
	// keyEnv names the variable that sets the API key, over config.xml's.
	keyEnv = "PROWLARR__AUTH__APIKEY"
	// pingPath is Prowlarr's health route, which answers anyone.
	pingPath = "/ping"
)

// suiteProbe is the suite's own probe, which the harness builds for the
// image's architecture and mounts at [harness.AppProbePath].
const suiteProbe = "./test/probe"

// asRoot runs a container as root, with the engine's default
// capabilities, CAP_CHOWN among them, on a read-only root and with no new
// privileges: how the suite leaves a volume as root on the host, or an
// image run as root, would leave it.
var asRoot = harness.Confinement{User: "0:0", ReadOnly: true, NoNewPrivileges: true}

// configVolume is a fresh named volume at /config, as a user mounts one.
// Each engine gives it the owner and mode of the image's empty /config,
// uid 65532's, on its first mount.
func configVolume(tb testing.TB, e harness.Engine) harness.Mount {
	tb.Helper()
	name, err := e.Volume(tb.Context(), tb, "")
	if err != nil {
		tb.Fatal(err)
	}
	return harness.Mount{Source: name, Target: configDir, Volume: true}
}

// keyed returns env with the API key set, so a test can call the API.
func keyed(env map[string]string) map[string]string {
	keyed := maps.Clone(env)
	if keyed == nil {
		keyed = map[string]string{}
	}
	keyed[keyEnv] = testAPIKey
	return keyed
}

// definitionsHost is where Prowlarr downloads its indexer definitions
// from each time its host starts, 4.8 MB of them. Every container served
// here finds it at 127.0.0.1, which refuses the connection at once: no
// test needs the definitions, none should wait on a download, and a suite
// should not fetch them a dozen times over.
const definitionsHost = "indexers.prowlarr.com"

// analyticsOff turns off what Prowlarr sends upstream, its error reports
// among them. With the definitions refused, each container logs an error,
// which it would report to Servarr's Sentry, and wait 2 s for that report
// to go as it exits. The image ships with analytics on, as upstream does:
// the contract's own container runs that way.
const analyticsOff = "PROWLARR__LOG__ANALYTICSENABLED"

// serveProwlarr serves the image under the contract, with env and mounts,
// and waits until readyPath answers. Prowlarr listens only once its
// database is migrated, so an answer at its ping route means it is up. The
// container is not let fetch the indexer definitions, and sends nothing
// upstream.
func serveProwlarr(t *testing.T, e harness.Engine, image, readyPath string, env map[string]string, mounts ...harness.Mount) *harness.Serving {
	t.Helper()
	env = maps.Clone(env)
	if env == nil {
		env = map[string]string{}
	}
	env[analyticsOff] = "False"
	s, err := e.Serve(t.Context(), t, &harness.ServeSpec{
		Run: harness.RunSpec{
			Image:     image,
			Confine:   harness.Contract(tmpfsSize),
			Env:       env,
			Mounts:    mounts,
			HealthCmd: healthCmd,
			Extra:     []string{"--add-host", definitionsHost + ":127.0.0.1"},
		},
		AppPort:   appPort,
		ReadyPath: readyPath,
	})
	if err != nil {
		t.Fatalf("serving %s: %v", image, err)
	}
	return s
}

// serveFresh serves the image with the API key, on a fresh /config, as a
// new install runs.
func serveFresh(t *testing.T, e harness.Engine, image string) *harness.Serving {
	t.Helper()
	return serveProwlarr(t, e, image, pingPath, keyed(nil), configVolume(t, e))
}

// api sends method path to s's API from the host with the API key, and
// with body as JSON when it is not nil.
func api(ctx context.Context, s *harness.Serving, method, path string, body any) (harness.Response, error) {
	header := http.Header{"X-Api-Key": {testAPIKey}}
	var b []byte
	if body != nil {
		var err error
		if b, err = json.Marshal(body); err != nil {
			return harness.Response{}, err
		}
		header.Set("Content-Type", "application/json")
	}
	return s.Do(ctx, nil, method, path, header, b)
}

// callAPI sends method path to s's API as [api] does, checks that it
// answers want, as the check what, and decodes the answer's JSON into a
// T. It returns false, having failed a check, when either does not hold.
func callAPI[T any](t *testing.T, s *harness.Serving, what string, want int, method, path string, body any) (T, bool) {
	t.Helper()
	var v T
	resp, err := api(t.Context(), s, method, path, body)
	harness.Answers(t, what, want, resp, err)
	if err != nil || resp.Status != want {
		return v, false
	}
	if err := jsonInto(resp, &v); err != nil {
		harness.NewChecks(t).True(what+", in JSON", false, err.Error())
		return v, false
	}
	return v, true
}

// jsonInto decodes resp's body, JSON, into v, and says what the body was
// when it cannot.
func jsonInto(resp harness.Response, v any) error {
	if err := json.Unmarshal(resp.Body, v); err != nil {
		return fmt.Errorf("%w: %s", err, harness.Clip(string(resp.Body)))
	}
	return nil
}

// healthy checks, as the check what, that the shipped probe, run in s's
// container as the engine runs it, exits 0 and prints nothing.
func healthy(t *testing.T, s *harness.Serving, what string) {
	t.Helper()
	checks := harness.NewChecks(t)
	res, err := s.C.Exec(t.Context(), nil, healthCmd...)
	if err != nil {
		checks.True(what, false, err.Error())
		return
	}
	out := string(res.Stdout) + string(res.Stderr)
	checks.True(what, res.Code == 0 && out == "", fmt.Sprintf("exit status %d, printing %s", res.Code, harness.Clip(out)))
}

// prepare runs the suite's own probe with args as the entrypoint of a
// container of image, under confine, with no network and vol mounted, and
// fails t unless it exits 0: how a test puts a volume in the state it
// needs before Prowlarr mounts it.
func prepare(t *testing.T, e harness.Engine, image string, confine harness.Confinement, vol harness.Mount, args ...string) {
	t.Helper()
	ctx := t.Context()
	c, err := e.Start(ctx, t, &harness.RunSpec{
		Image:      image,
		Network:    "none",
		Confine:    confine,
		Mounts:     []harness.Mount{vol},
		Entrypoint: []string{harness.AppProbePath},
		Args:       args,
		Probe:      &harness.ProbeSpec{AppPackage: suiteProbe},
	})
	if err != nil {
		t.Fatalf("preparing %s with %q: %v", vol.Target, args, err)
	}
	code, err := c.WaitExit(ctx, harness.ServeWithin)
	if err == nil && code != 0 {
		err = harness.WithLog(ctx, c, fmt.Errorf("exit status %d", code))
	}
	if err != nil {
		t.Fatalf("preparing %s with %q: %v", vol.Target, args, err)
	}
}

// tag is a tag as Prowlarr's API gives it.
type tag struct {
	ID    int    `json:"id,omitempty"`
	Label string `json:"label"`
}

// hostConfig is the part of GET /api/v1/config/host the tests read: what
// config.xml says now.
type hostConfig struct {
	UpdateMechanism string `json:"updateMechanism"`
}

// systemStatus is the part of GET /api/v1/system/status the tests read.
type systemStatus struct {
	PackageUpdateMechanism string `json:"packageUpdateMechanism"`
	Authentication         string `json:"authentication"`
}

// logged waits until c's log holds needle, and returns an error, with the
// log, once within has passed, or at once if c stops.
func logged(ctx context.Context, c *harness.Container, needle string, within time.Duration) error {
	deadline := time.Now().Add(within)
	for {
		logs, err := c.Logs(ctx)
		if err != nil {
			return err
		}
		if strings.Contains(logs, needle) {
			return nil
		}
		ins, err := c.Inspect(ctx)
		switch {
		case err != nil:
			return err
		case !ins.State.Running:
			return harness.WithLog(ctx, c, fmt.Errorf("the container stopped, exit status %d, before its log said %q", ins.State.ExitCode, needle))
		case !time.Now().Before(deadline):
			return harness.WithLog(ctx, c, fmt.Errorf("its log did not say %q within %s", needle, within))
		}
		if err := harness.Pause(ctx, 250*time.Millisecond); err != nil {
			return err
		}
	}
}

// errText is err's text, or "ok" for none, for a check's detail.
func errText(err error) string {
	if err == nil {
		return "ok"
	}
	return err.Error()
}
