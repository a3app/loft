# loft RouterOS container: static linux loft + CA store. No shell.
# Build: make -C clients/MikroTik image GOARCH=arm64
FROM alpine:3.20 AS certs
RUN apk add --no-cache ca-certificates

FROM scratch
COPY --from=certs /etc/ssl/certs/ca-certificates.crt /etc/ssl/certs/ca-certificates.crt
COPY loft /loft
COPY client.yaml /data/client.yaml.example
ENTRYPOINT ["/loft"]
CMD ["-c", "/data/client.yaml", "-if", "eth0"]
