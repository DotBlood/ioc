package main

import (
	"fmt"

	"github.com/DotBlood/ioc/internal/retrieval"
	"github.com/spf13/cobra"
)

func newRetrievalCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "retrieval",
		Short: "Search the knowledge graph",
	}
}

var retrievalTopK int
var retrievalVectorTopK int
var retrievalTextTopK int

func newRetrievalQueryCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "query <text>",
		Short: "Hybrid search (vector + BM25 + fusion)",
		Args:  cobra.ExactArgs(1),
		Long: `Execute a hybrid retrieval query using vector similarity and
BM25 keyword search with reciprocal rank fusion.

Retrieval indexes are rebuilt per CLI invocation in v0.1.`,
		RunE: func(cmd *cobra.Command, args []string) error {
			state := getState(cmd)

			engine, err := state.RetrievalEngine(cmd.Context())
			if err != nil {
				return fmt.Errorf("build engine: %w", err)
			}

			results, err := engine.Query(cmd.Context(), args[0], retrieval.QueryOptions{
				TopK:       retrievalTopK,
				VectorTopK: retrievalVectorTopK,
				TextTopK:   retrievalTextTopK,
			})
			if err != nil {
				return fmt.Errorf("query: %w", err)
			}

			if jsonOutput {
				outputJSON(results)
				return nil
			}

			var rows [][]string
			for _, r := range results {
				rows = append(rows, []string{
					r.ID.String(),
					fmt.Sprintf("%.6f", r.Score),
				})
			}
			return printTable(cmd.OutOrStdout(),
				[]string{"ID", "SCORE"},
				rows,
			)
		},
	}

	cmd.Flags().IntVar(&retrievalTopK, "topk", 10, "final result count")
	cmd.Flags().IntVar(&retrievalVectorTopK, "vector-topk", 50, "candidate count from vector search")
	cmd.Flags().IntVar(&retrievalTextTopK, "text-topk", 50, "candidate count from text search")
	return cmd
}

func newRetrievalTraceCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "trace <text>",
		Short: "Search with trace output",
		Args:  cobra.ExactArgs(1),
		Long: `Execute a hybrid retrieval query and output a detailed trace
showing each pipeline stage (vector_search, text_search, fusion).`,
		RunE: func(cmd *cobra.Command, args []string) error {
			state := getState(cmd)

			engine, err := state.RetrievalEngine(cmd.Context())
			if err != nil {
				return fmt.Errorf("build engine: %w", err)
			}

			results, trace, err := engine.Trace(cmd.Context(), args[0], retrieval.QueryOptions{
				TopK:       retrievalTopK,
				VectorTopK: retrievalVectorTopK,
				TextTopK:   retrievalTextTopK,
			})
			if err != nil {
				return fmt.Errorf("trace: %w", err)
			}

			if jsonOutput {
				traceJSON, tErr := retrieval.TraceAsJSON(trace)
				if tErr != nil {
					return fmt.Errorf("format trace: %w", tErr)
				}
				fmt.Fprintln(cmd.OutOrStdout(), traceJSON)
				return nil
			}

			fmt.Fprintf(cmd.OutOrStdout(), "Results (%d):\n", len(results))
			for _, r := range results {
				fmt.Fprintf(cmd.OutOrStdout(), "  %s  %.6f\n", r.ID, r.Score)
			}
			fmt.Fprintln(cmd.OutOrStdout())
			fmt.Fprintln(cmd.OutOrStdout(), "Stages:")
			for _, s := range trace.Stages {
				fmt.Fprintf(cmd.OutOrStdout(), "  %s: in=%d out=%d %s\n",
					s.Name, s.InputSize, s.OutputSize, s.Duration)
			}
			fmt.Fprintf(cmd.OutOrStdout(), "Total: %s\n", trace.Duration)
			if len(trace.Errors) > 0 {
				fmt.Fprintln(cmd.OutOrStdout(), "Errors:")
				for _, e := range trace.Errors {
					fmt.Fprintf(cmd.OutOrStdout(), "  %s\n", e)
				}
			}
			return nil
		},
	}

	cmd.Flags().IntVar(&retrievalTopK, "topk", 10, "final result count")
	cmd.Flags().IntVar(&retrievalVectorTopK, "vector-topk", 50, "candidate count from vector search")
	cmd.Flags().IntVar(&retrievalTextTopK, "text-topk", 50, "candidate count from text search")
	return cmd
}
