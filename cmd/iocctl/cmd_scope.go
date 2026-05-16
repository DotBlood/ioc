package main

import (
	"fmt"
	"sort"
	"time"

	"github.com/DotBlood/ioc/internal/model"
	"github.com/spf13/cobra"
)

func newScopeCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "scope",
		Short: "Manage scopes (worktree, workspace, session)",
	}
}

func newScopeCreateCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "create <worktree|workspace|session>",
		Short: "Create a new scope",
		Args:  cobra.ExactArgs(1),
		Long: `Create a new scope of the specified type.

Scopes form an automatic hierarchy:
  worktree  — root scope, no parent
  workspace — parent is the most recent worktree
  session   — parent is the most recent workspace

Scope creation is progressively committing.
Partial scope state may exist after failures — valid for v0.1.`,
		RunE: func(cmd *cobra.Command, args []string) error {
			state := getState(cmd)
			nodeType, err := parseNodeType(args[0])
			if err != nil {
				return err
			}

			// Root worktree scopes have no ownership parent.
			parentID, err := resolveParent(state, nodeType)
			if err != nil {
				return err
			}

			// Validate parent type chain.
			if !parentID.IsZero() {
				parentType, err := state.disk.NodeType(parentID)
				if err != nil {
					return fmt.Errorf("parent node: %w", err)
				}
				if err := validateParentPair(parentType, nodeType); err != nil {
					return err
				}
			}

			id := model.NewID()
			scopeID := model.ScopeID(id.String())

			node := &model.Artifact{
				ArtifactID: id,
				NodeType:   nodeType,
				Scope:      scopeID,
				CreatedAt:  time.Now(),
			}
			if err := state.disk.SaveNode(node); err != nil {
				return fmt.Errorf("save node: %w", err)
			}

			// Ownership edge: parent → child.
			if !parentID.IsZero() {
				edge := &model.Edge{
					EdgeID:    model.NewID(),
					Type:      model.EdgeOwnership,
					Direction: model.DirectionDirected,
					Source:    parentID,
					Target:    id,
					Valid:     true,
					ValidFrom: time.Now(),
				}
				if err := state.disk.SaveEdge(edge); err != nil {
					return fmt.Errorf("save ownership edge: %w", err)
				}

				parentScopeID := model.ScopeID(parentID.String())
				if err := state.disk.SaveScopeChild(parentScopeID, scopeID); err != nil {
					return fmt.Errorf("save scope child: %w", err)
				}
			}

			scopeState := &model.ScopeState{
				ScopeID:   scopeID,
				Type:      nodeType,
				State:     model.LifecycleDraft,
				ParentID:  parentID,
				CreatedAt: time.Now(),
			}
			if err := state.disk.SaveScopeState(scopeState); err != nil {
				return fmt.Errorf("save scope state: %w", err)
			}

			// CLI convenience pointers — NOT authoritative graph state.
			// Hierarchy is captured in ownership edges + scope_children bucket.
			metaKey := currentMetaKey(nodeType)
			if metaKey != "" {
				if err := state.disk.SetMeta(metaKey, id.String()); err != nil {
					return fmt.Errorf("save meta: %w", err)
				}
			}

			if jsonOutput {
				outputJSON(map[string]string{
					"scope_id": string(scopeID),
					"type":     nodeType.String(),
					"state":    "draft",
				})
			} else {
				fmt.Fprintf(cmd.OutOrStdout(), "%s\n", scopeID)
			}
			return nil
		},
	}
}

func newScopeListCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "list",
		Short: "List all scopes with lifecycle state",
		RunE: func(cmd *cobra.Command, args []string) error {
			state := getState(cmd)

			scopes, err := state.disk.ListAllScopeStates()
			if err != nil {
				return fmt.Errorf("list scopes: %w", err)
			}

			// Deterministic sort by ScopeID.
			sort.Slice(scopes, func(i, j int) bool {
				return string(scopes[i].ScopeID) < string(scopes[j].ScopeID)
			})

			if jsonOutput {
				outputJSON(scopes)
				return nil
			}

			var rows [][]string
			for _, s := range scopes {
				children, _ := state.disk.ScopeChildren(nil, s.ScopeID)
				childCount := len(children)
				rows = append(rows, []string{
					string(s.ScopeID),
					s.Type.String(),
					s.State.String(),
					fmt.Sprintf("%d", childCount),
				})
			}
			return printTable(cmd.OutOrStdout(),
				[]string{"SCOPE ID", "TYPE", "STATE", "CHILDREN"},
				rows,
			)
		},
	}
}

func parseNodeType(s string) (model.NodeType, error) {
	switch s {
	case "worktree":
		return model.NodeTypeWorktree, nil
	case "workspace":
		return model.NodeTypeWorkspace, nil
	case "session":
		return model.NodeTypeSession, nil
	default:
		return 0, fmt.Errorf("unknown scope type %q; use worktree, workspace, or session", s)
	}
}

func validateParentPair(parent, child model.NodeType) error {
	switch child {
	case model.NodeTypeWorkspace:
		if parent != model.NodeTypeWorktree {
			return fmt.Errorf("workspace parent must be worktree, got %s", parent)
		}
	case model.NodeTypeSession:
		if parent != model.NodeTypeWorkspace {
			return fmt.Errorf("session parent must be workspace, got %s", parent)
		}
	}
	return nil
}

func resolveParent(state *appState, nodeType model.NodeType) (model.ID, error) {
	switch nodeType {
	case model.NodeTypeWorktree:
		return model.NilID, nil
	case model.NodeTypeWorkspace:
		return resolveMetaParent(state, "current_worktree", "worktree")
	case model.NodeTypeSession:
		return resolveMetaParent(state, "current_workspace", "workspace")
	default:
		return model.NilID, fmt.Errorf("unknown node type %v", nodeType)
	}
}

func resolveMetaParent(state *appState, metaKey, typeName string) (model.ID, error) {
	val, err := state.disk.GetMeta(metaKey)
	if err != nil {
		return model.NilID, fmt.Errorf("no %s found; create one with `iocctl scope create %s`", typeName, typeName)
	}
	id, err := model.ParseID(val)
	if err != nil {
		return model.NilID, fmt.Errorf("corrupt meta %s: %w", metaKey, err)
	}
	return id, nil
}

func currentMetaKey(t model.NodeType) string {
	switch t {
	case model.NodeTypeWorktree:
		return "current_worktree"
	case model.NodeTypeWorkspace:
		return "current_workspace"
	default:
		return "" // session not tracked — creates from current workspace
	}
}

func newScopeArchiveCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "archive <scope-id>",
		Short: "Archive a scope (freeze + snapshot + lifecycle transition)",
		Args:  cobra.ExactArgs(1),
		Long: `Archive a scope by creating a structural snapshot and transitioning
it to the archived lifecycle state.

This operation rebuilds an in-memory graph snapshot from disk
and is infrequent in normal use.`,
		RunE: func(cmd *cobra.Command, args []string) error {
			state := getState(cmd)
			scopeID := model.ScopeID(args[0])

			if _, err := state.disk.ScopeState(cmd.Context(), scopeID); err != nil {
				return fmt.Errorf("scope %q not found", scopeID)
			}

			ap, err := state.ArchivePipeline(cmd.Context())
			if err != nil {
				return fmt.Errorf("build archive pipeline: %w", err)
			}

			anchorID, err := ap.Archive(cmd.Context(), scopeID)
			if err != nil {
				return fmt.Errorf("archive: %w", err)
			}

			if jsonOutput {
				outputJSON(map[string]string{"anchor_id": anchorID.String()})
			} else {
				fmt.Fprintf(cmd.OutOrStdout(), "%s\n", *anchorID)
			}
			return nil
		},
	}
}

func newScopeRestoreCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "restore <anchor-id>",
		Short: "Restore a scope from an anchor snapshot",
		Args:  cobra.ExactArgs(1),
		Long: `Restore a scope from a previously created anchor snapshot.
The scope graph is rebuilt from the anchor data.

This operation rebuilds an in-memory graph snapshot from disk
and is infrequent in normal use.`,
		RunE: func(cmd *cobra.Command, args []string) error {
			state := getState(cmd)

			id, err := model.ParseID(args[0])
			if err != nil {
				return fmt.Errorf("parse anchor id: %w", err)
			}

			ap, err := state.ArchivePipeline(cmd.Context())
			if err != nil {
				return fmt.Errorf("build archive pipeline: %w", err)
			}

			if err := ap.Restore(cmd.Context(), model.AnchorID(id)); err != nil {
				return fmt.Errorf("restore: %w", err)
			}

			if !jsonOutput {
				fmt.Fprintln(cmd.OutOrStdout(), "scope restored")
			}
			return nil
		},
	}
}
