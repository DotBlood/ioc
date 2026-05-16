package main

import (
	"context"
	"fmt"
	"os"

	"github.com/DotBlood/ioc/internal/knowledge"
	"github.com/DotBlood/ioc/internal/model"
	"github.com/DotBlood/ioc/internal/store"
	"github.com/spf13/cobra"
)

func newRetentionCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "retention",
		Short: "Manage retention and cleanup",
	}
}

func newRetentionRunCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "run",
		Short: "Run retention sweep (prune old projections and edge revisions)",
		Long: `Run a full retention sweep across all scopes.

Prunes old projection revisions and edge revisions based on
the configured retention policy. Protected revisions (latest,
anchor-reachable, or with no expiry) are never pruned.`,
		RunE: func(cmd *cobra.Command, args []string) error {
			state := getState(cmd)

			policy := knowledge.NewRetentionPolicy(
				knowledge.DefaultRetentionConfig(),
				&retentionArtifactAdapter{disk: state.disk},
				state.AnchorStore(),
				&retentionEdgeAdapter{disk: state.disk},
			)

			result, err := policy.Run(cmd.Context())
			if err != nil {
				return fmt.Errorf("retention: %w", err)
			}

			if jsonOutput {
				outputJSON(result)
				return nil
			}

			fmt.Fprintf(cmd.OutOrStdout(), "Projections pruned:   %d\n", result.ProjectionsPruned)
			fmt.Fprintf(cmd.OutOrStdout(), "Edge revisions pruned: %d\n", result.EdgeRevisionsPruned)
			fmt.Fprintf(cmd.OutOrStdout(), "Duration:             %s\n", result.Duration)
			if len(result.Errors) > 0 {
				fmt.Fprintf(cmd.OutOrStdout(), "Errors (%d):\n", len(result.Errors))
				for _, e := range result.Errors {
					fmt.Fprintf(os.Stderr, "  warning: %s\n", e)
				}
			}
			return nil
		},
	}
}

// retentionArtifactAdapter adapts DiskStore to knowledge.RetentionArtifactStore.
type retentionArtifactAdapter struct {
	disk *store.DiskStore
}

func (a *retentionArtifactAdapter) ListProjections(ctx context.Context, artifactID model.ID) ([]model.ProjectionKey, error) {
	return a.disk.ListProjectionKeys(artifactID)
}

func (a *retentionArtifactAdapter) LoadProjection(ctx context.Context, key model.ProjectionKey) (*model.ArtifactProjection, error) {
	return a.disk.LoadProjection(key)
}

func (a *retentionArtifactAdapter) DeleteProjection(key model.ProjectionKey) error {
	return a.disk.DeleteProjection(key)
}

func (a *retentionArtifactAdapter) ListAllArtifactIDs(ctx context.Context) ([]model.ID, error) {
	return a.disk.ListAllArtifactIDs(ctx)
}

// retentionEdgeAdapter adapts DiskStore to knowledge.RetentionEdgeStore.
type retentionEdgeAdapter struct {
	disk *store.DiskStore
}

func (a *retentionEdgeAdapter) ListEdgeRevisions(ctx context.Context) ([]model.EdgeRevisionKey, error) {
	return a.disk.ListEdgeRevisions(ctx)
}

func (a *retentionEdgeAdapter) LoadEdgeRevision(ctx context.Context, key model.EdgeRevisionKey) (*model.Edge, error) {
	return a.disk.LoadEdgeRevision(key)
}

func (a *retentionEdgeAdapter) DeleteEdgeRevision(key model.EdgeRevisionKey) error {
	return a.disk.DeleteEdgeRevision(key)
}
