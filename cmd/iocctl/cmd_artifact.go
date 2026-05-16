package main

import (
	"context"
	"fmt"
	"io"
	"os"
	"time"

	"github.com/DotBlood/ioc/internal/embedding"
	"github.com/DotBlood/ioc/internal/model"
	"github.com/DotBlood/ioc/internal/pipeline"
	"github.com/DotBlood/ioc/internal/retrieval"
	"github.com/DotBlood/ioc/internal/store"
	"github.com/spf13/cobra"
)

var textFlag, fileFlag string

func newArtifactCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "artifact",
		Short: "Manage artifacts in scopes",
	}
}

func newArtifactAddCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "add <scope>",
		Short: "Add an artifact to a scope",
		Args:  cobra.ExactArgs(1),
		Long: `Add an artifact to a scope from stdin, --text, or --file.

Examples:
  iocctl artifact add <scope> --text "hello world"
  iocctl artifact add <scope> --file doc.txt
  cat doc.txt | iocctl artifact add <scope>`,
		RunE: func(cmd *cobra.Command, args []string) error {
			state := getState(cmd)
			scopeID := model.ScopeID(args[0])

			// Validate scope exists.
			if _, err := state.disk.ScopeState(cmd.Context(), scopeID); err != nil {
				return fmt.Errorf("scope %q not found", scopeID)
			}

			// Read content: stdin pipe → --text → --file.
			var content []byte
			stat, err := os.Stdin.Stat()
			if err != nil {
				return fmt.Errorf("stat stdin: %w", err)
			}
			if (stat.Mode() & os.ModeCharDevice) == 0 {
				content, err = io.ReadAll(os.Stdin)
				if err != nil {
					return fmt.Errorf("read stdin: %w", err)
				}
			} else if textFlag != "" {
				content = []byte(textFlag)
			} else if fileFlag != "" {
				content, err = os.ReadFile(fileFlag)
				if err != nil {
					return fmt.Errorf("read file: %w", err)
				}
			} else {
				return fmt.Errorf("provide content via stdin, --text, or --file")
			}

			// BM25 index is process-local and rebuilt per command in v0.1 CLI.
			p := pipeline.NewIngestionPipeline(
				state.cas,
				state.ArtifactStore(),
				&embeddingStoreWriter{s: state.embStore},
				&diskOwnershipWriter{disk: state.disk},
				&diskScopeResolver{disk: state.disk},
				state.Embedder(),
				retrieval.NewBM25Index(),
			)

			id, err := p.Process(cmd.Context(), content, scopeID, "")
			if err != nil {
				return fmt.Errorf("ingest: %w", err)
			}

			if jsonOutput {
				outputJSON(map[string]string{"id": id.String()})
			} else {
				fmt.Fprintf(cmd.OutOrStdout(), "%s\n", id)
			}
			return nil
		},
	}

	cmd.Flags().StringVar(&textFlag, "text", "", "artifact content as text")
	cmd.Flags().StringVar(&fileFlag, "file", "", "read artifact content from file")
	return cmd
}

func newArtifactGetCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "get <id>",
		Short: "Show artifact with latest projection and content",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			state := getState(cmd)
			id, err := model.ParseID(args[0])
			if err != nil {
				return fmt.Errorf("parse id: %w", err)
			}

			art, err := state.disk.LoadNode(id)
			if err != nil {
				return fmt.Errorf("load artifact: %w", err)
			}

			proj, err := state.ArtifactStore().LatestProjection(cmd.Context(), id)
			if err != nil {
				return fmt.Errorf("load projection: %w", err)
			}

			rc, err := state.cas.Open(cmd.Context(), art.ContentHash)
			if err != nil {
				return fmt.Errorf("open content: %w", err)
			}
			content, err := io.ReadAll(rc)
			rc.Close()
			if err != nil {
				return fmt.Errorf("read content: %w", err)
			}

			if jsonOutput {
				outputJSON(map[string]any{
					"artifact":   art,
					"projection": proj,
					"content":    string(content),
				})
				return nil
			}

			fmt.Fprintf(cmd.OutOrStdout(), "ID:       %s\n", art.ArtifactID)
			fmt.Fprintf(cmd.OutOrStdout(), "Type:     %s\n", art.NodeType)
			fmt.Fprintf(cmd.OutOrStdout(), "Scope:    %s\n", art.Scope)
			fmt.Fprintf(cmd.OutOrStdout(), "Revision: %d\n", proj.Revision)
			fmt.Fprintf(cmd.OutOrStdout(), "Summary:  %s\n", proj.Summary)
			fmt.Fprintf(cmd.OutOrStdout(), "Content:  %s\n", string(content))
			return nil
		},
	}
}

func newArtifactRevisionsCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "revisions <id>",
		Short: "List projection revisions for an artifact",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			state := getState(cmd)
			id, err := model.ParseID(args[0])
			if err != nil {
				return fmt.Errorf("parse id: %w", err)
			}

			keys, err := state.ArtifactStore().ListProjections(cmd.Context(), id)
			if err != nil {
				return fmt.Errorf("list projections: %w", err)
			}

			if jsonOutput {
				outputJSON(keys)
				return nil
			}

			var rows [][]string
			for _, k := range keys {
				proj, err := state.ArtifactStore().LoadProjection(cmd.Context(), k)
				if err != nil {
					continue
				}
				readiness := "stored"
				if proj.Readiness.Has(model.ReadinessEmbedded) {
					readiness = "embedded"
				}
				if proj.Readiness.Has(model.ReadinessIndexed) {
					readiness = "indexed"
				}
				rows = append(rows, []string{
					fmt.Sprintf("%d", k.Revision),
					proj.Summary,
					readiness,
					proj.ValidFrom.Format("2006-01-02 15:04:05"),
				})
			}
			return printTable(cmd.OutOrStdout(),
				[]string{"REVISION", "SUMMARY", "READINESS", "CREATED"},
				rows,
			)
		},
	}
}

// Embedding writer adapter for IngestionPipeline.
type embeddingStoreWriter struct {
	s *store.EmbeddingStore
}

func (w *embeddingStoreWriter) SaveEmbedding(_ context.Context, _ model.ID, vec embedding.Vector) (model.EmbeddingRefID, error) {
	return w.s.Put(vec.Data)
}

// Ownership writer adapter for IngestionPipeline.
type diskOwnershipWriter struct {
	disk *store.DiskStore
}

func (w *diskOwnershipWriter) AddOwnership(_ context.Context, parent, child model.ID) error {
	edge := &model.Edge{
		EdgeID:    model.NewID(),
		Type:      model.EdgeOwnership,
		Direction: model.DirectionDirected,
		Source:    parent,
		Target:    child,
		Valid:     true,
		ValidFrom: time.Now(),
	}
	return w.disk.SaveEdge(edge)
}

// Scope resolver adapter for IngestionPipeline.
type diskScopeResolver struct {
	disk *store.DiskStore
}

func (r *diskScopeResolver) ResolveScope(_ context.Context, scopeID model.ScopeID) (model.ID, error) {
	id, err := model.ParseID(string(scopeID))
	if err != nil {
		return model.NilID, err
	}
	return id, nil
}
