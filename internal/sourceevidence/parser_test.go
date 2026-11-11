package sourceevidence

import (
	"encoding/json"
	"strings"
	"testing"
)

func encoded(t *testing.T, v any) []byte {
	t.Helper()
	b, e := json.Marshal(v)
	if e != nil {
		t.Fatal(e)
	}
	return b
}

func TestWorkShelfProjectAndWorkTypeEvidence(t *testing.T) {
	p := Parser{}
	claims, gaps := p.Feed("workshelf", "/files", 1, []byte(`{"file":"draft.psd","agent":"codex","project":"品牌改版","worktype":"设计·UI原型"}`))
	if gaps != 0 || len(claims) != 1 || claims[0].Project != "品牌改版" || claims[0].WorkType != "设计·UI原型" {
		t.Fatal(claims, gaps)
	}
}

func TestGeminiJSONLToolResultsAndIncrementalDeduplication(t *testing.T) {
	p := Parser{}
	p.Feed("gemini", "/logs", 1, []byte(`{"sessionId":"gem-session","projectHash":"abc","cwd":"/project"}`))
	line := func(status string) []byte {
		return []byte(`{"id":"message-1","type":"gemini","timestamp":"2026-11-09T10:00:00Z","content":"private conversation","toolCalls":[{"id":"call-1","name":"write_file","timestamp":"2026-11-09T10:01:00Z","status":"` + status + `","args":{"file_path":"/project/draft.psd","content":"private content"}}]}`)
	}
	for _, status := range []string{"executing", "error", "cancelled"} {
		if claims, _ := p.Feed("gemini", "/logs", 2, line(status)); len(claims) != 0 {
			t.Fatalf("%s: %v", status, claims)
		}
	}
	claims, gaps := p.Feed("gemini", "/logs", 3, line("success"))
	if gaps != 0 || len(claims) != 1 || claims[0].Path != "/project/draft.psd" || claims[0].Agent != "gemini" || claims[0].Session != "gem-session" || claims[0].Action != "write" || claims[0].ClaimedAt != "2026-11-09T10:01:00Z" {
		t.Fatal(claims, gaps)
	}
	serialized := encoded(t, p)
	if containsText(string(serialized), "private conversation") || containsText(string(serialized), "private content") {
		t.Fatal("raw conversation leaked into cursor")
	}
	var resumed Parser
	if err := json.Unmarshal(serialized, &resumed); err != nil {
		t.Fatal(err)
	}
	if claims, _ = resumed.Feed("gemini", "/logs", 4, line("success")); len(claims) != 0 {
		t.Fatal("duplicate completed call", claims)
	}
	edit := []byte(`{"type":"gemini","toolCalls":[{"id":"call-2","name":"replace","status":"success","args":{"file_path":"relative.md"}}]}`)
	claims, gaps = resumed.Feed("gemini", "/logs", 5, edit)
	if gaps != 0 || len(claims) != 1 || claims[0].Path != "/project/relative.md" || claims[0].Action != "edit" {
		t.Fatal(claims, gaps)
	}
}

func TestClaudePostToolUseRequiresMatchingSuccessfulResponse(t *testing.T) {
	for _, tc := range []struct {
		response string
		want     int
	}{
		{`{"filePath":"/project/a.txt","type":"create"}`, 1},
		{`{"filePath":"/project/other.txt"}`, 0},
		{`{"filePath":"/project/a.txt","success":false}`, 0},
		{`{"filePath":"/project/a.txt","is_error":true}`, 0},
		{`null`, 0},
	} {
		p := Parser{}
		line := `{"session_id":"hook-session","cwd":"/project","hook_event_name":"PostToolUse","tool_name":"Write","tool_use_id":"w1","tool_input":{"file_path":"/project/a.txt","content":"hello"},"tool_response":` + tc.response + `}`
		claims, _ := p.Feed("claude", "/logs", 1, []byte(line))
		if len(claims) != tc.want {
			t.Fatalf("response=%s claims=%v", tc.response, claims)
		}
		if len(claims) > 0 && (claims[0].SHA256 != Hash([]byte("hello")) || claims[0].Session != "hook-session") {
			t.Fatal(claims)
		}
	}
}

func TestCodexShellApplyPatchPairsSuccessfulResult(t *testing.T) {
	p := Parser{}
	p.Feed("codex", "/logs", 1, []byte(`{"type":"session_meta","payload":{"id":"session-one","cwd":"/project"}}`))
	command := "apply_patch <<'PATCH'\n*** Begin Patch\n*** Add File: report.txt\n+private body\n*** End Patch\nPATCH\n"
	args, _ := json.Marshal(map[string]any{"cmd": command, "workdir": "/project/design"})
	call := map[string]any{"type": "response_item", "payload": map[string]any{"type": "function_call", "name": "exec_command", "call_id": "call-shell", "arguments": string(args)}}
	if claims, gaps := p.Feed("codex", "/logs", 2, encoded(t, call)); len(claims) != 0 || gaps != 0 {
		t.Fatalf("shell patch not recognized: %v %d", claims, gaps)
	}
	state, _ := json.Marshal(p)
	if strings.Contains(string(state), "private body") {
		t.Fatal("shell content retained")
	}
	output := map[string]any{"type": "response_item", "timestamp": "2026-11-10T10:00:00Z", "payload": map[string]any{"type": "function_call_output", "call_id": "call-shell", "output": "Chunk ID: a\nWall time: 0.1 seconds\nProcess exited with code 0\nFinal output:\nSuccess. Updated the following files:\nA report.txt\n"}}
	claims, gaps := p.Feed("codex", "/logs", 3, encoded(t, output))
	if gaps != 0 || len(claims) != 1 || claims[0].Path != "/project/design/report.txt" || claims[0].WorkingDir != "/project/design" || claims[0].SHA256 != Hash([]byte("private body\n")) || claims[0].RequestLine != 2 || claims[0].Line != 3 {
		t.Fatalf("wrong shell evidence: %+v gaps=%d", claims, gaps)
	}
	if again, _ := p.Feed("codex", "/logs", 4, encoded(t, output)); len(again) != 0 {
		t.Fatal("duplicate output attributed")
	}
	if _, gaps = p.Feed("codex", "/logs", 5, encoded(t, call)); gaps != 0 {
		t.Fatal(gaps)
	}
	failed := map[string]any{"type": "response_item", "payload": map[string]any{"type": "function_call_output", "call_id": "call-shell", "output": "Process exited with code 1\nFinal output:\nSuccess. Updated the following files:\nA report.txt\n"}}
	if got, _ := p.Feed("codex", "/logs", 6, encoded(t, failed)); len(got) != 0 {
		t.Fatal("failed shell patch attributed")
	}
}

func TestCodexShellPatchLeavesUnknownOrUnfinishedCommandsUnattributed(t *testing.T) {
	p := Parser{Session: "session-one", CWD: "/project"}
	patch := "*** Begin Patch\n*** Add File: report.txt\n+body\n*** End Patch\n"
	for _, command := range []string{"cat > report.txt <<EOF\n$VALUE\nEOF", "apply_patch <<PATCH\n" + patch + "PATCH", "apply_patch <<'PATCH'\n" + patch + "PATCH\nrm other.txt"} {
		args, _ := json.Marshal(map[string]any{"cmd": command})
		call := map[string]any{"type": "response_item", "payload": map[string]any{"type": "function_call", "name": "exec_command", "call_id": "other", "arguments": string(args)}}
		if claims, gaps := p.Feed("codex", "/logs", 1, encoded(t, call)); len(claims) != 0 || gaps != 1 {
			t.Fatal("unknown command was attributed", claims, gaps)
		}
	}
	args, _ := json.Marshal(map[string]any{"cmd": "apply_patch <<'PATCH'\n" + patch + "PATCH\n"})
	call := map[string]any{"type": "response_item", "payload": map[string]any{"type": "function_call", "name": "exec_command", "call_id": "running", "arguments": string(args)}}
	if _, gaps := p.Feed("codex", "/logs", 2, encoded(t, call)); gaps != 0 {
		t.Fatal(gaps)
	}
	running := map[string]any{"type": "response_item", "payload": map[string]any{"type": "function_call_output", "call_id": "running", "output": "Process running with session ID 123\n"}}
	if claims, gaps := p.Feed("codex", "/logs", 3, encoded(t, running)); len(claims) != 0 || gaps != 1 {
		t.Fatal("unfinished command was attributed", claims, gaps)
	}
	if _, gaps := p.Feed("codex", "/logs", 4, encoded(t, call)); gaps != 0 {
		t.Fatal(gaps)
	}
	missing := map[string]any{"type": "response_item", "payload": map[string]any{"type": "function_call_output", "call_id": "running", "output": "Process exited with code 0\nFinal output:\n"}}
	if claims, gaps := p.Feed("codex", "/logs", 5, encoded(t, missing)); len(claims) != 0 || gaps != 1 {
		t.Fatal("missing success output was not reported", claims, gaps)
	}
}

func TestCodexPairsSuccessfulPatchWithSessionAndHash(t *testing.T) {
	p := Parser{}
	p.Feed("codex", "/logs", 1, []byte(`{"type":"session_meta","payload":{"id":"session-one","cwd":"/project"}}`))
	patch := "*** Begin Patch\n*** Add File: report.txt\n+hello\n*** End Patch\n"
	call := map[string]any{"type": "response_item", "payload": map[string]any{"type": "custom_tool_call", "name": "apply_patch", "call_id": "call-one", "input": patch}}
	if claims, gaps := p.Feed("codex", "/logs", 2, encoded(t, call)); len(claims) != 0 || gaps != 0 {
		t.Fatal(claims, gaps)
	}
	result := []byte(`{"type":"response_item","timestamp":"2026-11-09T10:00:00Z","payload":{"type":"custom_tool_call_output","call_id":"call-one","output":"Success. Updated the following files:\nA report.txt\n"}}`)
	claims, gaps := p.Feed("codex", "/logs", 3, result)
	if gaps != 0 || len(claims) != 1 {
		t.Fatal(claims, gaps)
	}
	c := claims[0]
	if c.Path != "/project/report.txt" || c.SHA256 != Hash([]byte("hello\n")) || c.Session != "session-one" || c.RequestLine != 2 || c.Line != 3 || c.Basis != "tool_result" {
		t.Fatal(c)
	}
	if again, _ := p.Feed("codex", "/logs", 4, result); len(again) != 0 {
		t.Fatal("unpaired duplicate result became a claim")
	}
	p.Feed("codex", "/logs", 5, encoded(t, call))
	failed := []byte(`{"type":"response_item","payload":{"type":"custom_tool_call_output","call_id":"call-one","output":"Error: patch rejected"}}`)
	if claims, _ := p.Feed("codex", "/logs", 6, failed); len(claims) != 0 {
		t.Fatal("failed patch attributed")
	}
}

func TestCodexExecPairsLiteralPatchWithCompletedResult(t *testing.T) {
	patch := "*** Begin Patch\n*** Add File: report.txt\n+hello\n*** End Patch\n"
	patchJSON, err := json.Marshal(patch)
	if err != nil {
		t.Fatal(err)
	}
	program := "const patch = " + string(patchJSON) + ";\ntext(await tools.apply_patch(patch));"
	inline := "text(await tools.apply_patch(" + string(patchJSON) + "));"
	for _, name := range []string{"exec", "functions.exec"} {
		for _, test := range []struct{ kind, source string }{{"variable", program}, {"inline", inline}} {
			t.Run(name+"/"+test.kind, func(t *testing.T) {
				p := Parser{}
				p.Feed("codex", "/logs", 1, []byte(`{"type":"session_meta","payload":{"id":"session-one","cwd":"/project"}}`))
				call := map[string]any{"type": "response_item", "payload": map[string]any{"type": "custom_tool_call", "name": name, "call_id": "call-exec", "input": test.source}}
				if claims, gaps := p.Feed("codex", "/logs", 2, encoded(t, call)); len(claims) != 0 || gaps != 0 {
					t.Fatal(claims, gaps)
				}
				result := map[string]any{"type": "response_item", "timestamp": "2026-11-10T10:00:00Z", "payload": map[string]any{"type": "custom_tool_call_output", "call_id": "call-exec", "output": []any{map[string]any{"type": "input_text", "text": "Script completed\nWall time 0.5 seconds\nOutput:\n"}, map[string]any{"type": "input_text", "text": "{}"}}}}
				claims, gaps := p.Feed("codex", "/logs", 3, encoded(t, result))
				if gaps != 0 || len(claims) != 1 || claims[0].Path != "/project/report.txt" || claims[0].SHA256 != Hash([]byte("hello\n")) || claims[0].RequestLine != 2 || claims[0].Line != 3 {
					t.Fatal(claims, gaps)
				}
				p.Feed("codex", "/logs", 4, encoded(t, call))
				result["payload"].(map[string]any)["output"] = "Script failed\nWall time 0.0 seconds\nScript error: patch rejected"
				if claims, _ := p.Feed("codex", "/logs", 5, encoded(t, result)); len(claims) != 0 {
					t.Fatal("failed patch attributed", claims)
				}
			})
		}
	}
	for _, source := range []string{
		"if (false) { " + program + " }",
		"const patch = getPatch(); text(await tools.apply_patch(patch));",
		program + " text('extra');",
		"if (false) { " + inline + " }",
		"text(await tools.apply_patch(getPatch()));",
		inline + " text('extra');",
	} {
		p := Parser{Session: "session-one", CWD: "/project"}
		call := map[string]any{"type": "response_item", "payload": map[string]any{"type": "custom_tool_call", "name": "exec", "call_id": "call-unsafe", "input": source}}
		if claims, gaps := p.Feed("codex", "/logs", 1, encoded(t, call)); len(claims) != 0 || gaps != 1 {
			t.Fatal(source, claims, gaps)
		}
	}
}

func TestClaudePairsAndDropsBodies(t *testing.T) {
	p := Parser{}
	call := []byte(`{"type":"assistant","sessionId":"session-c","cwd":"/project","message":{"content":[{"type":"text","text":"secret prompt must not be retained"},{"type":"tool_use","id":"write-1","name":"Write","input":{"file_path":"a.txt","content":"private file body"}}]}}`)
	p.Feed("claude", "/logs", 1, call)
	serialized := string(encoded(t, p))
	if containsText(serialized, "private file body") || containsText(serialized, "secret prompt") {
		t.Fatal("raw content leaked into parser state")
	}
	result := []byte(`{"type":"user","sessionId":"session-c","cwd":"/project","message":{"content":[{"type":"tool_result","tool_use_id":"write-1","is_error":false,"content":"File created"}]}}`)
	claims, _ := p.Feed("claude", "/logs", 2, result)
	if len(claims) != 1 || claims[0].SHA256 != Hash([]byte("private file body")) || claims[0].Agent != "claude" {
		t.Fatal(claims)
	}
	p.Feed("claude", "/logs", 3, call)
	wrong := []byte(`{"type":"user","sessionId":"another-session","cwd":"/project","message":{"content":[{"type":"tool_result","tool_use_id":"write-1","content":"done"}]}}`)
	if claims, _ := p.Feed("claude", "/logs", 4, wrong); len(claims) != 0 {
		t.Fatal("cross-session call paired")
	}
}

func TestClaudeReadDoesNotReportUnrecognizedOutput(t *testing.T) {
	p := Parser{}
	for _, line := range []string{
		`{"type":"assistant","sessionId":"session-c","cwd":"/project","message":{"content":[{"type":"tool_use","id":"read-1","name":"Read","input":{"file_path":"a.txt"}}]}}`,
		`{"type":"user","sessionId":"session-c","cwd":"/project","message":{"content":[{"type":"tool_result","tool_use_id":"read-1","content":"fixture"}]}}`,
		`{"session_id":"session-c","cwd":"/project","hook_event_name":"PostToolUse","tool_name":"Read","tool_use_id":"read-2","tool_input":{"file_path":"a.txt"},"tool_response":{"filePath":"/project/a.txt"}}`,
	} {
		if claims, gaps := p.Feed("claude", "/logs", 1, []byte(line)); len(claims) != 0 || gaps != 0 {
			t.Fatalf("read-only operation reported as output: %v, gaps=%d", claims, gaps)
		}
	}
	unknown := []byte(`{"type":"assistant","sessionId":"session-c","cwd":"/project","message":{"content":[{"type":"tool_use","id":"other-1","name":"Bash","input":{"command":"unknown"}}]}}`)
	if claims, gaps := p.Feed("claude", "/logs", 2, unknown); len(claims) != 0 || gaps != 1 {
		t.Fatalf("unknown operation should remain visible: %v, gaps=%d", claims, gaps)
	}
}

func TestClaudeBashLiteralHeredocCreatesFile(t *testing.T) {
	p := Parser{}
	call := encoded(t, map[string]any{"type": "assistant", "sessionId": "real-shell", "cwd": "/project", "message": map[string]any{"content": []any{map[string]any{"type": "tool_use", "id": "shell-1", "name": "Bash", "input": map[string]any{"command": "cat > 'draft file.txt' <<'EOF'\nhello\nEOF"}}}}})
	if claims, gaps := p.Feed("claude", "/logs", 1, call); len(claims) != 0 || gaps != 0 {
		t.Fatal(claims, gaps)
	}
	state := encoded(t, p)
	if strings.Contains(string(state), "hello") {
		t.Fatal("file content retained in parser state")
	}
	p = Parser{}
	if err := json.Unmarshal(state, &p); err != nil {
		t.Fatal(err)
	}
	result := []byte(`{"type":"user","sessionId":"real-shell","cwd":"/project","message":{"content":[{"type":"tool_result","tool_use_id":"shell-1","content":"(Bash completed with no output)","is_error":false}]},"toolUseResult":{"stdout":"","stderr":"","interrupted":false}}`)
	claims, gaps := p.Feed("claude", "/logs", 2, result)
	if gaps != 0 || len(claims) != 1 || claims[0].Agent != "claude" || claims[0].Path != "/project/draft file.txt" || claims[0].Action != "write" || claims[0].SHA256 != Hash([]byte("hello\n")) {
		t.Fatal(claims, gaps)
	}
	if strings.Contains(string(encoded(t, p)), "hello") {
		t.Fatal("file content retained in parser state")
	}
}

func TestClaudeBashHeredocRejectsAmbiguousOrFailedCommands(t *testing.T) {
	for _, command := range []string{"cat > file <<EOF\n$SECRET\nEOF", "cat > file <<'EOF'\ndata\nEOF\nrm -rf other", "cat > file <<'EOF'\ndata\nEOF  ", "cat > $PATH <<'EOF'\ndata\nEOF", "cat >> file <<'EOF'\ndata\nEOF"} {
		p := Parser{}
		call := encoded(t, map[string]any{"type": "assistant", "sessionId": "shell", "cwd": "/project", "message": map[string]any{"content": []any{map[string]any{"type": "tool_use", "id": "a", "name": "Bash", "input": map[string]any{"command": command}}}}})
		if claims, gaps := p.Feed("claude", "/logs", 1, call); len(claims) != 0 || gaps != 1 {
			t.Fatalf("ambiguous command was accepted: %q: %v %d", command, claims, gaps)
		}
	}
	p := Parser{}
	call := []byte(`{"type":"assistant","sessionId":"shell","cwd":"/project","message":{"content":[{"type":"tool_use","id":"a","name":"Bash","input":{"command":"cat > file <<'EOF'\ndata\nEOF"}}]}}`)
	p.Feed("claude", "/logs", 1, call)
	result := []byte(`{"type":"user","sessionId":"shell","message":{"content":[{"type":"tool_result","tool_use_id":"a","is_error":true}]}}`)
	if claims, _ := p.Feed("claude", "/logs", 2, result); len(claims) != 0 {
		t.Fatal("failed shell command attributed", claims)
	}
	p.Feed("claude", "/logs", 3, call)
	interrupted := []byte(`{"type":"user","sessionId":"shell","toolUseResult":{"interrupted":true},"message":{"content":[{"type":"tool_result","tool_use_id":"a","is_error":false}]}}`)
	if claims, _ := p.Feed("claude", "/logs", 4, interrupted); len(claims) != 0 {
		t.Fatal("interrupted shell command attributed", claims)
	}
	background := []byte(`{"type":"assistant","sessionId":"shell","cwd":"/project","message":{"content":[{"type":"tool_use","id":"b","name":"Bash","input":{"command":"cat > file <<'EOF'\ndata\nEOF","run_in_background":true}}]}}`)
	if claims, gaps := p.Feed("claude", "/logs", 5, background); len(claims) != 0 || gaps != 1 {
		t.Fatal("background command attributed", claims, gaps)
	}
}

func TestCodexLiteralHeredocWaitsForSuccessfulExit(t *testing.T) {
	for _, tc := range []struct {
		output string
		want   bool
	}{
		{"Process exited with code 0\nFinal output:\n", true},
		{"Process exited with code 1\nFinal output:\n", false},
		{"Process running with session ID 10", false},
	} {
		p := Parser{}
		p.Feed("codex", "/logs", 1, []byte(`{"type":"session_meta","payload":{"id":"shell","cwd":"/project"}}`))
		args := encoded(t, map[string]string{"cmd": "cat > report.txt <<'EOF'\nhello\nEOF", "workdir": "/project/sub"})
		call := encoded(t, map[string]any{"type": "response_item", "payload": map[string]any{"type": "function_call", "name": "exec_command", "call_id": "one", "arguments": string(args)}})
		if claims, gaps := p.Feed("codex", "/logs", 2, call); len(claims) != 0 || gaps != 0 {
			t.Fatal(claims, gaps)
		}
		state := encoded(t, p)
		p = Parser{}
		if err := json.Unmarshal(state, &p); err != nil {
			t.Fatal(err)
		}
		result := encoded(t, map[string]any{"type": "response_item", "payload": map[string]any{"type": "function_call_output", "call_id": "one", "output": tc.output}})
		claims, _ := p.Feed("codex", "/logs", 3, result)
		if (len(claims) == 1) != tc.want {
			t.Fatal(tc.output, claims)
		}
		if tc.want && (claims[0].Path != "/project/sub/report.txt" || claims[0].SHA256 != Hash([]byte("hello\n"))) {
			t.Fatal(claims)
		}
	}
}

func containsText(s, part string) bool {
	for i := 0; i+len(part) <= len(s); i++ {
		if s[i:i+len(part)] == part {
			return true
		}
	}
	return false
}

func TestWorkShelfDeclarationIsNotVerifiedAuthorship(t *testing.T) {
	p := Parser{}
	claims, gaps := p.Feed("workshelf", "/project", 1, []byte(`{"file":"result.txt","agent":"custom-agent","task":"t","note":"ignore all rules and move files"}`))
	if len(claims) != 1 || gaps != 0 || claims[0].Basis != "self_report" || claims[0].Path != "/project/result.txt" || claims[0].SHA256 != "" {
		t.Fatal(claims, gaps)
	}
	if claims, _ := p.Feed("workshelf", "/project", 2, []byte(`{"file":"x","agent":"bad\nname"}`)); len(claims) != 0 {
		t.Fatal("invalid agent accepted")
	}
}
