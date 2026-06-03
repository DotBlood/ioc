// Command ioc drives the IOC slice: run scripted eval scenarios, ping the
// embedder, and operate IOC as a persistent memory store from the shell (so an
// agent can use it via Bash). JSON output makes results machine-parseable.
//
// Embedder endpoint (-embed): empty = deterministic mock; "http://host:port" or
// "unix:/path" = external embedding service (py/embed_server.py).
//
// IMPORTANT: a given -dir must always be used with the SAME embedder; mock and
// real embeddings live in different vector spaces and must not be mixed.
package main

import (
	"fmt"
	"os"
)

func main() {
	if len(os.Args) < 2 {
		usage()
		os.Exit(2)
	}
	cmd, args := os.Args[1], os.Args[2:]
	var err error
	switch cmd {
	case "run-scenario":
		os.Exit(runScenario(args))
	case "embed-ping":
		err = embedPing(args)
	case "gen-scenario":
		err = genScenario(args)
	case "ingest":
		err = ingestCmd(args)
	case "create-scope":
		err = createScope(args)
	case "fork":
		err = fork(args)
	case "consolidate":
		err = consolidate(args)
	case "crossversion":
		err = crossversion(args)
	case "rollup":
		err = rollupCmd(args)
	case "siblings":
		err = siblings(args)
	case "ancestors":
		err = ancestors(args)
	case "push":
		err = push(args)
	case "drill":
		err = drill(args)
	case "publish":
		err = publish(args)
	case "query":
		err = query(args)
	case "traces":
		err = traces(args)
	case "trace":
		err = traceCmd(args)
	default:
		usage()
		os.Exit(2)
	}
	if err != nil {
		fmt.Fprintln(os.Stderr, "error:", err)
		os.Exit(1)
	}
}

func usage() {
	fmt.Fprintln(os.Stderr, `usage: ioc <command> [flags]

commands:
  run-scenario <file.json> [-dir d] [-embed e] [-mode m]  run a scripted eval scenario
  gen-scenario -out F [-n N] [-clusters C] [-shape flat|tree]  generate a scale scenario
  embed-ping [-embed e]                          check the embedding service
  create-scope [-parent ID] -role R -title T     create a scope
  push -scope ID -summary S [-kind K] [-content X|-content-file F] [-publish]
  ingest <path> [-scope ID] [-title T] [-maxchars N] [-overlap N]  mirror a dir tree into scopes; chunk files as documents
  query -scope ID -text T [-detail overview|entry|raw] [-topk N] [-tier t] [-kind k] [-mode m]
  drill -artifact ID [-detail raw]
  publish -artifact ID
  siblings -scope ID
  ancestors -scope ID
  fork -scope ID -title T
  consolidate -scope ID -summary S
  crossversion -scope ID -constraints C -lessons L
  rollup -scope ID -summary S
  traces [-n N]                                  list recent query traces
  trace -query ID

common flags: -dir (default `+defaultDataDir+`)  -embed (empty=mock; http://host:port or unix:/path)`)
}
