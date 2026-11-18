// SPDX-License-Identifier: GPL-3.0-only
// SPDX-FileCopyrightText: 2026 Aryan Ameri <github@aryan.ameri.coffee>

// Package globaljson tests test/global-json.sh, the rewrite the
// Containerfile's backend stage runs over Prowlarr's global.json. The
// script runs against fixtures written to a temporary file: upstream's own
// file and the shapes a release might give it, which it must rewrite, and
// every way it can refuse, each seen refusing with the file left as it was.
package globaljson

import (
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

// script is the script under test, from this package's directory.
const script = "../global-json.sh"

// upstream is global.json byte for byte as Prowlarr's release commit
// 847d4876 ships it.
const upstream = "{\n  \"sdk\": {\n    \"version\": \"10.0.404\"\n  }\n}\n"

// rewritten is what the script must make of a file naming 10.0.404.
const rewritten = `{"sdk":{"version":"10.0.404","rollForward":"latestFeature"}}` + "\n"

// result is how a run of the script ended.
type result struct {
	code           int
	stdout, stderr string
}

// TestRewrite checks the script keeps upstream's sdk.version as the floor
// and gives it the latestFeature policy, whatever else the sdk object
// held.
func TestRewrite(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name, in, want string
	}{
		{name: "upstream", in: upstream, want: rewritten},
		{name: "one-line", in: `{"sdk":{"version":"10.0.404"}}`, want: rewritten},
		{
			// A policy of upstream's own is replaced, not kept beside ours
			name: "policy-replaced",
			in:   `{"sdk":{"version":"10.0.404","rollForward":"disable"}}`,
			want: rewritten,
		},
		{
			name: "version-after-other-keys",
			in:   `{"sdk":{"allowPrerelease":false,"version":"10.0.404"}}`,
			want: rewritten,
		},
		{
			// A preview SDK, should upstream ever pin one
			name: "preview",
			in:   `{"sdk":{"version":"11.0.100-rc.2.25502.107"}}`,
			want: `{"sdk":{"version":"11.0.100-rc.2.25502.107","rollForward":"latestFeature"}}` + "\n",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			file := fixture(t, tt.in)
			r := run(t, file)
			if r.code != 0 || r.stderr != "" {
				t.Fatalf("exit %d, want 0 with nothing on stderr; stdout:\n%s\nstderr:\n%s", r.code, r.stdout, r.stderr)
			}
			if got := read(t, file); got != tt.want {
				t.Errorf("the file holds %q, want %q", got, tt.want)
			}
			if r.stdout != tt.want {
				t.Errorf("stdout is %q, want what the file now holds, %q", r.stdout, tt.want)
			}
		})
	}
}

// TestRefusals checks each file the script cannot keep a floor from is
// refused with exit 1 and one ERROR line saying why, and left untouched,
// so the build fails rather than running on an SDK policy no one chose.
func TestRefusals(t *testing.T) {
	t.Parallel()

	const (
		noVersion  = "has no sdk.version"
		notVersion = "not an SDK version"
	)
	tests := []struct {
		name, in, want string
	}{
		{name: "empty-file", in: "", want: noVersion},
		{name: "no-sdk", in: `{}`, want: noVersion},
		{name: "no-version", in: `{"sdk":{"rollForward":"latestFeature"}}`, want: noVersion},
		{
			// msbuild-sdks pins MSBuild project SDKs, not the .NET SDK
			name: "msbuild-sdks-only",
			in:   `{"msbuild-sdks":{"Microsoft.Build.Traversal":"4.1.0"}}`,
			want: noVersion,
		},
		{
			name: "version-outside-sdk",
			in:   `{"sdk":{"rollForward":"patch"},"tools":{"version":"10.0.404"}}`,
			want: noVersion,
		},
		{name: "word", in: `{"sdk":{"version":"latest"}}`, want: "is 'latest', " + notVersion},
		{name: "two-numbers", in: `{"sdk":{"version":"10.0"}}`, want: notVersion},
		{name: "empty-version", in: `{"sdk":{"version":""}}`, want: notVersion},
		{name: "leading-v", in: `{"sdk":{"version":"v10.0.404"}}`, want: notVersion},
		{
			// An escaped quote, which would carry JSON of its own into the
			// file the script writes
			name: "escaped-quote",
			in:   `{"sdk":{"version":"10.0.404\",\"rollForward\":\"disable"}}`,
			want: notVersion,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			file := fixture(t, tt.in)
			r := run(t, file)
			if r.code != 1 {
				t.Fatalf("exit %d, want 1 with an ERROR line saying %q; stdout:\n%s\nstderr:\n%s", r.code, tt.want, r.stdout, r.stderr)
			}
			lines := errorLines(r.stderr)
			if len(lines) != 1 || !strings.Contains(lines[0], tt.want) || !strings.Contains(lines[0], file) {
				t.Errorf("want one ERROR line naming %s and saying %q; stderr:\n%s", file, tt.want, r.stderr)
			}
			if r.stdout != "" {
				t.Errorf("stdout is %q, want nothing", r.stdout)
			}
			if got := read(t, file); got != tt.in {
				t.Errorf("a refused file was changed: it holds %q, want %q", got, tt.in)
			}
		})
	}
}

// TestUsage checks the script refuses a command line that names no one
// file, rather than reporting a missing sdk.version in a file that is not
// there.
func TestUsage(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	file := fixture(t, upstream)
	tests := []struct {
		name string
		args []string
	}{
		{name: "no-file"},
		{name: "two-files", args: []string{file, file}},
		{name: "a-directory", args: []string{dir}},
		{name: "absent", args: []string{filepath.Join(dir, "absent.json")}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			r := run(t, tt.args...)
			if r.code != 2 {
				t.Errorf("exit %d, want 2; stderr:\n%s", r.code, r.stderr)
			}
			if !strings.HasPrefix(r.stderr, "usage: ") || strings.Count(r.stderr, "\n") != 1 {
				t.Errorf("stderr is %q, want one usage line", r.stderr)
			}
		})
	}
	if got := read(t, file); got != upstream {
		t.Errorf("a usage error changed a file it was given: it holds %q", got)
	}
}

// TestContainerfileRunsIt fails unless the Containerfile copies the script
// in and runs it over global.json before the first dotnet command, which
// is the first thing that reads global.json.
func TestContainerfileRunsIt(t *testing.T) {
	t.Parallel()

	root, err := os.OpenRoot(filepath.Join("..", ".."))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = root.Close() })
	data, err := root.ReadFile("Containerfile")
	if err != nil {
		t.Fatal(err)
	}
	containerfile := string(data)

	copied := regexp.MustCompile(`(?m)^COPY test/global-json\.sh (\S+)$`).FindStringSubmatch(containerfile)
	if copied == nil {
		t.Fatal("the Containerfile does not COPY test/global-json.sh into a build stage")
	}
	ran := regexp.MustCompile(`(?m)^RUN ` + regexp.QuoteMeta(copied[1]) + ` global\.json$`).FindStringIndex(containerfile)
	if ran == nil {
		t.Fatalf("the Containerfile copies the script to %s but never runs RUN %s global.json", copied[1], copied[1])
	}
	dotnet := regexp.MustCompile(`(?m)^RUN (--\S+ )*dotnet `).FindStringIndex(containerfile)
	if dotnet == nil {
		t.Fatal("the Containerfile runs no dotnet command, so this test no longer knows what must follow the rewrite")
	}
	if ran[0] > dotnet[0] {
		t.Error("the Containerfile runs dotnet before it rewrites global.json")
	}
}

// fixture writes content to a new file, and returns its path.
func fixture(t *testing.T, content string) string {
	t.Helper()

	file := filepath.Join(t.TempDir(), "global.json")
	if err := os.WriteFile(file, []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
	return file
}

func read(t *testing.T, file string) string {
	t.Helper()

	data, err := os.ReadFile(file) //nolint:gosec // G304: a file this test wrote under its own TempDir
	if err != nil {
		t.Fatal(err)
	}
	return string(data)
}

// run runs the script with args, and nothing of the host's environment
// but its PATH.
func run(t *testing.T, args ...string) result {
	t.Helper()

	cmd := exec.CommandContext(t.Context(), script, args...) //nolint:gosec // G204: the script is this repository's own, run on files the test made
	cmd.Env = []string{"PATH=" + os.Getenv("PATH")}
	var stdout, stderr strings.Builder
	cmd.Stdout, cmd.Stderr = &stdout, &stderr
	if err := cmd.Run(); err != nil {
		if _, ok := errors.AsType[*exec.ExitError](err); !ok {
			t.Fatalf("%s: %v", script, err)
		}
	}
	return result{cmd.ProcessState.ExitCode(), stdout.String(), stderr.String()}
}

// errorLines returns the lines of stderr that report a refusal.
func errorLines(stderr string) []string {
	var lines []string
	for line := range strings.Lines(stderr) {
		if strings.HasPrefix(line, "ERROR: ") {
			lines = append(lines, line)
		}
	}
	return lines
}
