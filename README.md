# git-remote-cloak

`git-remote-cloak` is a single-owner private Git backup tool. An Authorized Host works with an ordinary Plaintext Workspace while an untrusted standard Git Repository Host stores only a Ciphertext Repository. The same binary provides the human commands and Git's `remote-cloak` helper.

The v1 writer supports Ciphertext Repository format v1.0 on Linux and macOS. Windows is supported through WSL, not through a native Windows binary. Run `git-remote-cloak version` for the binary's exact build and format capabilities.

See [the release and operating contract](docs/release.md) before relying on a release. Provider certification is intentionally separate from ordinary CI and uses disposable private repositories as described in [the certification runbook](docs/provider-certification.md).
