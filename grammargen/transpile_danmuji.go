package grammargen

import (
	"fmt"
	"regexp"
	"strings"
	"sync"

	gotreesitter "github.com/odvcencio/gotreesitter"
)

// Package-level cached language to avoid regenerating on every TranspileDanmuji call.
var (
	danmujiLangOnce   sync.Once
	danmujiLangCached *gotreesitter.Language
	danmujiLangErr    error
)

func getDanmujiLanguage() (*gotreesitter.Language, error) {
	danmujiLangOnce.Do(func() {
		danmujiLangCached, danmujiLangErr = GenerateLanguage(DanmujiGrammar())
	})
	return danmujiLangCached, danmujiLangErr
}

// TranspileDanmuji parses a .dmj source file and emits valid Go test code.
func TranspileDanmuji(source []byte) (string, error) {
	lang, err := getDanmujiLanguage()
	if err != nil {
		return "", fmt.Errorf("generate danmuji language: %w", err)
	}

	parser := gotreesitter.NewParser(lang)
	tree, err := parser.Parse(source)
	if err != nil {
		return "", fmt.Errorf("parse: %w", err)
	}

	root := tree.RootNode()
	if root.HasError() {
		return "", fmt.Errorf("parse errors:\n%s", root.SExpr(lang))
	}

	tr := &dmjTranspiler{src: source, lang: lang, testVar: "t"}
	// First pass: collect package-level declarations (mocks)
	tr.collectTopLevel(root)
	// Second pass: emit the code
	output := tr.emit(root)

	// Inject all collected imports
	output = tr.injectImports(output)

	return output, nil
}

// ---------------------------------------------------------------------------
// Transpiler state
// ---------------------------------------------------------------------------

type dmjTranspiler struct {
	src     []byte
	lang    *gotreesitter.Language
	testVar string // "t" for tests, "b" for benchmarks
	// Package-level mock declarations collected during first pass.
	// These are emitted before the test function that contained them.
	mockDecls []string
	// Set of mock_declaration nodes (by start byte) that have been collected
	// so emit() can skip emitting them inline.
	collectedMockStarts map[uint32]bool
	// Collected imports (deduped by package path).
	neededImports map[string]bool
	// Whether we are inside an exec block (for special identifier translation).
	inExecBlock bool
}

// addImport records a package path that should be injected into the import block.
func (t *dmjTranspiler) addImport(pkg string) {
	if t.neededImports == nil {
		t.neededImports = make(map[string]bool)
	}
	t.neededImports[pkg] = true
}

func (t *dmjTranspiler) text(n *gotreesitter.Node) string {
	return string(t.src[n.StartByte():n.EndByte()])
}

func (t *dmjTranspiler) nodeType(n *gotreesitter.Node) string {
	return n.Type(t.lang)
}

func (t *dmjTranspiler) childByField(n *gotreesitter.Node, field string) *gotreesitter.Node {
	return n.ChildByFieldName(field, t.lang)
}

// ---------------------------------------------------------------------------
// First pass: collect mock declarations so they can be emitted at package level
// ---------------------------------------------------------------------------

func (t *dmjTranspiler) collectTopLevel(n *gotreesitter.Node) {
	if t.collectedMockStarts == nil {
		t.collectedMockStarts = make(map[uint32]bool)
	}
	nt := t.nodeType(n)
	if nt == "mock_declaration" {
		t.mockDecls = append(t.mockDecls, t.buildMockDecl(n))
		t.collectedMockStarts[n.StartByte()] = true
		return
	}
	for i := 0; i < int(n.ChildCount()); i++ {
		t.collectTopLevel(n.Child(i))
	}
}

// ---------------------------------------------------------------------------
// Main emit dispatcher
// ---------------------------------------------------------------------------

func (t *dmjTranspiler) emit(n *gotreesitter.Node) string {
	switch t.nodeType(n) {
	case "test_block":
		return t.emitTestBlock(n)
	case "given_block":
		return t.emitBDDBlock(n, "given")
	case "when_block":
		return t.emitBDDBlock(n, "when")
	case "then_block":
		return t.emitBDDBlock(n, "then")
	case "expect_statement":
		return t.emitExpect(n)
	case "reject_statement":
		return t.emitReject(n)
	case "mock_declaration":
		// Already collected at package level — emit a blank (whitespace preserved
		// by emitDefault's gap logic on the parent).
		if t.collectedMockStarts[n.StartByte()] {
			return ""
		}
		return t.text(n)
	case "lifecycle_hook":
		return t.emitLifecycleHook(n)
	case "verify_statement":
		return t.emitVerify(n)
	case "needs_block":
		return t.emitNeedsBlock(n)
	case "load_block":
		return t.emitLoad(n)
	case "load_config":
		return "" // handled by emitLoad
	case "target_block":
		return "" // handled by emitLoad
	case "exec_block":
		return t.emitExec(n)
	case "run_command":
		return "" // handled by emitExec
		default:
		return t.emitDefault(n)
	}
}

// emitDefault preserves whitespace by walking children and copying inter-child gaps.
func (t *dmjTranspiler) emitDefault(n *gotreesitter.Node) string {
	cc := int(n.ChildCount())
	if cc == 0 {
		return t.text(n)
	}
	var b strings.Builder
	prev := n.StartByte()
	for i := 0; i < cc; i++ {
		c := n.Child(i)
		if c.StartByte() > prev {
			b.Write(t.src[prev:c.StartByte()])
		}
		b.WriteString(t.emit(c))
		prev = c.EndByte()
	}
	if n.EndByte() > prev {
		b.Write(t.src[prev:n.EndByte()])
	}
	return b.String()
}

// emitTestBody is the same as emitDefault — recurse into block children.
func (t *dmjTranspiler) emitTestBody(n *gotreesitter.Node) string {
	cc := int(n.ChildCount())
	if cc == 0 {
		return t.text(n)
	}
	var b strings.Builder
	prev := n.StartByte()
	for i := 0; i < cc; i++ {
		c := n.Child(i)
		if c.StartByte() > prev {
			b.Write(t.src[prev:c.StartByte()])
		}
		b.WriteString(t.emit(c))
		prev = c.EndByte()
	}
	if n.EndByte() > prev {
		b.Write(t.src[prev:n.EndByte()])
	}
	return b.String()
}

// ---------------------------------------------------------------------------
// Test block → func TestXxx(t *testing.T)
// ---------------------------------------------------------------------------

var nonAlphaNum = regexp.MustCompile(`[^a-zA-Z0-9]+`)

func sanitizeTestName(name string) string {
	name = strings.Trim(name, "\"'`")
	parts := nonAlphaNum.Split(name, -1)
	for i, p := range parts {
		if len(p) > 0 {
			parts[i] = strings.ToUpper(p[:1]) + p[1:]
		}
	}
	return strings.Join(parts, "")
}

func (t *dmjTranspiler) emitTestBlock(n *gotreesitter.Node) string {
	var b strings.Builder

	// Extract category
	category := ""
	for i := 0; i < int(n.NamedChildCount()); i++ {
		c := n.NamedChild(i)
		if t.nodeType(c) == "test_category" {
			category = t.text(c)
			break
		}
	}

	// Extract name
	nameNode := t.childByField(n, "name")
	name := "Test"
	if nameNode != nil {
		name = sanitizeTestName(t.text(nameNode))
	}

	// Extract tags
	var tags []string
	for i := 0; i < int(n.NamedChildCount()); i++ {
		c := n.NamedChild(i)
		if t.nodeType(c) == "tag_list" {
			for j := 0; j < int(c.NamedChildCount()); j++ {
				tc := c.NamedChild(j)
				if t.nodeType(tc) == "tag" {
					tags = append(tags, t.text(tc))
				}
			}
		}
	}

	// Emit tags as comments
	for _, tag := range tags {
		fmt.Fprintf(&b, "// Tag: %s\n", tag)
	}

	// Build constraint for category
	if category == "integration" || category == "e2e" {
		fmt.Fprintf(&b, "//go:build %s\n\n", category)
	}

	// Emit any collected mock declarations before the function
	if len(t.mockDecls) > 0 {
		for _, md := range t.mockDecls {
			b.WriteString(md)
		}
		// Clear so we don't re-emit for a second test_block
		t.mockDecls = nil
	}

	// Function signature
	fmt.Fprintf(&b, "func Test%s(%s *testing.T) ", name, t.testVar)

	// Emit body block
	for i := 0; i < int(n.NamedChildCount()); i++ {
		c := n.NamedChild(i)
		if t.nodeType(c) == "block" {
			b.WriteString(t.emitTestBody(c))
			break
		}
	}
	b.WriteString("\n")

	return b.String()
}

// ---------------------------------------------------------------------------
// given/when/then → t.Run("description", func(t *testing.T) { ... })
// ---------------------------------------------------------------------------

func (t *dmjTranspiler) emitBDDBlock(n *gotreesitter.Node, keyword string) string {
	desc := t.childByField(n, "description")
	descText := `"` + keyword + `"`
	if desc != nil {
		descText = t.text(desc)
	}

	var b strings.Builder
	fmt.Fprintf(&b, "%s.Run(%s, func(%s *testing.T) ", t.testVar, descText, t.testVar)

	// Find and emit the block
	for i := 0; i < int(n.NamedChildCount()); i++ {
		c := n.NamedChild(i)
		if t.nodeType(c) == "block" {
			b.WriteString(t.emitTestBody(c))
			break
		}
	}
	b.WriteString(")")

	return b.String()
}

// ---------------------------------------------------------------------------
// expect → assertion
//
// CRITICAL: Go's grammar absorbs == / != into binary_expression, so when
// expect's "actual" field is a binary_expression node we must extract
// left/op/right from its children (Child(0), Child(1), Child(2)) and emit
// the appropriate assertion. For bare expect (no binary op), emit truthiness.
// ---------------------------------------------------------------------------

func (t *dmjTranspiler) emitExpect(n *gotreesitter.Node) string {
	actual := t.childByField(n, "actual")
	expected := t.childByField(n, "expected")

	if actual == nil {
		return t.text(n)
	}

	// Check for matchers in the raw text of the node
	nodeText := t.text(n)

	if strings.Contains(nodeText, "is_nil") {
		t.addImport("github.com/stretchr/testify/assert")
		actualText := t.emit(actual)
		return fmt.Sprintf("assert.Nil(%s, %s)", t.testVar, actualText)
	}
	if strings.Contains(nodeText, "not_nil") {
		t.addImport("github.com/stretchr/testify/assert")
		actualText := t.emit(actual)
		return fmt.Sprintf("assert.NotNil(%s, %s)", t.testVar, actualText)
	}
	if strings.Contains(nodeText, "contains") && expected != nil {
		t.addImport("github.com/stretchr/testify/assert")
		actualText := t.emit(actual)
		expectedText := t.emit(expected)
		return fmt.Sprintf("assert.Contains(%s, %s, %s)", t.testVar, actualText, expectedText)
	}

	// If the grammar's explicit expected field is populated, use it directly.
	if expected != nil {
		actualText := t.emit(actual)
		expectedText := t.emit(expected)
		if strings.Contains(nodeText, "!=") {
			// Special case: x != nil
			if expectedText == "nil" {
				t.addImport("github.com/stretchr/testify/assert")
				return fmt.Sprintf("assert.NotNil(%s, %s)", t.testVar, actualText)
			}
			t.addImport("github.com/stretchr/testify/assert")
			return fmt.Sprintf("assert.NotEqual(%s, %s, %s)", t.testVar, expectedText, actualText)
		}
		// Special case: err == nil → require.NoError
		if expectedText == "nil" && strings.HasSuffix(actualText, "err") {
			t.addImport("github.com/stretchr/testify/require")
			return fmt.Sprintf("require.NoError(%s, %s)", t.testVar, actualText)
		}
		// Special case: x == nil
		if expectedText == "nil" {
			t.addImport("github.com/stretchr/testify/assert")
			return fmt.Sprintf("assert.Nil(%s, %s)", t.testVar, actualText)
		}
		t.addImport("github.com/stretchr/testify/assert")
		return fmt.Sprintf("assert.Equal(%s, %s, %s)", t.testVar, expectedText, actualText)
	}

	// If actual is a binary_expression (e.g. Go absorbed "x == 5" into one node),
	// extract left/op/right from its children.
	if t.nodeType(actual) == "binary_expression" && actual.ChildCount() >= 3 {
		left := actual.Child(0)
		op := actual.Child(1)
		right := actual.Child(2)
		lT := t.emit(left)
		opT := t.text(op)
		rT := t.emit(right)
		switch opT {
		case "==":
			// Special case: err == nil → require.NoError
			if rT == "nil" && strings.HasSuffix(lT, "err") {
				t.addImport("github.com/stretchr/testify/require")
				return fmt.Sprintf("require.NoError(%s, %s)", t.testVar, lT)
			}
			// Special case: x == nil
			if rT == "nil" {
				t.addImport("github.com/stretchr/testify/assert")
				return fmt.Sprintf("assert.Nil(%s, %s)", t.testVar, lT)
			}
			t.addImport("github.com/stretchr/testify/assert")
			return fmt.Sprintf("assert.Equal(%s, %s, %s)", t.testVar, rT, lT)
		case "!=":
			if rT == "nil" {
				t.addImport("github.com/stretchr/testify/assert")
				return fmt.Sprintf("assert.NotNil(%s, %s)", t.testVar, lT)
			}
			t.addImport("github.com/stretchr/testify/assert")
			return fmt.Sprintf("assert.NotEqual(%s, %s, %s)", t.testVar, rT, lT)
		case "<":
			t.addImport("github.com/stretchr/testify/assert")
			return fmt.Sprintf("assert.Less(%s, %s, %s)", t.testVar, lT, rT)
		case ">":
			t.addImport("github.com/stretchr/testify/assert")
			return fmt.Sprintf("assert.Greater(%s, %s, %s)", t.testVar, lT, rT)
		case "<=":
			t.addImport("github.com/stretchr/testify/assert")
			return fmt.Sprintf("assert.LessOrEqual(%s, %s, %s)", t.testVar, lT, rT)
		case ">=":
			t.addImport("github.com/stretchr/testify/assert")
			return fmt.Sprintf("assert.GreaterOrEqual(%s, %s, %s)", t.testVar, lT, rT)
		}
	}

	// Bare expect (truthiness check)
	t.addImport("github.com/stretchr/testify/assert")
	actualText := t.emit(actual)
	return fmt.Sprintf("assert.True(%s, %s)", t.testVar, actualText)
}

// ---------------------------------------------------------------------------
// reject → inverse truthiness
// ---------------------------------------------------------------------------

func (t *dmjTranspiler) emitReject(n *gotreesitter.Node) string {
	actual := t.childByField(n, "actual")
	if actual == nil {
		return t.text(n)
	}
	t.addImport("github.com/stretchr/testify/assert")
	actualText := t.emit(actual)
	return fmt.Sprintf("assert.False(%s, %s)", t.testVar, actualText)
}

// ---------------------------------------------------------------------------
// mock → struct with call counters + methods (package-level)
// ---------------------------------------------------------------------------

type mockMethodInfo struct {
	name       string
	params     string
	returnType string
	defaultVal string
}

func (t *dmjTranspiler) parseMockMethod(n *gotreesitter.Node) mockMethodInfo {
	info := mockMethodInfo{}
	if name := t.childByField(n, "name"); name != nil {
		info.name = t.text(name)
	}
	if params := t.childByField(n, "parameters"); params != nil {
		info.params = t.text(params)
	}
	if ret := t.childByField(n, "return_type"); ret != nil {
		info.returnType = t.text(ret)
	}
	if def := t.childByField(n, "default_value"); def != nil {
		info.defaultVal = t.text(def)
	}
	return info
}

// buildMockDecl generates the Go struct type + methods string for a mock_declaration
// node. The result is meant to be emitted at package level.
func (t *dmjTranspiler) buildMockDecl(n *gotreesitter.Node) string {
	nameNode := t.childByField(n, "name")
	if nameNode == nil {
		return t.text(n)
	}
	mockName := t.text(nameNode)
	structName := "mock" + mockName

	var methods []mockMethodInfo
	// Walk block children looking for mock_method nodes.
	// The block may contain them directly or inside a statement_list.
	t.findMockMethods(n, &methods)

	var b strings.Builder

	// Struct with call counters
	fmt.Fprintf(&b, "type %s struct {\n", structName)
	for _, m := range methods {
		fmt.Fprintf(&b, "\t%sCalls int\n", m.name)
		if m.returnType != "" {
			fmt.Fprintf(&b, "\t%sResult %s\n", m.name, m.returnType)
		}
	}
	fmt.Fprintf(&b, "}\n\n")

	// Methods
	for _, m := range methods {
		fmt.Fprintf(&b, "func (m *%s) %s%s", structName, m.name, m.params)
		if m.returnType != "" {
			fmt.Fprintf(&b, " %s", m.returnType)
		}
		fmt.Fprintf(&b, " {\n")
		fmt.Fprintf(&b, "\tm.%sCalls++\n", m.name)
		if m.defaultVal != "" {
			fmt.Fprintf(&b, "\treturn %s\n", m.defaultVal)
		} else if m.returnType != "" {
			fmt.Fprintf(&b, "\treturn m.%sResult\n", m.name)
		}
		fmt.Fprintf(&b, "}\n\n")
	}

	return b.String()
}

// findMockMethods recursively finds mock_method nodes under n.
func (t *dmjTranspiler) findMockMethods(n *gotreesitter.Node, out *[]mockMethodInfo) {
	for i := 0; i < int(n.ChildCount()); i++ {
		c := n.Child(i)
		if t.nodeType(c) == "mock_method" {
			*out = append(*out, t.parseMockMethod(c))
		} else {
			t.findMockMethods(c, out)
		}
	}
}

// ---------------------------------------------------------------------------
// lifecycle hooks
// ---------------------------------------------------------------------------

func (t *dmjTranspiler) emitLifecycleHook(n *gotreesitter.Node) string {
	nodeText := t.text(n)
	isAfter := strings.HasPrefix(strings.TrimSpace(nodeText), "after")

	for i := 0; i < int(n.NamedChildCount()); i++ {
		c := n.NamedChild(i)
		if t.nodeType(c) == "block" {
			blockContent := t.emitTestBody(c)
			if isAfter {
				return fmt.Sprintf("%s.Cleanup(func() %s)", t.testVar, blockContent)
			}
			// before each: inline the block contents (strip outer braces)
			inner := strings.TrimSpace(blockContent)
			if strings.HasPrefix(inner, "{") && strings.HasSuffix(inner, "}") {
				inner = inner[1 : len(inner)-1]
			}
			return inner
		}
	}
	return t.text(n)
}

// ---------------------------------------------------------------------------
// verify → call count assertion
// ---------------------------------------------------------------------------

func (t *dmjTranspiler) emitVerify(n *gotreesitter.Node) string {
	target := t.childByField(n, "target")
	assertion := t.childByField(n, "assertion")
	if target == nil || assertion == nil {
		return t.text(n)
	}
	targetText := t.emit(target)
	assertText := t.text(assertion)

	if strings.Contains(assertText, "not_called") {
		return fmt.Sprintf("if %sCalls != 0 { %s.Errorf(\"expected %%s not called, got %%d calls\", %q, %sCalls) }",
			targetText, t.testVar, targetText, targetText)
	}
	if strings.Contains(assertText, "called") && strings.Contains(assertText, "times") {
		parts := strings.Fields(assertText)
		count := "0"
		for i, p := range parts {
			if p == "called" && i+1 < len(parts) {
				count = parts[i+1]
				break
			}
		}
		return fmt.Sprintf("if %sCalls != %s { %s.Errorf(\"expected %%d calls to %%s, got %%d\", %s, %q, %sCalls) }",
			targetText, count, t.testVar, count, targetText, targetText)
	}
	return t.text(n)
}

// ---------------------------------------------------------------------------
// needs_block → testcontainers setup
// ---------------------------------------------------------------------------

func (t *dmjTranspiler) emitNeedsBlock(n *gotreesitter.Node) string {
	t.addImport("github.com/stretchr/testify/require")

	serviceNode := t.childByField(n, "service")
	nameNode := t.childByField(n, "name")
	if serviceNode == nil || nameNode == nil {
		return t.text(n)
	}
	serviceType := t.text(serviceNode)
	varName := t.text(nameNode)

	tv := t.testVar
	var b strings.Builder
	switch serviceType {
	case "postgres":
		fmt.Fprintf(&b, "%sContainer, err := postgres.Run(ctx, \"postgres:15\",\n"+
			"\tpostgres.WithDatabase(\"test\"),\n"+
			"\ttestcontainers.WithWaitStrategy(wait.ForListeningPort(\"5432/tcp\")),\n"+
			")\n"+
			"require.NoError(%s, err)\n"+
			"%s.Cleanup(func() { %sContainer.Terminate(ctx) })\n"+
			"%sURL, err := %sContainer.ConnectionString(ctx)\n"+
			"require.NoError(%s, err)", varName, tv, tv, varName, varName, varName, tv)
	case "redis":
		fmt.Fprintf(&b, "%sContainer, err := redis.Run(ctx, \"redis:7\")\n"+
			"require.NoError(%s, err)\n"+
			"%s.Cleanup(func() { %sContainer.Terminate(ctx) })\n"+
			"%sURL, err := %sContainer.ConnectionString(ctx)\n"+
			"require.NoError(%s, err)", varName, tv, tv, varName, varName, varName, tv)
	case "mysql":
		fmt.Fprintf(&b, "%sContainer, err := mysql.Run(ctx, \"mysql:8\")\n"+
			"require.NoError(%s, err)\n"+
			"%s.Cleanup(func() { %sContainer.Terminate(ctx) })\n"+
			"%sURL, err := %sContainer.ConnectionString(ctx)\n"+
			"require.NoError(%s, err)", varName, tv, tv, varName, varName, varName, tv)
	case "kafka":
		fmt.Fprintf(&b, "%sContainer, err := kafka.Run(ctx, \"confluentinc/confluent-local:7.5.0\")\n"+
			"require.NoError(%s, err)\n"+
			"%s.Cleanup(func() { %sContainer.Terminate(ctx) })\n"+
			"%sBrokers, err := %sContainer.Brokers(ctx)\n"+
			"require.NoError(%s, err)", varName, tv, tv, varName, varName, varName, tv)
	case "mongo":
		fmt.Fprintf(&b, "%sContainer, err := mongodb.Run(ctx, \"mongo:7\")\n"+
			"require.NoError(%s, err)\n"+
			"%s.Cleanup(func() { %sContainer.Terminate(ctx) })\n"+
			"%sURI := %sContainer.GetConnectionString()", varName, tv, tv, varName, varName, varName)
	case "rabbitmq":
		fmt.Fprintf(&b, "%sContainer, err := rabbitmq.Run(ctx, \"rabbitmq:3-management\")\n"+
			"require.NoError(%s, err)\n"+
			"%s.Cleanup(func() { %sContainer.Terminate(ctx) })\n"+
			"%sURL, err := %sContainer.AmqpURL(ctx)\n"+
			"require.NoError(%s, err)", varName, tv, tv, varName, varName, varName, tv)
	case "nats":
		fmt.Fprintf(&b, "%sContainer, err := nats.Run(ctx, \"nats:2\")\n"+
			"require.NoError(%s, err)\n"+
			"%s.Cleanup(func() { %sContainer.Terminate(ctx) })\n"+
			"%sURL, err := %sContainer.ConnectionString(ctx)\n"+
			"require.NoError(%s, err)", varName, tv, tv, varName, varName, varName, tv)
	case "container":
		fmt.Fprintf(&b, "%sReq := testcontainers.ContainerRequest{\n"+
			"\tImage:        \"alpine:latest\",\n"+
			"\tExposedPorts: []string{},\n"+
			"}\n"+
			"%sContainer, err := testcontainers.GenericContainer(ctx, testcontainers.GenericContainerRequest{\n"+
			"\tContainerRequest: %sReq,\n"+
			"\tStarted:          true,\n"+
			"})\n"+
			"require.NoError(%s, err)\n"+
			"%s.Cleanup(func() { %sContainer.Terminate(ctx) })", varName, varName, varName, tv, tv, varName)
	default:
		return fmt.Sprintf("// unsupported needs service type: %s", serviceType)
	}

	return b.String()
}

// ---------------------------------------------------------------------------
// load_block → func TestLoadXxx(t *testing.T) with vegeta attack
// ---------------------------------------------------------------------------

func (t *dmjTranspiler) emitLoad(n *gotreesitter.Node) string {
	t.addImport("time")
	t.addImport("github.com/tsenart/vegeta/v12/lib")

	// Extract name
	nameNode := t.childByField(n, "name")
	name := "Load"
	if nameNode != nil {
		name = sanitizeTestName(t.text(nameNode))
	}

	// Walk the body block's children for load_config, target_block, then_block
	bodyNode := t.childByField(n, "body")
	if bodyNode == nil {
		return t.text(n)
	}

	rate := "10"
	duration := "5"
	method := "GET"
	url := `"http://localhost"`
	var thenBlocks []*gotreesitter.Node

	t.walkChildren(bodyNode, func(child *gotreesitter.Node) {
		switch t.nodeType(child) {
		case "load_config":
			configText := t.text(child)
			// Extract the value (everything after the keyword)
			if strings.HasPrefix(configText, "rate") {
				val := strings.TrimSpace(strings.TrimPrefix(configText, "rate"))
				if val != "" {
					rate = val
				}
			} else if strings.HasPrefix(configText, "duration") {
				val := strings.TrimSpace(strings.TrimPrefix(configText, "duration"))
				if val != "" {
					duration = val
				}
			}
		case "target_block":
			if m := t.childByField(child, "method"); m != nil {
				method = strings.ToUpper(t.text(m))
			}
			if u := t.childByField(child, "url"); u != nil {
				url = t.text(u)
			}
		case "then_block":
			thenBlocks = append(thenBlocks, child)
		}
	})

	var b strings.Builder

	// Build constraint
	fmt.Fprintf(&b, "//go:build e2e\n\n")

	// Function signature
	fmt.Fprintf(&b, "func TestLoad%s(t *testing.T) {\n", name)

	// Vegeta setup
	fmt.Fprintf(&b, "\trate := vegeta.Rate{Freq: %s, Per: time.Second}\n", rate)
	fmt.Fprintf(&b, "\tduration := %s * time.Second\n", duration)
	fmt.Fprintf(&b, "\ttargeter := vegeta.NewStaticTargeter(vegeta.Target{\n")
	fmt.Fprintf(&b, "\t\tMethod: %q,\n", method)
	fmt.Fprintf(&b, "\t\tURL:    %s,\n", url)
	fmt.Fprintf(&b, "\t})\n")
	fmt.Fprintf(&b, "\tattacker := vegeta.NewAttacker()\n")
	fmt.Fprintf(&b, "\tvar metrics vegeta.Metrics\n")
	fmt.Fprintf(&b, "\tfor res := range attacker.Attack(targeter, rate, duration, %q) {\n", name)
	fmt.Fprintf(&b, "\t\tmetrics.Add(res)\n")
	fmt.Fprintf(&b, "\t}\n")
	fmt.Fprintf(&b, "\tmetrics.Close()\n")

	// Emit then blocks
	for _, tb := range thenBlocks {
		b.WriteString("\t")
		b.WriteString(t.emitBDDBlock(tb, "then"))
		b.WriteString("\n")
	}

	fmt.Fprintf(&b, "}\n")

	return b.String()
}

// ---------------------------------------------------------------------------
// injectImports adds all collected import paths into the existing import block
// ---------------------------------------------------------------------------

func (t *dmjTranspiler) injectImports(code string) string {
	if len(t.neededImports) == 0 {
		return code
	}

	// Build sorted list of import paths for deterministic output.
	var imports []string
	for pkg := range t.neededImports {
		imports = append(imports, fmt.Sprintf("%q", pkg))
	}
	// Sort for deterministic output
	sortImports(imports)

	importBlock := "\n\t" + strings.Join(imports, "\n\t")

	// Try to find an existing import block and append inside it.
	// Look for the closing paren of an import(...) block.
	if idx := strings.Index(code, "import ("); idx >= 0 {
		// Find the matching closing paren
		closeIdx := strings.Index(code[idx:], ")")
		if closeIdx >= 0 {
			insertAt := idx + closeIdx
			return code[:insertAt] + importBlock + "\n" + code[insertAt:]
		}
	}

	// If there's a single import "..." line, convert to block form
	if idx := strings.Index(code, "import \""); idx >= 0 {
		endIdx := strings.Index(code[idx:], "\n")
		if endIdx >= 0 {
			origImport := code[idx : idx+endIdx]
			// Extract the import path from: import "path"
			path := strings.TrimPrefix(origImport, "import ")
			newBlock := "import (\n\t" + path + importBlock + "\n)"
			return code[:idx] + newBlock + code[idx+endIdx:]
		}
	}

	return code
}

// sortImports sorts import strings lexicographically.
func sortImports(imports []string) {
	for i := 1; i < len(imports); i++ {
		for j := i; j > 0 && imports[j] < imports[j-1]; j-- {
			imports[j], imports[j-1] = imports[j-1], imports[j]
		}
	}
}

// ---------------------------------------------------------------------------
// exec_block → t.Run with os/exec command execution
// ---------------------------------------------------------------------------

func (t *dmjTranspiler) emitExec(n *gotreesitter.Node) string {
	t.addImport("bytes")
	t.addImport("os/exec")

	nameNode := t.childByField(n, "name")
	name := "exec"
	if nameNode != nil {
		name = strings.Trim(t.text(nameNode), "\"`'")
	}

	// Find the body block
	bodyNode := t.childByField(n, "body")
	if bodyNode == nil {
		return t.text(n)
	}

	// Collect run_command nodes and other statements from the body
	type runCmd struct {
		command string
	}
	var runs []runCmd
	var otherStatements []*gotreesitter.Node

	t.walkChildren(bodyNode, func(child *gotreesitter.Node) {
		switch t.nodeType(child) {
		case "run_command":
			cmdNode := t.childByField(child, "command")
			if cmdNode != nil {
				runs = append(runs, runCmd{command: t.text(cmdNode)})
			}
		case "expect_statement":
			otherStatements = append(otherStatements, child)
		}
	})

	var b strings.Builder
	fmt.Fprintf(&b, "%s.Run(%q, func(%s *testing.T) {\n", t.testVar, name, t.testVar)

	// For each run command, emit the exec boilerplate
	for _, r := range runs {
		fmt.Fprintf(&b, "\tvar stdout, stderr bytes.Buffer\n")
		fmt.Fprintf(&b, "\tcmd := exec.Command(\"sh\", \"-c\", %s)\n", r.command)
		fmt.Fprintf(&b, "\tcmd.Stdout = &stdout\n")
		fmt.Fprintf(&b, "\tcmd.Stderr = &stderr\n")
		fmt.Fprintf(&b, "\terr := cmd.Run()\n")
		fmt.Fprintf(&b, "\texitCode := 0\n")
		fmt.Fprintf(&b, "\tif err != nil {\n")
		fmt.Fprintf(&b, "\t\tif exitErr, ok := err.(*exec.ExitError); ok {\n")
		fmt.Fprintf(&b, "\t\t\texitCode = exitErr.ExitCode()\n")
		fmt.Fprintf(&b, "\t\t} else {\n")
		fmt.Fprintf(&b, "\t\t\texitCode = -1\n")
		fmt.Fprintf(&b, "\t\t}\n")
		fmt.Fprintf(&b, "\t}\n")
		fmt.Fprintf(&b, "\t_ = exitCode // used by expect assertions\n")
	}

	// Emit expect statements with exec identifier translation
	oldInExec := t.inExecBlock
	t.inExecBlock = true
	for _, stmt := range otherStatements {
		b.WriteString("\t")
		b.WriteString(t.emitExecExpect(stmt))
		b.WriteString("\n")
	}
	t.inExecBlock = oldInExec

	fmt.Fprintf(&b, "})")
	return b.String()
}

// walkChildren calls fn for each named child (recursing into statement_list/block).
func (t *dmjTranspiler) walkChildren(n *gotreesitter.Node, fn func(*gotreesitter.Node)) {
	for i := 0; i < int(n.ChildCount()); i++ {
		child := n.Child(i)
		nt := t.nodeType(child)
		switch nt {
		case "block", "statement_list":
			t.walkChildren(child, fn)
		default:
			if child.IsNamed() {
				fn(child)
			}
		}
	}
}

// emitExecExpect translates expect statements inside exec blocks,
// replacing exec-specific identifiers with their Go equivalents.
func (t *dmjTranspiler) emitExecExpect(n *gotreesitter.Node) string {
	nodeText := t.text(n)

	// Handle "expect stdout contains X"
	if strings.Contains(nodeText, "stdout") && strings.Contains(nodeText, "contains") {
		// Extract the expected value after "contains"
		expected := t.childByField(n, "expected")
		if expected != nil {
			t.addImport("github.com/stretchr/testify/assert")
			return fmt.Sprintf("assert.Contains(%s, stdout.String(), %s)", t.testVar, t.emit(expected))
		}
	}

	// Handle "expect stderr contains X"
	if strings.Contains(nodeText, "stderr") && strings.Contains(nodeText, "contains") {
		expected := t.childByField(n, "expected")
		if expected != nil {
			t.addImport("github.com/stretchr/testify/assert")
			return fmt.Sprintf("assert.Contains(%s, stderr.String(), %s)", t.testVar, t.emit(expected))
		}
	}

	// Handle "expect exit_code == 0" — the grammar absorbs this as binary_expression
	actual := t.childByField(n, "actual")
	if actual != nil {
		actualText := t.text(actual)
		// Check if it's a binary expression with exit_code
		if t.nodeType(actual) == "binary_expression" && actual.ChildCount() >= 3 {
			left := actual.Child(0)
			op := actual.Child(1)
			right := actual.Child(2)
			lT := t.translateExecIdent(t.text(left))
			opT := t.text(op)
			rT := t.emit(right)
			t.addImport("github.com/stretchr/testify/assert")
			switch opT {
			case "==":
				return fmt.Sprintf("assert.Equal(%s, %s, %s)", t.testVar, rT, lT)
			case "!=":
				return fmt.Sprintf("assert.NotEqual(%s, %s, %s)", t.testVar, rT, lT)
			}
		}
		// Bare identifier like "expect exit_code"
		if strings.Contains(actualText, "exit_code") || strings.Contains(actualText, "stdout") || strings.Contains(actualText, "stderr") {
			translated := t.translateExecIdent(actualText)
			t.addImport("github.com/stretchr/testify/assert")
			return fmt.Sprintf("assert.True(%s, %s)", t.testVar, translated)
		}
	}

	// Fall through to normal expect emission
	return t.emitExpect(n)
}

// translateExecIdent maps exec-specific identifiers to Go variable names.
func (t *dmjTranspiler) translateExecIdent(ident string) string {
	switch strings.TrimSpace(ident) {
	case "exit_code":
		return "exitCode"
	case "stdout":
		return "stdout.String()"
	case "stderr":
		return "stderr.String()"
	default:
		return ident
	}
}
