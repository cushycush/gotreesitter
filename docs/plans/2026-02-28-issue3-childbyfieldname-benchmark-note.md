# Issue #3 ChildByFieldName benchmark note

## Scope
- Field lookup behavior after fixing field mapping propagation through flattened/invisible nodes.
- Focused microbenchmark: `BenchmarkIssue3ChildByFieldName` in `grammars/child_by_field_name_issue3_regression_test.go`.

## Command
```bash
GOMAXPROCS=1 go test ./grammars -run '^$' -bench '^BenchmarkIssue3ChildByFieldName$' -benchmem -count=10 -benchtime=750ms
```

## Result
- Not executed in this environment because the Go toolchain binary (`go`) is unavailable in PATH.
- No performance claims are made from this run; execute the command above in a Go-enabled environment to record final numbers.
