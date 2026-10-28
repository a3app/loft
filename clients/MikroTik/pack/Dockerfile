# loft RouterOS container: static linux loft + CA store. No shell.
# Build: make -C clients/MikroTik image GOARCH=arm64 (or dist / push).
#
# The CA store is the same file on every architecture, so it is taken on
# the build machine's own platform: no emulation of ARM is needed to build
# the ARM images.
FROM --platform=$BUILDPLATFORM alpine:3.24 AS certs
RUN apk add --no-cache ca-certificates

FROM scratch
# Set by docker buildx for the image being built: amd64 / arm64 / arm (+ v7).
ARG TARGETARCH
ARG TARGETVARIANT
LABEL org.opencontainers.image.source="https://github.com/a3app/loft" \
      org.opencontainers.image.description="loft client for MikroTik RouterOS containers" \
      org.opencontainers.image.licenses="Apache-2.0"
COPY --from=certs /etc/ssl/certs/ca-certificates.crt /etc/ssl/certs/ca-certificates.crt
# make loft-bin names the binaries loft-amd64, loft-arm64, loft-armv7.
COPY loft-${TARGETARCH}${TARGETVARIANT} /loft
COPY client.yaml /data/client.yaml.example
ENTRYPOINT ["/loft"]
# -if takes the veth gateway from install.rsc (172.31.20.1), not a name.
# Inside a RouterOS container the interface is named after the veth
# ("veth-loft"), there is no eth0; with an address loft picks whichever
# interface has it in its subnet, so a renamed veth still works.
CMD ["-c", "/data/client.yaml", "-if", "172.31.20.1"]
