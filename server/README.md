# loft-node

**English** · [Русский](README_ru.md)

Node implementation: controller or relay. One binary, role selected in YAML.

Operation: [docs/guide.md](../docs/guide.md). Wire: [docs/protocol.md](../docs/protocol.md). Layout: [docs/architecture.md](../docs/architecture.md).

```bash
go build -o loft-node ./server/cmd/loft-node
./loft-node -c server/configs/server.yaml -dir /var/lib/loft
./loft-node -c server/configs/relay.yaml -dir /var/lib/loft-relay
./loft-node --version
```

| Flag | Meaning |
|------|---------|
| `-c` | YAML for role, decoy, fronts, TLS |
| `-dir` | `network.conf`, controller seed |
| `-listen` `-cert` `-key` | Overrides |
| `-trace` | Session event log (GET seq); empty = off |
| `--version` | Program and protocol versions |

`server/origin` serves the HTTPS origin: cover page, GET, WebSocket, HTTP/2 CONNECT, public roster and lists. Edge presets: `internal/cdn`.

A controller holds keys, leases, and the Ed25519 seed. A relay terminates public TLS and copies plaintext onto a trunk (`hub:1` / `hub:2`, `ctl:1`). Member identity stays on the controller.

Debian host (systemd, installer, `.deb`): [debian/README.md](debian/README.md).

```bash
make dist-node
# copy dist/node/linux-amd64/loft-node-*-linux-amd64.tar.gz to the host
sudo ./install.sh
```
