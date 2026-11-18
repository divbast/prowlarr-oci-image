// SPDX-License-Identifier: GPL-3.0-only
// SPDX-FileCopyrightText: 2026 Aryan Ameri <github@aryan.ameri.coffee>

package acceptance

import (
	"net/http"
	"testing"

	"github.com/divbast/ci/harness"
)

// urlBase is the URL base a reverse proxy serving Prowlarr under a path
// gives it.
const urlBase = "/prowlarr"

// TestHealthyWithURLBase runs the image with a URL base, as behind a
// reverse proxy that serves it under a path. Prowlarr then answers /ping
// only under the base, and redirects the bare path, which the probe does
// not follow: the probe must ask under the base, and stay healthy.
func TestHealthyWithURLBase(t *testing.T) {
	t.Parallel()
	e, image := underTest(t)
	s := serveProwlarr(t, e, image, urlBase+pingPath, keyed(map[string]string{
		"PROWLARR__SERVER__URLBASE": urlBase,
	}), configVolume(t, e))

	healthy(t, s, "with the URL base "+urlBase+", the probe exits 0 and prints nothing")
	harness.StatusIs(t, s, "GET "+urlBase+pingPath+" from the host answers 200", http.StatusOK, urlBase+pingPath)
}
