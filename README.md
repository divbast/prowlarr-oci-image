<!-- SPDX-License-Identifier: GPL-3.0-only -->
<!-- SPDX-FileCopyrightText: 2026 Aryan Ameri <github@aryan.ameri.coffee> -->

# prowlarr-oci-image

[Prowlarr](https://github.com/Prowlarr/Prowlarr), the indexer manager for
Sonarr, Lidarr and the other Servarr apps, in an image that runs at zero
Linux capabilities, with no new privileges, on a read-only root filesystem
as a non-root user, with no shell in it. It is built from Prowlarr's
source, and goes through divbast/ci's shared pipeline: lint, a build for
amd64 and arm64, the confinement contract and its own acceptance suite
under Docker and rootless Podman, a vulnerability scan, and a daily
rehearsed publish.

Its publish job does not publish yet. [Publishing](#publishing) says what
it does until it is switched on.

## Base

The image is Prowlarr's stable release, built from source: the version and
commit the Containerfile's `PROWLARR_VERSION` and `PROWLARR_COMMIT` name,
which are the release and the commit its tag names. It runs on Docker
Hardened Images'
[`aspnetcore`](https://hub.docker.com/hardened-images/catalog) for
Debian 13: the .NET runtime, ICU and OpenSSL, no shell and no package
manager. The app is built framework-dependent, so rebuilding the image
patches the runtime. Every stage of the build is also
a DHI image, and CI refuses to build until Docker's signature on each is
verified:

- `fetch` checks out the commit and asserts the release's tag still names
  it.
- `frontend` builds the UI with yarn, from `yarn.lock`, which pins every
  package by its hash.
- `backend` publishes the .NET app, with NuGet checking the signature of
  each package. Upstream keeps no lock file and pins every package by an
  exact version from nuget.org alone, so there is none here.
- `probe` builds the health probe, a static Go binary on the standard
  library alone.

Prowlarr is never patched. The one change the build makes to an upstream
file is to `global.json`, in the backend stage, so that any later .NET SDK
of the same major and minor can build it; that file does not ship.

## Container Features

| Property | Value |
|---|---|
| Capabilities | none (`--cap-drop ALL`) |
| Privileges | no new ones (`--security-opt no-new-privileges`), under the engine's seccomp filter |
| Root filesystem | read-only |
| User | uid/gid 65532 (`nonroot`) |
| Port | 8080 (the UI, the API and `/ping`), and nothing else listens |
| tmpfs required | `/tmp`, which nothing can run from (`--tmpfs /tmp:size=64m,noexec,nosuid,nodev`); [size it](#tmp) to your database |
| Volume | `/config`, which you mount, owned by 65532 |
| Processes | one: `Prowlarr`, as PID 1 |
| Shell in image | none |
| Package manager | none |
| Outbound network | none needed to serve or be healthy; [Prowlarr reaches out at rest](#egress-at-rest) |
| Healthy | the shipped probe: `/ping` 200 **and** `GET /api/v1/system/status` reporting Prowlarr at this image's version |

## Running

There is no published image, so build one first, with Podman to match the
command below: `docker login dhi.io`, since every image the build starts
from is Docker Hardened, then `just setup`, then `ENGINE=podman just
build`, which tags it `localhost/prowlarr:dev`.

```bash
podman run -d --name prowlarr \
  --cap-drop ALL --read-only --read-only-tmpfs=false \
  --security-opt no-new-privileges \
  --user 65532:65532 \
  --tmpfs /tmp:size=64m,noexec,nosuid,nodev \
  -v prowlarr-config:/config \
  -p 127.0.0.1:8080:8080 \
  --health-cmd '["/usr/local/bin/divbast-prowlarr","health"]' \
  localhost/prowlarr:dev
```

`--read-only-tmpfs=false` and `--health-cmd` are Podman's alone. Without
the first, Podman mounts writable tmpfs of its own at `/run`, `/var/tmp`
and elsewhere beside the read-only root; without the second, it never
runs the image's health check, as [Health](#health) says. Docker takes
the same command without them, and runs the image's `HEALTHCHECK` itself.

Prowlarr serves its UI and API on 8080. **A first start on an empty
`/config` migrates a new database and leaves authentication method `None`
with no user, so anyone who can reach the port can use Prowlarr.** That
is why the command publishes the port on the host's loopback alone. Open
`http://127.0.0.1:8080` on the host first: Prowlarr's first-run prompt
sets the authentication method, a username and a password. Only then
publish the port beyond the host, or put Prowlarr behind your reverse
proxy. [Authentication](#authentication) says more. A stop (SIGTERM) ends
Prowlarr well inside the engine's timeout, with exit 0. Restarting it
from its UI happens inside the same process, so the container keeps
running.

### `/config`

The image carries an empty `/config` owned by 65532, and declares no
`VOLUME`. A new named volume takes its owner from it on first mount, so
`-v prowlarr-config:/config` just works. Prowlarr keeps everything it
writes there: `config.xml`, its databases, logs, backups and indexer
definitions.

- A `/config` that 65532 cannot write, such as a bind mount root made, or a
  volume an image run as root left, makes Prowlarr exit 1 at once, with
  `Access to the path '/config…'` in its log. Give the directory, and
  everything in it, to 65532 instead of running the image as root: `chown
  -R 65532:65532 <dir>` as root, or under rootless Podman `podman unshare
  chown -R 65532:65532 <dir>`. For a Podman bind mount, the `:U` option
  does the same at start: `-v ./config:/config:U`.
- On an SELinux host, such as Fedora or RHEL, a bind mount also needs
  Podman to relabel it, or the container cannot use it: add `:Z`, as in
  `-v ./config:/config:U,Z`. `/config` is Prowlarr's alone, so the private
  label `Z` fits it.
- With no mount at all, the read-only root refuses Prowlarr's first write
  and it stops at once, so the mistake is visible. Nothing would have been
  kept.
- A startup failure Prowlarr calls non-recoverable, such as a PID file it
  cannot write, exits 3. The image passes `-exitimmediately`; without it
  Prowlarr waits an hour for a key press no one can make in a container.

### Coming from another image

Copy the old `/config` into the new volume or directory, then give it all
to 65532 as above, with `chown -R`: the files another image wrote are
usually its PUID's, such as 1000.

This image serves on 8080, not Prowlarr's usual 9696, and its
`PROWLARR__SERVER__PORT` wins over the old `config.xml`. Prowlarr wrote
its own address, with the port, into every indexer it synced into Sonarr,
Radarr, Lidarr and its other apps, and those indexers fail until they
point at the new one. If the apps reach Prowlarr through a port on the
host, publishing the new port on the old number (`-p 9696:8080`) keeps
that address working. Otherwise, in Prowlarr, under Settings, Apps, open
each application, set its Prowlarr Server to the new address, such as
`http://prowlarr:8080`, and save, then press Sync App Indexers. Saving an
application on Full Sync pushes the new address to each of its indexers.
Prowlarr knows the indexers it made by its records and by its API key,
which `config.xml` brought along, so the sync updates them rather than
adding copies. An application on Add and Remove Only gets no updates:
change the URL of each Prowlarr indexer in that app itself.

### `/tmp`

`/tmp` is the contract's one tmpfs, and Prowlarr uses it to stage a backup:
it copies the whole `prowlarr.db` there before it zips the backup into
`/config`. So `/tmp` must hold at least twice `prowlarr.db`, or the backup,
the weekly scheduled one included, fails with "database or disk is full".

64 MiB, the size above and the one the acceptance suite runs under, is far
more than a new install needs: a database holding 50 tags staged 229,962
bytes. To size it for your own, read the size of `prowlarr.db` in the
`/config` volume, with `ls -l` on the host path `docker volume inspect` or
`podman volume inspect` gives, double it and keep to 64 MiB or more. For
example, a 100 MiB database wants `--tmpfs /tmp:size=256m,noexec,nosuid,nodev`.
A tmpfs counts against the container's memory, and a backup fills it only
while it runs, so size it for the backup and no further.

## Configuration

Any `config.xml` key can be set as `PROWLARR__<SECTION>__<KEY>`, and the
environment wins over the file. The image sets `PROWLARR__SERVER__PORT=8080`,
which also wins over a `config.xml` brought from another image that says
9696: [Coming from another image](#coming-from-another-image) says what
that means for the apps Prowlarr syncs to. The keys most needed:

| Variable | What it does |
|---|---|
| `PROWLARR__AUTH__METHOD` | `Forms`, once a user exists, or `External`, behind a proxy that authenticates ([Authentication](#authentication) says why; `None` turns authentication off; `Basic` is retired upstream and runs as `Forms`) |
| `PROWLARR__AUTH__REQUIRED` | `Enabled`, or `DisabledForLocalAddresses` |
| `PROWLARR__AUTH__APIKEY` | The API key, over the one in `config.xml` |
| `PROWLARR__SERVER__URLBASE` | The path a reverse proxy serves Prowlarr under, such as `/prowlarr` |
| `PROWLARR__SERVER__TRUSTEDNETWORKS` | Comma-separated networks, in CIDR form, whose `X-Forwarded-For`, `-Proto` and `-Host` headers Prowlarr trusts |
| `PROWLARR__LOG__ANALYTICSENABLED` | `false` turns [analytics](#analytics) off |
| `PROWLARR__POSTGRES__HOST`, `__PORT`, `__USER`, `__PASSWORD`, `__MAINDB`, `__LOGDB` | [PostgreSQL](#postgresql) in place of SQLite |

### Authentication

**A fresh install starts with the authentication method `None`, and no
user.** Upstream's `AuthenticationRequired` defaults to `Enabled`, but a
method of `None` leaves nothing to require. **With `None`, anyone who can
reach the port can download a backup, which holds `config.xml` and so the
API key**, and can use every indexer and app you have set up.

So keep the port on the host's loopback, as [Running](#running) does,
and open the UI there first. While the method is `None`, Prowlarr opens a
first-run prompt that sets the method, a username and a password, and
creates that user. Then publish the port, or put Prowlarr behind your
proxy. A user is made only there, or later under Settings, General, both
of which save through `PUT /api/v1/config/host`: the environment has no
username or password to give.

**Do not set `PROWLARR__AUTH__METHOD=Forms` on a fresh install.** With
`Forms` in force and no user, there is nobody to log in as, and the
first-run prompt, which opens only while the method is `None`, never
appears. Set the variable once a user exists, or set it to `External`
when Prowlarr sits behind a proxy that authenticates every request
itself: `External` makes Prowlarr let in everyone who reaches it, so
nothing but that proxy may.

The probe does not mind which method is in force: it sends the API key,
which every method accepts.

### Behind a reverse proxy

Set `PROWLARR__SERVER__TRUSTEDNETWORKS` to the proxy's address or network,
such as `172.18.0.0/16`, so that Prowlarr takes the client's address and
scheme from the headers the proxy sends, and not the proxy's own. Without
it, every request looks to be from the proxy.

**Do not use `PROWLARR__AUTH__REQUIRED=DisabledForLocalAddresses` behind a
proxy** unless the proxy sets `X-Forwarded-For` and is listed in
`PROWLARR__SERVER__TRUSTEDNETWORKS`. Prowlarr treats a client as local when
its address is local and the request carries no `X-Forwarded-For` header.
A proxy on a private network that sends none makes every proxied client
local, and authentication is skipped for everyone who can reach the proxy.

If the proxy serves Prowlarr under a path, set the URL base, in the
environment or in the UI. Prowlarr answers nothing at the bare path but a
redirect. The health probe reads the base from the environment, else from
`config.xml`, so either works, and it never follows a redirect.

### Analytics

Analytics are on, as upstream ships them. They and the error reports
Prowlarr sends to Sentry go to Servarr. Turn them off in the UI, under
Settings, General, or with `PROWLARR__LOG__ANALYTICSENABLED=false`.

### Updates

Prowlarr's own updater is off. The image's `/app/package_info` names Docker
as the update mechanism, so the UI offers no update, and a `config.xml`
brought from another install that says `BuiltIn` is switched to Docker at
start. You update by pulling a new image.

## Egress at rest

The contract's no-egress check holds: the image serves and is healthy on a
network with no route out. Prowlarr, though, reaches out while it runs, as
upstream ships it:

- Indexer definitions come from `indexers.prowlarr.com`, at start and on a
  schedule. Without them, indexers cannot be added or updated.
- Analytics and error reports go to Servarr, until turned off.
- Then whatever indexers, download clients and apps you configure.

None of it is needed to serve or to be healthy.

## Not supported

There is no shell in the image, so these cannot work:

- Custom Script connections.
- Import Using Script.
- The Script update mechanism.
- Any other interpreted script.

A static binary on a mount still runs, unless you mount it from somewhere
`noexec`.

## Hardlinks

Prowlarr imports no files, so this is advice for the apps beside it that
do, such as Sonarr, Radarr and Lidarr. Hardlinking needs the source and
the destination on one filesystem, so give those apps and the download
client one shared `/data` mount, not a mount each for downloads and
media. Under `--cap-drop ALL`, the kernel's protected-hardlinks rule lets
a process link only a file it owns or can read and write; when the app's
uid can do neither, it copies the file instead. On an SELinux host, a
`/data` bind mount several containers share takes Podman's `:z`, the
shared label, not `:Z`, which would keep it to one container.

## PostgreSQL

Prowlarr can use PostgreSQL through the `PROWLARR__POSTGRES__*` variables
above. This image has not been tested with it.

## Health

A server that answers can still be broken, so the image ships its own probe
and declares a `HEALTHCHECK`: `--interval=30s --timeout=10s
--start-period=300s --retries=3`. The start period is long because
Prowlarr answers nothing until its database migrations finish, and an
upgrade's can be slow on a large database; the first check that passes
ends it, so a healthy start loses nothing. Docker runs the check with no
configuration.

Podman does not. Podman's `libimage` reads a healthcheck only out of a
Docker-media-type manifest, and divbast images are published as an OCI
index, so Podman's inspection path never looks at the config's healthcheck
field — [podman/podman#25454](https://github.com/containers/podman/issues/25454)
and [#18904](https://github.com/containers/podman/issues/18904) track it. A
Podman user passes it explicitly, with `--health-cmd` as in
[Running](#running). Under a Quadlet, the equivalent is:

```
HealthCmd=["/usr/local/bin/divbast-prowlarr","health"]
```

The array above has no leading `"CMD"` on purpose: Podman before 5.8.0
re-splits an array that starts with `"CMD"` into one useless token, so
omitting it is what makes the check work across versions. Podman takes
none of the image's timings either, and by default allows no start
period: add `--health-start-period=300s` (`HealthStartPeriod=300s` in a
Quadlet) if an upgrade's migrations could outlast three of its checks.

| | |
|---|---|
| `/usr/local/bin/divbast-prowlarr health` | The in-container probe `HEALTHCHECK` runs. Asserts that `/ping` answers 200 with `{"status":"OK"}` **and** that `GET /api/v1/system/status`, with the API key, says `appName` is `Prowlarr` and `version` is this image's. Prints nothing when healthy; otherwise one line, `ping: <reason>` or `status: <reason>`, for each half that failed, and exits 1 |
| `/usr/local/bin/divbast-prowlarr version` | Prints the version |

`/ping` runs a database query, so it shows the server and its database
answer; only the status shows the build is the pinned one. The probe reads
the API key from `PROWLARR__AUTH__APIKEY`, else `/config/config.xml`, and
fails with a line saying so when it finds neither. Prowlarr makes a key for
itself on its first start, so a plain run needs nothing set.

There is no shell in this image, so a health command given as a plain
string, which Docker and Podman both run through `/bin/sh -c`, can never
pass.

## The pipeline

Three workflows, each a short caller of divbast/ci's reusable workflow of
the same purpose, at one pinned release of it:

- `build.yml` calls `app-check.yml` on every push, pull request, daily slot
  and button run: lint, the image built for amd64 and arm64 once every
  image the Containerfile builds from is verified, the acceptance suite
  against each under Docker and rootless Podman, and the scan. Its
  `required` job is the one check `main`'s ruleset requires. From the daily
  slot and the button, on `main`, it then calls `app-publish.yml`.
- `renovate.yml` runs Renovate, as below.
- `stall.yml` fails, and GitHub emails you, when a Renovate pull request
  has been open more than 14 days or red or pending for more than a day, or
  when Renovate has not succeeded in a day.

The acceptance suite, on top of the contract, checks that Prowlarr serves
the UI and `/ping`, refuses the API without a key, stops on SIGTERM and
exits 0, keeps its state across a restart, restarts from its UI inside the
process, reports Docker as its updater, is healthy under every
authentication method and URL base, refuses an unwritable `/config`, exits
on a startup failure, runs on a `/config` another image left, and makes and
serves a backup within the `/tmp` it is given.

### Publishing

A merge never publishes. The daily slot publishes `main` when `main` moved
since the commit the published `:latest` was built from, and the button, a
manual run of `build` from `main`, publishes at once.

This repository does not publish yet. Its publish job still calls
`app-publish.yml` with `image: ghcr.io/divbast/oci-image-template` and
`rehearse: true`, as the template does, which rehearses a publish against a
registry it starts inside the job, so nothing reaches ghcr.io and there is
no image to pull. divbast/ci rehearses only in `divbast/oci-image-template`,
so until `build.yml` is switched to `ghcr.io/divbast/prowlarr` without
`rehearse`, each slot's publish job here fails by design.

## Dependency updates

Every input the Containerfile names is pinned by digest: the DHI runtime,
and the DHI Node, .NET SDK and Go images the stages build on. Prowlarr's
own version and commit are pinned together in two adjacent `ARG`s. The
probe is the standard library alone, so `go.mod` requires nothing but
divbast/ci's harness for its tests.

A pin only moves through a pull request, and
[Renovate](https://docs.renovatebot.com/) opens those. It runs every
morning, after every merge to `main`, whenever CI finishes on one of its
branches and when the Dependency Dashboard issue is edited, from
`.github/workflows/renovate.yml`, as the divbast org's `divbast-renovate`
GitHub App. Its settings are divbast/renovate-config's shared presets
(`:app` among them), and `renovate.json` adds the one thing they cannot
know: where Prowlarr's version lives. The pull requests Renovate opens
here are:

- A new Prowlarr release moves `PROWLARR_VERSION` and `PROWLARR_COMMIT`
  together. Renovate follows Prowlarr's stable GitHub releases alone (the
  `github-releases` datasource), which skips the pre-releases Prowlarr
  publishes for every develop build, and any tag with no release. The
  commit is the one the release's tag names, and the build fails if the tag
  ever names another.
- A new DHI image arrives as a pull request that moves its digest, and CI
  verifies Docker's signature before it builds. The .NET SDK and the
  ASP.NET Core runtime move together in one pull request, and a new .NET
  major waits for approval on the Dependency Dashboard.
- A release of divbast/ci arrives as one pull request that moves every
  workflow's pin of it together.
- The toolchain in `mise.toml` moves by pull requests of its own.

Renovate merges its own pull requests once every check on one has passed,
while the repository variable `PROWLARR_AUTOMERGE` is `enabled`. Two more
kinds first wait for someone to approve them on the Dependency Dashboard:
a new major version of divbast/ci, and a raise of `go.mod`'s `go` line.
Any other value of the variable, or none, stops merging from the next run,
and Renovate goes on opening and rebasing pull requests for a person to
merge:

    gh variable set PROWLARR_AUTOMERGE --body disabled

After a merge it rebases every open update the merge left behind, so each
is tested again against the new `main` before it merges. It merges with a
squash, which GitHub signs, because `main` accepts only signed commits.

The App, the `renovate` environment holding its key, `PROWLARR_AUTOMERGE`
and this repository's rulesets are all managed from
[divbast/infra](https://github.com/divbast/infra).

## Development

- `just setup` fetches divbast/ci into `.ci/` at the commit the workflows
  pin, then installs the pinned toolchain from `mise.toml`.
- `just build` builds the image locally, without verifying the images it
  builds from, and needs `docker login dhi.io`; `just ci-tools &&
  .ci/bin/divbast-verify-images` does verify them, with cosign from
  `mise.toml`.
- `just scan` reads the published image with
  [skopeo](https://github.com/containers/skopeo), which must be on `PATH`;
  so must it for `just ci`. CI runners already have it.
- `just test` builds, then runs the acceptance suite against the image,
  under `ENGINE`, docker unless it says podman. Under Docker it needs
  `zstd` on `PATH`, since every layer of a Docker Hardened Image is zstd
  and the suite reads the image's layers through it; no `mise.toml` entry
  provides it. CI runners already have it, and a Mac gets it with `brew
  install zstd`.
- `just lint` runs every lint gate, and needs a running Docker daemon, for
  `docker build --check`.
- `just ci` runs every gate, the scan included. Run it before committing.

The recipes that call GitHub, pinact's and zizmor's, read a fine-grained
personal access token from `GITHUB_TOKEN`: divbast/ci's README says which,
and how to pass it.

## AI/LLM Disclosure

This project was developed with significant LLM involvement. Each git commit contains an `Assisted-by:` tag detailing the particular model/tool used.
