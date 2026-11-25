# SPDX-License-Identifier: GPL-3.0-only
# SPDX-FileCopyrightText: 2026 Aryan Ameri <github@aryan.ameri.coffee>

# Renovate moves these two in one PR; its regex needs them adjacent and in
# this order, and follows Prowlarr's stable releases alone. The version is
# all four numbers: the image is tagged with it (app-publish.yml reads it
# from this line, by the name build.yml gives as version-arg), the
# assemblies carry it, and the probe compares it with the version Prowlarr
# reports. The fetch stage checks out the commit and asserts the release's
# tag still names it.
ARG PROWLARR_VERSION=3.1.0.5799
ARG PROWLARR_COMMIT=b0b5774bf3bd594d0fb2dcd151495b5d034920ef
# The runtime: Docker Hardened Images' ASP.NET Core on Debian 13, with ICU,
# OpenSSL and the .NET runtime the app runs on, no shell and no package
# manager, and its nonroot user 65532. The app is built framework-dependent,
# so a rebuild of this image patches the runtime. CI verifies its signature
# by Docker before anything is built, as it does every stage's image below.
ARG RUNTIME_IMAGE=dhi.io/aspnetcore:10.0.15-debian13@sha256:b1b32dadf1d9ecd91f307ea059dfc457478495de45d00e8de99993d154cc8879

# The same image as the frontend's, which ships git and CA certificates.
FROM dhi.io/node:26.13.1-debian13-dev@sha256:d4f8c755168bbea66fc667bf4bc0a8462dc57a29da3c04e8c93ebe0592e231b5 AS fetch
ARG PROWLARR_VERSION
ARG PROWLARR_COMMIT
# git checks a fetch by commit against its hash. The tag is asked for
# separately, peeled if it is ever annotated, so a tag moved upstream fails
# the build instead of shipping a commit no release names. The fetch and
# the tag lookup each get three retries, 10s, 30s and 90s apart, so one
# dropped connection does not cost the daily slot. The remote is named so
# that the .NET build's Source Link finds the repository it came from.
RUN set -eux; \
    retry() { \
        for delay in 10 30 90; do "$@" && return 0; sleep "${delay}"; done; \
        "$@"; \
    }; \
    git init -q /src; \
    git -C /src remote add origin https://github.com/Prowlarr/Prowlarr.git; \
    retry git -C /src fetch -q --depth 1 origin "${PROWLARR_COMMIT}"; \
    git -C /src checkout -q --detach FETCH_HEAD; \
    head=$(git -C /src rev-parse HEAD); \
    test "${head}" = "${PROWLARR_COMMIT}"; \
    retry git -C /src ls-remote origin \
        "refs/tags/v${PROWLARR_VERSION}" "refs/tags/v${PROWLARR_VERSION}^{}" >/tmp/tag; \
    tagged=$(awk -v tag="refs/tags/v${PROWLARR_VERSION}" \
        '$2 == tag "^{}" { peeled = $1 } $2 == tag { plain = $1 } END { print (peeled != "" ? peeled : plain) }' /tmp/tag); \
    if [ "${tagged}" != "${PROWLARR_COMMIT}" ]; then \
        echo "tag v${PROWLARR_VERSION} names '${tagged}', the Containerfile pins ${PROWLARR_COMMIT}" >&2; \
        exit 1; \
    fi

# The UI, as upstream's build.sh builds it, into _output/UI. yarn 1.22.22
# ships in the image, and yarn.lock pins every package by its sha512.
FROM dhi.io/node:26.13.1-debian13-dev@sha256:d4f8c755168bbea66fc667bf4bc0a8462dc57a29da3c04e8c93ebe0592e231b5 AS frontend
WORKDIR /src
COPY --from=fetch /src ./
RUN yarn install --frozen-lockfile --network-timeout 120000 \
    && yarn run build --env production

# The backend, framework-dependent for this stage's own architecture.
FROM dhi.io/dotnet:10.0.404-sdk-debian13@sha256:711001f2c5bef9b6ce3d6d66847e5c81348b623ff05f515169c0007d11394230 AS backend
ARG PROWLARR_VERSION
ARG TARGETARCH
# Upstream keeps no lock file, and pins every package by an exact version
# from nuget.org alone, which signs each one: NuGet checks those signatures
# here.
ENV DOTNET_CLI_TELEMETRY_OPTOUT=1 \
    DOTNET_NUGET_SIGNATURE_VERIFICATION=true
SHELL ["/bin/bash", "-euo", "pipefail", "-c"]
WORKDIR /src
COPY --from=fetch /src ./
COPY test/global-json.sh /tmp/global-json.sh
# The one change made to an upstream file, which never leaves this stage.
# global.json pins the SDK with no roll-forward policy, which accepts only
# that feature band, so an SDK of a later band, which Renovate moves this
# stage's image to, would refuse to build. The script keeps upstream's
# version as the floor and accepts any later band of the same major and
# minor, after checking that major and minor are this image's: a .NET
# major on one side alone fails here, with the steps to take. dotnet asked
# from / reports the image's own SDK, since no global.json is in the way
# there; asked from the source, it would refuse before the rewrite.
# hadolint's DL3003 asks for WORKDIR in place of that cd, which lasts for
# the one command substitution: WORKDIR would move the whole RUN away from
# the source.
# hadolint ignore=DL3003
RUN sdk=$(cd / && dotnet --version); /tmp/global-json.sh global.json "${sdk}"
# The properties restore and publish share, one per line, in a response
# file both read. The version is passed rather than left at upstream's
# 10.0.0.*, which runs as a development build; -p: overrides it, and the
# branch, without editing Directory.Build.props.
RUN case "${TARGETARCH}" in \
        amd64) rid=linux-x64 ;; \
        arm64) rid=linux-arm64 ;; \
        *) echo "no .NET runtime identifier for ${TARGETARCH}" >&2; exit 1 ;; \
    esac; \
    printf '%s\n' -p:Configuration=Release -p:Platform=Posix -p:SelfContained=false \
        "-p:RuntimeIdentifiers=${rid}" "-p:AssemblyVersion=${PROWLARR_VERSION}" \
        -p:AssemblyConfiguration=master -p:EnableAnalyzers=false -p:SentryUploadSymbols=false \
        >/tmp/build.rsp; \
    printf '%s\n' "${rid}" >/tmp/rid
RUN dotnet msbuild src/Prowlarr.sln -t:Restore @/tmp/build.rsp
# Everything it needs was restored above, so the publish runs offline.
RUN --network=none dotnet msbuild src/Prowlarr.sln -t:PublishAllRids @/tmp/build.rsp
# /app/bin as upstream's Linux package lays it out, without the Windows and
# service helpers, and without Prowlarr.Update, the in-app updater the
# image never runs: package_info names Docker as the update mechanism, and
# upstream reads it only from beside a directory named bin. Publish writes
# the app under its target framework, _output/net10.0 today, which a .NET
# major moves, so the folder is found rather than named, and must be the
# only one: none means publish wrote it somewhere else, and with several
# nothing says which is the app. nullglob, for the find alone, makes no
# match an empty list rather than the pattern itself.
RUN rid=$(</tmp/rid); \
    shopt -s nullglob; \
    published=(_output/net*/"${rid}"/publish); \
    shopt -u nullglob; \
    if ((${#published[@]} != 1)); then \
        echo "ERROR: ${#published[@]} folders match _output/net*/${rid}/publish, where publish writes the app to one: ${published[*]:-none}." >&2; \
        exit 1; \
    fi; \
    mkdir -p /out/config; \
    cp -a "${published[0]}" /out/bin; \
    rm /out/bin/Prowlarr.Windows.* /out/bin/ServiceInstall* /out/bin/ServiceUninstall*; \
    test ! -e /out/bin/Prowlarr.Update; \
    printf '%s\n' "PackageVersion=${PROWLARR_VERSION}" \
        'PackageAuthor=[divbast](https://github.com/divbast/prowlarr-oci-image)' \
        UpdateMethod=Docker Branch=master >/out/package_info
COPY test/runtime-major.sh /tmp/runtime-major.sh
# With no default of its own, RUNTIME_IMAGE takes the global one at the
# top of this file, which the runtime stage is built FROM. It is declared
# here, for the check alone: BuildKit gives every ARG in scope to each
# later RUN as its environment, and so to its cache key, and declared any
# higher it would have a new runtime pin rebuild the restore and publish.
# It stands alone, with nothing after it on its line: Renovate reads ARG
# NAME VALUE as NAME=VALUE, so anything there would become the runtime
# pin it moves.
ARG RUNTIME_IMAGE
# An app built for one .NET does not start on another's runtime, and
# nothing else would say so: the script compares the .NET the shipped
# runtimeconfigs name with the one leading the runtime image's tag, and
# fails a mismatch with the steps to take. It reads /out/bin once nothing
# more is written there, so what it passes is what ships.
RUN /tmp/runtime-major.sh /out/bin "${RUNTIME_IMAGE}"

# The image's health probe, static and on the standard library alone.
FROM dhi.io/golang:1.27.4-debian13-dev@sha256:ca700aac1d0d9690382a3f728dd435190d54d95ba93098343abead65428cb410 AS probe
ARG PROWLARR_VERSION
WORKDIR /src
# go.mod's replace names ./.ci, which the build context leaves out: the
# probe imports nothing from divbast/ci, so Go never looks for it
COPY go.mod ./
COPY cmd/ cmd/
COPY internal/ internal/
RUN CGO_ENABLED=0 GOFLAGS=-mod=readonly GOTOOLCHAIN=local \
    go build -trimpath -ldflags "-s -w -X main.version=${PROWLARR_VERSION}" \
    -o /out/divbast-prowlarr ./cmd/divbast-prowlarr

FROM ${RUNTIME_IMAGE}
ARG PROWLARR_VERSION

# Root-owned, and never written: Prowlarr writes to /config and TMPDIR alone
COPY --from=backend /out/bin /app/bin
COPY --from=frontend /src/_output/UI /app/bin/UI
COPY --from=backend /out/package_info /app/package_info
# Prowlarr's licence, where upstream's release tarball puts it: build.sh
# copies it into the tarball's Prowlarr/ folder, which is this /app/bin
COPY --from=fetch /src/LICENSE /app/bin/LICENSE
COPY --from=probe /out/divbast-prowlarr /usr/local/bin/divbast-prowlarr
# The data directory, empty and uid 65532's, and not a VOLUME: a new named
# volume mounted here takes its owner from it, so Prowlarr can write to it.
# Without a mount, the read-only root refuses every write, and Prowlarr
# stops at once.
COPY --from=backend --chown=65532:65532 /out/config /config

# Any config.xml key can be set as PROWLARR__<SECTION>__<KEY>. The port is
# 8080, as every divbast image's. The runtime opens no diagnostics socket.
# PGGSSENCMODE stops Npgsql probing for GSS encryption, which matters only
# for PostgreSQL. Analytics stay as upstream ships them, on.
ENV PROWLARR__SERVER__PORT=8080 \
    DOTNET_EnableDiagnostics=0 \
    PGGSSENCMODE=disable \
    TMPDIR=/tmp

LABEL org.opencontainers.image.source="https://github.com/divbast/prowlarr-oci-image" \
    org.opencontainers.image.url="https://github.com/divbast/prowlarr-oci-image" \
    org.opencontainers.image.title="Prowlarr" \
    org.opencontainers.image.version="${PROWLARR_VERSION}" \
    org.opencontainers.image.licenses="GPL-3.0-only"

EXPOSE 8080
# Exec form: the shell form would need /bin/sh, which the image lacks.
# --timeout must clear divbast-prowlarr health's own budget: it allows each
# of its two exchanges 4 s and runs them one after the other, so a listener
# that accepts and never answers costs 8 s. A shorter timeout kills the
# probe mid-flight and records a bare engine timeout instead of the probe's
# own line, which is the only diagnostic there is. Podman reads no
# HEALTHCHECK from an image published as an OCI index: its users pass the
# same array as --health-cmd. On the machine it was built on, migrating a
# new /config, Prowlarr answered /ping 0.74 to 1.46 s after the run in 49
# of 50 runs on Docker and Podman, and 3.5 s after it in the other. How
# long an upgrade's migrations take on a large database was never
# measured, and Prowlarr listens only once they finish: migrations that
# outlast the start period and three failed checks get the container
# marked unhealthy, and autoheal or a Swarm service would then kill it
# mid-migration. The first check that passes ends the start period, so a
# long one costs a healthy start nothing; it only delays the verdict on a
# start that never comes up. Hence 300 s. The contract suite's
# Healthcheck says the same, and must.
HEALTHCHECK --interval=30s --timeout=10s --start-period=300s --retries=3 \
    CMD ["/usr/local/bin/divbast-prowlarr", "health"]
USER 65532:65532
# Prowlarr itself runs as PID 1, with nothing before it. .NET handles
# SIGTERM there and reaps its child processes, so it needs no init. The
# apphost finds the runtime through the base's DOTNET_ROOT. -exitimmediately
# is upstream's own flag, read in one place: a startup failure upstream
# calls non-recoverable, such as a PID file it cannot write, then exits 3
# at once, where without it Prowlarr would wait an hour for a key that no
# one can press in a container, and the container would sit there looking
# alive.
ENTRYPOINT ["/app/bin/Prowlarr", "-nobrowser", "-exitimmediately", "-data=/config"]
