# gartograph

Go dependency graph tool — read a Go module, build its dependency graph,
and run queries on top: cycles, reachability, symbol neighbors, layer rules.

The design in one sentence: **the graph is the artifact; everything else is
a query over it.**

Sibling projects: [cartograph](https://github.com/ictechgy/cartograph) (Swift) ·
kartograph (Kotlin/Android) · dartograph (Dart/Flutter) ·
[schemagraph](https://github.com/ictechgy/schemagraph) (databases) ·
isthmus (cross-language joins).

## Status

Early scaffold. Package-level import graph with `graph`, `cycles`, and
`query` commands. Type/symbol levels (`call`/`implements`/`embeds` edges),
`dead`, and `rules` are on the roadmap — see [HANDOFF.md](HANDOFF.md).

## Install

```bash
go install github.com/ictechgy/gartograph/cmd/gartograph@latest
```

Or from source:

```bash
git clone https://github.com/ictechgy/gartograph.git
cd gartograph
go build -o gartograph ./cmd/gartograph
```

## Usage

```bash
# Emit the package-level dependency graph (deterministic JSON)
gartograph graph

# Mermaid flowchart
gartograph graph --format mermaid

# Detect dependency cycles (exit 1 with --strict when cycles exist)
gartograph cycles --strict

# Ask about one vertex: what does it use, what uses it (JSON for agents)
gartograph query github.com/ictechgy/gartograph/cli --depth 2
```

Common flags:

| Flag | Default | Meaning |
|---|---|---|
| `--dir` | `.` | module root to analyze |
| `--pattern` | `./...` | package pattern (repeatable) |
| `--tests` | off | include test variant packages |
| `--deps` | off | include dependencies outside the main module |

Exit codes: `0` ok · `1` `--strict` violation · `2` usage/analysis error.

## Output contract (for agents)

- Deterministic JSON: sorted keys, sorted arrays — same input, same bytes.
- `query` reports `depth`, `truncated`, and every edge kind between
  neighbors (`edges: ["call", "implements"]`).
- `limitations` is counted per run (omitted external imports, load errors) —
  absent means nothing to report, not a boilerplate warning.
- No delete verdicts. `unreachable` is a graph fact ("not reachable from
  retention roots"), never "safe to delete".
- Optional fields are omitted when empty (`omitempty`).

## Architecture

```
go/packages ──> source ──> graph.Document ──> analysis ──> export ──> cli
                (harvest)   (pure domain)      (queries)     (json/mermaid)
```

- `graph` — pure domain, zero external deps. The artifact.
- `source` — the only package importing `golang.org/x/tools`. Harvests, never judges.
- `analysis` — queries over the document (SCC cycles, neighbor walks).
- `export` — deterministic JSON and Mermaid.
- `cli` — commands and the exit-code contract.

## Roadmap

- Symbol/type level harvest via `go/packages` + `go/ssa` + RTA call graph
  (the `deadcode` approach) — `call`/`implements`/`embeds`/`references` edges
- `dead` — reachability from retention roots (`main`, `init`, exported API
  behind `retain_public`), reported as `state` + `reason`, not verdicts
- `rules` — layer/component dependency rules from config
- Persisted `.gartograph/graph.json` — since Go has no compiler index store,
  this document is the durable artifact
- isthmus bridge-facts producer (cgo/gomobile boundary — open question)

## License

MIT — see [LICENSE](LICENSE).
