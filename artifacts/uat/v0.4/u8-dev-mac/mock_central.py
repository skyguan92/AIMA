#!/usr/bin/env python3
import json
import sys
import time
from datetime import datetime, timezone
from http.server import BaseHTTPRequestHandler, HTTPServer
from urllib.parse import parse_qs, urlparse


DELAY_SECONDS = 130


def utc_now():
    return datetime.now(timezone.utc).isoformat()


class Handler(BaseHTTPRequestHandler):
    server_version = "u8-mock-central/1.0"

    def log_message(self, fmt, *args):
        sys.stdout.write("[%s] %s\n" % (utc_now(), fmt % args))
        sys.stdout.flush()

    def do_POST(self):
        parsed = urlparse(self.path)
        length = int(self.headers.get("Content-Length", "0"))
        raw = self.rfile.read(length) if length else b""
        body = raw.decode("utf-8", errors="replace")
        self.log_message("POST %s query=%s body=%s", parsed.path, parsed.query, body)

        if parsed.path != "/api/v1/advise":
            self.send_response(404)
            self.send_header("Content-Type", "application/json")
            self.end_headers()
            self.wfile.write(b'{"error":"not found"}')
            return

        q = parse_qs(parsed.query)
        payload = json.loads(body or "{}")

        self.log_message("sleeping %ss before advise response", DELAY_SECONDS)
        time.sleep(DELAY_SECONDS)

        response = {
            "recommendation": {
                "engine": payload.get("engine") or "vllm",
                "config": {
                    "gpu_memory_utilization": 0.8,
                    "max_model_len": 8192,
                },
            },
            "advisory": {
                "id": "adv-u8-mock",
                "type": "recommendation",
                "status": "pending",
                "confidence": "high",
                "hardware": payload.get("hardware") or "mock-hw",
                "model": payload.get("model") or "unknown-model",
                "engine": payload.get("engine") or "vllm",
                "summary": "mock delayed central advise response",
                "reasoning": "delayed 130 seconds to validate edge timeout > 120s",
                "created_at": utc_now(),
                "device_id_seen": q.get("device_id", [""])[0],
            },
        }
        encoded = json.dumps(response).encode("utf-8")
        self.send_response(200)
        self.send_header("Content-Type", "application/json")
        self.send_header("Content-Length", str(len(encoded)))
        self.end_headers()
        self.wfile.write(encoded)
        self.log_message("responded 200 for %s", parsed.path)


def main():
    host = "127.0.0.1"
    port = 18081
    httpd = HTTPServer((host, port), Handler)
    print("[%s] listening on http://%s:%d delay=%ss" % (utc_now(), host, port, DELAY_SECONDS), flush=True)
    httpd.serve_forever()


if __name__ == "__main__":
    main()
