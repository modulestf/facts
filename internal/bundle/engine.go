package bundle

import (
	"errors"
	"fmt"
	"io"
	"os"
	"path"
	"slices"
	"strings"
)

// EngineText maps a rule id to its section in references/engine/: the lines
// from its "## `<rule_id>`" heading to the next level-2 heading.
type EngineText map[string]string

// LoadEngine reads every .md file of the engine directory, through an
// os.Root so no read leaves it, and indexes its rule sections. A link, a
// special file, a file over the cap or a rule with two sections is an error.
func LoadEngine(dir string) (EngineText, error) {
	root, err := os.OpenRoot(dir)
	if err != nil {
		return nil, fmt.Errorf("engine text: %w", err)
	}
	defer root.Close()
	d, err := root.Open(".")
	if err != nil {
		return nil, fmt.Errorf("engine text: %w", err)
	}
	defer d.Close()
	names, err := d.Readdirnames(maxEngineFiles + 1)
	if err != nil && !errors.Is(err, io.EOF) { // io.EOF: an empty directory
		return nil, fmt.Errorf("engine text: %w", err)
	}
	if len(names) > maxEngineFiles {
		return nil, fmt.Errorf("engine text: more than %d entries", maxEngineFiles)
	}
	slices.Sort(names)
	out := EngineText{}
	for _, n := range names {
		if path.Ext(n) != ".md" {
			continue
		}
		data, err := readRootFile(root, n)
		if err != nil {
			return nil, fmt.Errorf("engine text: %w", err)
		}
		if err := out.add(n, string(data)); err != nil {
			return nil, err
		}
	}
	return out, nil
}

// readRootFile reads one regular file of root, not a link, under the cap.
func readRootFile(root *os.Root, name string) ([]byte, error) {
	if st, err := root.Lstat(name); err != nil || !st.Mode().IsRegular() {
		return nil, fmt.Errorf("%s is not a regular file", name)
	}
	f, err := root.Open(name)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	return readCapped(f, name, maxEngineBytes)
}

// add indexes the rule sections of one file.
func (e EngineText) add(file, doc string) error {
	lines := strings.Split(strings.ReplaceAll(doc, "\r\n", "\n"), "\n")
	id, start := "", 0
	flush := func(end int) error {
		if id == "" {
			return nil
		}
		if _, dup := e[id]; dup {
			return fmt.Errorf("engine text: %s: rule %q has a second section", file, id)
		}
		e[id] = strings.TrimRight(strings.Join(lines[start:end], "\n"), "\n ") + "\n"
		return nil
	}
	for i, l := range lines {
		if !strings.HasPrefix(l, "## ") {
			continue
		}
		if err := flush(i); err != nil {
			return err
		}
		id, start = "", i
		if h := strings.TrimPrefix(l, "## "); len(h) > 2 && h[0] == '`' && h[len(h)-1] == '`' {
			id = h[1 : len(h)-1]
		}
	}
	return flush(len(lines))
}

// notEvaluated is one (rule, area) the bundle leaves not_evaluated.
type notEvaluated struct {
	rule, area, reason string
}

// schemaNote is added to an injection whose reason is that the provider
// schema is unavailable.
const schemaNote = "The provider schema is unavailable here. Never supply the missing schema from memory: " +
	"take the fact from a source Rule 2 names, or report review.check-not-run."

// injection renders the injected rule text: per rule, the areas it was not
// evaluated in with their reasons, then its section from the engine text.
// Area paths come from the tree and are written as JSON strings, so they
// cannot start a line of their own. A rule with no section is an error.
func injection(entries []notEvaluated, text EngineText) ([]byte, error) {
	if len(entries) == 0 {
		return []byte{}, nil
	}
	var b strings.Builder
	b.WriteString("# Injected Rules\n\nThe facts bundle leaves each rule below not_evaluated in the areas listed. " +
		"Judge it there from the files, under its text below.\n")
	var missing []error
	for i, e := range entries {
		if i == 0 || entries[i-1].rule != e.rule {
			if i > 0 {
				b.WriteString("\n" + text[entries[i-1].rule])
			}
			if _, ok := text[e.rule]; !ok {
				missing = append(missing, fmt.Errorf("rule %q has no engine text", e.rule))
			}
			fmt.Fprintf(&b, "\nRule `%s` is not_evaluated in:\n", e.rule)
		}
		fmt.Fprintf(&b, "- area %s, reason %s", quote(e.area), e.reason)
		if e.reason == "schema_unavailable" {
			b.WriteString(". " + schemaNote)
		}
		b.WriteString("\n")
	}
	b.WriteString("\n" + text[entries[len(entries)-1].rule])
	if len(missing) > 0 {
		return nil, errors.Join(missing...)
	}
	return []byte(b.String()), nil
}
