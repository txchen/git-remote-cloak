# Survey existing private Git backup solutions

Type: research
Status: resolved

## Question

Against the agreed Cloak contract, which maintained existing tools or designs can be adopted, combined, or rejected?

Use primary project documentation and source repositories to compare at least Git remote encryption helpers, selective Git encryption tools, encrypted filesystem/remotes, backup tools, and encrypted Git bundle/pack approaches. Evaluate:

- protection of file contents, original paths, and commit messages;
- exact full-repository recovery rather than a decrypted snapshot;
- ordinary local Git CLI push, fetch, pull, and clone behavior;
- stable Linux/macOS service use with a separately supplied secret;
- standard GitHub, GitLab, and self-hosted HTTPS/SSH remotes;
- storage/delta efficiency and known maintenance or security constraints.

The answer must make an evidence-backed adopt/combine/build recommendation without relaxing the agreed threat model.

## Answer

No maintained solution meets the full Cloak contract unchanged. Build an independent `git-remote-cloak`, adopting Git's remote-helper and native pack plumbing plus the encrypted-pack/encrypted-manifest architecture proven by `git-remote-gcrypt`; do not adopt its GPG model, GPL implementation, effective-force-push behavior, or whole-history-per-push hosted-Git strategy. Selective file encryption, SOPS, encrypted filesystems/remotes, backup repositories, and git-annex either expose Git path/message metadata or replace ordinary Git remote semantics.

Evidence and the full comparison: [Existing private Git backup solutions](../research/01-existing-solutions.md).
