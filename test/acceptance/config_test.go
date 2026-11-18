// SPDX-License-Identifier: GPL-3.0-only
// SPDX-FileCopyrightText: 2026 Aryan Ameri <github@aryan.ameri.coffee>

package acceptance

import (
	"net/http"
	"testing"

	"github.com/divbast/ci/harness"
)

// deniedConfig opens what .NET says when Prowlarr may not write in
// /config, whichever path under it Prowlarr tries first.
const deniedConfig = "Access to the path '/config"

// Prowlarr's exit statuses, as its ConsoleApp names them: UnknownFailure
// for an exception it did not foresee, as .NET's refusal to write in
// /config is to it, and NonRecoverableFailure for a ProwlarrStartupException,
// which -exitimmediately has it exit with at once rather than wait an
// hour.
const (
	unknownFailure        = 1
	nonRecoverableFailure = 3
)

// TestRefusesUnwritableConfig runs the image on a /config its user cannot
// write: a volume root owns, mode 0755, as a directory root made and bound
// in, or a volume an image run as root left, would be. The container must
// exit within a minute, with UnknownFailure, rather than serve with
// nowhere to keep its state, or wait to be told to stop, and its log must
// name /config, since the log is all a user sees of why.
func TestRefusesUnwritableConfig(t *testing.T) {
	t.Parallel()
	e, image := underTest(t)
	vol := configVolume(t, e)
	prepare(t, e, image, asRoot, vol, "root-own")
	harness.Refuses(t, e, refusing(image, vol), unknownFailure, "on a /config root owns", deniedConfig)
}

// pidUnwritable is what Prowlarr says when it cannot write its PID file.
const pidUnwritable = "Prowlarr failed to start: Unable to write PID file /config/prowlarr.pid"

// TestExitsOnStartupFailure runs the image on a /config it can write, in
// which its PID file, prowlarr.pid, is a directory. Prowlarr cannot write
// the file, and fails to start with a ProwlarrStartupException, which
// upstream calls non-recoverable: unless given -exitimmediately, it then
// waits an hour for a key that no one presses in a container. The
// container must exit within a minute with NonRecoverableFailure, the
// status only -exitimmediately gives that wait, and its log must say why.
func TestExitsOnStartupFailure(t *testing.T) {
	t.Parallel()
	e, image := underTest(t)
	vol := configVolume(t, e)
	prepare(t, e, image, harness.Contract(tmpfsSize), vol, "mkdir", "prowlarr.pid")
	harness.Refuses(t, e, refusing(image, vol), nonRecoverableFailure, "with prowlarr.pid a directory", pidUnwritable)
}

// refusing is a run of the image under the contract, with vol at /config
// and no network, for a start Prowlarr must refuse.
func refusing(image string, vol harness.Mount) *harness.RunSpec {
	return &harness.RunSpec{
		Image:   image,
		Network: "none",
		Confine: harness.Contract(tmpfsSize),
		Mounts:  []harness.Mount{vol},
	}
}

// migratedConfig is a config.xml as another Prowlarr image leaves it in
// /config: upstream's port, 9696, the built-in updater, and the API key
// that install made.
const migratedConfig = `<Config>
  <BindAddress>*</BindAddress>
  <Port>9696</Port>
  <SslPort>6969</SslPort>
  <EnableSsl>False</EnableSsl>
  <LaunchBrowser>True</LaunchBrowser>
  <ApiKey>` + testAPIKey + `</ApiKey>
  <AuthenticationMethod>None</AuthenticationMethod>
  <AuthenticationRequired>Enabled</AuthenticationRequired>
  <Branch>master</Branch>
  <LogLevel>info</LogLevel>
  <UrlBase></UrlBase>
  <InstanceName>Prowlarr</InstanceName>
  <UpdateMechanism>BuiltIn</UpdateMechanism>
</Config>
`

// mechanismChanged is what Prowlarr logs as it changes config.xml's
// BuiltIn update mechanism to the Docker one package_info names, once its
// host has started.
const mechanismChanged = "Update mechanism BuiltIn not supported in the current configuration, changing to Docker."

// TestMigratedConfig runs the image on a /config another image left, whose
// config.xml says port 9696 and the built-in updater, with no API key in
// the environment. It must serve on 8080, which the image sets over
// config.xml; answer the API with config.xml's key; once its host has
// started, have changed config.xml's update mechanism to Docker; and be
// healthy, the probe reading the API key from config.xml.
func TestMigratedConfig(t *testing.T) {
	t.Parallel()
	e, image := underTest(t)
	vol := configVolume(t, e)
	prepare(t, e, image, harness.Contract(tmpfsSize), vol, "write", "config.xml", migratedConfig)
	s := serveProwlarr(t, e, image, pingPath, nil, vol)
	checks := harness.NewChecks(t)

	harness.StatusIs(t, s, "GET /ping on 8080, from the host, answers 200", http.StatusOK, pingPath)
	err := logged(t.Context(), s.C, hostStarted, harness.ServeWithin)
	checks.True("Prowlarr's host finishes starting", err == nil, errText(err))
	harness.LogContains(t, s.C, "Prowlarr says it changes BuiltIn to Docker", mechanismChanged)
	host, ok := callAPI[hostConfig](t, s, "GET /api/v1/config/host with config.xml's key answers 200", http.StatusOK,
		http.MethodGet, "/api/v1/config/host", nil)
	if ok {
		harness.Equal(checks, "config.xml's update mechanism is docker", dockerMechanism, host.UpdateMechanism)
	}
	healthy(t, s, "the probe, with config.xml's key, exits 0 and prints nothing")
}
