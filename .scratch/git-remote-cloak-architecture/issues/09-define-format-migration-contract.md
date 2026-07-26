# Define the repository-format migration contract

Type: grilling
Blocked by: 07

## Question

How does Cloak recognize, read, and migrate versioned Ciphertext Repositories without corrupting the current backup or silently weakening its security contract?

Specify Bootstrap Header version negotiation, reader and writer compatibility, unsupported-version failure, safe same-secret format upgrades from a complete Logical Repository, migration validation and atomic publication, rollback-checkpoint continuity, interaction with compaction and rekey, and whether any downgrade path belongs in v1.
