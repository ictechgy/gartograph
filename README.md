# gartograph

Go dependency graph tool — read a Go module, build its dependency graph,
and run queries on top: cycles, reachability, symbol neighbors, layer rules.

The design in one sentence: **the graph is the artifact; everything else is
a query over it.**

[한국어](README.ko.md)

Sibling projects: [cartograph](https://github.com/ictechgy/cartograph) (Swift) ·
kartograph (Kotlin/Android) · dartograph (Dart/Flutter) ·
[schemagraph](https://github.com/ictechgy/schemagraph) (databases) ·
isthmus (cross-language joins).

## Why

Go already has `deadcode`, `goda`, `go-arch-lint`, and `go-callvis` — but each
answers one kind of question with its own output shape. gartograph instead
builds **one versioned graph** at four levels (module/package/type/symbol) and
expresses every analysis as a query over it, with a deterministic JSON contract
meant for coding agents: facts and evidence, never delete verdicts.

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
# Emit the dependency graph (deterministic JSON)
gartograph graph                          # package level
gartograph graph --level symbol           # call/implements/embeds/references
gartograph graph --level module           # modules (go.work workspaces)
gartograph graph --level type --format mermaid
gartograph graph --level symbol --out .gartograph/graph.json

# Detect dependency cycles — package cycles are impossible in Go,
# so real checks run at type/symbol level
gartograph cycles --level symbol --strict

# Report symbols unreachable from retention roots (main, init)
gartograph dead                           # symbol level, always
gartograph dead --retain-public           # libraries: keep exported API
gartograph dead --root my/pkg.Setup       # extra retention root
gartograph dead --explain my/pkg.F        # why alive? show a reachability path

# Check layer rules from .gartograph.yml
gartograph rules --strict

# Ask about one vertex: what it uses, what uses it (JSON for agents)
gartograph query github.com/ictechgy/gartograph/cli --depth 2

# Query a saved document instead of re-harvesting
gartograph cycles --graph .gartograph/graph.json --level type
gartograph dead   --graph .gartograph/graph.json
```

Harvest flags (all analysis commands):

| Flag | Default | Meaning |
|---|---|---|
| `--dir` | `.` | module root to analyze |
| `--pattern` | `./...` | package pattern (repeatable) |
| `--tests` | off | include test variant packages |
| `--deps` | off | include dependency packages/modules outside the main module |
| `--graph` | — | read a saved graph document instead of harvesting |

Exit codes: `0` ok · `1` `--strict` violation · `2` usage/analysis error.

## Rules configuration

`gartograph rules` reads `.gartograph.yml` (or `.gartograph.yaml`) at the
module root. Components map module-relative package paths; `deps` is an
**allowlist** — a component with no entry may depend on nothing outside
itself.

```yaml
components:
  cli:      ["cli"]
  analysis: ["analysis"]
  core:     ["graph"]
deps:
  cli:      ["analysis", "core"]
  analysis: ["core"]
  core:     []
```

Patterns: exact match, `x/**` recursive prefix, `*` segment glob. Packages
matching no component are reported as `unmapped` — a mapping gap is "rules
don't know this area", not "no rules apply".

## Output contract (for agents)

- Deterministic JSON: sorted keys, sorted arrays — same input, same bytes.
- `query` reports `depth`, `truncated`, and every edge kind between
  neighbors (`edges: ["call", "implements"]`).
- `dead` reports `state` + `reason` per finding and always includes the
  `roots` it used — reachability depends on them.
- `limitations` is counted per run (omitted external imports/references,
  `reflect` use, `//go:linkname`, packages without type info) — absent
  means nothing to report, not a boilerplate warning.
- No delete verdicts. `unreachable` is a graph fact ("not reachable from
  retention roots"), never "safe to delete". Methods satisfying interfaces
  declared outside the module are a known blind spot — the report says so
  when it applies.
- Optional fields are omitted when empty (`omitempty`).

## Graph document

`version: 1`, `tool: "gartograph"`, `level`, `root` (filesystem dir),
`module` (module path), `roots` (harvested retention roots: `main`, `init`),
`vertices`, `edges`, `limitations`.

Vertex IDs: `pkg/path` for packages, `pkg/path.Name` for package-level
symbols, `pkg/path.(Recv).Name` for methods. Vertex `kind`:
`module`/`package`/`type`/`func`/`method`/`var`/`const`. Edge `kind`:
`import`/`contains`/`embeds`/`implements`/`references`/`call`.

Interface calls get CHA fan-out: an edge to the interface method *and* to
every known implementation — over-approximation errs toward "alive" so
`dead` never reports reachable code.

## Architecture

```
go/packages ──> source ──> graph.Document ──> analysis ──> export ──> cli
                (harvest)   (pure domain)      (queries)     (json/mermaid)
```

- `graph` — pure domain, zero external deps. The artifact.
- `source` — the only package importing `golang.org/x/tools`. Harvests, never judges.
- `analysis` — queries over the document (SCC cycles, neighbors, reachability, rules).
- `config` — `.gartograph.yml` parsing (the only yaml.v3 importer).
- `export` — deterministic JSON, Mermaid, graph file I/O.
- `cli` — commands and the exit-code contract.

## Development

```bash
go test ./...                    # tests
go vet ./...                     # vet
Scripts/coverage.sh              # tests + 90% coverage gate
Scripts/verify-cli-contract.sh   # exit-code contract on a fixture module
```

Dogfooding — this repo's own `.gartograph.yml` encodes the layering:

```bash
go run ./cmd/gartograph rules --strict
go run ./cmd/gartograph cycles --level type --strict
go run ./cmd/gartograph cycles --level symbol --strict
go run ./cmd/gartograph dead
```

## Roadmap

- ~~Symbol/type level harvest~~ — done via `go/packages` + `go/types` + AST
- ~~`dead`, `rules`, persisted `graph.json`~~ — done
- ~~Module-level graph~~ — done (go.work workspaces; `--deps` adds dependency modules)
- ~~Verification scripts + CI~~ — `Scripts/coverage.sh`, `Scripts/verify-cli-contract.sh`
- Homebrew tap
- isthmus bridge-facts producer (cgo/gomobile boundary — open question)

## License

MIT — see [LICENSE](LICENSE).
