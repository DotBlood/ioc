package main

import (
	"fmt"
	"time"

	"github.com/DotBlood/ioc/internal/model"
	"github.com/spf13/cobra"
)

var archiveScopeFilter string

func newArchiveCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "archive",
		Short: "Manage archive snapshots",
	}
	cmd.PersistentFlags().StringVar(&archiveScopeFilter, "scope", "", "filter by exact ScopeID")
	return cmd
}

func newArchiveListCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "list",
		Short: "List archive snapshots",
		Long: `List all archive snapshots, optionally filtered by --scope.

Filtering is by exact ScopeID match only — no hierarchy resolution.`,
		RunE: func(cmd *cobra.Command, args []string) error {
			state := getState(cmd)

			var anchors []model.Anchor
			var err error
			if archiveScopeFilter != "" {
				anchors, err = state.AnchorStore().List(cmd.Context(), model.ScopeID(archiveScopeFilter))
			} else {
				anchors, err = state.AnchorStore().ListAll(cmd.Context())
			}
			if err != nil {
				return fmt.Errorf("list anchors: %w", err)
			}

			if jsonOutput {
				outputJSON(anchors)
				return nil
			}

			var rows [][]string
			for _, a := range anchors {
				kindStr := "full"
				if a.Kind == model.AnchorDiff {
					kindStr = "diff"
				}
				rows = append(rows, []string{
					a.AnchorID.String(),
					string(a.ScopeID),
					kindStr,
					fmt.Sprintf("%d", a.ArtifactCount),
					fmt.Sprintf("%d", a.EdgeCount),
					a.CreatedAt.Format("2006-01-02 15:04:05"),
				})
			}
			return printTable(cmd.OutOrStdout(),
				[]string{"ANCHOR ID", "SCOPE", "KIND", "ARTIFACTS", "EDGES", "CREATED"},
				rows,
			)
		},
	}
}

func newArchiveShowCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "show <anchor-id>",
		Short: "Show anchor snapshot details",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			state := getState(cmd)

			id, err := model.ParseID(args[0])
			if err != nil {
				return fmt.Errorf("parse anchor id: %w", err)
			}

			anchor, err := state.AnchorStore().Load(cmd.Context(), model.AnchorID(id))
			if err != nil {
				return fmt.Errorf("load anchor: %w", err)
			}

			if jsonOutput {
				outputJSON(anchor)
				return nil
			}

			kindStr := "full"
			if anchor.Kind == model.AnchorDiff {
				kindStr = "diff"
			}

			fmt.Fprintf(cmd.OutOrStdout(), "AnchorID:   %s\n", anchor.AnchorID)
			fmt.Fprintf(cmd.OutOrStdout(), "Scope:      %s\n", anchor.ScopeID)
			fmt.Fprintf(cmd.OutOrStdout(), "Kind:       %s\n", kindStr)
			fmt.Fprintf(cmd.OutOrStdout(), "Revision:   %d\n", anchor.Revision)
			fmt.Fprintf(cmd.OutOrStdout(), "Created:    %s\n", anchor.CreatedAt.Format(time.RFC3339))
			fmt.Fprintf(cmd.OutOrStdout(), "Artifacts:  %d\n", anchor.ArtifactCount)
			fmt.Fprintf(cmd.OutOrStdout(), "Edges:      %d\n", anchor.EdgeCount)
			fmt.Fprintf(cmd.OutOrStdout(), "Projections:%d\n", anchor.ProjectionCount)
			if anchor.Kind == model.AnchorDiff {
				fmt.Fprintf(cmd.OutOrStdout(), "ParentAnchor:%s\n", anchor.ParentAnchor)
				if len(anchor.RemovedArtifactIDs) > 0 {
					fmt.Fprintf(cmd.OutOrStdout(), "Removed:    %d artifacts\n", len(anchor.RemovedArtifactIDs))
				}
			}
			return nil
		},
	}
}
