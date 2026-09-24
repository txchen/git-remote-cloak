# Automatic Compaction policy study

Date: 2026-09-24

## Question

When should an ordinary push rebuild the complete Ciphertext Snapshot instead
of retaining incremental Pack Payloads?

## Production observations

All observations below used the production Go binary and a local bare Git
Repository Host. They do not measure hosted transport latency or GitHub object
retention.

| Workload | Original 50% trigger | Revised trigger |
| --- | ---: | ---: |
| One-line initial file, then 11 pushes each adding one line | 7 automatic Compactions | 0 automatic Compactions |
| One 2 MiB incompressible addition to a tiny repository | Meets the old byte trigger on the second push | Remains an incremental push with two live Pack Payloads |

The one-line workload used a Cloak clone for the later pushes. Under the revised
policy it ended with 12 original commits and 13 visible Storage History commits:
the initial empty publication, the first logical push, and 11 incremental
publications. A separate one-off 33-push end-to-end run confirmed that the
thirty-third live Pack Payload still triggers automatic Compaction; that
81-second scenario is represented in the permanent suite by a fast policy
boundary test and smaller end-to-end publication tests.

`BenchmarkMarkdownHeavyCompaction` used 200 Markdown documents and eight
incremental updates with automatic Compaction disabled. Its fragmented live
ciphertext measured 108,355 bytes; manual Compaction reduced it to 72,620
bytes, saving 35,735 bytes. The fragmented reachable Storage History was
121,959 bytes versus 73,294 bytes after Compaction. The eight pushes introduced
111,172 bytes of new ciphertext objects in total. The former 50% ratio alone
would treat this modest absolute growth as urgent, even though its storage
savings are only tens of kilobytes.

## Decision

Keep automatic Compaction at more than 32 live Pack Payloads. Retain the
relative byte trigger only when there are at least eight live Pack Payloads
and added ciphertext is at least 1 MiB as well as at least half the previous
compacted snapshot size. Older snapshots lacking a Compaction baseline still
receive one compatibility rebuild. Manual Compaction and its validation rules
are unchanged.

The 1 MiB floor is above the observed small-repository overhead by an order of
magnitude. The eight-Pack Payload floor prevents a single large addition from
forcing an immediate complete upload. The 32-Pack Payload cap still bounds
fragmentation and recovery work. These are policy thresholds, not ciphertext
format changes.

Added ciphertext is not a direct estimate of recoverable storage savings.
Eight unrelated incompressible Pack Payloads could still trigger a rebuild
that saves little. A future policy could compare an actual compacted candidate
against the fragmented snapshot before publication, but that requires a full
local repack to make the decision and a way to avoid repeating unproductive
probes on every later push.
