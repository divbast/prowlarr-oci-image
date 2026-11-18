// SPDX-License-Identifier: GPL-3.0-only
// SPDX-FileCopyrightText: 2026 Aryan Ameri <github@aryan.ameri.coffee>

package acceptance

import (
	"net/http"
	"testing"

	"github.com/divbast/ci/harness"
)

// TestHealthyUnderEveryAuthMethod runs the image under each authentication
// method, with authentication required, and checks the probe stays
// healthy: it sends the API key, which every method accepts, so neither
// half needs a session. Each method must be the one in force, as the
// status gives it, or the probe would only have been run under None four
// times.
func TestHealthyUnderEveryAuthMethod(t *testing.T) {
	t.Parallel()
	e, image := underTest(t)
	for _, tc := range []struct{ method, inForce string }{
		{method: "None", inForce: "none"},
		// Prowlarr has retired Basic, and runs Forms in its place
		{method: "Basic", inForce: "forms"},
		{method: "Forms", inForce: "forms"},
		{method: "External", inForce: "external"},
	} {
		t.Run(tc.method, func(t *testing.T) {
			t.Parallel()
			s := serveProwlarr(t, e, image, pingPath, keyed(map[string]string{
				"PROWLARR__AUTH__METHOD":   tc.method,
				"PROWLARR__AUTH__REQUIRED": "Enabled",
			}), configVolume(t, e))

			status, ok := callAPI[systemStatus](t, s, "GET /api/v1/system/status answers 200", http.StatusOK,
				http.MethodGet, "/api/v1/system/status", nil)
			if ok {
				harness.Equal(harness.NewChecks(t), "the authentication in force is "+tc.inForce, tc.inForce, status.Authentication)
			}
			healthy(t, s, "under "+tc.method+", the probe exits 0 and prints nothing")
		})
	}
}
