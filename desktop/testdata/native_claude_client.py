import http.server
import json
import os
import pathlib
import shlex
import subprocess
import sys
import threading

root = pathlib.Path(os.environ["WORKSHELF_TEST_FIXTURE"])
mode = sys.argv[1]
shell_write = os.environ.get("WORKSHELF_NATIVE_CLAUDE_SHELL") == "1"
for name in ("home", "claude", "tmp", "Desktop", "Downloads"):
    (root / name).mkdir(parents=True, exist_ok=True)
subdir = "UI界面设计" if os.environ.get("WORKSHELF_TEST_DESIGN_PATHS") == "1" else ""
paths = [root / "Desktop" / subdir / "首页.txt", root / "Downloads" / subdir / "封面.txt"]
for path in paths:
    path.parent.mkdir(parents=True, exist_ok=True)
if mode == "write" and not shell_write:
    for path in paths:
        path.write_text("draft", encoding="utf-8")
requests = []

class Handler(http.server.BaseHTTPRequestHandler):
    def log_message(self, *args):
        pass

    def do_POST(self):
        body = json.loads(self.rfile.read(int(self.headers["Content-Length"])))
        messages = body.get("messages", [])
        has_result = any(any(isinstance(c, dict) and c.get("type") == "tool_result" for c in m.get("content", []) if isinstance(m.get("content"), list)) for m in messages)
        tools = [v.get("name") for v in body.get("tools", [])]
        requests.append({"tools": tools, "has_result": has_result})
        used = {c.get("name") for m in messages if m.get("role") == "assistant" and isinstance(m.get("content"), list) for c in m["content"] if c.get("type") == "tool_use"}
        tool = ("Bash" if "Bash" not in used else "") if shell_write else ("Read" if "Read" not in used else "Edit" if "Edit" not in used else "")
        use_tools = mode == "write" and tool in tools
        msg = {"id": "msg_fixture", "type": "message", "role": "assistant", "model": "claude-sonnet-4-5", "content": [], "stop_reason": None, "stop_sequence": None, "usage": {"input_tokens": 10, "output_tokens": 0}}
        events = [("message_start", {"type": "message_start", "message": msg})]
        if use_tools:
            for i, path in enumerate(paths):
                args = {"command": "cat > " + shlex.quote(str(path)) + " <<'EOF'\nfixture artwork\nEOF"} if tool == "Bash" else {"file_path": str(path)}
                if tool == "Edit":
                    args.update({"old_string": "draft", "new_string": "fixture artwork"})
                args = json.dumps(args, ensure_ascii=False)
                events.extend([("content_block_start", {"type": "content_block_start", "index": i, "content_block": {"type": "tool_use", "id": "toolu_fixture_" + tool + str(i), "name": tool, "input": {}}}), ("content_block_delta", {"type": "content_block_delta", "index": i, "delta": {"type": "input_json_delta", "partial_json": args}}), ("content_block_stop", {"type": "content_block_stop", "index": i})])
        else:
            events.extend([("content_block_start", {"type": "content_block_start", "index": 0, "content_block": {"type": "text", "text": ""}}), ("content_block_delta", {"type": "content_block_delta", "index": 0, "delta": {"type": "text_delta", "text": "fixture complete"}}), ("content_block_stop", {"type": "content_block_stop", "index": 0})])
        events.extend([("message_delta", {"type": "message_delta", "delta": {"stop_reason": "tool_use" if use_tools else "end_turn", "stop_sequence": None}, "usage": {"output_tokens": 10}}), ("message_stop", {"type": "message_stop"})])
        self.send_response(200)
        self.send_header("Content-Type", "text/event-stream")
        self.end_headers()
        for event, data in events:
            self.wfile.write(("event: " + event + "\ndata: " + json.dumps(data, ensure_ascii=False) + "\n\n").encode())

server = http.server.ThreadingHTTPServer(("127.0.0.1", 0), Handler)
threading.Thread(target=server.serve_forever, daemon=True).start()
profile = root / "local-only.sb"
profile.write_text('(version 1)\n(allow default)\n(deny network-outbound)\n(allow network-outbound (remote ip "localhost:' + str(server.server_port) + '"))\n')
env = {"PATH": "/usr/bin:/bin:/usr/sbin:/sbin", "HOME": str(root / "home"), "TMPDIR": str(root / "tmp"), "CLAUDE_CONFIG_DIR": str(root / "claude"), "ANTHROPIC_BASE_URL": "http://127.0.0.1:" + str(server.server_port), "ANTHROPIC_API_KEY": "fixture-key", "CLAUDE_CODE_DISABLE_NONESSENTIAL_TRAFFIC": "1", "LANG": "en_US.UTF-8"}
command = ["/usr/bin/sandbox-exec", "-f", str(profile), os.path.expanduser("~/.local/bin/claude"), "--bare", "--strict-mcp-config", "--model", "claude-sonnet-4-5", "--allowedTools", "Bash" if shell_write else "Read,Edit", "--permission-mode", "acceptEdits"]
sid = "c1152058-0665-47ca-a6c8-49e8a3d712fd"
command += (["--session-id", sid, "-p", "Update the two fixture images."] if mode == "write" else ["--resume", sid, "-p", "这样不妥，请把刚刚挪走的文件放回原来的地方"])
try:
    result = subprocess.run(command, cwd=os.environ.get("WORKSHELF_TEST_WORKDIR", str(root)), env=env, capture_output=True, text=True, timeout=50)
    print(json.dumps({"exit": result.returncode, "stdout": result.stdout[:300], "stderr": result.stderr[:300], "requests": requests, "session_files": len(list((root / "claude").rglob("*.jsonl"))), "updated": [p.exists() and p.read_text(encoding="utf-8") == ("fixture artwork\n" if shell_write else "fixture artwork") for p in paths]}, ensure_ascii=False))
    sys.exit(result.returncode)
finally:
    server.shutdown()
