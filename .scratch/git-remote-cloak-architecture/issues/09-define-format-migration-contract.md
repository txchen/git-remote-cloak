# Define the repository-format migration contract

Type: grilling
Status: resolved
Blocked by: 07

## Question

How does Cloak recognize, read, and migrate versioned Ciphertext Repositories without corrupting the current backup or silently weakening its security contract?

Specify Bootstrap Header version negotiation, reader and writer compatibility, unsupported-version failure, safe same-secret format upgrades from a complete Logical Repository, migration validation and atomic publication, rollback-checkpoint continuity, interaction with compaction and rekey, and whether any downgrade path belongs in v1.

## Answer

Format Migration is an explicit maintenance operation. Installing or invoking a newer `git-remote-cloak` binary never changes a Ciphertext Repository format during ordinary clone, fetch, push, or Compaction. If a binary can read an existing format but no longer supports writing it, a write fails with a migration-required error.

### Version discovery and authentication

The fixed Storage Ref and Bootstrap Header locator remain stable across repository formats. A bounded public Bootstrap Preamble carries only:

- Cloak magic bytes;
- the bootstrap framing version;
- repository format major and minor versions;
- the bounded Bootstrap Header length; and
- required feature identifiers.

The preamble selects a parser but is never trusted. It contains no Protected Plaintext, has strict allocation limits, and is covered by the subsequently authenticated Bootstrap Header. Unknown bootstrap framing, an unsupported major version, or an unknown required feature fails closed before any encrypted payload is processed. The parsed full header must authenticate under the Recovery Secret and must agree with the preamble.

A format version does not imply capability by itself. Every binary explicitly declares the exact formats and required features it can read and write:

```text
git-remote-cloak version --formats
```

Machine-readable output is available for service deployment checks. A binary may offer read-only clone, fetch, and `doctor` for a newer minor format only when its declared reader understands every required feature and the schema marks all other fields optional. It never writes a format unless it implements that exact writer. Generic CBOR unknown-field tolerance is not writer compatibility.

Format retirement normally proceeds from read/write, to read-only with Migration required for writes, and finally to unsupported. A critical security defect may move a format directly to unsupported without a compatibility grace period.

The `cloak-v1:` Recovery Mnemonic version is independent of the Ciphertext Repository format version. Format Migration does not change the mnemonic.

### Explicit command and target selection

The human command is:

```text
git-remote-cloak migrate <remote-name>
```

It selects the current binary's default latest writable format only after displaying the current and target formats, Logical Ref count, estimated full upload, and writer compatibility effect, then requiring confirmation.

Unattended Migration must pin its target and consent:

```text
git-remote-cloak migrate <remote-name> --to <format> --yes
```

Automation cannot use `--yes` without `--to`, because a binary upgrade must not silently change the selected target. `--dry-run --json` performs read-only compatibility, capacity, and migration-plan checks.

### Complete rebuild and validation

Migration uses the current Recovery Secret to fetch and authenticate the latest remote Ciphertext Snapshot. The remote Logical Refs and Logical HEAD are authoritative; Migration does not infer the ref set from whichever local branches happen to exist. It first recovers every required original object.

The target keeps the Recovery Secret and Repository ID, derives target-format keys with format-specific domain separation, and increments storage generation exactly once. It creates a complete compacted target-format snapshot and re-encrypts every pack, index, and manifest rather than carrying cross-format ciphertext dependencies.

Before publication, Cloak restores the candidate into a temporary repository, compares every Logical Ref and reachable original Git object ID, confirms Logical HEAD, and runs `git fsck --full`. The new authenticated manifest records the previous format, generation, and Storage Ref as its Migration source.

A local operation lock prevents push, Compaction, or another Migration from the same Logical Repository during construction. Publication uploads immutable ciphertext first and then compare-and-swap replaces the expected old Storage Ref. Any concurrent remote update aborts Migration and requires a new plan; Cloak never overwrites it.

### Storage and rollback continuity

The migrated snapshot is a parentless Storage History root. It does not retain old-format storage commits as Git parents, so fresh recovery never requires the old parser and Migration also removes current-format fragmentation. The protected Logical Repository, commits, refs, messages, paths, and original object IDs remain unchanged.

Repository ID continuity, strictly increasing generation, the authenticated Migration source, and the existing crash journal allow a returning client to distinguish a completed Migration from an old-state replay. The Rollback Checkpoint updates only after the target snapshot validates and Storage Ref publication is confirmed. Fresh-clone rollback limitations remain unchanged.

The old Storage History and ciphertext become unreachable from the current Storage Ref, but Repository Host retention may preserve them. Migration cannot promise erasure or immediate quota recovery.

### Boundaries with other maintenance

Migration itself performs Compaction and resets the Pack Payload count and compacted-size baseline. Ordinary Compaction preserves the current format and never upgrades it.

When both a new Recovery Secret and a newer format are desired, the Repository Owner runs Rekey directly. Rekey creates a new Repository ID at generation one using the current binary's default latest writable format; it does not first migrate the old identity.

V1 has no in-place downgrade, cross-format Storage History, dual-format publication, or second Storage Ref. An older authenticated generation whose objects remain available in local cache or Repository Host storage may be recovered explicitly into a separate local directory, but normal operations never rewrite the remote to an older format. Re-establishing an old format requires a new Ciphertext Repository identity and is a destructive Rekey/reinitialization rather than a downgrade.
