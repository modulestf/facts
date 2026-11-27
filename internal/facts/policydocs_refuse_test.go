package facts

import (
	"strings"
	"testing"
)

// One case per refusal of the content reader: each shape must leave the
// document's content undecidable, never read part of it.
func TestPolicyDocsContentRefusals(t *testing.T) {
	data := func(stmt string) string {
		return "data \"aws_iam_policy_document\" \"x\" {\n" + stmt + "}\n"
	}
	statement := func(body string) string {
		return data("  statement {\n" + body + "  }\n")
	}
	ok := "    actions   = [\"s3:GetObject\"]\n    resources = [\"*\"]\n"
	enc := func(stmt string) string {
		return "resource \"aws_iam_policy\" \"p\" {\n  policy = jsonencode({\n    Version   = \"2012-10-17\"\n    Statement = " + stmt + "\n  })\n}\n"
	}
	encStmt := func(fields string) string {
		return enc("[{\n      " + fields + "\n    }]")
	}
	text := func(json string) string {
		return "resource \"aws_iam_policy\" \"p\" {\n  policy = <<-EOT\n" + json + "\n  EOT\n}\n"
	}
	many := func(item string, n int) string {
		return strings.TrimSuffix(strings.Repeat(item+", ", n), ", ")
	}
	nest := func(n int) string {
		return strings.Repeat("{a = ", n) + "1" + strings.Repeat("}", n)
	}
	textNest := func(n int) string {
		return strings.Repeat(`{"a": `, n) + "1" + strings.Repeat("}", n)
	}
	const dataAddr, encAddr = "data.aws_iam_policy_document.x", "aws_iam_policy.p.policy"
	cases := map[string][2]string{ // name -> file, address
		"a reference with a null index":           {statement("    actions   = [local.a[null]]\n    resources = [\"*\"]\n") + "locals {\n  a = {}\n}\n", dataAddr},
		"a template reference with a null index":  {statement("    actions   = [\"${local.a[null]}\"]\n    resources = [\"*\"]\n") + "locals {\n  a = {}\n}\n", dataAddr},
		"a reference with a bool index":           {statement("    actions   = [local.a[true]]\n    resources = [\"*\"]\n") + "locals {\n  a = {}\n}\n", dataAddr},
		"a null list value":                       {statement("    actions   = [null]\n    resources = [\"*\"]\n"), dataAddr},
		"a list over the value cap":               {statement("    actions   = [" + many(`"s3:GetObject"`, maxPolicyValues+1) + "]\n    resources = [\"*\"]\n"), dataAddr},
		"an interpolated effect":                  {statement("    effect    = \"${var.e}\"\n"+ok) + "variable \"e\" {}\n", dataAddr},
		"an effect that is a list":                {statement("    effect    = [\"Allow\"]\n" + ok), dataAddr},
		"an effect other than Allow or Deny":      {statement("    effect    = \"Maybe\"\n" + ok), dataAddr},
		"override_policy_documents alone":         {data("  override_policy_documents = []\n"), dataAddr},
		"override_json alone":                     {data("  override_json = \"{}\"\n"), dataAddr},
		"a version that is a list":                {data("  version = [\"2012-10-17\"]\n"), dataAddr},
		"a policy_id that is a list":              {data("  policy_id = [\"x\"]\n"), dataAddr},
		"statements over the cap":                 {data(strings.Repeat("  statement {\n"+ok+"  }\n", maxPolicyStatements+1)), dataAddr},
		"a statement with a label":                {data("  statement \"l\" {\n" + ok + "  }\n"), dataAddr},
		"principal identifiers not a list":        {statement(ok + "    principals {\n      type        = \"AWS\"\n      identifiers = \"x\"\n    }\n"), dataAddr},
		"an unknown principal argument":           {statement(ok + "    principals {\n      type        = \"AWS\"\n      identifiers = [\"x\"]\n      extra       = 1\n    }\n"), dataAddr},
		"principals without identifiers":          {statement(ok + "    principals {\n      type = \"AWS\"\n    }\n"), dataAddr},
		"principals without a type":               {statement(ok + "    principals {\n      identifiers = [\"x\"]\n    }\n"), dataAddr},
		"principals with a nested block":          {statement(ok + "    principals {\n      type        = \"AWS\"\n      identifiers = [\"x\"]\n      inner {}\n    }\n"), dataAddr},
		"condition values not a list":             {statement(ok + "    condition {\n      test     = \"StringEquals\"\n      variable = \"k\"\n      values   = \"x\"\n    }\n"), dataAddr},
		"a condition without values":              {statement(ok + "    condition {\n      test     = \"StringEquals\"\n      variable = \"k\"\n    }\n"), dataAddr},
		"a condition without a test":              {statement(ok + "    condition {\n      variable = \"k\"\n      values   = [\"x\"]\n    }\n"), dataAddr},
		"a condition without a variable":          {statement(ok + "    condition {\n      test   = \"StringEquals\"\n      values = [\"x\"]\n    }\n"), dataAddr},
		"a condition with a nested block":         {statement(ok + "    condition {\n      test     = \"StringEquals\"\n      variable = \"k\"\n      values   = [\"x\"]\n      inner {}\n    }\n"), dataAddr},
		"jsonencode nested too deep":              {encStmt("Effect = \"Allow\", Action = \"*\", Resource = \"*\", Condition = " + nest(20)), encAddr},
		"jsonencode key that is not a name":       {encStmt("(var.k) = \"Allow\", Effect = \"Allow\", Action = \"*\"") + "variable \"k\" {}\n", encAddr},
		"jsonencode key twice":                    {encStmt("Effect = \"Allow\", Action = \"s3:GetObject\", Action = \"*\""), encAddr},
		"jsonencode value from a function":        {encStmt("Effect = \"Allow\", Action = upper(\"x\")"), encAddr},
		"jsonencode list over the value cap":      {encStmt("Effect = \"Allow\", Action = [" + many(`"s3:GetObject"`, maxPolicyValues+1) + "]"), encAddr},
		"jsonencode list element from a function": {encStmt("Effect = \"Allow\", Action = [upper(\"x\")]"), encAddr},
		"jsonencode effect from a function":       {encStmt("Effect = upper(\"Allow\"), Action = \"*\""), encAddr},
		"jsonencode effect that is a list":        {encStmt("Effect = [\"Allow\"], Action = \"*\""), encAddr},
		"jsonencode effect interpolated":          {encStmt("Effect = \"${var.e}\", Action = \"*\"") + "variable \"e\" {}\n", encAddr},
		"jsonencode effect other than Allow":      {encStmt("Effect = \"Maybe\", Action = \"*\""), encAddr},
		"jsonencode statement with no effect":     {encStmt("Action = \"*\", Resource = \"*\""), encAddr},
		"jsonencode version that is a list":       {"resource \"aws_iam_policy\" \"p\" {\n  policy = jsonencode({\n    Version   = [\"x\"]\n    Statement = []\n  })\n}\n", encAddr},
		"jsonencode statements over the cap":      {enc("[" + many(`{Effect = "Allow", Action = "*"}`, maxPolicyStatements+1) + "]"), encAddr},
		"jsonencode statement that is a string":   {enc("[\"x\"]"), encAddr},
		"jsonencode statement that is a number":   {enc("1"), encAddr},
		"jsonencode sid that is a list":           {encStmt("Sid = [\"x\"], Effect = \"Allow\", Action = \"*\""), encAddr},
		"jsonencode action that is an object":     {encStmt("Effect = \"Allow\", Action = {a = \"x\"}"), encAddr},
		"jsonencode action list of lists":         {encStmt("Effect = \"Allow\", Action = [[\"x\"]]"), encAddr},
		"jsonencode principal interpolated star":  {encStmt("Effect = \"Allow\", Action = \"*\", Principal = \"${var.p}\"") + "variable \"p\" {}\n", encAddr},
		"jsonencode principal a plain string":     {encStmt("Effect = \"Allow\", Action = \"*\", Principal = \"x\""), encAddr},
		"jsonencode principal value an object":    {encStmt("Effect = \"Allow\", Action = \"*\", Principal = {AWS = {a = \"x\"}}"), encAddr},
		"jsonencode condition a string":           {encStmt("Effect = \"Allow\", Action = \"*\", Condition = \"x\""), encAddr},
		"jsonencode condition operator a string":  {encStmt("Effect = \"Allow\", Action = \"*\", Condition = {StringEquals = \"x\"}"), encAddr},
		"jsonencode condition value an object":    {encStmt("Effect = \"Allow\", Action = \"*\", Condition = {StringEquals = {k = {a = \"x\"}}}"), encAddr},
		"JSON text that does not parse":           {text(`{"Statement": oops}`), encAddr},
		"JSON text with text after it":            {text(`{"Statement": []} {}`), encAddr},
		"JSON text nested too deep":               {text(`{"Statement": [{"Effect": "Allow", "Action": "*", "Condition": ` + textNest(20) + `}]}`), encAddr},
		"JSON text cut short":                     {text(`{"Statement": `), encAddr},
		"JSON text with a bad key":                {text(`{"Statement" []}`), encAddr},
		"JSON text key twice":                     {text(`{"Statement": [{"Effect": "Allow", "Action": "s3:GetObject", "Action": "*"}]}`), encAddr},
		"JSON text bad value in an object":        {text(`{"Statement": [{"Effect": "Allow", "Action": ]}]}`), encAddr},
		"JSON text object not closed":             {text(`{"Statement": []`), encAddr},
		"JSON text list over the value cap":       {text(`{"Statement": [{"Effect": "Allow", "Action": [` + many(`"s3:GetObject"`, maxPolicyValues+1) + `]}]}`), encAddr},
		"JSON text bad value in a list":           {text(`{"Statement": [{"Effect": "Allow", "Action": [,]}]}`), encAddr},
		"JSON text list not closed":               {text(`{"Statement": [{"Effect": "Allow", "Action": "*"}`), encAddr},
	}
	for name, c := range cases {
		t.Run(name, func(t *testing.T) {
			r, err := PolicyDocs(PolicyDocsOptions{Head: writeTree(t, map[string]string{"main.tf": c[0]}), Limits: DefaultLimits})
			if err != nil {
				t.Fatal(err)
			}
			for _, d := range r.Head.Directories {
				if d.Undecidable != nil {
					t.Fatalf("directory undecidable, the case tests nothing: %+v", d.Undecidable)
				}
			}
			if d := policyDoc(t, r.Head, ".", c[1]); d.Content != nil || d.ContentUndecidable == nil {
				t.Errorf("content read: %s", canonJSON(d.Content))
			}
		})
	}
	t.Run("a number reads as its text", func(t *testing.T) {
		r, err := PolicyDocs(PolicyDocsOptions{Head: writeTree(t, map[string]string{"main.tf": statement("    actions   = [1]\n    resources = [\"*\"]\n")}), Limits: DefaultLimits})
		if err != nil {
			t.Fatal(err)
		}
		if d := policyDoc(t, r.Head, ".", dataAddr); d.Content == nil || values(d.Content.Statements[0].Actions) != `["1"]` {
			t.Errorf("content: %+v %+v", d.Content, d.ContentUndecidable)
		}
	})
}
