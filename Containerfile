# SPDX-License-Identifier: GPL-3.0-only
# SPDX-FileCopyrightText: 2026 Aryan Ameri <github@aryan.ameri.coffee>

# Renovate moves these two in one PR; its regex needs them adjacent and in
# this order, and follows Prowlarr's stable releases alone. The version is
# all four numbers: the image is tagged with it (app-publish.yml reads it
# from this line, by the name build.yml gives as version-arg), the
# assemblies carry it, and the probe compares it with the version Prowlarr
# reports. The fetch stage checks out the commit and asserts the release's
# tag still names it.
ARG PROWLARR_VERSION=3.0.2.5780
ARG PROWLARR_COMMIT=847d4876aabf6a31dce5ae40d44ec2ae73ba536b
# The runtime: Docker Hardened Images' ASP.NET Core on Debian 13, with ICU,
# OpenSSL and the .NET runtime the app runs on, no shell and no package
# manager, and its nonroot user 65532. The app is built framework-dependent,
# so a rebuild of this image patches the runtime. CI verifies its signature
# by Docker before anything is built, as it does every stage's image below.
ARG RUNTIME_IMAGE=dhi.io/aspnetcore:10.0.15-debian13@sha256:9221d55b3bcf618cc56da297a64b1c3294458048109b95b8f3ce39d2dd8e0b33

# The same image as the frontend's, which ships git and CA certificates.
FROM dhi.io/node:24.22.3-debian13-dev@sha256:dee4bb11f4cb32ad31000287af8795f772ac801338a166a0e9fbbadd9674a2f8 AS fetch
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
FROM dhi.io/node:24.22.3-debian13-dev@sha256:dee4bb11f4cb32ad31000287af8795f772ac801338a166a0e9fbbadd9674a2f8 AS frontend
WORKDIR /src
COPY --from=fetch /src ./
RUN yarn install --frozen-lockfile --network-timeout 120000 \
    && yarn run build --env production

# The backend, framework-dependent for this stage's own architecture.
FROM dhi.io/dotnet:10.0.404-sdk-debian13@sha256:40c7a4fd82c981f0df98ced709d21cabfd5491324f567c74b3b0d716477873ae AS backend
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
# The one change made to an upstream file, which never leaves this stage.
# global.json pins the SDK with no roll-forward policy, which accepts only
# that feature band, so an SDK of a later band, which Renovate moves this
# stage's image to, would refuse to build. Upstream's version stays the
# floor, and any later band of the same major and minor is accepted.
RUN json=$(<global.json); \
    re='"sdk"[[:space:]]*:[[:space:]]*[{][^}]*"version"[[:space:]]*:[[:space:]]*"([0-9]+[.][0-9]+[.][0-9]+[^"]*)"'; \
    if [[ ! "${json}" =~ ${re} ]]; then \
        echo "global.json has no sdk.version to keep as the floor:" >&2; \
        printf '%s\n' "${json}" >&2; \
        exit 1; \
    fi; \
    printf '{"sdk":{"version":"%s","rollForward":"latestFeature"}}\n' "${BASH_REMATCH[1]}" >global.json; \
    cat global.json
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
# upstream reads it only from beside a directory named bin.
RUN rid=$(</tmp/rid); \
    mkdir -p /out/config; \
    cp -a "_output/net10.0/${rid}/publish" /out/bin; \
    rm /out/bin/Prowlarr.Windows.* /out/bin/ServiceInstall* /out/bin/ServiceUninstall*; \
    test ! -e /out/bin/Prowlarr.Update; \
    printf '%s\n' "PackageVersion=${PROWLARR_VERSION}" \
        'PackageAuthor=[divbast](https://github.com/divbast/prowlarr-oci-image)' \
        UpdateMethod=Docker Branch=master >/out/package_info

# The image's health probe, static and on the standard library alone.
FROM dhi.io/golang:1.27.4-debian13-dev@sha256:fad833d40821cebfb55c0af4b5b2dc966b65b7534880929cee157ca07ab13ffd AS probe
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
# Prowlarr's licence, beside the app it covers, as upstream's packages ship it
COPY --from=fetch /src/LICENSE /app/LICENSE
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
# same array as --health-cmd. On the machine it was built on, Prowlarr
# answered /ping within 0.9 s of docker run, migrating a new /config; the
# start period leaves room for a slower host and for an upgrade's
# migrations on a large database.
HEALTHCHECK --interval=30s --timeout=10s --start-period=60s --retries=3 \
    CMD ["/usr/local/bin/divbast-prowlarr", "health"]
USER 65532:65532
# Prowlarr itself, as PID 1, which .NET handles SIGTERM and reaps children
# as. The apphost finds the runtime through the base's DOTNET_ROOT.
ENTRYPOINT ["/app/bin/Prowlarr", "-nobrowser", "-data=/config"]
