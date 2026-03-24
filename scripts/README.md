# Scripts

For heavy correctness, parity, and race work, prefer the Docker runners under
`cgo_harness/docker` and keep runs to one language at a time. The scripts in
this directory are host-side helpers, not the default path for OOM diagnosis.

`with_grammar_subset.sh` is the host-side low-memory wrapper for focused grammar
work. It forces serial subset builds, wires in external blob loading, and can
point built-in grammar loaders at local grammargen `.bin` overrides.

`test_race_serial.sh` is a legacy host-side race wrapper. Prefer CI or a
dedicated container first; if you ever fall back to it, keep it scoped to one
package and one target at a time.
