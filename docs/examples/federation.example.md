# Federation config (`federation.json`)

Cross-host messaging federation is configured by a single JSON file
(ADR-0002 §7). It is loaded from `TORQUE_FEDERATION_CONFIG`, or — if that is
unset — from `<ConfigDir>/federation.json`.

**Standalone guarantee:** when the file is **absent**, federation is fully
disabled — no mTLS listener, no foreign routes, zero extra configuration and
zero new attack surface. A standalone Torque install needs no federation
config at all. The file is the opt-in.

A copy-pasteable starting point is in [`federation.example.json`](./federation.example.json).
The loader rejects unknown keys, so the example contains only real fields.

## Fields

| Key | Meaning |
|---|---|
| `listen_addr` | Bind address for the dedicated federation mTLS listener — separate from the plaintext GUI/API server. |
| `local_authorities` | The URN `authority` segments this install homes. Required, non-empty. Drives the routing decorator's strict mode and the authorization routing-invariant. |
| `identity.cert_file` / `identity.key_file` | This install's self-signed X.509 federation identity. Presented as the TLS **client** cert when dialing out and the TLS **server** cert when accepting. Relative paths resolve against the config file's directory. |
| `peers[]` | **Inbound** authorization. Each peer: a `label`, one-or-more pinned SHA-256 leaf-cert `fingerprints`, and the `authorities` it may act for. |
| `foreign_routes[]` | **Outbound** routes. Each: the foreign `authority`, the peer `endpoint` (`https://` only — the hop is mTLS), and the pinned `server_pins` fingerprint(s) of that endpoint's server cert. |

## Trust model (ADR-0002)

- **Identity** — mutual TLS. Both peers present a self-signed certificate,
  pinned by the other by SHA-256 leaf fingerprint. No CA, no CRL/OCSP.
- **Authorization** — a peer may only originate mail for authorities listed
  under its `peers[]` entry (no impersonation), and mail is only accepted for
  `local_authorities` (no relay abuse). Both checks are enforced server-side,
  fail-closed.
- **Rotation** — list two fingerprints for a peer (old + new) on both ends,
  deploy the new cert, then drop the old fingerprint: a zero-downtime overlap
  window with no CA.

## Producing a fingerprint

A pin is the lower-case hex SHA-256 of the certificate's DER-encoded leaf:

```sh
openssl x509 -in this-install.crt -outform DER | openssl dgst -sha256
```

The colon-separated form `openssl x509 -fingerprint -sha256` prints is also
accepted — the loader normalizes it.
