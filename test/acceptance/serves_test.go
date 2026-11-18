// SPDX-License-Identifier: GPL-3.0-only
// SPDX-FileCopyrightText: 2026 Aryan Ameri <github@aryan.ameri.coffee>

package acceptance

import (
	"mime"
	"net/http"
	"testing"

	"github.com/divbast/ci/harness"
)

// pingBody is exactly what /ping answers once Prowlarr is up: upstream's
// JSON, indented as it sends it.
const pingBody = "{\n  \"status\": \"OK\"\n}"

// uiTitle is the title of the UI's page, which only the UI the frontend
// stage built carries.
const uiTitle = "<title>Prowlarr</title>"

// TestServes checks the app port from a client outside the container,
// through the published port, with no session and no API key: /ping
// answers 200 with exactly upstream's body, / answers 200 with the UI's
// HTML page, and the API refuses a client without the key with 401.
func TestServes(t *testing.T) {
	t.Parallel()
	e, image := underTest(t)
	ctx := t.Context()
	s := serveFresh(t, e, image)
	checks := harness.NewChecks(t)

	resp, err := s.Get(ctx, pingPath, nil)
	harness.Answers(t, "GET /ping from the host answers 200", http.StatusOK, resp, err)
	if err == nil {
		harness.Equal(checks, "GET /ping from the host is exactly upstream's body", pingBody, string(resp.Body))
	}

	resp, err = s.Get(ctx, "/", nil)
	harness.Answers(t, "GET / from the host answers 200", http.StatusOK, resp, err)
	if err == nil {
		media, _, merr := mime.ParseMediaType(resp.Header.Get("Content-Type"))
		if merr != nil {
			media = merr.Error()
		}
		harness.Equal(checks, "GET / from the host is HTML", "text/html", media)
		harness.Contains(t, "GET / from the host is the UI's page", uiTitle, string(resp.Body))
	}

	harness.StatusIs(t, s, "GET /api/v1/system/status from the host without the API key answers 401",
		http.StatusUnauthorized, "/api/v1/system/status")
}
