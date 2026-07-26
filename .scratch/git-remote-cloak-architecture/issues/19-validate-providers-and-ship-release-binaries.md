# 19 — Validate providers and ship release binaries

**What to build:** Certify the complete v1 product against real standard Repository Hosts and supported platforms, then produce Linux and macOS release binaries whose claims, limitations, cryptographic dependencies, and operational behavior match the [v1 specification](../spec.md).

**Blocked by:** 13 — Support Git recovery modes and enforce exclusions; 15 — Protect concurrent publication and known rollback; 16 — Compact a fragmented Ciphertext Repository; 17 — Rekey from a complete Logical Repository; 18 — Migrate repository format explicitly.

**Status:** ready-for-agent

- [ ] The same production binary completes init, clone, push, fetch, logical ref workflows, Compaction, Rekey, and applicable format checks against temporary private GitHub and GitLab repositories without provider-specific APIs.
- [ ] Real-provider coverage includes SSH and HTTPS transport, configured Git credential helpers, Repository Host authentication rejection, object or quota failure, and Storage Ref branch-protection rejection.
- [ ] A service using each supported Recovery Secret source resumes clone, fetch, and push after restart without an interactive prompt.
- [ ] Provider tests prove that compare-and-swap publication and maintenance force-re-rooting behave as required or narrow the published provider claim rather than adding an incidental provider API.
- [ ] The production verification matrix passes for cryptographic format conformance, key separation, deterministic metadata, concurrent publication, rollback, fault safety, Git object formats, LFS rejection, partial-clone rejection, and credentials.
- [ ] Linux and macOS release binaries are produced from pinned Go and module dependencies, include checksums, and report build and exact format capabilities through the version command.
- [ ] The release process confirms that the binary does not depend on CGo, a native cryptographic library, the Python prototype, or an additional Cloak executable.
- [ ] A WSL smoke test documents the supported Windows path without claiming a native Windows binary.
- [ ] Release documentation states the single-owner scope, accepted metadata leakage, fresh-clone rollback limitation, Git LFS and partial-clone exclusions, submodule independence, service Secret model, and Repository Host retention limits.
- [ ] Temporary provider resources and credentials are handled without logging Protected Plaintext or Recovery Secrets and are removed through the provider's normal recoverable lifecycle after certification.
