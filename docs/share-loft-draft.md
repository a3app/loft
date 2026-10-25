# Draft: laconic `.loft`

**Not in force.** Live spec: [share.md](share.md) §8 (YAML `.loft`). Come back to this before changing panel export or client import.

## Split

| File | What it is |
|------|------------|
| `.loft` | Share. One URI per line. QR, paste, and the file are the same format. |
| `.yaml` | Engine config (`client.yaml`): `outbounds`, `select`, `bootstrap`, `listen`. CLI, MikroTik, local SOCKS/TUN. |

Panel default download for a member is `.loft`. `.yaml` is the extra for operators. Panel checkboxes host / member / front (default member+front) fill the URI `#name`.

## `.loft` body

UTF-8. Trim BOM. One connection per non-empty line. `#` starts a comment only at the beginning of a line (not a URI fragment). Blank lines ignored.

```
loft://lt_…@api.loft.cx/static/v3/build?type=get&fp=chrome#bob-get
loft://lt_…@api.loft.cx/api/v1/tunnel?type=h2&fp=chrome#bob-h2
sstp://alice:secret@vpn.example.com#Office
vless://uuid@de1.example.com:443?security=reality&sni=www.example.com#Travel
```

Each line is its own **kind** of connection (`loft` / `sstp` / `vless` / `trojan`). Two `loft://` with the same token are extra fronts of one loft session: the client imports one profile, handshakes GET, then attaches the rest with `0x04` ([protocol.md](protocol.md) §4.4). One `loft://` GET is enough: Status `ticket` plus node caps list the other fronts. Duplicate `#name` on import of different kinds: keep both, suffix the second.

Name lives on the URI (`#…`). A missing fragment falls back to host, or host-front. Do not invent `# name:` comment keys for grouping.

Schemes: `loft://`, `sstp://`, `vless://`, `trojan://` as in [share.md](share.md) §§2, 5–7.

## Import

Detector still runs on content, not extension:

1. URI schemes (line by line if the body has several).
2. YAML with `token:` / `password:` / `outbounds:` / `connections:` — **old `.loft`**, keep working.
3. Else reject.

## URI vs YAML

A `loft://` line already carries token, host, port, path, front (`type` or query `get=`/`ws=`/`h2=`/`h3=`/`quic`), `fp`, `cdn`, `pbk`, name. Live `loft://` now prefers query front keys (see [share.md](share.md) §2); this draft is still the URI-line `.loft` file, not in force.

Not in the URI (stay in `.yaml` or on the device): `listen` / SOCKS / TUN, `instance_id`, `pause`.

Poorer than today’s panel YAML `.loft`:

- `bootstrap` (roster before a session). After connect, or a default `https://host` + roster path, can fill this later.
- `controller_pub` (pin). Without it, first roster is TOFU.
- several fronts in the file (`select: auto`). One GET `loft://` is enough; Status `fronts` plus `0x04` attach the rest (protocol §4.4 / §5.2). Query `get=`/`ws=`/`h2=` can still list them up front.
- `lists_url`, `p2p`, `decoy`, `sni`, `mux`, `inflight`, `h2_max_age_ms` — panel does not put these in issued files today; clients use defaults.

## Panel

- «Скачать .loft» — URI line(s), `#` from the host/member/front checkboxes.
- «Скачать .yaml» — full profile, `outbounds[].name` from the same checkboxes.
- Re-download uses current checkboxes if member keys are kept.
