// SPDX-License-Identifier: GPL-3.0-only
// SPDX-FileCopyrightText: 2026 Aryan Ameri <github@aryan.ameri.coffee>

package acceptance

import (
	"net/http"
	"testing"

	"github.com/divbast/ci/harness"
)

// dockerMechanism is how Prowlarr's status names the update mechanism
// that leaves updates to the image, which the image's package_info asks
// for: the UI then offers no update of its own, which in a read-only
// image could only fail.
const dockerMechanism = "docker"

// TestUpdaterIsDocker checks that Prowlarr reports Docker as its update
// mechanism.
func TestUpdaterIsDocker(t *testing.T) {
	t.Parallel()
	e, image := underTest(t)
	s := serveFresh(t, e, image)

	status, ok := callAPI[systemStatus](t, s, "GET /api/v1/system/status answers 200", http.StatusOK,
		http.MethodGet, "/api/v1/system/status", nil)
	if ok {
		harness.Equal(harness.NewChecks(t), "Prowlarr's update mechanism is docker", dockerMechanism, status.PackageUpdateMechanism)
	}
}
