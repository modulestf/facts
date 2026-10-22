package main

import (
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"

	factsmerge "github.com/modulestf/facts/internal/merge"
	"github.com/modulestf/facts/internal/registry"
)

// Exit codes of merge check, beside exitError.
const (
	exitReplyValid  = 0
	exitReplyRepair = 1
)

// maxRegistryFileBytes bounds the rule-facts.md the merge reads.
const maxRegistryFileBytes = 1 << 20

const mergeUsage = `usage:
  mtf-facts merge check <bundle> <reply>
        print {"result":"valid"} and exit 0, or {"result":"repair","reasons":[...]}
        and exit 1 when the reply needs the repair turn
  mtf-facts merge final --registry <rule-facts.md> [--report <lint.json>]
                        [--templates <file>] <bundle> <reply> [<repair-reply>]
        print the merged findings; the repair reply is read only when the
        first reply is invalid, and must then be given (an empty file when
        the repair turn returned nothing)
`

// merge checks a lead reply or merges it with the facts into the final
// findings.
func merge(args []string, stdout, stderr io.Writer) int {
	if len(args) > 0 {
		switch args[0] {
		case "check":
			return mergeCheck(args[1:], stdout, stderr)
		case "final":
			return mergeFinal(args[1:], stdout, stderr)
		}
	}
	fmt.Fprint(stderr, mergeUsage)
	return exitError
}

// checkResult is what merge check prints.
type checkResult struct {
	Result  string   `json:"result"`
	Reasons []string `json:"reasons,omitempty"`
}

func mergeCheck(args []string, stdout, stderr io.Writer) int {
	if len(args) != 2 {
		fmt.Fprint(stderr, mergeUsage)
		return exitError
	}
	b, err := mergeBundle(args[0])
	if err != nil {
		fmt.Fprintln(stderr, "mtf-facts:", err)
		return exitError
	}
	reply, err := mergeReadFile(args[1], factsmerge.MaxReplyBytes)
	if err != nil {
		fmt.Fprintln(stderr, "mtf-facts:", err)
		return exitError
	}
	res, code := checkResult{Result: "valid"}, exitReplyValid
	if reasons := factsmerge.Problems(b, factsmerge.ParseReply(reply)); len(reasons) > 0 {
		res, code = checkResult{Result: "repair", Reasons: reasons}, exitReplyRepair
	}
	out, err := json.Marshal(res)
	if err != nil {
		fmt.Fprintln(stderr, "mtf-facts:", err)
		return exitError
	}
	if _, err := fmt.Fprintf(stdout, "%s\n", out); err != nil {
		return exitError
	}
	return code
}

func mergeFinal(args []string, stdout, stderr io.Writer) int {
	fs := flag.NewFlagSet("merge final", flag.ContinueOnError)
	fs.SetOutput(stderr)
	regFile := fs.String("registry", "", "rule-facts.md, the rule registry")
	reportFile := fs.String("report", "", "lint report whose index derives the subjects of lead findings")
	templatesFile := fs.String("templates", "", "per-rule host templates")
	var files []string
	for rest := args; ; {
		if err := fs.Parse(rest); err != nil {
			return exitError
		}
		if fs.NArg() == 0 {
			break
		}
		files, rest = append(files, fs.Arg(0)), fs.Args()[1:]
	}
	if *regFile == "" || len(files) < 2 || len(files) > 3 {
		fmt.Fprint(stderr, mergeUsage)
		return exitError
	}
	out, err := mergeFinalWork(*regFile, *reportFile, *templatesFile, files)
	if err != nil {
		fmt.Fprintln(stderr, "mtf-facts:", err)
		return exitError
	}
	if _, err := stdout.Write(out); err != nil {
		return exitError
	}
	return 0
}

// mergeFinalWork reads the inputs and returns the merged findings as JSON.
func mergeFinalWork(regFile, reportFile, templatesFile string, files []string) ([]byte, error) {
	in := factsmerge.Inputs{}
	data, err := mergeReadFile(regFile, maxRegistryFileBytes)
	if err != nil {
		return nil, err
	}
	if len(data) > maxRegistryFileBytes {
		return nil, fmt.Errorf("registry is larger than %d bytes", maxRegistryFileBytes)
	}
	if in.Registry, err = registry.Parse(string(data)); err != nil {
		return nil, fmt.Errorf("registry: %w", err)
	}
	if in.Bundle, err = mergeBundle(files[0]); err != nil {
		return nil, err
	}
	if reportFile != "" {
		if data, err = mergeReadFile(reportFile, factsmerge.MaxReportBytes); err != nil {
			return nil, err
		}
		if in.Index, err = factsmerge.ParseIndex(data); err != nil {
			return nil, err
		}
	}
	if templatesFile != "" {
		if data, err = mergeReadFile(templatesFile, factsmerge.MaxTemplateBytes); err != nil {
			return nil, err
		}
		if in.Templates, err = factsmerge.ParseTemplates(data); err != nil {
			return nil, err
		}
	}
	first, err := mergeReadFile(files[1], factsmerge.MaxReplyBytes)
	if err != nil {
		return nil, err
	}
	var repair []byte
	if len(files) == 3 {
		if repair, err = mergeReadFile(files[2], factsmerge.MaxReplyBytes); err != nil {
			return nil, err
		}
	}
	findings, err := factsmerge.Final(in, first, repair)
	if err != nil {
		return nil, err
	}
	return factsmerge.Write(findings)
}

func mergeBundle(path string) (*factsmerge.Bundle, error) {
	data, err := mergeReadFile(path, factsmerge.MaxBundleBytes)
	if err != nil {
		return nil, err
	}
	return factsmerge.ParseBundle(data)
}

// mergeReadFile reads a regular file of at most limit bytes, plus one byte
// more when it is larger, so a reply over its cap is read as too large
// rather than cut. It never returns nil data without an error.
func mergeReadFile(path string, limit int) ([]byte, error) {
	f, err := os.Open(path) //nolint:gosec // G703: the CLI opens the paths its user names
	if err != nil {
		return nil, err
	}
	defer f.Close()
	if st, err := f.Stat(); err != nil {
		return nil, err
	} else if !st.Mode().IsRegular() {
		return nil, errors.New(path + " is not a regular file")
	}
	data, err := io.ReadAll(io.LimitReader(f, int64(limit)+1))
	if err != nil {
		return nil, err
	}
	if data == nil {
		data = []byte{}
	}
	return data, nil
}
