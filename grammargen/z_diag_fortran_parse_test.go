package grammargen

import (
	"context"
	"os"
	"testing"
	"time"

	gotreesitter "github.com/odvcencio/gotreesitter"
	"github.com/odvcencio/gotreesitter/grammars"
)

func TestDiagFortranParse(t *testing.T) {
	if os.Getenv("DIAG_FORTRAN_PARSE") != "1" {
		t.Skip("set DIAG_FORTRAN_PARSE=1")
	}

	// Import and generate
	pg := lookupParityGrammarByName("fortran")
	if pg == nil {
		t.Fatal("fortran not found")
	}
	gram, err := importParityGrammarSource(*pg)
	if err != nil {
		t.Fatalf("import: %v", err)
	}

	// Optionally disable inlining for testing
	if os.Getenv("DIAG_FORTRAN_NO_INLINE") == "1" {
		gram.Inline = nil
		t.Log("WARNING: inline disabled for testing")
	}

	ctx, cancel := context.WithTimeout(context.Background(), 12*time.Minute)
	defer cancel()
	genLang, err := GenerateLanguageWithContext(ctx, gram)
	if err != nil {
		t.Fatalf("generate: %v", err)
	}

	refLang := grammars.FortranLanguage()
	adaptExternalScanner(refLang, genLang)

	// Log parse table statistics
	genGLR := 0
	genSingleAction := 0
	for _, entry := range genLang.ParseActions {
		if len(entry.Actions) > 1 {
			genGLR++
		} else {
			genSingleAction++
		}
	}
	refGLR := 0
	refSingleAction := 0
	for _, entry := range refLang.ParseActions {
		if len(entry.Actions) > 1 {
			refGLR++
		} else {
			refSingleAction++
		}
	}
	t.Logf("parse-table: gen_entries=%d gen_glr=%d gen_single=%d", len(genLang.ParseActions), genGLR, genSingleAction)
	t.Logf("parse-table: ref_entries=%d ref_glr=%d ref_single=%d", len(refLang.ParseActions), refGLR, refSingleAction)

	// Log external symbol comparison
	t.Logf("gen externals: %d, ref externals: %d", len(genLang.ExternalSymbols), len(refLang.ExternalSymbols))
	for i := 0; i < len(genLang.ExternalSymbols) || i < len(refLang.ExternalSymbols); i++ {
		genName, refName := "-", "-"
		if i < len(genLang.ExternalSymbols) {
			s := genLang.ExternalSymbols[i]
			if int(s) < len(genLang.SymbolNames) {
				genName = genLang.SymbolNames[s]
			}
		}
		if i < len(refLang.ExternalSymbols) {
			s := refLang.ExternalSymbols[i]
			if int(s) < len(refLang.SymbolNames) {
				refName = refLang.SymbolNames[s]
			}
		}
		match := "OK"
		if genName != refName {
			match = "MISMATCH"
		}
		t.Logf("  ext[%d]: gen=%q ref=%q %s", i, genName, refName, match)
	}
	t.Logf("gen ExternalScanner: %v", genLang.ExternalScanner != nil)

	genParser := gotreesitter.NewParser(genLang)
	refParser := gotreesitter.NewParser(refLang)

	samples := []struct {
		name string
		src  string
	}{
		{"minimal_program", "program test\nend program\n"},
		{"program_with_var", "program test\n  integer :: x\n  x = 1\nend program\n"},
		{"module_empty", "module foo\nend module foo\n"},
		{"module_contains", "module foo\ncontains\nsubroutine bar()\nend subroutine bar\nend module foo\n"},
		{"subroutine", "subroutine test(x)\n  integer, intent(in) :: x\nend subroutine test\n"},
		{"if_stmt", "program test\n  if (x > 0) y = 1\nend program\n"},
		{"do_loop", "program test\n  do i = 1, 10\n    x = x + 1\n  end do\nend program\n"},
		{"where_stmt", "program test\nWHERE(A .NE. 0) C = B / A\nend program\n"},
		{"type_decl", "program test\n  type simple\n  end type\nend program\n"},
		{"use_stmt", "module foo\n  use bar, only: baz\nend module foo\n"},
		// From real corpus failures:
		{"module_typed_subroutines", "module foo\ncontains\n\nsubroutine test(arg1, arg2)\n  type(dt), intent(in) :: arg1\n  type(real), intent(out) :: arg2\nend subroutine test\n\nsubroutine test2(arg1, arg2)\n  class(dt), intent(in) :: arg1\n  type(real(real32)), intent(out) :: arg2\nend subroutine test2\n\nend module foo\n"},
		{"program_derived_type", "program test\n  type simple\n  end type\n  type, public :: custom_type\n    sequence\n    private\n    real(8) :: x,y,z\n    integer :: w,h,l\n  end type\nend program\n"},
		{"program_data_stmt", "program test\n  integer :: x(5)\n  data x /1, 2, 3, 4, 5/\nend program\n"},
		{"subroutine_implicit", "subroutine test()\n  implicit none\n  integer :: x\n  x = 1\nend subroutine test\n"},
		{"program_select_case", "program test\n  integer :: x\n  x = 1\n  select case (x)\n  case (1)\n    x = 2\n  case default\n    x = 3\n  end select\nend program\n"},
		{"program_if_else", "program test\n  if (x > 0) then\n    y = 1\n  else if (x < 0) then\n    y = -1\n  else\n    y = 0\n  end if\nend program\n"},
		{"program_format", "program test\n  write(*,'(A)') 'hello'\nend program\n"},
		{"program_include", "program test\n  include 'myfile.f90'\nend program\n"},
		{"use_write_formatted", "program test\n  use stdlib_string_type, only: string_type, assignment(=), write (formatted)\nend program example_padl\n"},
		{"program_where_block", "program test\nWHERE(PRESSURE .GE. 1.0)\n  PRESSURE = PRESSURE + 1.0\nELSEWHERE\n  PRECIPITATION = .TRUE.\nENDWHERE\nend program\n"},
	}

	for _, s := range samples {
		genTree, _ := genParser.Parse([]byte(s.src))
		refTree, _ := refParser.Parse([]byte(s.src))

		genRoot := genTree.RootNode()
		refRoot := refTree.RootNode()

		genSexpr := genRoot.SExpr(genLang)
		refSexpr := refRoot.SExpr(refLang)

		genHasError := nodeHasError(genRoot, genLang)
		refHasError := nodeHasError(refRoot, refLang)

		status := "MATCH"
		if genHasError && !refHasError {
			status = "GEN_ERROR"
		} else if genSexpr != refSexpr {
			status = "DIVERGE"
		}

		t.Logf("[%s] %s", s.name, status)
		if status != "MATCH" {
			if len(genSexpr) > 300 {
				genSexpr = genSexpr[:300] + "..."
			}
			if len(refSexpr) > 300 {
				refSexpr = refSexpr[:300] + "..."
			}
			t.Logf("  gen: %s", genSexpr)
			t.Logf("  ref: %s", refSexpr)
		}
	}
}

func lookupParityGrammarByName(name string) *importParityGrammar {
	for i := range importParityGrammars {
		if importParityGrammars[i].name == name {
			return &importParityGrammars[i]
		}
	}
	return nil
}

func nodeHasError(n *gotreesitter.Node, lang *gotreesitter.Language) bool {
	if n == nil {
		return false
	}
	if n.Type(lang) == "ERROR" {
		return true
	}
	for i := 0; i < n.ChildCount(); i++ {
		if nodeHasError(n.Child(i), lang) {
			return true
		}
	}
	return false
}
