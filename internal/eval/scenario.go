// Package eval runs scripted dogfood scenarios against an Engine and measures
// the "wall": is mini-summary + embedding enough that an agent rarely drills to
// raw content, and do distilled constraints survive a version boundary.
package eval

import (
	"encoding/json"
	"fmt"
	"os"
)

// Scenario is a scripted multi-turn workflow.
type Scenario struct {
	Name  string `json:"name"`
	TopK  int    `json:"topk"`
	Turns []Turn `json:"turns"`
}

// Turn is one scripted operation. Op selects which fields are used.
//
// Ops:
//
//	create_scope  {id, parent, role, title}
//	push          {scope, kind, summary, content, publish, as}
//	publish       {ref}
//	query         {scope, text, expect}
//	fork          {scope, title, id}
//	consolidate   {scope, summary}
//	crossversion  {scope, constraints, lessons, id}
type Turn struct {
	Op string `json:"op"`

	// symbolic-name bindings (resolved to real IDs at run time)
	ID     string `json:"id,omitempty"`     // name to bind a created scope to
	As     string `json:"as,omitempty"`     // name to bind a pushed artifact to
	Ref    string `json:"ref,omitempty"`    // artifact name (publish)
	Scope  string `json:"scope,omitempty"`  // scope name
	Parent string `json:"parent,omitempty"` // parent scope name ("" => root)

	Role    string `json:"role,omitempty"`  // worktree|workspace|session
	Title   string `json:"title,omitempty"` //
	Kind    string `json:"kind,omitempty"`  // answer|insight|summary|document|reasoning|seed
	Summary string `json:"summary,omitempty"`
	Content string `json:"content,omitempty"`
	Publish bool   `json:"publish,omitempty"`

	Constraints string `json:"constraints,omitempty"`
	Lessons     string `json:"lessons,omitempty"`

	Text   string       `json:"text,omitempty"`
	Expect *Expectation `json:"expect,omitempty"`
}

// Expectation describes what a query turn must achieve.
type Expectation struct {
	MustContain    string   `json:"mustContain,omitempty"`    // artifact name that must appear
	MustMentionAny []string `json:"mustMentionAny,omitempty"` // summary must contain one of these
	ForbidMention  []string `json:"forbidMention,omitempty"`  // summary must contain none of these
	MaxDrills      int      `json:"maxDrills,omitempty"`      // success allows ≤ this many drill-to-raw
	RawBaseline    []string `json:"rawBaseline,omitempty"`    // artifacts an agent would carry WITHOUT IOC
}

// LoadScenario reads a scenario from a JSON file.
func LoadScenario(path string) (*Scenario, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("eval: read scenario: %w", err)
	}
	var s Scenario
	if err := json.Unmarshal(data, &s); err != nil {
		return nil, fmt.Errorf("eval: parse scenario: %w", err)
	}
	if s.TopK <= 0 {
		s.TopK = 5
	}
	return &s, nil
}
