package main

import (
	"fmt"
	"os"
	"path/filepath"

	"github.com/DotBlood/ioc/internal/store"
	"github.com/spf13/cobra"
)

func newInitCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "init",
		Short: "Initialize IOC data directory",
		Long: `Creates the IOC data directory structure at the configured
location (default ~/.ioc/) with database, CAS, and embedding store.`,
		RunE: func(cmd *cobra.Command, args []string) error {
			dir := rootDir

			if err := os.MkdirAll(filepath.Join(dir, "db"), 0755); err != nil {
				return fmt.Errorf("create db dir: %w", err)
			}
			if err := os.MkdirAll(filepath.Join(dir, "cas"), 0755); err != nil {
				return fmt.Errorf("create cas dir: %w", err)
			}
			if err := os.MkdirAll(filepath.Join(dir, "emb"), 0755); err != nil {
				return fmt.Errorf("create emb dir: %w", err)
			}

			disk, err := store.OpenOrCreate(filepath.Join(dir, "db", "ioc.db"))
			if err != nil {
				return fmt.Errorf("create database: %w", err)
			}
			defer disk.Close()

			emb, err := store.OpenEmbeddingStore(filepath.Join(dir, "emb", "default.emb"), 384)
			if err != nil {
				return fmt.Errorf("create embedding store: %w", err)
			}
			emb.Close()

			if err := disk.SetMeta("ioc_version", "0.1.0"); err != nil {
				return fmt.Errorf("save version: %w", err)
			}

			fmt.Fprintf(cmd.OutOrStdout(), "Initialized IOC repository at %s\n", dir)
			return nil
		},
	}
}
