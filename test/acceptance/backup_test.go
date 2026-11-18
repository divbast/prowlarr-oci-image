// SPDX-License-Identifier: GPL-3.0-only
// SPDX-FileCopyrightText: 2026 Aryan Ameri <github@aryan.ameri.coffee>

package acceptance

import (
	"context"
	"fmt"
	"net/http"
	"strconv"
	"testing"
	"time"

	"github.com/divbast/ci/harness"
)

// seedTags is how many tags the test makes before the backup, so the
// database it backs up holds what the API wrote: as many as tmpfsSize was
// measured with.
const seedTags = 50

// backupWithin bounds the backup command: it took 48 ms when tmpfsSize
// was measured.
const backupWithin = time.Minute

// command is a command as Prowlarr's API gives it.
type command struct {
	ID      int    `json:"id"`
	Status  string `json:"status"`
	Message string `json:"message"`
}

// backup is a backup as GET /api/v1/system/backup lists it.
type backup struct {
	Name string `json:"name"`
	Path string `json:"path"`
	Type string `json:"type"`
	Size int    `json:"size"`
}

// TestBackupFitsTmpfs checks that a backup fits the tmpfs the contract
// grants: Prowlarr copies the database and config.xml into /tmp before it
// zips them into /config. A Backup command sent through the API, after 50
// tags, completes; GET /api/v1/system/backup lists the backup it made;
// and downloading it answers 200 with every byte the listing gives.
func TestBackupFitsTmpfs(t *testing.T) {
	t.Parallel()
	e, image := underTest(t)
	ctx := t.Context()
	s := serveFresh(t, e, image)
	checks := harness.NewChecks(t)

	made, last := 0, ""
	for i := range seedTags {
		resp, err := api(ctx, s, http.MethodPost, "/api/v1/tag", tag{Label: "seed-" + strconv.Itoa(i)})
		switch {
		case err != nil:
			last = err.Error()
		case resp.Status != http.StatusCreated:
			last = fmt.Sprintf("status %d: %s", resp.Status, harness.Clip(string(resp.Body)))
		default:
			made++
		}
	}
	detail := fmt.Sprintf("%d made", made)
	if made != seedTags {
		detail += "; the last refused with " + last
	}
	checks.True(fmt.Sprintf("POST /api/v1/tag makes %d tags", seedTags), made == seedTags, detail)

	cmd, ok := callAPI[command](t, s, "POST /api/v1/command for a Backup answers 201", http.StatusCreated,
		http.MethodPost, "/api/v1/command", map[string]string{"name": "Backup"})
	if !ok {
		return
	}
	done, err := finished(ctx, s, cmd.ID)
	if err != nil {
		checks.True("the Backup command finishes within "+backupWithin.String(), false, err.Error())
		return
	}
	checks.True("the Backup command completes", done.Status == "completed",
		fmt.Sprintf("status %q: %s", done.Status, done.Message))

	backups, ok := callAPI[[]backup](t, s, "GET /api/v1/system/backup answers 200", http.StatusOK,
		http.MethodGet, "/api/v1/system/backup", nil)
	if !ok {
		return
	}
	checks.True("GET /api/v1/system/backup lists one backup, a manual one", len(backups) == 1 && backups[0].Type == "manual",
		fmt.Sprintf("%+v", backups))
	if len(backups) != 1 {
		return
	}
	resp, err := api(ctx, s, http.MethodGet, backups[0].Path, nil)
	harness.Answers(t, "GET "+backups[0].Path+" answers 200", http.StatusOK, resp, err)
	if err == nil {
		harness.Equal(checks, "the download is as long as the listing says", backups[0].Size, len(resp.Body))
	}
}

// finished polls command id until it is no longer queued or started, and
// returns it then, or an error once backupWithin has passed.
func finished(ctx context.Context, s *harness.Serving, id int) (command, error) {
	path := "/api/v1/command/" + strconv.Itoa(id)
	deadline := time.Now().Add(backupWithin)
	var last string
	for {
		resp, err := api(ctx, s, http.MethodGet, path, nil)
		var cmd command
		switch {
		case err != nil:
			last = err.Error()
		case resp.Status != http.StatusOK:
			last = fmt.Sprintf("status %d", resp.Status)
		default:
			if err := jsonInto(resp, &cmd); err != nil {
				last = err.Error()
				break
			}
			if cmd.Status != "queued" && cmd.Status != "started" {
				return cmd, nil
			}
			last = "command status " + cmd.Status
		}
		if !time.Now().Before(deadline) {
			return command{}, fmt.Errorf("GET %s: still not finished after %s: %s", path, backupWithin, last)
		}
		if err := harness.Pause(ctx, 250*time.Millisecond); err != nil {
			return command{}, err
		}
	}
}
