# loft RouterOS container: static linux loft + CA store. No shell.
# Build: make -C clients/MikroTik image GOARCH=arm64
FROM alpine:3.24 AS certs
RUN apk add --no-cache ca-certificates

FROM scratch
COPY --from=certs /etc/ssl/certs/ca-certificates.crt /etc/ssl/certs/ca-certificates.crt
COPY loft /loft
COPY client.yaml /data/client.yaml.example
ENTRYPOINT ["/loft"]
# -if takes the veth gateway from install.rsc (172.31.20.1), not a name.
# Inside a RouterOS container the interface is named after the veth
# ("veth-loft"), there is no eth0; with an address loft picks whichever
# interface has it in its subnet, so a renamed veth still works.
CMD ["-c", "/data/client.yaml", "-if", "172.31.20.1"]
