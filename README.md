# gartograph

<img src="icon.png" alt="gartograph's bird mascot" width="112" height="112" align="right">

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
brew install ictechgy/tap/gartograph
# or
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
gartograph graph --level type --format mermaid    # or --format dot for Graphviz
gartograph graph --level symbol --out .gartograph/graph.json

# Detect dependency cycles — package cycles are impossible in Go,
# so real checks run at type/symbol level
gartograph cycles --level symbol --strict

# Report symbols unreachable from retention roots (main, init)
# Symbol level includes struct fields — unreferenced members are reported
# with kind "field" (see limitations for reflection/serialization blind spots)
gartograph dead                           # symbol level, always
gartograph dead --retain-public           # libraries: keep exported API
gartograph dead --root my/pkg.Setup       # extra retention root
gartograph dead --explain my/pkg.F        # why alive? show a reachability path
gartograph dead --algo rta                # RTA precision: needs source, not --graph
gartograph dead --algo rta --explain my/pkg.F   # path on the RTA call graph itself
# Source-level keep: //deadcode:keep or //gartograph:keep on a declaration
# (also on a struct field) marks it as a retention root in the document.

# Emit an isthmus bridge-facts document (platform "go")
# Go reports cgo via unscanned-ffi-interop limitations — no channel facts.
gartograph bridges --out go-facts.json

# Check layer rules from .gartograph.yml
gartograph rules --strict

# Ask about one vertex: what it uses, what uses it (JSON for agents)
gartograph query github.com/ictechgy/gartograph/cli --depth 2

# Reverse transitive closure: what breaks if this vertex changes
gartograph impact github.com/ictechgy/gartograph/graph --depth 2

# Diff-aware impact: what breaks given the files changed since a revision
gartograph impact --since origin/main...HEAD          # or --files cli/cli.go

# Shortest dependency path: why does 'from' reach 'to'
gartograph path github.com/ictechgy/gartograph/cmd/gartograph github.com/ictechgy/gartograph/graph

# Shared reachables: what do two roots both pull in (and each alone)
gartograph shared example.com/a example.com/b

# go.mod hygiene: requirements no package imports (test imports count)
gartograph unused-deps --strict

# Compare two saved documents: structure drift and breaking signals
# breaking = exported symbol removed/unexported, kind changed, signature
# reference dropped, interface gained a method, struct field contract
# broken, exported const value changed
gartograph diff old.json new.json --strict

# Coupling metrics (Ca/Ce/instability) + abstractness (A) and
# main-sequence distance (D = |A+I-1|) + orphan packages.
# Harvested at type level so A has a denominator; a package-level saved
# document omits A/D and says so in limitations.
gartograph metrics                # component-level when .gartograph.yml exists
gartograph mapping                # how packages resolve to components

# Scaffold .gartograph.yml — deps mirror observed imports, so rules pass clean
gartograph init

# Serve the harvested document to agents over MCP stdio
gartograph mcp --level symbol

# Query a saved document instead of re-harvesting
gartograph cycles --graph .gartograph/graph.json --level type
gartograph dead   --graph .gartograph/graph.json
```

Harvest flags (all analysis commands):

| Flag | Default | Meaning |
|---|---|---|
| `--dir` | `.` | module root to analyze |
| `--pattern` | `./...` | package pattern (repeatable) |
| `--tests` | off | include test variant packages (Test/Benchmark/Example/Fuzz become retention roots) |
| `--deps` | off | include dependency packages/modules outside the main module |
| `--tags` | — | build tags for the loader; files excluded by constraints are counted in `limitations` |
| `--goos`/`--goarch` | host | harvest for another target platform; a `limitations` note records the choice |
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

Two optional sections narrow it further:

```yaml
deny:                     # hard bans — beat deps entries
  analysis: ["cli"]       # analysis must never reach back into cli
  web:                    # entries may carry a reason for the reader
    - {to: db, reason: "go through internal/store instead"}
signature:                # public-API type leakage (needs symbol level)
  api:      ["core"]      # api's exported signatures may only name core types
```

- `deny` wins over `deps` — "usually allowed, but this pair is forbidden".
- `signature` checks `signature` edges of **exported** symbols: a component's
  public API may only reference types from listed components, even when body
  dependencies are allowed. Violations carry `rule: "deny"|"signature"|"allow"`.

More optional sections widen the contract vocabulary:

```yaml
common: ["core"]          # every component may depend on these, unlisted
visibleTo:                # provider-side rule: who may depend on me
  db: ["store"]           # only the store component may import db
forbidden:                # transitive bans — no path at all, direct or not
  - {from: api, to: db}   # api must not reach db even via other components
independent: [web, cli]   # web and cli must not reach each other either way
```

- `common` removes allowlist boilerplate for shared components.
- `visibleTo` is the mirror of `deps`: `deps` says what *I* may use,
  `visibleTo` says who may use *me*. Both are allowlists — `visibleTo`
  only narrows, never widens. Violations carry `rule: "visibleTo"`.
- `forbidden` checks reachability, not just direct edges — deps can only
  see direct imports. A violation reports one witness `path`.
- `independent` is a bidirectional `forbidden` between every listed pair —
  the name keeps the intent. Violations carry `rule: "independence"`.
- `stability: true` enforces the stable-dependencies direction —
  dependency-cruiser's `moreUnstable`. A component may not depend on a
  component with higher instability (I = Ce/(Ca+Ce), the same number
  `metrics` reports). Violations carry `rule: "stability"` and both
  instability values in the reason.
- `limits` caps component size: `maxOut` bounds how many *other* components
  it may depend on (Ce), `maxIn` bounds how many may depend on it (Ca) —
  dependency-cruiser's `max-dependencies` contract. Exceeding a limit is a
  component-level fact, not an edge violation — violations carry
  `rule: "limit"`, `name: "maxIn"|"maxOut"`, and the observed count.
- `exclude` is a harvest-time filter, not a rule: packages matching its
  patterns (same glob semantics as components — module-relative for the
  main module, full import path otherwise) never become vertices, and
  imports to them are counted in `limitations`. Use it for generated or
  vendored trees:
- `fileRules` scopes a ban to the *file* where the import happens —
  dependency-cruiser's `not-to-dev-dep` contract. `from` is a glob matched
  against the file basename, or the module-relative path when it contains
  `/`; a leading `!` inverts it. Violations carry `rule: "fileScope"`, the
  rule `name`, and the offending position. Edges without positions can't
  be checked and are counted in `fileScopeUnchecked`.
- `deny` entries accept a `reason` — it lands on the violation so the
  reader knows what to do instead.
- Every name referenced by a rule must be a defined component — `Load`
  rejects dead references instead of letting a typo pretend to be a rule.

```yaml
fileRules:
  - name: no-testdeps-in-prod       # production files may not import test helpers
    from: "!*_test.go"              # files NOT matching the glob violate
    to: testhelpers                 # a component
    reason: "test helpers must not leak into production code"

stability: true                     # deps must point toward more stable components
limits:                             # size caps, not direction
  - {component: web, maxOut: 4}     # web may depend on at most 4 components
  - {component: db, maxIn: 2}       # at most 2 components may depend on db
exclude: [gen/**, testdata/**]      # these packages are never harvested
```

**External/vendor rules.** Component patterns also match the full import
paths of external packages harvested with `--deps`, so `deps`/`deny` can
gate third-party modules:

```yaml
components:
  store: ["internal/store/**"]
  aws:   ["github.com/aws/**"]     # matches external vertices under --deps
deps:
  store: ["core", "aws"]           # only the store layer may import AWS
```

Run `rules --deps` so external vertices exist. A component pattern matching
zero packages is reported in `unmatchedComponents` — the signal that an
external pattern ran without `--deps` (or the pattern is stale). External
packages matching no component are reported separately as
`unmappedExternal`.

**Baseline.** Adopting rules on an existing repo: record today's violations
once, then only new violations fail `--strict`. The same flags work on
`cycles` and `dead` — each kind carries its own baseline file kind, so a
file can't silently cross-apply.

```bash
gartograph rules  --write-baseline .gartograph-baseline.json
gartograph rules  --baseline .gartograph-baseline.json --strict
gartograph cycles --level symbol --write-baseline .cycles-baseline.json
gartograph dead   --baseline .dead-baseline.json --strict
```

Baselined violations are reported under `baselined`; entries that stop
occurring come back as `staleBaseline` — regenerate when they pile up.
SARIF output and `--strict` see only fresh violations.

Patterns: exact match, `x/**` recursive prefix, `*` segment glob. Packages
matching no component are reported as `unmapped` — a mapping gap is "rules
don't know this area", not "no rules apply".

`rules`, `cycles`, and `dead` all accept `--format sarif` for CI code
scanning — cycles report as `dependency-cycle` errors, unreachable symbols
as `unreachable-symbol` warnings (a fact, not a deletion verdict).

## Output contract (for agents)

- Deterministic JSON: sorted keys, sorted arrays — same input, same bytes.
- `query` reports `depth`, `truncated`, and every edge kind between
  neighbors (`edges: ["call", "implements"]`).
- `dead` reports `state` + `reason` + `exported` per finding and always
  includes the `roots` it used — reachability depends on them. `exported`
  is the triage axis: an unexported unreachable symbol is confined to the
  repo, while an exported one may have external callers, reflection, or
  plugin use — classify on the fact, the tool does not grade confidence.
- `dead` also reports struct fields (`kind: "field"`) — a field with no
  named access (`x.F`, `T{F: v}`, positional literal, promotion path,
  `==`) is unreachable. Reflection, serialization, and whole-struct copies
  are invisible — the report carries that limitation when fields appear.
- `//deadcode:keep` and `//gartograph:keep` on a declaration (function,
  method, var, const, type, or struct field) mark it as a retention root
  in `roots` — the keep intent lives next to the declaration, not in a
  CLI flag. The marker must be the first token of its comment line
  (`//deadcode:keep` or `// deadcode:keep <reason>`); put it on the doc
  comment above the declaration or on a field line inside a struct —
  a trailing comment after a `func` body does not attach to it.
- `limitations` is counted per run (omitted external imports/references,
  `reflect` use, `//go:linkname`, packages without type info) — absent
  means nothing to report, not a boilerplate warning.
- No delete verdicts. `unreachable` is a graph fact ("not reachable from
  retention roots"), never "safe to delete". Methods satisfying interfaces
  declared outside the module are a known blind spot — the report says so
  when it applies.
- Optional fields are omitted when empty (`omitempty`).

## Graph document

`version: 2`, `tool: "gartograph"`, `level`, `root` (filesystem dir),
`module` (module path), `roots` (harvested retention roots: `main`, `init`,
plus `Test*`/`Benchmark*`/`Example*`/`Fuzz*` entry points under `--tests`),
`vertices`, `edges`, `limitations`.

Edges carry `positions` — every source site where the relation holds
(import decls for `import`, call expressions for `call`, and so on).
Relations without a single site (`contains`, `implements`, module edges)
omit it. `dead --algo rta` swaps the harvested CHA edges for SSA-based
rapid type analysis — narrower, source-only, and it under-approximates:
the report says so in `limitations`.

Vertex IDs: `pkg/path` for packages, `pkg/path.Name` for package-level
symbols, `pkg/path.(Recv).Name` for methods. Vertex `kind`:
`module`/`package`/`type`/`func`/`method`/`var`/`const`. Vertices carry
`generated: true` when they come from files marked
`// Code generated ... DO NOT EDIT.` — marked, never hidden. Type vertices
also carry `interface: true` or `fields` (declared `"name:Type"` list) so
`diff` can classify interface method additions and struct-field contract
breaks as breaking. Edge `kind`:
`import`/`contains`/`embeds`/`implements`/`references`/`call`/`signature`
(signature = type references inside declaration signatures; a dependency
edge, unlike `contains` which is ownership).

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

## MCP server

`gartograph mcp` serves the harvested document over MCP stdio
(newline-delimited JSON-RPC): `gartograph_summary`, `gartograph_query`,
`gartograph_impact`, `gartograph_path`, `gartograph_cycles`,
`gartograph_dead`, `gartograph_rules`, `gartograph_metrics`,
`gartograph_mapping`. The document is harvested once at startup so every
tool call answers over the same snapshot. Example client config:

```json
{"mcpServers": {"gartograph": {
  "command": "gartograph",
  "args": ["mcp", "--dir", "/path/to/repo", "--level", "symbol"]}}}
```

## Roadmap

- ~~Symbol/type level harvest~~ — done via `go/packages` + `go/types` + AST
- ~~`dead`, `rules`, persisted `graph.json`~~ — done
- ~~Module-level graph~~ — done (go.work workspaces; `--deps` adds dependency modules)
- ~~Verification scripts + CI~~ — `Scripts/coverage.sh`, `Scripts/verify-cli-contract.sh`
- ~~Homebrew tap~~ — `brew install ictechgy/tap/gartograph`
- ~~`impact`, `deny`/`signature` rules, SARIF, MCP, `--tags`, generated marking~~ — done
- ~~`path`, `diff`, `impact --since/--files`, rules baseline, external
  (vendor) rules, `--format dot`~~ — done
- ~~`visibleTo`, `common`, transitive `forbidden`, deny reasons, `metrics`,
  `mapping`, `init`~~ — done
- ~~isthmus bridge-facts producer~~ — `bridges` emits platform `go` docs;
  cgo observations are `unscanned-ffi-interop` limitations per the v1 contract
- ~~RTA~~ — `dead --algo rta` (opt-in; Andersen pointer analysis deferred)
- ~~Edge positions (schema v2)~~, ~~test-variant deduplication~~ — done
- ~~`fileRules`, cycles/dead SARIF + baselines, deeper `diff` breaking
  classification~~ — done

## License

MIT — see [LICENSE](LICENSE).
