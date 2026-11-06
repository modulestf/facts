package main

import (
	"encoding/json"
	"errors"
	"fmt"

	"github.com/modulestf/facts/internal/facts"
	"github.com/modulestf/facts/internal/profile"
	"github.com/modulestf/facts/internal/snapshot"
	"github.com/spf13/cobra"
)

// Exit code of profile-check for a profile that reads but is not valid.
const exitProfileInvalid = 1

// profileCheckReport is what profile-check prints. Options and Relaxing are
// set for a valid profile; Gates and GatedRules with --gates, DowngradeIDs
// with --downgrade-ids.
type profileCheckReport struct {
	Valid        bool             `json:"valid"`
	Problems     []string         `json:"problems"`
	Options      *profile.Options `json:"options,omitempty"`
	Relaxing     bool             `json:"relaxing"`
	Gates        []profile.Gate   `json:"gates,omitempty"`
	GatedRules   []string         `json:"gated_rules,omitempty"`
	DowngradeIDs []string         `json:"downgrade_ids,omitempty"`
}

// newProfileCheckCmd builds profile-check, which validates a profile and
// prints its options.
func newProfileCheckCmd() *cobra.Command {
	var dir string
	var gates, downgrade bool
	cmd := &cobra.Command{
		Use:     "profile-check --profile-dir <dir>",
		Short:   profileCheckShort,
		Long:    profileCheckLong,
		Example: profileCheckExample,
		GroupID: groupPipeline,
		Args:    cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			if dir == "" {
				return status(usageError(cmd, profileDirRequired))
			}
			return status(profileCheck(cmd, dir, gates, downgrade))
		},
	}
	f := cmd.Flags()
	f.SortFlags = false
	f.StringVar(&dir, "profile-dir", "", profileDirUsage)
	f.BoolVar(&gates, "gates", false, profileGatesUsage)
	f.BoolVar(&downgrade, "downgrade-ids", false, profileDowngradeUsage)
	return cmd
}

func profileCheck(cmd *cobra.Command, dir string, gates, downgrade bool) int {
	p, unreadable, invalid := facts.ReadProfile(dir)
	if unreadable != nil {
		fmt.Fprintln(cmd.ErrOrStderr(), "mtf-facts: profile:", unreadable)
		return exitError
	}
	rep := profileCheckReport{Valid: invalid == nil, Problems: []string{}}
	if invalid != nil {
		rep.Problems = append(rep.Problems, invalid.Error())
	} else {
		rep.Options, rep.Relaxing = &p.Options, p.Options.Relaxing()
	}
	if gates {
		rep.Gates = profile.Gates
		if p != nil {
			rep.GatedRules = p.Options.GatedRules()
		}
	}
	if downgrade {
		rep.DowngradeIDs = profile.DowngradeRules
	}
	if code := writeJSON(cmd, rep); code != exitClean {
		return code
	}
	if invalid != nil {
		return exitProfileInvalid
	}
	return exitClean
}

// newProfileSnapshotCmd builds profile-snapshot, which copies a profile
// directory into a private temporary directory.
func newProfileSnapshotCmd() *cobra.Command {
	var dir string
	cmd := &cobra.Command{
		Use:     "profile-snapshot --profile-dir <dir>",
		Short:   profileSnapshotShort,
		Long:    profileSnapshotLong,
		Example: profileSnapshotExample,
		GroupID: groupPipeline,
		Args:    cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			if dir == "" {
				return status(usageError(cmd, profileDirRequired))
			}
			s, err := snapshot.Take(dir)
			if err != nil {
				if !errors.Is(err, snapshot.ErrRefused) {
					err = fmt.Errorf("profile: %w", err)
				}
				fmt.Fprintln(cmd.ErrOrStderr(), "mtf-facts:", err)
				return status(exitError)
			}
			return status(writeJSON(cmd, s))
		},
	}
	f := cmd.Flags()
	f.SortFlags = false
	f.StringVar(&dir, "profile-dir", "", profileDirUsage)
	return cmd
}

// writeJSON prints v as indented JSON to stdout.
func writeJSON(cmd *cobra.Command, v any) int {
	out, err := json.MarshalIndent(v, "", "  ")
	if err != nil {
		fmt.Fprintln(cmd.ErrOrStderr(), "mtf-facts:", err)
		return exitError
	}
	if _, err := cmd.OutOrStdout().Write(append(out, '\n')); err != nil {
		return exitError
	}
	return exitClean
}
