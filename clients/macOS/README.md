# loft · macOS

**English** · [Русский](README_ru.md) · [中文](README_zh.md)

Menu-bar agent. Minimum OS: **macOS 10.13**. UX: [`design/DESIGN.md`](design/DESIGN.md). Mock: [`design/index.html`](design/index.html).

```
make app          # ../../dist/clients/macOS/loft.app  (version patch +1)
make dmg          # ../../dist/clients/macOS/loft-*-macos-<arch>.dmg
make dmg BUMP=0   # same DMG, leave VERSION as-is (CI)
make dmg ARCH=x86_64 BUMP=0   # Intel slice (cross from Apple Silicon)
make dmg ARCH=arm64 BUMP=0    # Apple Silicon slice (cross from Intel)
make dmgs         # both DMGs; first may bump VERSION, second keeps it
make run
make test-crypto  # RFC 8439 vector → dist/clients/macOS/obj/<arch>/test_aead
make shot         # window PNGs → /tmp/loft-shots
make front-probe  # one loft-front handshake, no TUN
```

From the repo root: `make macos` / `make macos-dmg`.

The DMG has `loft.app`, an **Applications** symlink (drag to install), and [`pack/README.txt`](pack/README.txt).

Signing is ad-hoc until a Developer ID is in the keychain:

```bash
# local
make dmg

# first Developer ID Application in the keychain
CODESIGN_IDENTITY=auto make dmg

# explicit
CODESIGN_IDENTITY="Developer ID Application: Name (TEAMID)" make dmg

# optional notarization (keychain profile from `xcrun notarytool store-credentials`)
CODESIGN_IDENTITY=auto NOTARY_PROFILE=loft make dmg
```

`pack/sign.sh` signs `loft-priv`, `loft-front`, Swift dylibs, then the bundle. Entitlements: [`pack/entitlements.plist`](pack/entitlements.plist) (network + unsigned executable memory for `loft-front`). No App Sandbox — TUN and the setuid helper need it off.

`.o` and `test_aead` land in `dist/clients/macOS/obj/<arch>/` next to the app. The AEAD test source is [`tests/test_aead.c`](tests/test_aead.c). `--probe` and `--shot` stay on the app binary because they need AppKit.

Each `make app` raises the patch in [`VERSION`](VERSION) (`1.0.0` → `0.9.1` → `0.9.2`) and stamps `Info.plist`. About reads that string. Protocol software stays the repo-root `VERSION`. `BUMP=0` stamps the current file and does not write it. The DMG name includes `ARCH` (`arm64` / `x86_64`), default `uname -m`. CI (`make macos-dmgs BUMP=0`) builds both on the Apple Silicon runner: native arm64 and `ARCH=x86_64` cross.

The UI binary is Swift 5 + AppKit (in-process Go cannot target 10.13 after Go 1.20). Swift stdlib is copied into `Contents/Frameworks`. The overlay session is **`loft-front`** (Go) in `Contents/MacOS/` next to `loft`. [`gooverlay/`](gooverlay/) replaces stdlib `crypto/x509` Darwin verify so dyld does not need macOS 12 Security symbols. `loft-front` speaks GET, WS, H2, QUIC and H3, mux, clockless auth, mask, CmdLeave, overlay ICMP as Dgram `:1`. Hello offers the profile’s mux fronts (GET plus `ws` / `h2` / `quic` / `h3`) so 0x04 can attach. `l3` is its own session. Hello caps come from the Go client, not from `GETFront.swift`.

| Local | On the wire |
|-------|-------------|
| TUN (utun, default route) via loft-front | One connection, mux fronts GET/WS/H2/QUIC/H3, protocol 1.0 |
| SSTP | PPP over HTTPS to a Windows/accel-ppp SSTP server |

First Open asks for an administrator password (`ifconfig` / `route`). Hold closes the native default when Loft dies. **Reset routes** puts it back.

A profile is one `client.yaml` / `.loft` under `~/Library/Application Support/loft/profiles/`. Import, export, and manual edit are in **Open Loft…**.

Icons: `../../docs/brand/` (`app/loft.icns`, `menubar/loftTemplate.png`). Mark, palette, type: [`brand/README.md`](../../docs/brand/README.md).
