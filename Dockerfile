# SPDX-License-Identifier: Apache-2.0
# Copyright (C) 2026 Dave O'Connor
#
# Base images are pinned by digest as well as tag; Dependabot moves both.

# The builder runs on the build machine's platform and cross-compiles, so a
# multi-arch build needs no emulation. CGO_ENABLED=0 is what makes that work,
# and what lets the runtime image be distroless/static.
FROM --platform=$BUILDPLATFORM golang:1.27.3-alpine@sha256:d200d2dd76b03a99b52651fd1325aff7dcd35422c35c01bac56571b0507f3deb AS build

WORKDIR /src

# Dependencies first, so that a source-only change does not download them again.
COPY go.mod go.sum ./
RUN go mod download

COPY api/ api/
COPY cmd/ cmd/
COPY internal/ internal/
COPY pkg/ pkg/

ARG VERSION=dev
ARG COMMIT=unknown
ARG DATE=unknown
ARG TARGETOS
ARG TARGETARCH

ENV CGO_ENABLED=0 GOOS=${TARGETOS} GOARCH=${TARGETARCH}
RUN LDFLAGS="-X github.com/gerrowadat/andante/internal/buildinfo.version=${VERSION} \
      -X github.com/gerrowadat/andante/internal/buildinfo.commit=${COMMIT} \
      -X github.com/gerrowadat/andante/internal/buildinfo.date=${DATE}" \
 && go build -trimpath -ldflags="${LDFLAGS}" -o /out/andante-server ./cmd/andante-server \
 && go build -trimpath -ldflags="${LDFLAGS}" -o /out/andante-catalogue ./cmd/andante-catalogue \
 && go build -trimpath -ldflags="${LDFLAGS}" -o /out/andante ./cmd/andante

# No shell and no package manager, so there is no apk upgrade step to keep the
# image clean of CVEs.
FROM gcr.io/distroless/static-debian12:nonroot@sha256:afa5c872c891853ca7fcf1f12c3edb23f7eeef36189728842dd51042ff57f7ab

ARG VERSION=dev
ARG COMMIT=unknown
ARG DATE=unknown

LABEL org.opencontainers.image.title="andante" \
      org.opencontainers.image.source="https://github.com/gerrowadat/andante" \
      org.opencontainers.image.licenses="Apache-2.0" \
      org.opencontainers.image.version="${VERSION}" \
      org.opencontainers.image.revision="${COMMIT}" \
      org.opencontainers.image.created="${DATE}"

COPY --from=build /out/andante-server /out/andante-catalogue /out/andante /usr/local/bin/

ENTRYPOINT ["/usr/local/bin/andante-server"]
