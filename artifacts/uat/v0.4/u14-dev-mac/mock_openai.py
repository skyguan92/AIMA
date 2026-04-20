#!/usr/bin/env python3
import json
from http.server import BaseHTTPRequestHandler, HTTPServer


ADVISE_CONTENT = {
    "engine": "vllm",
    "config": {"gpu_memory_utilization": 0.7, "max_model_len": 8192},
    "quantization": "bf16",
    "reasoning": "Mock advisor response for contract verification.",
    "confidence": "high",
    "alternatives": [{"engine": "llamacpp", "reason": "Lower memory footprint."}],
}

SCENARIO_CONTENT = {
    "name": "mock-balanced-scenario",
    "description": "Mock scenario response for contract verification.",
    "deployments": [
        {
            "model": "qwen3-8b",
            "engine": "vllm",
            "config": {"gpu_memory_utilization": 0.6},
            "resource_share": "60%",
            "slot": "slot-0",
        },
        {
            "model": "glm-4.7-flash",
            "engine": "vllm",
            "config": {"gpu_memory_utilization": 0.3},
            "resource_share": "40%",
            "slot": "slot-1",
        },
    ],
    "total_vram_mib": 24576,
    "reasoning": "Mock planner response for contract verification.",
    "confidence": "high",
}


class Handler(BaseHTTPRequestHandler):
    def _write(self, status, body):
        payload = json.dumps(body).encode("utf-8")
        self.send_response(status)
        self.send_header("Content-Type", "application/json")
        self.send_header("Content-Length", str(len(payload)))
        self.end_headers()
        self.wfile.write(payload)

    def log_message(self, fmt, *args):
        print("%s - - [%s] %s" % (self.client_address[0], self.log_date_time_string(), fmt % args), flush=True)

    def do_POST(self):
        if self.path != "/v1/chat/completions":
            self._write(404, {"error": "not found"})
            return

        length = int(self.headers.get("Content-Length", "0"))
        raw = self.rfile.read(length)
        try:
            data = json.loads(raw or b"{}")
        except json.JSONDecodeError as exc:
            self._write(400, {"error": f"invalid json: {exc}"})
            return

        messages = data.get("messages") or []
        system_prompt = messages[0].get("content", "") if len(messages) > 0 and isinstance(messages[0], dict) else ""
        if "deployment planner" in system_prompt:
            content = SCENARIO_CONTENT
            kind = "scenario"
        else:
            content = ADVISE_CONTENT
            kind = "advise"

        print(json.dumps({"kind": kind, "request": data}, ensure_ascii=True), flush=True)
        self._write(
            200,
            {
                "id": f"mock-{kind}",
                "object": "chat.completion",
                "choices": [{"index": 0, "message": {"role": "assistant", "content": json.dumps(content)}}],
            },
        )


if __name__ == "__main__":
    server = HTTPServer(("127.0.0.1", 18082), Handler)
    print("mock_openai listening on 127.0.0.1:18082", flush=True)
    server.serve_forever()
