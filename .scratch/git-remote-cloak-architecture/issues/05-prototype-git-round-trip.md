# Prototype the full Git round trip

Type: prototype
Blocked by: 04

## Question

Does a throwaway implementation of the selected representation actually behave like an ordinary Git remote while round-tripping exact original objects?

Exercise a local bare server through clone, fetch, push, pull/merge, branches, annotated and lightweight tags, ref deletion, force-push, divergent updates, signed objects if available, submodule metadata, empty repositories, interrupted operations, and shallow clone. Record which behaviors are guaranteed, emulated, inefficient, or unsupported.
