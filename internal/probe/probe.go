// SPDX-License-Identifier: GPL-3.0-only
// SPDX-FileCopyrightText: 2026 Aryan Ameri <github@aryan.ameri.coffee>

// Package probe is the image's health check: it asks Prowlarr whether it
// answers, and whether it is the build the image pins.
package probe

import (
	"context"
	"encoding/json"
	"encoding/xml"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"
)

// Options says where Prowlarr is and what it must answer.
type Options struct {
	// Base is the URL up to and including the URL base, with no trailing /.
	Base string
	// APIKey is sent as X-Api-Key. It works under every authentication
	// method, so the probe needs no login.
	APIKey string
	// Version is the Prowlarr version the image pins.
	Version string
	// Within bounds each half, ping and status, separately.
	Within time.Duration
}

// Probe checks Prowlarr in two halves, neither of which implies the other.
// ping: GET <Base>/ping answers 200 with {"status":"OK"}, which proves
// the server and its database answer. status: GET
// <Base>/api/v1/system/status with the API key answers 200 for appName
// Prowlarr at Version, which proves the build is the pinned one. It prints
// nothing and returns 0 when both hold. Otherwise it prints one
// "ping: <reason>" or "status: <reason>" line to stderr for each half that
// failed, the only record of why, and returns 1.
func Probe(ctx context.Context, o Options, stderr io.Writer) int {
	code := 0
	if err := get(ctx, o, "/ping", "", func(body []byte) error {
		var p struct {
			Status string `json:"status"`
		}
		if err := json.Unmarshal(body, &p); err != nil {
			return fmt.Errorf("body is not JSON: %w", err)
		}
		if p.Status != "OK" {
			return fmt.Errorf("status is %q, want %q", p.Status, "OK")
		}
		return nil
	}); err != nil {
		_, _ = fmt.Fprintln(stderr, "ping:", err)
		code = 1
	}
	if err := get(ctx, o, "/api/v1/system/status", o.APIKey, func(body []byte) error {
		var s struct {
			AppName string `json:"appName"`
			Version string `json:"version"`
		}
		if err := json.Unmarshal(body, &s); err != nil {
			return fmt.Errorf("body is not JSON: %w", err)
		}
		if s.AppName != "Prowlarr" {
			return fmt.Errorf("appName is %q, want %q", s.AppName, "Prowlarr")
		}
		if s.Version != o.Version {
			return fmt.Errorf("version is %q, want %q", s.Version, o.Version)
		}
		return nil
	}); err != nil {
		_, _ = fmt.Fprintln(stderr, "status:", err)
		code = 1
	}
	return code
}

// get sends GET <Base><path> within o.Within, with the API key if there is
// one, and returns what check makes of a 200 body, or why there was none.
func get(ctx context.Context, o Options, path, apiKey string, check func(body []byte) error) error {
	ctx, cancel := context.WithTimeout(ctx, o.Within)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, o.Base+path, http.NoBody)
	if err != nil {
		return err
	}
	if apiKey != "" {
		req.Header.Set("X-Api-Key", apiKey)
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return err
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("GET %s answered %d, want 200", path, resp.StatusCode)
	}
	body, err := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if err != nil {
		return err
	}
	return check(body)
}

// Base is where Prowlarr listens, as the probe reaches it: loopback on
// PROWLARR__SERVER__PORT (8080 if unset), then PROWLARR__SERVER__URLBASE
// with a leading / and no trailing /, or nothing.
func Base(getenv func(string) string) string {
	port := getenv("PROWLARR__SERVER__PORT")
	if port == "" {
		port = "8080"
	}
	base := ""
	if u := strings.Trim(getenv("PROWLARR__SERVER__URLBASE"), "/"); u != "" {
		base = "/" + u
	}
	return "http://127.0.0.1:" + port + base
}

// APIKey is PROWLARR__AUTH__APIKEY if set, else the ApiKey in config.xml,
// which configXML opens. Neither is an error naming both.
func APIKey(getenv func(string) string, configXML func() (io.ReadCloser, error)) (string, error) {
	if k := getenv("PROWLARR__AUTH__APIKEY"); k != "" {
		return k, nil
	}
	r, err := configXML()
	if err != nil {
		return "", fmt.Errorf("no API key: PROWLARR__AUTH__APIKEY is unset and config.xml is unreadable: %w", err)
	}
	defer func() { _ = r.Close() }()
	var c struct {
		APIKey string `xml:"ApiKey"`
	}
	if err := xml.NewDecoder(r).Decode(&c); err != nil {
		return "", fmt.Errorf("no API key: PROWLARR__AUTH__APIKEY is unset and config.xml is invalid: %w", err)
	}
	if c.APIKey == "" {
		return "", errors.New("no API key: PROWLARR__AUTH__APIKEY is unset and config.xml has no ApiKey")
	}
	return c.APIKey, nil
}
