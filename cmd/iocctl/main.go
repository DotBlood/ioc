package main

import (
	"context"
	"os"

	"github.com/spf13/cobra"
)

func main() {
	if err := rootCmd.Execute(); err != nil {
		os.Exit(1)
	}
}

var rootCmd = &cobra.Command{
	Use:   "iocctl",
	Short: "IOC — Stateful Knowledge Graph Runtime",
	Long: `IOC (Stateful Knowledge Graph Runtime) is a knowledge graph
database with deterministic retrieval, revision management,
and structural archiving.`,

	PersistentPreRunE: func(cmd *cobra.Command, args []string) error {
		if !requiresStore(cmd) {
			return nil
		}
		if err := ensureInitialized(rootDir); err != nil {
			return err
		}
		state, err := openState(rootDir)
		if err != nil {
			return err
		}
		cmd.SetContext(context.WithValue(cmd.Context(), ctxState{}, state))
		return nil
	},

	PersistentPostRunE: func(cmd *cobra.Command, args []string) error {
		if s, ok := cmd.Context().Value(ctxState{}).(*appState); ok {
			return s.Close()
		}
		return nil
	},
}

type ctxState struct{}

var rootDir string
var jsonOutput bool

var cmdInit *cobra.Command
var cmdScope *cobra.Command
var cmdScopeCreate *cobra.Command
var cmdScopeList *cobra.Command
var cmdScopeArchive *cobra.Command
var cmdScopeRestore *cobra.Command
var cmdArtifact *cobra.Command
var cmdArtifactAdd *cobra.Command
var cmdArtifactGet *cobra.Command
var cmdArtifactRevisions *cobra.Command
var cmdRetrieval *cobra.Command
var cmdRetrievalQuery *cobra.Command
var cmdRetrievalTrace *cobra.Command
var cmdArchive *cobra.Command
var cmdArchiveList *cobra.Command
var cmdArchiveShow *cobra.Command
var cmdRetention *cobra.Command
var cmdRetentionRun *cobra.Command

func init() {
	rootCmd.PersistentFlags().StringVar(&rootDir, "dir", defaultRootDir(), "IOC data directory")
	rootCmd.PersistentFlags().BoolVar(&jsonOutput, "json", false, "JSON output")

	cmdInit = newInitCmd()
	cmdScope = newScopeCmd()
	cmdScopeCreate = newScopeCreateCmd()
	cmdScopeList = newScopeListCmd()
	cmdScopeArchive = newScopeArchiveCmd()
	cmdScopeRestore = newScopeRestoreCmd()
	cmdArtifact = newArtifactCmd()
	cmdArtifactAdd = newArtifactAddCmd()
	cmdArtifactGet = newArtifactGetCmd()
	cmdArtifactRevisions = newArtifactRevisionsCmd()
	cmdRetrieval = newRetrievalCmd()
	cmdRetrievalQuery = newRetrievalQueryCmd()
	cmdRetrievalTrace = newRetrievalTraceCmd()
	cmdArchive = newArchiveCmd()
	cmdArchiveList = newArchiveListCmd()
	cmdArchiveShow = newArchiveShowCmd()
	cmdRetention = newRetentionCmd()
	cmdRetentionRun = newRetentionRunCmd()

	cmdScope.AddCommand(cmdScopeCreate, cmdScopeList, cmdScopeArchive, cmdScopeRestore)
	cmdArtifact.AddCommand(cmdArtifactAdd, cmdArtifactGet, cmdArtifactRevisions)
	cmdRetrieval.AddCommand(cmdRetrievalQuery, cmdRetrievalTrace)
	cmdArchive.AddCommand(cmdArchiveList, cmdArchiveShow)
	cmdRetention.AddCommand(cmdRetentionRun)
	rootCmd.AddCommand(cmdInit, cmdScope, cmdArtifact, cmdRetrieval, cmdArchive, cmdRetention)
}
