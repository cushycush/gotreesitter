package grammargen

import "testing"

func TestSQLImportedCorpusSnippetParity(t *testing.T) {
	cases := []struct {
		name string
		src  string
	}{
		{
			name: "select_literal_list",
			src:  "SELECT 1, 2;\n",
		},
		{
			name: "select_identifier_list",
			src:  "SELECT a, b;\n",
		},
		{
			name: "select_parenthesized_boolean",
			src:  "SELECT (TRUE);\n",
		},
		{
			name: "select_dollar_quoted_string",
			src:  "SELECT $$hey$$;\n",
		},
		{
			name: "insert_multiple_values",
			src:  "INSERT INTO table1 VALUES (1, 'a'), (2, 'b');\n",
		},
	}

	assertImportedDeepParityCases(t, "sql", cases)
}
