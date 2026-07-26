# Cryptographic building blocks for the Cloak v1 format

Research date: 2026-07-25

## Recommendation

Use a small, versioned suite assembled entirely from published primitives:

- generate one 32-byte Recovery Secret with the operating system CSPRNG;
- encode those exact 32 bytes as a `cloak-v1:`-prefixed, 24-word English mnemonic using only BIP-39's entropy/checksum/word-list codec;
- derive independent keys with HKDF-SHA-256, binding every derivation to a random public Repository ID and immutable purpose label;
- encrypt pack payloads in fixed-size authenticated chunks with AES-256-GCM-SIV;
- derive stable opaque remote names with full-length HMAC-SHA-256 and encode them as lowercase, unpadded RFC 4648 Base32;
- authenticate the public repository header and make every payload chunk authenticate its format, repository, payload, position, length, and finality metadata.

This suite deliberately does not use a password KDF, wallet seed derivation, unauthenticated encryption, or one key for multiple purposes.

## Recovery Secret and mnemonic

Generate the Recovery Secret as 32 unpredictable bytes. Libsodium documents `randombytes_buf()` as suitable for creating secret keys and uses `getrandom` on recent Linux kernels; Rust's maintained `getrandom` crate documents `getrandom(2)` on Linux and `getentropy` on macOS ([libsodium random data](https://doc.libsodium.org/generating_random_data), [Rust `getrandom` platform table](https://docs.rs/getrandom/latest/getrandom/)).

The canonical human representation should be:

```text
cloak-v1:<word-1> <word-2> ... <word-24>
```

For exactly 256 bits of entropy, BIP-39 appends the first 8 bits of SHA-256 as a checksum and splits the resulting 264 bits into 24 indices in its 2,048-word list. The specification explicitly describes this as a transport encoding for computer-generated randomness, not a way to turn a user-created sentence into a key ([BIP-39](https://github.com/bitcoin/bips/blob/master/bip-0039.mediawiki)).

Pin the following as part of the Cloak mnemonic format:

- the BIP-39 English word list and its ordering;
- 256-bit entropy only, hence exactly 24 words;
- BIP-39 entropy-to-words and words-to-entropy/checksum processing only;
- lowercase canonical output with one ASCII space between words;
- a mandatory `cloak-v1:` prefix outside the 24 words.

Do **not** run the BIP-39 PBKDF2 “mnemonic to seed” step and do not accept an optional wallet passphrase. The decoded 32 entropy bytes are the Recovery Secret directly. Thus the phrase is neither a human-created password nor a cryptocurrency wallet seed workflow. The prefix is especially important because BIP-39 itself now records that it has no versioning scheme and is discouraged for new wallet designs; Cloak is borrowing only its well-known human transcription codec ([BIP-39 shortcomings](https://github.com/bitcoin/bips/blob/master/bip-0039.mediawiki#user-content-shortcomings)).

The 8-bit mnemonic checksum catches most transcription errors but accepts about one in 256 random 24-word sequences. It is not repository authentication. A maintained Rust implementation can convert 256-bit entropy to 24 English words and back while checking the checksum, without calling its seed-derivation methods ([`bip39::Mnemonic`](https://docs.rs/bip39/latest/bip39/struct.Mnemonic.html)).

## Repository binding and domain separation

Create a random 32-byte public Repository ID during `init` and store it in the public format header. The Repository ID is not a second secret and is not an envelope. Use it as HKDF salt:

```text
PRK = HKDF-Extract-SHA256(
  salt = repository_id,
  IKM  = recovery_secret
)
```

RFC 5869 recommends a salt when one is available and says that `info` binds derived material to application- and context-specific information, preventing the same derived key from appearing in different contexts ([RFC 5869, sections 3.1–3.2](https://www.rfc-editor.org/rfc/rfc5869.html#section-3.1)). Expand separate 32-byte keys using an unambiguous, length-delimited encoding of immutable labels such as:

```text
git-remote-cloak | mnemonic-v1 | format-v1 | suite-1 | header-auth
git-remote-cloak | mnemonic-v1 | format-v1 | suite-1 | payload-root
git-remote-cloak | mnemonic-v1 | format-v1 | suite-1 | path-component-id
git-remote-cloak | mnemonic-v1 | format-v1 | suite-1 | object-id
```

If a use is not needed in the eventual representation, do not derive its key. Never reuse the path key for object IDs, header authentication, or payload encryption. HKDF is suitable here because the input is already 256 random bits; RFC 5869 warns that HKDF cannot amplify a low-entropy password, which is another reason not to accept user-created passphrases as Recovery Secrets ([RFC 5869, section 4](https://www.rfc-editor.org/rfc/rfc5869.html#section-4)).

Derive each payload key from `payload-root` with a fresh random 32-byte Payload ID in the expansion context. This limits the consequences of accidental nonce reuse across independently encrypted packs. The Repository ID, format version, suite ID, and Payload ID must all be authenticated, not merely parsed.

A public Repository ID binds ciphertext and deterministic identifiers to this repository, but a clone possessing only a mnemonic has no independent knowledge of which remote URL is the intended one. The product must enforce the existing “one independent Recovery Secret per repository” rule. Reusing one secret across repositories would permit a malicious host to substitute a different complete repository encrypted under that same secret.

## Payload authenticated encryption

### Preferred v1 primitive: AES-256-GCM-SIV

AES-256-GCM-SIV is specified by the CFRG in RFC 8452. It takes a 32-byte key, a 12-byte nonce, and produces ciphertext 16 bytes longer than the plaintext. Unlike ordinary AES-GCM or ChaCha20-Poly1305, a repeated nonce is not catastrophic; repetition reveals whether the repeated-nonce plaintexts are equal. The RFC nevertheless recommends generating nonces randomly rather than intentionally reusing them ([RFC 8452](https://www.rfc-editor.org/rfc/rfc8452.html)).

For a pack or other large payload, use fixed-size chunks (1 MiB is a reasonable benchmark baseline), a fresh 32-byte Payload ID, and a payload-specific HKDF key. An implicit 96-bit nonce can be the fixed-width unsigned chunk index under that unique payload key. The format must cap the index before wraparound and reject duplicate Payload IDs. AES-GCM-SIV's misuse resistance remains defense in depth if a Payload ID is accidentally reused.

Every chunk's associated data should be a canonical fixed-width or length-delimited encoding of:

- format and suite version;
- Repository ID and Payload ID;
- payload kind;
- chunk index;
- plaintext chunk length;
- a final-chunk flag, plus total plaintext length on the final chunk.

This makes chunk replacement, reordering, duplication, and truncation fail authentication or format validation. Decrypted bytes must not be published into the working repository until all expected chunks and the authenticated final chunk have been validated. RFC 8452 also requires that unauthenticated plaintext not be output before tag confirmation ([RFC 8452 decryption requirements](https://www.rfc-editor.org/rfc/rfc8452.html#section-5)).

The maintained RustCrypto `aes-gcm-siv` crate is a pure-Rust RFC 8452 implementation and currently exposes AES-256-GCM-SIV on Linux and macOS without a native library dependency ([RustCrypto `aes-gcm-siv`](https://docs.rs/crate/aes-gcm-siv/latest/source/)). The format must pin RFC behavior, not a crate's serialization, and ship independent test vectors.

### Safe alternative: libsodium `secretstream`

Libsodium's `crypto_secretstream_xchacha20poly1305` is a strong alternative if avoiding custom chunk framing is more important than choosing an RFC-specified on-disk primitive. It automatically generates its stream header/nonces, authenticates each chunk, detects added, removed, duplicated, truncated, reordered, or modified chunks, and supports arbitrary stream length ([libsodium `secretstream`](https://doc.libsodium.org/secret-key_cryptography/secretstream)).

Its exact overhead is a 24-byte stream header plus 17 bytes per chunk. Libsodium is portable and maintained for Linux and macOS, but `secretstream` is a libsodium-defined construction rather than an IETF RFC file format. For a long-lived backup format, AES-GCM-SIV plus a fully specified Cloak frame has the clearer independent interoperability story. `secretstream` remains useful as a prototype comparison and may be selected instead if the prototype shows that framing risk outweighs that benefit.

## Stable opaque remote names

When the storage representation needs a stable name for a plaintext path component or logical object, use HMAC-SHA-256 under its separately derived key:

```text
token = "c1-" || lowercase(
  base32_nopad(
    HMAC-SHA256(
      purpose_key,
      typed_length_prefixed_input
    )
  )
)
```

HMAC is a standardized deterministic keyed authenticator: the same key/message pair produces the same tag, while the key is required to compute a valid tag ([RFC 2104](https://www.rfc-editor.org/rfc/rfc2104.html), [libsodium authentication](https://doc.libsodium.org/secret-key_cryptography/secret-key_authentication)). Use the full 32-byte tag rather than truncating it. Unpadded Base32 encodes it in 52 characters, so the complete token is 55 ASCII characters.

RFC 4648 Base32 uses only letters and digits `2`–`7`; specifying canonical lowercase output, no padding, and a fixed `c1-` prefix yields a component that contains no `/`, is not `.`, `..`, or `.git`, avoids Windows reserved device basenames, and has identical behavior on case-sensitive and case-insensitive filesystems ([RFC 4648 Base32](https://www.rfc-editor.org/rfc/rfc4648.html#section-6), [Microsoft filename rules](https://learn.microsoft.com/en-us/windows/win32/fileio/naming-a-file)). Decode only the exact canonical form; do not accept visually permissive character substitutions.

For a path component, authenticate its exact raw Git pathname bytes. Do not Unicode-normalize or case-fold them, because Git distinguishes byte sequences that some filesystems do not. Length-prefix and type-tag the input so two structured inputs cannot serialize identically.

This token is an identifier, not reversible encryption. Restoring the original name requires authenticated encrypted metadata elsewhere in the chosen repository representation. A same-basename token intentionally leaks equality across the repository, as accepted by the threat model, but a host without the Recovery Secret cannot confirm a filename guess.

## Header authentication and validation

The public repository header should contain only non-secret bootstrap data: a magic value, format version, suite ID, Repository ID, and strictly bounded lengths. Store a full 32-byte HMAC-SHA-256 over the canonical header under `header-auth`.

Validation order should be:

1. enforce hard byte-size limits and parse only the canonical encoding;
2. reject unknown major versions, suite IDs, impossible lengths, and non-canonical identifiers;
3. parse the required `cloak-v1:` mnemonic, exactly 24 English words, and verify its 8-bit checksum;
4. recover the 32-byte secret, derive keys, and verify the header HMAC in constant time;
5. authenticate every payload chunk and require exactly one authenticated final chunk before publishing recovered state;
6. detect duplicate IDs or deterministic-name collisions and fail rather than overwrite.

The header MAC gives a strong wrong-secret/wrong-repository check. It also creates an offline verifier for Recovery Secret guesses, but that is acceptable only because the secret is uniformly random with 256 bits; it would be unsafe with a user-created password. Error messages and logs must never print the mnemonic, secret, derived keys, decrypted names, or unverified plaintext.

The binary format version and mnemonic prefix serve different purposes. `cloak-v1:` identifies the human encoding and root-secret semantics. The binary header independently selects the repository format and cryptographic suite. Unknown suites must fail closed, with no heuristic fallback. Publish end-to-end test vectors covering mnemonic decoding, all HKDF outputs, header MAC, name token, one- and multi-chunk payloads, corruption, wrong secret, final-chunk omission, and version rejection.

The current maintained pure-Rust building blocks are available across the two supported platforms: `getrandom` for OS entropy, `bip39` for entropy/word conversion, RustCrypto `hkdf`, `hmac`, and `sha2`, RustCrypto `aes-gcm-siv`, and an RFC 4648 encoder such as `data-encoding` ([HKDF crate](https://docs.rs/hkdf/latest/hkdf/), [HMAC crate](https://docs.rs/hmac/latest/hmac/), [`data-encoding`](https://docs.rs/crate/data-encoding/latest)). Dependency versions must be pinned by the implementation lockfile, but the Ciphertext Repository contract must be defined by the cited specifications and Cloak test vectors rather than those versions.

## Tamper detection is not rollback detection

Header MACs, AEAD tags, and Git object hashes detect unauthorized modification and incomplete/mixed payloads. They cannot tell a newly cloned machine that the host supplied an older, complete, previously valid Ciphertext Repository. Rollback defenses require trusted state outside the potentially rolled-back repository, such as a locally persisted last-seen generation/tip or an independent witness. Systems such as TUF detect rollback by comparing signed versions to previously trusted versions, illustrating the need for client-side trusted state ([TUF client workflow](https://theupdateframework.github.io/specification/draft/#detailed-client-workflow)).

Therefore v1 should:

- authenticate generation/ref metadata so arbitrary edits are rejected;
- warn or fail on rollback relative to a locally stored last-seen state when one exists;
- explicitly state that a fresh machine with only the mnemonic and remote URL cannot detect replay of an older complete valid state.

This limitation matches the current threat model; do not claim cryptographic rollback prevention from AEAD alone.

## Capacity overhead

With 1 MiB fixed chunks and implicit per-chunk nonces, AES-256-GCM-SIV adds 16 bytes per chunk:

```text
ciphertext bytes =
  plaintext packed bytes
  + 16 * ceil(plaintext packed bytes / 1 MiB)
  + small versioned headers/trailers
```

For 1 GiB, the tags total 16 KiB, about 0.00153%. A Payload ID, lengths, version fields, and an authentication value add only tens to low hundreds of bytes per encrypted pack. Libsodium `secretstream` would add 24 bytes plus 17 KiB per GiB at the same chunk size, about 0.00162%.

Small payloads have a larger percentage overhead, which is why the architecture should compress and Git-pack first, then encrypt the resulting pack-sized payload. Encrypting individual source files before Git sees them would make ciphertext incompressible and destroy Git's cross-version delta compression; that storage loss can dwarf all cryptographic headers. For a pack-first design, raw cryptographic expansion should remain far below 1%. Any larger steady-state increase will come from the Ciphertext Repository's container/index layout, retained obsolete packs, and garbage-collection policy, and must be measured by the round-trip prototype rather than attributed to encryption itself.
