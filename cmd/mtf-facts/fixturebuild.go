package main

import (
	"fmt"
	"regexp"
	"time"

	"github.com/modulestf/facts/internal/facts"
	"github.com/modulestf/facts/internal/fixture"
	"github.com/modulestf/facts/internal/readcap"
	"github.com/modulestf/facts/internal/stats"
	"github.com/spf13/cobra"
)

// fixtureBuildFlags are the flags of fixture-build.
type fixtureBuildFlags struct {
	template, resourceTemplate, out string
	level, attribute, resource      string
	timeout                         time.Duration
	statsTo                         string
}

// The names --attribute and --resource take: a Terraform attribute name,
// and a resource type with its provider prefix.
var (
	buildAttribute = regexp.MustCompile(`\A[a-z][a-z0-9_]{0,63}\z`)
	buildResource  = regexp.MustCompile(`\A[a-z][a-z0-9]*_[a-z0-9_]{1,63}\z`)
)

// newFixtureBuildCmd builds fixture-build.
func newFixtureBuildCmd() *cobra.Command {
	var fl fixtureBuildFlags
	cmd := &cobra.Command{
		Use:     "fixture-build <trigger.json>",
		Short:   fixtureBuildShort,
		Long:    fixtureBuildLong,
		Example: fixtureBuildExample,
		GroupID: groupPipeline,
		Args:    cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			return withStats(cmd, args, fl.statsTo, func(rec *stats.Recorder) int {
				return fixtureBuild(cmd, args[0], fl, rec)
			})
		},
	}
	f := cmd.Flags()
	f.SortFlags = false
	f.StringVar(&fl.template, "template", "", fixtureBuildTemplateUsage)
	f.StringVar(&fl.out, "out", "", fixtureBuildOutUsage)
	f.StringVar(&fl.level, "level", "L1", fixtureBuildLevelUsage)
	f.StringVar(&fl.attribute, "attribute", "", fixtureBuildAttributeUsage)
	f.StringVar(&fl.resource, "resource", "", fixtureBuildResourceUsage)
	f.StringVar(&fl.resourceTemplate, "resource-template", "", fixtureBuildResourceTemplateUsage)
	addTimeoutFlag(cmd, &fl.timeout)
	addStatsFlag(cmd, &fl.statsTo)
	return cmd
}

// fixtureBuild builds one fixture and writes the result. It exits 0 when
// the fixture is written, 1 when the trigger is refused and 2 on bad input,
// the output cap or the timeout. The files are written after the worker is
// done and the caps pass, so a timeout or a cap writes none.
func fixtureBuild(cmd *cobra.Command, trigger string, fl fixtureBuildFlags, rec *stats.Recorder) int {
	if msg := fl.check(); msg != "" {
		return usageError(cmd, msg)
	}
	if err := fixture.CheckOut(fl.out); err != nil {
		fmt.Fprintln(cmd.ErrOrStderr(), "mtf-facts: fixture-build:", oneLine(err.Error()))
		return exitError
	}
	lim := facts.DefaultLimits
	lim.Stats = rec
	return runBounded(cmd, fl.timeout, &lim, func() workResult {
		b, err := fixtureBuildWork(trigger, fl)
		if err != nil {
			return workResult{err: fmt.Errorf("fixture-build: %w", err)}
		}
		out, err := fixture.EncodeResult(b.Result)
		if err != nil {
			return workResult{err: fmt.Errorf("fixture-build: %w", err)}
		}
		res := workResult{out: append(out, '\n'), code: exitFindings}
		if !b.OK {
			return res
		}
		// The files are output too: each is held to the output cap.
		for _, f := range b.Files {
			if len(f.Data) > lim.MaxOutputBytes {
				return workResult{err: fmt.Errorf("fixture-build: a file over %d bytes", lim.MaxOutputBytes)}
			}
		}
		res.code = exitClean
		res.commit = func() error {
			if err := fixture.WriteFiles(fl.out, b.Files); err != nil {
				return fmt.Errorf("fixture-build: %w", err)
			}
			return nil
		}
		return res
	}, nil)
}

// check names the first flag combination the level does not take.
func (fl fixtureBuildFlags) check() string {
	switch {
	case fl.template == "":
		return "--template is required"
	case fl.out == "":
		return "--out is required"
	case fl.attribute != "" && !buildAttribute.MatchString(fl.attribute):
		return "--attribute must be an attribute name"
	case fl.resource != "" && !buildResource.MatchString(fl.resource):
		return "--resource must be a resource type"
	}
	l3 := fl.resource != "" || fl.resourceTemplate != ""
	switch fl.level {
	case "L1":
		if fl.attribute != "" || l3 {
			return "--attribute, --resource and --resource-template are not taken at L1"
		}
	case "L2":
		if fl.attribute == "" {
			return "--attribute is required at L2"
		}
		if l3 {
			return "--resource and --resource-template are taken at L3 only"
		}
	case "L3":
		if fl.attribute == "" || fl.resource == "" || fl.resourceTemplate == "" {
			return "--attribute, --resource and --resource-template are required at L3"
		}
	default:
		return "--level must be L1, L2 or L3"
	}
	return ""
}

// fixtureBuildWork reads the input files and builds the fixture.
func fixtureBuildWork(trigger string, fl fixtureBuildFlags) (*fixture.Built, error) {
	o := fixture.BuildOptions{Level: fl.level, Attribute: fl.attribute, Resource: fl.resource}
	var err error
	if o.Trigger, err = readcap.File(trigger, fixture.MaxTriggerBytes); err != nil {
		return nil, fmt.Errorf("trigger: %w", err)
	}
	if o.Template, err = readcap.File(fl.template, fixture.MaxTemplateBytes); err != nil {
		return nil, fmt.Errorf("template: %w", err)
	}
	if fl.resourceTemplate != "" {
		if o.ResourceTemplate, err = readcap.File(fl.resourceTemplate, fixture.MaxTemplateBytes); err != nil {
			return nil, fmt.Errorf("resource template: %w", err)
		}
	}
	return fixture.Build(o)
}
