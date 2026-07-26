# Benchmark storage and transfer overhead

Type: prototype
Blocked by: 04

## Question

What storage, transfer, and latency overhead does the selected representation impose on the intended small, Markdown-heavy repositories?

Build a reproducible fixture with many small Markdown edits plus a small number of already-compressed binary files. Compare a normal `git gc` repository with Cloak after initial push, many small incremental pushes, fresh clone, incremental fetch, and any proposed compaction. Report ratios and the mechanism behind them, not just elapsed times.

The working target to validate is roughly 1.0–1.2× the ordinary packed repository in the steady state and no worse than 1.3–1.5× before compaction under typical small pushes.
