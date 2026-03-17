package grammargen

// DanmujiGrammar returns a BDD testing DSL grammar that extends Go.
// It adds test blocks, BDD structure (given/when/then), assertions,
// test doubles (mock/fake/spy), lifecycle hooks, data tables, and tags.
func DanmujiGrammar() *Grammar {
	return ExtendGrammar("danmuji", GoGrammar(), func(g *Grammar) {
		// ---------------------------------------------------------------
		// Test categories
		// ---------------------------------------------------------------
		g.Define("test_category",
			Choice(
				Str("unit"),
				Str("integration"),
				Str("e2e"),
			))

		// ---------------------------------------------------------------
		// Tags: @slow, @smoke, @skip, @focus, @flaky, @parallel,
		//       or any @identifier
		// ---------------------------------------------------------------
		g.Define("tag",
			Seq(
				Str("@"),
				ImmToken(Pat(`[a-zA-Z_][a-zA-Z0-9_]*`)),
			))

		g.Define("tag_list",
			Repeat1(Sym("tag")))

		// ---------------------------------------------------------------
		// Test block: [tag_list] test_category "name" { body }
		// ---------------------------------------------------------------
		g.Define("test_block",
			Seq(
				Optional(Field("tags", Sym("tag_list"))),
				Field("category", Sym("test_category")),
				Field("name", Sym("_string_literal")),
				Sym("block"),
			))

		// ---------------------------------------------------------------
		// BDD structure: given/when/then blocks
		// ---------------------------------------------------------------
		g.Define("given_block",
			Seq(
				Str("given"),
				Field("description", Sym("_string_literal")),
				Sym("block"),
			))

		g.Define("when_block",
			Seq(
				Str("when"),
				Field("description", Sym("_string_literal")),
				Sym("block"),
			))

		g.Define("then_block",
			Seq(
				Str("then"),
				Field("description", Sym("_string_literal")),
				Sym("block"),
			))

		// ---------------------------------------------------------------
		// Assertions
		// ---------------------------------------------------------------
		g.Define("expect_statement",
			Seq(
				Str("expect"),
				Field("actual", Sym("_expression")),
				Optional(
					Choice(
						Seq(Str("=="), Field("expected", Sym("_expression"))),
						Seq(Str("!="), Field("expected", Sym("_expression"))),
						Seq(Str("contains"), Field("expected", Sym("_expression"))),
						Str("is_nil"),
						Str("not_nil"),
					),
				),
			))

		g.Define("reject_statement",
			Seq(
				Str("reject"),
				Field("actual", Sym("_expression")),
			))

		// ---------------------------------------------------------------
		// Test doubles: mock, fake, spy
		// ---------------------------------------------------------------

		// mock_method: identifier parameter_list ["->" type ["=" default_value]]
		g.Define("mock_method",
			Seq(
				Field("name", Sym("identifier")),
				Field("parameters", Sym("parameter_list")),
				Optional(Seq(
					Str("->"),
					Field("return_type", Sym("_simple_type")),
					Optional(Seq(
						Str("="),
						Field("default_value", Sym("_expression")),
					)),
				)),
			))

		// mock_declaration: "mock" identifier block
		// (mock_methods appear as statements inside the block)
		g.Define("mock_declaration",
			Seq(
				Str("mock"),
				Field("name", Sym("identifier")),
				Field("body", Sym("block")),
			))

		// fake_method: identifier parameter_list ["->" type] block
		g.Define("fake_method",
			Seq(
				Field("name", Sym("identifier")),
				Field("parameters", Sym("parameter_list")),
				Optional(Seq(
					Str("->"),
					Field("return_type", Sym("_simple_type")),
				)),
				Field("body", Sym("block")),
			))

		// fake_declaration: "fake" identifier block
		// (fake_methods appear as statements inside the block)
		g.Define("fake_declaration",
			Seq(
				Str("fake"),
				Field("name", Sym("identifier")),
				Field("body", Sym("block")),
			))

		// spy_declaration: "spy" identifier
		g.Define("spy_declaration",
			Seq(
				Str("spy"),
				Field("name", Sym("identifier")),
			))

		// ---------------------------------------------------------------
		// Verification
		// ---------------------------------------------------------------
		g.Define("verify_assertion",
			Choice(
				Seq(Str("called"), Sym("int_literal"), Str("times")),
				Seq(Str("called"), Str("with"), Str("("), CommaSep(Sym("_expression")), Str(")")),
				Str("not_called"),
			))

		g.Define("verify_statement",
			Seq(
				Str("verify"),
				Field("target", Sym("_expression")),
				Field("assertion", Sym("verify_assertion")),
			))

		// ---------------------------------------------------------------
		// Lifecycle hooks
		// ---------------------------------------------------------------
		g.Define("lifecycle_hook",
			Seq(
				Choice(Str("before"), Str("after")),
				Choice(Str("each"), Str("all")),
				Sym("block"),
			))

		// ---------------------------------------------------------------
		// Data tables
		// ---------------------------------------------------------------
		g.Define("table_row",
			Seq(
				Str("|"),
				Repeat1(Seq(Sym("_expression"), Str("|"))),
			))

		g.Define("table_declaration",
			Seq(
				Str("table"),
				Field("name", Sym("identifier")),
				Str("{"),
				Repeat(Sym("table_row")),
				Str("}"),
			))

		g.Define("each_row_block",
			Seq(
				Str("each"),
				Str("row"),
				Str("in"),
				Field("table", Sym("identifier")),
				Sym("block"),
			))

		// ---------------------------------------------------------------
		// Needs blocks: service dependency declarations
		// ---------------------------------------------------------------
		g.Define("service_type",
			Choice(
				Str("postgres"),
				Str("redis"),
				Str("mysql"),
				Str("kafka"),
				Str("mongo"),
				Str("rabbitmq"),
				Str("nats"),
				Str("container"),
			))

		g.Define("needs_block",
			Seq(
				Str("needs"),
				Field("service", Sym("service_type")),
				Field("name", Sym("identifier")),
				Sym("block"),
			))

		// ---------------------------------------------------------------
		// Wire into Go: extend _top_level_declaration and _statement
		// ---------------------------------------------------------------
		AppendChoice(g, "_top_level_declaration",
			Sym("test_block"),
		)

		AppendChoice(g, "_statement",
			Sym("given_block"),
			Sym("when_block"),
			Sym("then_block"),
			Sym("expect_statement"),
			Sym("reject_statement"),
			Sym("mock_declaration"),
			Sym("fake_declaration"),
			Sym("spy_declaration"),
			Sym("verify_statement"),
			Sym("lifecycle_hook"),
			Sym("table_declaration"),
			Sym("each_row_block"),
			Sym("mock_method"),
			Sym("fake_method"),
			Sym("needs_block"),
		)

		// ---------------------------------------------------------------
		// GLR conflicts for keyword ambiguities
		// (given/when/then/expect/reject/verify/mock/fake/spy can look
		// like identifiers or call expressions until disambiguation)
		// ---------------------------------------------------------------
		AddConflict(g, "_statement", "given_block")
		AddConflict(g, "_statement", "when_block")
		AddConflict(g, "_statement", "then_block")
		AddConflict(g, "_statement", "expect_statement")
		AddConflict(g, "_statement", "reject_statement")
		AddConflict(g, "_statement", "verify_statement")
		AddConflict(g, "_statement", "mock_declaration")
		AddConflict(g, "_statement", "fake_declaration")
		AddConflict(g, "_statement", "spy_declaration")
		AddConflict(g, "_statement", "lifecycle_hook")
		AddConflict(g, "_statement", "mock_method")
		AddConflict(g, "_statement", "fake_method")
		AddConflict(g, "_statement", "needs_block")

		g.EnableLRSplitting = true
	})
}
