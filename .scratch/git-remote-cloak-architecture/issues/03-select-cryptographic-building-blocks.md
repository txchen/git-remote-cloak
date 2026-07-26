# Select cryptographic building blocks and Recovery Secret format

Type: research
Status: resolved

## Question

Which standardized, maintained cryptographic building blocks can implement a versioned Ciphertext Repository format without inventing cryptography?

Using primary specifications and maintained library documentation, evaluate:

- a 256-bit random Recovery Secret and versioned 24-word English mnemonic encoding with checksum;
- domain-separated key derivation for repository authentication, encrypted payloads, and any deterministic identifiers;
- authenticated encryption suitable for encrypted packs or chunks, including nonce and misuse-resistance requirements;
- keyed deterministic, portable identifiers when stable remote names are needed, with legal path-component encoding and bounded collision risk;
- repository binding, tamper detection, rollback limitations, format versioning, and secret validation without leaking Protected Plaintext;
- library availability and interoperability on Linux and macOS.

The answer must distinguish a mnemonic encoding from a human-created password and from a cryptocurrency wallet seed workflow.

## Answer

Use a 32-byte OS-generated Recovery Secret encoded as `cloak-v1:` plus the 24-word BIP-39 English entropy/checksum representation, without BIP-39 wallet seed derivation or a user-created passphrase. Bind HKDF-SHA-256 to a random public Repository ID and derive separate keys for header authentication, payload encryption, and each deterministic-identifier purpose.

The preferred independently specified payload primitive is chunked AES-256-GCM-SIV (RFC 8452), using a unique per-payload derived key, implicit chunk-index nonces, comprehensive associated data, and an authenticated final chunk. Libsodium `secretstream` is a safe lower-framing-risk alternative to benchmark, but its stream construction is library-defined rather than an IETF RFC format.

Use full HMAC-SHA-256 tags encoded as `c1-` plus lowercase unpadded RFC 4648 Base32 for stable opaque remote names. This produces a 55-character portable component but is not reversible by itself, so original names must live in authenticated encrypted metadata.

AEAD and MACs detect tampering, not replay of an older complete valid repository; fresh-clone rollback detection is impossible without trusted external state. At 1 MiB chunks, AES-GCM-SIV adds about 16 KiB per GiB (0.00153%) plus small headers. The material capacity risk is loss of Git compression if encryption happens before packing, so the format should pack/compress first and encrypt second.

Full findings: [03-cryptographic-building-blocks.md](../research/03-cryptographic-building-blocks.md)
