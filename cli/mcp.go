// MCP stdio 서버 — 코딩 에이전트가 프로세스를 띄워 그래프를 되묻는 통로다.
// 전송은 개행 구분 JSON-RPC 2.0(MCP stdio 전송 규약)이고, 문서는 기동 시
// 한 번 수확해 모든 도구 호출이 같은 스냅샷 위에서 답한다 — 호출마다
// 재수확하면 같은 세션 안에서 그래프가 흔들린다.
package cli

import (
	"bufio"
	"encoding/json"
	"fmt"
	"io"
	"os"

	"github.com/ictechgy/gartograph/analysis"
	"github.com/ictechgy/gartograph/config"
	"github.com/ictechgy/gartograph/graph"
)

// rpcRequest는 들어오는 JSON-RPC 메시지다. ID가 비어 있으면 알림이다 —
// 알림에는 응답을 돌려보내지 않는다.
type rpcRequest struct {
	JSONRPC string          `json:"jsonrpc"`
	ID      json.RawMessage `json:"id"`
	Method  string          `json:"method"`
	Params  json.RawMessage `json:"params"`
}

// rpcResponse는 JSON-RPC 응답이다. Result와 Error는 상호 배타적이다.
type rpcResponse struct {
	JSONRPC string          `json:"jsonrpc"`
	ID      json.RawMessage `json:"id"`
	Result  any             `json:"result,omitempty"`
	Error   *rpcError       `json:"error,omitempty"`
}

// rpcError는 JSON-RPC 오류 객체다.
type rpcError struct {
	Code    int    `json:"code"`
	Message string `json:"message"`
}

// toolCallParams는 tools/call의 params 형식이다.
type toolCallParams struct {
	Name      string          `json:"name"`
	Arguments json.RawMessage `json:"arguments"`
}

// mcpServer는 수확된 문서 하나를 서빙한다.
type mcpServer struct {
	doc      *graph.Document
	dir      string
	cfgPath  string
	protocol string
}

// cmdMcp는 MCP stdio 서버를 띄운다. EOF까지 stdin을 읽는다.
func cmdMcp(args []string, stdout, stderr io.Writer) int {
	return mcpRun(args, os.Stdin, stdout, stderr)
}

// mcpRun은 stdin을 파라미터로 받아 테스트에서 파이프 없이 서버를 구동한다.
func mcpRun(args []string, in io.Reader, stdout, stderr io.Writer) int {
	fs, opts, graphPath := flagSet("mcp", stderr)
	level := fs.String("level", string(graph.LevelSymbol), "harvest level: package|type|symbol")
	configPath := fs.String("config", "", "rules file for the rules tool (default: .gartograph.yml in --dir)")
	if fs.Parse(args) != nil {
		return 2
	}
	lvl, err := graph.ParseLevel(*level)
	if err != nil {
		return fail(stderr, err)
	}
	opts.Level = lvl
	doc, err := loadDoc(opts, *graphPath)
	if err != nil {
		return fail(stderr, err)
	}
	cfg := *configPath
	if cfg == "" {
		if found, ok := config.Find(opts.Dir); ok {
			cfg = found
		}
	}
	srv := &mcpServer{doc: doc, dir: opts.Dir, cfgPath: cfg, protocol: "2024-11-05"}
	return srv.serve(in, stdout, stderr)
}

// serve는 EOF까지 한 줄씩 JSON-RPC를 처리한다.
// 한 메시지가 최대 스캔 크기(64KiB)를 넘을 수 있어 버퍼를 키운다.
func (s *mcpServer) serve(in io.Reader, out, errOut io.Writer) int {
	sc := bufio.NewScanner(in)
	sc.Buffer(make([]byte, 0, 64*1024), 8*1024*1024)
	w := bufio.NewWriter(out)
	for sc.Scan() {
		line := sc.Bytes()
		if len(line) == 0 {
			continue
		}
		resp := s.handle(line)
		if resp == nil {
			continue
		}
		data, err := json.Marshal(resp)
		if err != nil {
			fmt.Fprintf(errOut, "mcp: encoding response: %v\n", err)
			continue
		}
		w.Write(data)
		w.WriteByte('\n')
		w.Flush()
	}
	if err := sc.Err(); err != nil {
		fmt.Fprintf(errOut, "mcp: reading stdin: %v\n", err)
		return 2
	}
	return 0
}

// handle은 메시지 하나를 디스패치한다. 알림은 nil을 돌려 응답을 생략한다.
func (s *mcpServer) handle(line []byte) *rpcResponse {
	var req rpcRequest
	if err := json.Unmarshal(line, &req); err != nil {
		return &rpcResponse{JSONRPC: "2.0", Error: &rpcError{
			Code: -32700, Message: "parse error: " + err.Error()}}
	}
	notify := len(req.ID) == 0
	reply := func(result any, rpcErr *rpcError) *rpcResponse {
		if notify {
			return nil
		}
		return &rpcResponse{JSONRPC: "2.0", ID: req.ID, Result: result, Error: rpcErr}
	}
	switch req.Method {
	case "initialize":
		var p struct {
			Protocol string `json:"protocolVersion"`
		}
		_ = json.Unmarshal(req.Params, &p)
		if p.Protocol != "" {
			s.protocol = p.Protocol
		}
		return reply(map[string]any{
			"protocolVersion": s.protocol,
			"capabilities":    map[string]any{"tools": map[string]any{}},
			"serverInfo":      map[string]any{"name": "gartograph", "version": Version},
		}, nil)
	case "ping":
		return reply(map[string]any{}, nil)
	case "tools/list":
		return reply(map[string]any{"tools": mcpTools()}, nil)
	case "tools/call":
		var p toolCallParams
		if err := json.Unmarshal(req.Params, &p); err != nil {
			return reply(nil, &rpcError{Code: -32602, Message: "invalid params"})
		}
		return reply(s.callTool(p), nil)
	default:
		if notify {
			return nil
		}
		return reply(nil, &rpcError{Code: -32601, Message: "method not found: " + req.Method})
	}
}

// callTool은 도구 하나를 실행해 MCP 도구 결과를 만든다.
// 도구 안의 실패는 프로토콜 오류가 아니라 isError 콘텐츠다 — 호출 자체는
// 성립했으므로 클라이언트에게 결과물로 돌려준다.
func (s *mcpServer) callTool(p toolCallParams) map[string]any {
	text, err := s.runTool(p.Name, p.Arguments)
	result := map[string]any{
		"content": []map[string]any{{"type": "text", "text": text}},
	}
	if err != nil {
		result["isError"] = true
		result["content"] = []map[string]any{{"type": "text", "text": err.Error()}}
	}
	return result
}

// runTool은 도구별로 분석을 실행하고 JSON 문자열을 돌려준다.
// 반환 형식은 CLI의 JSON 출력과 같다 — 같은 계약을 두 통로가 공유한다.
func (s *mcpServer) runTool(name string, args json.RawMessage) (string, error) {
	marshal := func(v any) (string, error) {
		b, err := json.MarshalIndent(v, "", "  ")
		return string(b), err
	}
	switch name {
	case "gartograph_summary":
		return marshal(map[string]any{
			"version": s.doc.Version, "level": s.doc.Level, "module": s.doc.Module,
			"root": s.doc.Root, "roots": s.doc.Roots,
			"vertices": len(s.doc.Vertices), "edges": len(s.doc.Edges),
			"limitations": s.doc.Limitations,
		})
	case "gartograph_query":
		var a struct {
			ID    string `json:"id"`
			Depth int    `json:"depth"`
			Max   int    `json:"max"`
		}
		if err := json.Unmarshal(args, &a); err != nil || a.ID == "" {
			return "", fmt.Errorf("query needs an \"id\" argument")
		}
		res, err := analysis.Query(s.doc, a.ID, a.Depth, a.Max)
		if err != nil {
			return "", err
		}
		return marshal(res)
	case "gartograph_impact":
		var a struct {
			ID    string `json:"id"`
			Depth int    `json:"depth"`
			Max   int    `json:"max"`
		}
		if err := json.Unmarshal(args, &a); err != nil || a.ID == "" {
			return "", fmt.Errorf("impact needs an \"id\" argument")
		}
		res, err := analysis.FindImpact(s.doc, a.ID, a.Depth, a.Max)
		if err != nil {
			return "", err
		}
		return marshal(res)
	case "gartograph_path":
		var a struct {
			From string `json:"from"`
			To   string `json:"to"`
		}
		if err := json.Unmarshal(args, &a); err != nil || a.From == "" || a.To == "" {
			return "", fmt.Errorf("path needs \"from\" and \"to\" arguments")
		}
		res, err := analysis.Path(s.doc, a.From, a.To)
		if err != nil {
			return "", err
		}
		return marshal(res)
	case "gartograph_cycles":
		var a struct {
			Level string `json:"level"`
		}
		_ = json.Unmarshal(args, &a)
		lvl := s.doc.Level
		if a.Level != "" {
			parsed, err := graph.ParseLevel(a.Level)
			if err != nil {
				return "", err
			}
			lvl = parsed
		}
		view, err := s.doc.View(lvl)
		if err != nil {
			return "", err
		}
		return marshal(analysis.Cycles(view))
	case "gartograph_dead":
		var a struct {
			RetainPublic bool     `json:"retainPublic"`
			Roots        []string `json:"roots"`
		}
		_ = json.Unmarshal(args, &a)
		roots, unknown := analysis.RetentionRoots(s.doc, a.RetainPublic, a.Roots)
		reachable := analysis.Reachable(s.doc, roots)
		findings := analysis.Dead(s.doc, reachable)
		return marshal(deadReport{
			Roots: roots, UnknownRoots: unknown,
			Unreachable: findings, Limitations: s.doc.Limitations,
		})
	case "gartograph_rules":
		if s.cfgPath == "" {
			return "", fmt.Errorf("no .gartograph.yml found in %s", s.dir)
		}
		cfg, err := config.Load(s.cfgPath)
		if err != nil {
			return "", err
		}
		rep := analysis.CheckRules(s.doc, cfg)
		return marshal(rulesReport{
			Violations: rep.Violations, Unmapped: rep.Unmapped,
			UnmappedExternal: rep.UnmappedExternal,
			UnmatchedComponents: rep.UnmatchedComponents,
			Limitations: s.doc.Limitations,
		})
	case "gartograph_metrics":
		// metrics는 설정이 선택이다 — 없으면 패키지 단위로 계산한다.
		var cfg *config.File
		var limitations []string
		if s.cfgPath != "" {
			loaded, err := config.Load(s.cfgPath)
			if err != nil {
				return "", err
			}
			cfg = loaded
		} else {
			limitations = append(limitations,
				"no .gartograph.yml found; metrics computed per package")
		}
		rep := analysis.Metrics(s.doc, cfg)
		return marshal(struct {
			*analysis.MetricsReport
			Limitations []string `json:"limitations,omitempty"`
		}{rep, append(limitations, s.doc.Limitations...)})
	case "gartograph_mapping":
		if s.cfgPath == "" {
			return "", fmt.Errorf("no .gartograph.yml found in %s", s.dir)
		}
		cfg, err := config.Load(s.cfgPath)
		if err != nil {
			return "", err
		}
		return marshal(analysis.MapComponents(s.doc, cfg))
	default:
		return "", fmt.Errorf("unknown tool %q — see tools/list", name)
	}
}

// mcpTools는 서빙하는 도구 목록과 입력 스키마다.
func mcpTools() []map[string]any {
	obj := func(props map[string]any, required []string) map[string]any {
		return map[string]any{"type": "object", "properties": props, "required": required}
	}
	str := func(desc string) map[string]any {
		return map[string]any{"type": "string", "description": desc}
	}
	num := func(desc string) map[string]any {
		return map[string]any{"type": "integer", "description": desc}
	}
	idProp := map[string]any{"id": str("vertex ID, e.g. example.com/mod/pkg.(T).Method")}
	depthProp := map[string]any{"depth": num("max depth; 0 = full transitive closure")}
	return []map[string]any{
		{"name": "gartograph_summary",
			"description": "Document metadata: level, module, vertex/edge counts, limitations",
			"inputSchema": obj(map[string]any{}, nil)},
		{"name": "gartograph_query",
			"description": "Bidirectional neighbors of a vertex: what it depends on and what depends on it",
			"inputSchema": obj(map[string]any{
				"id": idProp["id"], "depth": num("neighbor depth (default 1)"), "max": num("max neighbors per direction"),
			}, []string{"id"})},
		{"name": "gartograph_impact",
			"description": "Reverse transitive closure: what breaks if this vertex changes",
			"inputSchema": obj(map[string]any{
				"id": idProp["id"], "depth": depthProp["depth"], "max": num("max dependers"),
			}, []string{"id"})},
		{"name": "gartograph_path",
			"description": "Shortest dependency path between two vertices — why does 'from' reach 'to'",
			"inputSchema": obj(map[string]any{
				"from": str("source vertex ID"), "to": str("target vertex ID"),
			}, []string{"from", "to"})},
		{"name": "gartograph_cycles",
			"description": "Dependency cycles at a level (default: the document's level)",
			"inputSchema": obj(map[string]any{
				"level": str("module|package|type|symbol"),
			}, nil)},
		{"name": "gartograph_dead",
			"description": "Symbols unreachable from retention roots — graph facts, not delete verdicts",
			"inputSchema": obj(map[string]any{
				"retainPublic": map[string]any{"type": "boolean",
					"description": "retain all exported symbols (use for libraries)"},
				"roots": map[string]any{"type": "array", "items": map[string]any{"type": "string"},
					"description": "extra retention root vertex IDs"},
			}, nil)},
		{"name": "gartograph_rules",
			"description": "Check layer rules from .gartograph.yml; returns violations and unmapped packages",
			"inputSchema": obj(map[string]any{}, nil)},
		{"name": "gartograph_metrics",
			"description": "Coupling metrics per component (or per package without config): Ca, Ce, instability, orphans",
			"inputSchema": obj(map[string]any{}, nil)},
		{"name": "gartograph_mapping",
			"description": "Show how packages resolve to components: mapping, unmapped, unmatched components",
			"inputSchema": obj(map[string]any{}, nil)},
	}
}
