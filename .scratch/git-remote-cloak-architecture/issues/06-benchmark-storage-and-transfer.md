# Benchmark storage and transfer overhead

Type: prototype
Status: resolved
Blocked by: 04

## Question

What storage, transfer, and latency overhead does the selected representation impose on the intended small, Markdown-heavy repositories?

Build a reproducible fixture with many small Markdown edits plus a small number of already-compressed binary files. Compare a normal `git gc` repository with Cloak after initial push, many small incremental pushes, fresh clone, incremental fetch, and any proposed compaction. Report ratios and the mechanism behind them, not just elapsed times.

The working target to validate is roughly 1.0–1.2× the ordinary packed repository in the steady state and no worse than 1.3–1.5× before compaction under typical small pushes.

## Comments

- Benchmark prototype asset: branch `codex/prototype-storage-benchmark` at commit `c426a8c`, under `prototypes/git-round-trip/`.

## Answer

The user independently ran the deterministic prototype benchmark after the agent's validation run. Both runs produced materially identical capacity ratios, so the selected representation is accepted with mandatory operational compaction.

The fixture contained 250 Markdown files with 100 lines each, two already-compressed 384 KiB binary files, one initial push, and 51 subsequent pushes that each changed exactly two Markdown lines. It used real Git 2.55 with local `file://` transport, the prototype opaque codec, and canonical JSON rather than production cryptography and CBOR.

The independently reproduced measurements were:

- ordinary full-transfer pack: 934,785 bytes;
- fragmented Cloak live-snapshot pack: 1,292,481 bytes, or 1.38× ordinary Git;
- fragmented Cloak full Storage History pack: 1,755,324 bytes, or 1.88×;
- compacted Cloak live-snapshot pack: 968,248 bytes, or 1.04×;
- compacted Cloak full Storage History pack: 968,967 bytes, or 1.04×;
- cumulative ordinary incremental packs: 22,943 bytes;
- cumulative Cloak incremental packs: 850,770 bytes, or 37.08×; and
- live Pack Payload count before and after compaction: 52 and 1.

The large incremental ratio is real but small in absolute terms for this fixture: Cloak transferred about 808 KiB more across 51 pushes. Local median and p95 push times were 1.519 seconds and 3.492 seconds, but these timings do not validate GitHub, GitLab, HTTPS, or SSH latency.

Raw encryption framing is not the material cost. An ordinary thin pack can omit an old Markdown blob used as a delta base. A self-contained Cloak Pack Payload cannot depend on that omitted old base, so each small update carries a compressed complete changed blob plus its new commit and tree objects. The replacement encrypted manifest, complete snapshot trees, and retained outer Storage History add further overhead. Compaction restores cross-version delta selection by rebuilding one native Git pack.

V1 retains self-contained incremental Pack Payloads because independent recovery and limited corruption coupling are more valuable than ordinary-Git-like incremental efficiency for the intended small repositories. Thin cross-push payload dependencies remain a possible post-v1 optimization.

The proposed 1.3–1.5× pre-compaction figure is not a natural upper bound: the current live snapshot met it at 1.38× while the Repository Host's reachable Storage History reached 1.88×. It is therefore an operational threshold that must trigger compaction. The operational contract must define triggers using Pack Payload count, current-snapshot overhead, total Storage History size, and bytes added since the last compaction, with a manual override. Compaction must fully construct and validate a replacement snapshot before force-re-rooting the Storage Ref. Repository Host retention means it cannot guarantee immediate quota recovery or erasure of superseded ciphertext.
