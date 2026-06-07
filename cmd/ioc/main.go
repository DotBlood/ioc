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
	case "wall":
		os.Exit(runWall(args))
	case "embed-ping":
		err = embedPing(args)
	case "gen-scenario":
		err = genScenario(args)
	case "gen-wall":
		err = genWall(args)
	case "ingest":
		err = ingestCmd(args)
	case "config":
		err = configCmd(args)
	case "serve":
		err = serveCmd(args)
	case "runtime":
		err = runtimeCmd(args)
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
	case "supersede":
		err = supersede(args)
	case "relate":
		err = relate(args)
	case "related":
		err = related(args)
	case "history":
		err = history(args)
	case "query":
		err = query(args)
	case "calibrate":
		err = calibrate(args)
	case "neighbors":
		err = neighbors(args)
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
  wall <spec.json> [-dir d] [-embed e] [-out D] [-mode m] [-coarsek N] [-rerank]  reasoning-wall: build corpus, emit blind judge packets + gold
  calibrate -probe <spec.json> [-dir d] [-embed e] [-coverage 0.9] [-write]  derive per-embedder confidence floor from probe spec
  gen-wall -out F [-shape flat|tree] [-n N] [-distractor-clusters C]  generate a ~N-artifact wall spec (real seed + distractors)
  gen-scenario -out F [-n N] [-clusters C] [-shape flat|tree]  generate a scale scenario
  embed-ping [-embed e]                          check the embedding service
  create-scope [-parent ID] -role R -title T     create a scope
  push -scope ID -summary S [-kind K] [-content X|-content-file F] [-publish] [-supersedes id1,id2] [-relations kind:ID,...]
  supersede -old ID -by ID                       mark an artifact superseded by another (currency)
  relate -from ID -to ID [-kind K]               create an author-declared edge between artifacts
  related -artifact ID [-kind K,..] [-direction out|in|both] [-depth N]  walk the edge graph (e.g. what depends on X)
  history -artifact ID                           show an artifact's supersession chain
  ingest <path> [-scope ID] [-title T] [-maxchars N] [-overlap N]  mirror a dir tree into scopes; chunk files as documents (path must be inside IOC_INGEST_ROOT or cwd)
  config set-ingest-root <path> | get-ingest-root   pin/show the persisted ingest sandbox root
  config set <key> <value> | get <key>           persist/read a store config key (e.g. conf.rerank.<model> from calibrate)
  calibrate -probe spec.json [-rerank] [-coverage C] [-write]  derive a per-embedder confidence floor (cosine or rerank); print abstention FPR/FNR
  serve [-dir d] [-embed e]                      run the runtime daemon (single owner of -dir; clients connect via runtime.json)
  runtime status|stop|rotate|mint-read-token [-dir d]   inspect/stop daemon; rotate token(s); mint a read-only token
  query -scope ID -text T [-detail overview|entry|raw] [-topk N] [-tier t] [-kind k] [-mode m] [-rerank] [-rerank-n N] [-no-auto-rerank] [-graph-boost W] [-include-superseded]
  neighbors -scope ID -text T [-topk N]          similar CURRENT memory (run before push to find supersede candidates)
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
