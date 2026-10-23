# loft-node on Debian

**English** · [Русский](README_ru.md) · [中文](README_zh.md)

Software 1.0.27. Protocol 1.0. Protocol spec: [docs/protocol.md](../../docs/protocol.md). Operation: [docs/guide.md](../../docs/guide.md).

The Debian layout of the node is collected here: a POSIX installer, a systemd unit, sysctl, command config, and a `.deb`.

---

## 1. Layout on the machine

| Path | Role |
|------|------|
| `/usr/local/bin/loft-node` | binary (tarball installer) |
| `/usr/bin/loft-node` | binary (`.deb`) |
| `/etc/loft/node.conf` | role, fronts, decoy, advertise |
| `/etc/loft/loft.env` | `LOFT_TOKEN` (mode 0640, group `loft`) |
| `/etc/loft/tls/fullchain.pem` | public certificate |
| `/etc/loft/tls/privkey.pem` | private key |
| `/var/lib/loft/network.conf` | overlay CIDR, members, peers |
| `/var/lib/loft/members.keys` | plaintext member keys (mode 0600), issued by the operator |
| `/var/lib/loft/controller.seed` | Ed25519 seed, written on first start |
| `/etc/systemd/system/loft-node.service` | unit |
| `/etc/sysctl.d/99-loft.conf` | UDP buffers for QUIC; ping_group_range for internet ICMP |

The process runs as user `loft`. It binds TCP and UDP 443 with `CAP_NET_BIND_SERVICE`. The token is never on the command line.

A new install writes `fronts get` and `auth shared disable`, and an `operator` key in `members.keys`. `/root/loft-client.yaml` is GET-only with that `lt_`, not `LOFT_TOKEN`. `LOFT_TOKEN` in `loft.env` stays the node secret (mask, controller seed). To keep the old one-token overlay, put `auth shared enable` in `node.conf` while `members.keys` is empty. After a binary upgrade, an existing shared-token node without that line will refuse clients until you add the line or issue keys. List `ws`, `h2`, `quic`, or `h3` in `fronts` to opt those paths in.

---

## 2. Tarball installer

On the build machine:

```bash
make dist-node
```

Writes `dist/node/linux-amd64/` and `dist/node/linux-arm64/` (tarball, unpacked installer, `.deb`) plus `dist/node/SHA256SUMS`. Layout: [DIST.md](DIST.md).

On the Debian host:

```bash
tar -C /tmp -xzf loft-node-*-linux-amd64.tar.gz
cd /tmp/loft-node-*-linux-amd64
sudo ./install.sh
```

Useful flags:

| Flag | Meaning |
|------|---------|
| `--dry-run` | print every step, change nothing |
| `--unattended`, `-y` | no prompts |
| `--role=controller` | default |
| `--role=relay` | TLS edge; needs `--controller=` and `--id=` |
| `--advertise=https://host` | public URL in the roster |
| `--listen=:443` | bind address |
| `--decoy=host:443` | splice origin |
| `--mask` | borrowed-cert mask (with decoy) |
| `--cert=` `--key=` | existing PEM; otherwise a self-signed cert |
| `--peers=hub` | overlay policy (`hub` \| `mesh` \| `vlan`) |
| `--token=lt_…` | otherwise generated |
| `--no-firewall` | do not touch ufw |
| `--uninstall` | remove the service, keep data |
| `--remove` | remove the service and erase data |

Re-running replaces the binary and restarts the unit. Token, config, and certificates stay.

Debian 12 and 13, Ubuntu 22.04 / 24.04 / 26.04 are accepted. Debian 10/11 and Ubuntu 16/18/20 are refused. Other Debian-family IDs are asked.

---

## 3. Debian package

```bash
make dist-node            # tarball + .deb for amd64 and arm64
make deb                  # amd64 .deb only, needs dist-debian first
make deb ARCH=arm64
```

```bash
dpkg -i dist/node/linux-amd64/loft-node_*_amd64.deb
# edit /etc/loft/node.conf
systemctl enable --now loft-node
```

`postinst` creates the user, a token, a self-signed certificate, and default config. It does not start the unit. `apt purge loft-node` deletes `/etc/loft` and `/var/lib/loft`.

---

## 4. After install

`install.sh` writes `/root/loft-client.yaml` (mode 0600) with the token, advertise URL, and `controller_pub`. `insecure_skip_verify` is on while the certificate is self-signed. Replace the PEM with a public certificate and turn that field off.

```bash
journalctl -u loft-node -f
loft-node --version
loft-node -dir /var/lib/loft -print-pub
```

Open 443/tcp and 443/udp on the path in front of the host. QUIC and HTTP/3 use UDP.

---

## 5. Relay

On the edge:

```bash
sudo ./install.sh --role=relay \
  --id=ru1 \
  --controller=wss://controller.example/api/v1/sync \
  --advertise=https://203.0.113.10
```

The controller YAML must list the same `id` under `relays:` when `enrol: list`.
