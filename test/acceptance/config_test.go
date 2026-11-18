// SPDX-License-Identifier: GPL-3.0-only
// SPDX-FileCopyrightText: 2026 Aryan Ameri <github@aryan.ameri.coffee>

package acceptance

import (
	"fmt"
	"net/http"
	"testing"

	"github.com/divbast/ci/harness"
)

// deniedConfig opens what .NET says when Prowlarr may not write in
// /config, whichever path under it Prowlarr tries first.
const deniedConfig = "Access to the path '/config"

// TestRefusesUnwritableConfig runs the image on a /config its user cannot
// write: a volume root owns, mode 0755, as a directory root made and bound
// in, or a volume an image run as root left, would be. The container must
// stop within a minute, with a status that is not 0, rather than serve
// with nowhere to keep its state, or wait to be told to stop, and its log
// must name /config, since the log is all a user sees of why.
func TestRefusesUnwritableConfig(t *testing.T) {
	t.Parallel()
	e, image := underTest(t)
	ctx := t.Context()
	vol := configVolume(t, e)
	prepare(t, e, image, asRoot, vol, "root-own")
	checks := harness.NewChecks(t)

	c, err := e.Start(ctx, t, &harness.RunSpec{
		Image:   image,
		Network: "none",
		Confine: harness.Contract(tmpfsSize),
		Mounts:  []harness.Mount{vol},
	})
	if err != nil {
		checks.True("the container starts", false, err.Error())
		return
	}
	code, err := c.WaitExit(ctx, harness.ServeWithin)
	if err != nil {
		checks.True("the container stops within "+harness.ServeWithin.String(), false, harness.WithLog(ctx, c, err).Error())
		return
	}
	checks.True("the container stops within "+harness.ServeWithin.String()+", with a status that is not 0",
		code != 0, fmt.Sprintf("exit status %d", code))
	harness.LogContains(t, c, "its log says it may not write in /config", deniedConfig)
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

// TestMigratedConfig runs the image on a /config another image left, whose
// config.xml says port 9696 and the built-in updater, with no API key in
// the environment. It must serve on 8080, which the image sets over
// config.xml, report Docker as its update mechanism, and be healthy, the
// probe reading the API key from config.xml.
func TestMigratedConfig(t *testing.T) {
	t.Parallel()
	e, image := underTest(t)
	vol := configVolume(t, e)
	prepare(t, e, image, harness.Contract(tmpfsSize), vol, "write", "config.xml", migratedConfig)
	s := serveProwlarr(t, e, image, pingPath, nil, vol)

	harness.StatusIs(t, s, "GET /ping on 8080, from the host, answers 200", http.StatusOK, pingPath)
	status, ok := callAPI[systemStatus](t, s, "GET /api/v1/system/status with config.xml's key answers 200", http.StatusOK,
		http.MethodGet, "/api/v1/system/status", nil)
	if ok {
		harness.Equal(harness.NewChecks(t), "Prowlarr's update mechanism is docker", dockerMechanism, status.PackageUpdateMechanism)
	}
	healthy(t, s, "the probe, with config.xml's key, exits 0 and prints nothing")
}
