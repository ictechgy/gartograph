package cli

import (
	"regexp"
	"strings"
	"testing"
)

// usageCommands는 Run이 받는 하위 명령이다. version은 플래그가 없어 뺀다.
var usageCommands = []string{
	"graph", "cycles", "dead", "rules", "query", "impact", "reach", "path", "shared", "diff",
	"metrics", "mapping", "init", "bridges", "schema", "routes", "unused-deps", "mcp",
}

// flagDefinition은 flag 패키지가 `-h`에 찍는 플래그 정의 줄이다(`  -name type` 또는 `  -name`).
var flagDefinition = regexp.MustCompile(`(?m)^  -([a-z][a-z0-9-]*)`)

// TestUsageListsEveryFlag는 개요 도움말(gartograph help)이 각 명령이 실제로 받는 플래그를 빠짐없이 싣는지
// 본다. 플래그는 그 명령의 개요 줄에 있거나, 개요 줄이 [flags]를 달고 공통 수확 플래그 목록에 있으면 실린
// 것이다. 받는 플래그 목록은 명령의 `-h`가 flag 패키지로 찍는 정의에서 읽는다 — 손으로 쓴 개요가 코드와
// 갈라져 README에는 있는 dead --algo, 공통 --tags, mcp --level·--config, routes --tags가 개요에 없었다.
func TestUsageListsEveryFlag(t *testing.T) {
	var b strings.Builder
	usage(&b)
	head, harvest, ok := strings.Cut(b.String(), "\nHarvest flags")
	if !ok {
		t.Fatalf("usage has no harvest flags block:\n%s", b.String())
	}
	// 목록은 첫 빈 줄까지다 — 뒤의 안내 문단(ID 명령의 --level 기본값)이 플래그를 실은 것으로 셈되면 안 된다.
	harvest, _, _ = strings.Cut(harvest, "\n\n")
	synopses := usageSynopses(head)
	for _, cmd := range usageCommands {
		synopsis, ok := synopses[cmd]
		if !ok {
			t.Errorf("usage has no synopsis line for %q", cmd)
			continue
		}
		_, _, errb := run(t, cmd, "-h")
		if !strings.Contains(errb, "Usage of "+cmd+":") {
			t.Fatalf("%s -h printed no flag definitions:\n%s", cmd, errb)
		}
		for _, m := range flagDefinition.FindAllStringSubmatch(errb, -1) {
			flag := "--" + m[1]
			if mentionsFlag(synopsis, flag) {
				continue
			}
			if strings.Contains(synopsis, "[flags]") && mentionsFlag(harvest, flag) {
				continue
			}
			t.Errorf("usage does not list %s %s (add it to the synopsis, or to the harvest flags block "+
				"for a command whose synopsis ends in [flags])", cmd, flag)
		}
	}
}

// usageSynopses는 개요의 명령 줄을 명령별로 모은다 — `  gartograph <cmd>`로 시작하는 줄과 그 뒤에 더 깊이
// 들여쓴 이어지는 줄이다. impact처럼 개요 줄이 여럿인 명령은 전부 합친다.
func usageSynopses(text string) map[string]string {
	out := map[string]string{}
	cmd := ""
	for _, line := range strings.Split(text, "\n") {
		if rest, ok := strings.CutPrefix(line, "  gartograph "); ok {
			if fields := strings.Fields(rest); len(fields) > 0 {
				cmd = fields[0]
				out[cmd] += rest + "\n"
			}
			continue
		}
		if cmd != "" && strings.HasPrefix(line, "    ") {
			out[cmd] += line + "\n"
			continue
		}
		cmd = ""
	}
	return out
}

// mentionsFlag는 text가 flag를 온전한 토큰으로 담는지 본다 — `--root`가 `--roots-from` 안에서,
// `--baseline`이 `--write-baseline` 안에서 찾아지면 안 된다.
func mentionsFlag(text, flag string) bool {
	re := regexp.MustCompile(`(^|[^a-z0-9-])` + regexp.QuoteMeta(flag) + `($|[^a-z0-9-])`)
	return re.MatchString(text)
}
