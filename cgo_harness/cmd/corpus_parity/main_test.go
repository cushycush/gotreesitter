//go:build cgo && treesitter_c_parity

package main

import "testing"

func TestNormalizeArtifactMode(t *testing.T) {
	cases := []struct {
		name    string
		in      string
		want    string
		wantErr bool
	}{
		{name: "default empty", in: "", want: "all"},
		{name: "all", in: "all", want: "all"},
		{name: "all uppercase", in: "ALL", want: "all"},
		{name: "failures", in: "failures", want: "failures"},
		{name: "trimmed", in: " failures ", want: "failures"},
		{name: "invalid", in: "passes", wantErr: true},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got, err := normalizeArtifactMode(tc.in)
			if tc.wantErr {
				if err == nil {
					t.Fatalf("normalizeArtifactMode(%q) error = nil, want non-nil", tc.in)
				}
				return
			}
			if err != nil {
				t.Fatalf("normalizeArtifactMode(%q) error = %v", tc.in, err)
			}
			if got != tc.want {
				t.Fatalf("normalizeArtifactMode(%q) = %q, want %q", tc.in, got, tc.want)
			}
		})
	}
}

func TestShouldEmitArtifacts(t *testing.T) {
	cases := []struct {
		name     string
		dir      string
		mode     string
		pass     bool
		wantEmit bool
	}{
		{name: "empty dir", dir: "", mode: "all", pass: false, wantEmit: false},
		{name: "all passing", dir: "artifacts", mode: "all", pass: true, wantEmit: true},
		{name: "all failing", dir: "artifacts", mode: "all", pass: false, wantEmit: true},
		{name: "failures passing", dir: "artifacts", mode: "failures", pass: true, wantEmit: false},
		{name: "failures failing", dir: "artifacts", mode: "failures", pass: false, wantEmit: true},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := shouldEmitArtifacts(tc.dir, tc.mode, tc.pass)
			if got != tc.wantEmit {
				t.Fatalf("shouldEmitArtifacts(%q, %q, %v) = %v, want %v", tc.dir, tc.mode, tc.pass, got, tc.wantEmit)
			}
		})
	}
}

func TestShouldSkipGoOnOracleRootError(t *testing.T) {
	cases := []struct {
		name          string
		skip          bool
		cRootHasError bool
		want          bool
	}{
		{name: "disabled", skip: false, cRootHasError: true, want: false},
		{name: "oracle clean", skip: true, cRootHasError: false, want: false},
		{name: "oracle error", skip: true, cRootHasError: true, want: true},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := tc.skip && tc.cRootHasError
			if got != tc.want {
				t.Fatalf("skip=%v cRootHasError=%v => %v, want %v", tc.skip, tc.cRootHasError, got, tc.want)
			}
		})
	}
}

func TestOracleParseFailure(t *testing.T) {
	cases := []struct {
		name          string
		timeoutMicros uint64
		wantCategory  string
		wantError     string
	}{
		{
			name:         "no timeout configured",
			wantCategory: "c_parse",
			wantError:    "C oracle returned nil tree",
		},
		{
			name:          "timeout configured",
			timeoutMicros: 5_000_000,
			wantCategory:  "oracle_timeout",
			wantError:     "C oracle parse aborted after 5000ms timeout",
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			gotCategory, gotError := oracleParseFailure(tc.timeoutMicros)
			if gotCategory != tc.wantCategory {
				t.Fatalf("oracleParseFailure(%d) category = %q, want %q", tc.timeoutMicros, gotCategory, tc.wantCategory)
			}
			if gotError != tc.wantError {
				t.Fatalf("oracleParseFailure(%d) error = %q, want %q", tc.timeoutMicros, gotError, tc.wantError)
			}
		})
	}
}
