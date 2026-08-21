# Self-contained build environment for discovery-client: Go plus the packaging
# tools. Debian rather than Alpine because alien is only packaged there, and it
# pins rpm below 4.20, where %_builddir semantics change.
#
#   docker build -f env/build/Dockerfile -t discovery-client-builder .
#   docker run --rm -v "$PWD:$PWD" -w "$PWD" -u "$(id -u):$(id -g)" \
#       discovery-client-builder make discovery-packages-el8
FROM golang:1.22-bookworm

RUN apt-get update && \
    apt-get install -y --no-install-recommends \
        make \
        uuid-dev \
        rpm \
        alien \
        fakeroot \
        ca-certificates && \
    rm -rf /var/lib/apt/lists/*

# Builds are vendored, so nothing is fetched. Keep the caches on a world
# writable path so the image works under an arbitrary --user. HOME matters to
# rpm too: Debian keeps its database under ~/.rpmdb, and docker defaults HOME to
# / for a uid with no passwd entry, which makes every rpm query warn.
ENV HOME=/tmp \
    GOPATH=/tmp/go \
    GOCACHE=/tmp/go-build \
    GOFLAGS=-mod=vendor
