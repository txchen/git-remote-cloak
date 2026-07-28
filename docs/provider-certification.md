# Repository Host certification runbook

Provider certification runs the production release binary through ordinary Git SSH or HTTPS transport. It never calls a GitHub or GitLab API.

Create four protected CI environments: `github-ssh`, `github-https`, `gitlab-ssh`, and `gitlab-https`. Each environment must use disposable private repositories and define:

- `CLOAK_PROVIDER_URL`: an empty writable repository;
- `CLOAK_PROVIDER_AUTH_REJECT_URL`: a repository URL whose authentication is expected to fail;
- `CLOAK_PROVIDER_OBJECT_REJECT_URL`: an empty repository configured to reject an object upload or exceed a test quota;
- `CLOAK_PROVIDER_PROTECTED_URL`: an empty repository whose rules reject creation or update of `refs/heads/cloak-storage`;
- SSH key material, or HTTPS username/token, scoped only to those disposable repositories.

HTTPS URLs must not embed credentials. The workflow installs a Git credential helper whose file contains no credential value. SSH uses a temporary mode-0600 key. Git tracing and shell tracing must remain disabled. Fixture names, URLs, commit messages, and test output must not contain a Recovery Secret or Protected Plaintext beyond the synthetic values created by the acceptance test.

Run the `Provider certification` workflow once for each environment. Missing fixtures fail the certification job. The provider test exercises init, clone, push, fetch after process restart, Logical Refs, hosted compare-and-swap publication, Compaction and Rekey force-re-rooting, live format inspection, every applicable Recovery Secret source, privacy inspection, authentication rejection, upload/quota rejection, and Storage Ref protection rejection. The ordinary production matrix in the same job additionally covers cryptographic conformance and key separation, deterministic metadata, rollback, fault safety, SHA-1/SHA-256, LFS and partial-clone rejection, and Format Migration behavior requiring a test-only second format.

Compare-and-swap publication and maintenance force-re-rooting must work through ordinary Git force-with-lease/force transport. If a Repository Host configuration cannot support either behavior, record the limitation and exclude that configuration from the published provider claim.

The happy-path test deletes the Storage Ref during cleanup. After the workflow, remove each disposable repository and credential through the Repository Host's normal recoverable lifecycle. Confirm cleanup in the provider UI, revoke credentials, and remember that host retention or garbage collection may preserve ciphertext. Save only provider, transport, run URL, commit, binary checksum, result, and cleanup confirmation in the certification record.
