#!/usr/bin/env python3
import json
import os
from datetime import datetime, timezone
from http.server import BaseHTTPRequestHandler, ThreadingHTTPServer
from urllib.parse import parse_qs, urlparse


API_KEY = os.environ.get("AIMA_U5_API_KEY", "test-key")
PORT = int(os.environ.get("AIMA_U5_PORT", "18086"))
TARGET_HARDWARE = os.environ.get("AIMA_U5_HARDWARE", "nvidia-gb10-arm64")
TARGET_MODEL = os.environ.get("AIMA_U5_MODEL", "gemma-4-26b-a4b-it")
TARGET_ENGINE = os.environ.get("AIMA_U5_ENGINE", "vllm-gemma4-blackwell")
ADVISORY_ID = os.environ.get("AIMA_U5_ADVISORY_ID", "adv-u5-reject-1")
ADVISORY_SUMMARY = os.environ.get(
    "AIMA_U5_SUMMARY", "Use an invalid dtype so edge validation must reject it."
)
ADVISORY_REASONING = os.environ.get(
    "AIMA_U5_REASONING", "Intentional bad config for release-gate U5."
)
try:
    CONTENT = json.loads(
        os.environ.get(
            "AIMA_U5_CONTENT_JSON",
            '{"dtype":"definitely-not-real","gpu_memory_utilization":0.74,"max_model_len":155648}',
        )
    )
except json.JSONDecodeError as exc:
    raise SystemExit(f"invalid AIMA_U5_CONTENT_JSON: {exc}")


def now_rfc3339():
    return datetime.now(timezone.utc).replace(microsecond=0).isoformat().replace("+00:00", "Z")


STATE = {
    "advisory": {
        "id": ADVISORY_ID,
        "type": "config_recommend",
        "status": "pending",
        "confidence": "high",
        "target_hardware": TARGET_HARDWARE,
        "target_model": TARGET_MODEL,
        "target_engine": TARGET_ENGINE,
        "title": "Impossible Gemma advisory for rejection-path UAT",
        "summary": ADVISORY_SUMMARY,
        "reasoning": ADVISORY_REASONING,
        "created_at": now_rfc3339(),
        "content": CONTENT,
    },
    "events": [],
}


def log_event(kind, **fields):
    entry = {"ts": now_rfc3339(), "kind": kind, **fields}
    STATE["events"].append(entry)
    print(json.dumps(entry), flush=True)


class Handler(BaseHTTPRequestHandler):
    server_version = "AIMAU5MockCentral/0.1"

    def do_GET(self):
        if not self._authorize():
            return
        parsed = urlparse(self.path)
        if parsed.path == "/api/v1/sync":
            self._json(
                200,
                {
                    "schema_version": 1,
                    "data": {
                        "configurations": [],
                        "benchmark_results": [],
                        "knowledge_notes": [],
                    },
                    "stats": {
                        "configurations": 0,
                        "benchmark_results": 0,
                        "knowledge_notes": 0,
                    },
                },
            )
            log_event("sync_pull", query=parse_qs(parsed.query))
            return
        if parsed.path == "/api/v1/advisories":
            self._handle_list_advisories(parsed)
            return
        if parsed.path == "/api/v1/scenarios":
            self._json(200, [])
            log_event("list_scenarios", query=parse_qs(parsed.query))
            return
        if parsed.path == "/__state":
            self._json(200, STATE)
            return
        self._json(404, {"error": "not found"})

    def do_POST(self):
        if not self._authorize():
            return
        parsed = urlparse(self.path)
        if parsed.path.startswith("/api/v1/advisories/") and parsed.path.endswith("/feedback"):
            advisory_id = parsed.path.split("/")[4]
            length = int(self.headers.get("Content-Length", "0"))
            body = self.rfile.read(length) if length > 0 else b"{}"
            payload = json.loads(body or b"{}")
            accepted = payload.get("accepted")
            status = payload.get("status")
            if not status:
                status = "validated" if accepted else "rejected"
            feedback = payload.get("feedback", "")
            adv = STATE["advisory"]
            if adv["id"] != advisory_id:
                self._json(404, {"error": "missing advisory"})
                return
            adv["status"] = status
            adv["feedback"] = feedback
            adv["validated_at"] = now_rfc3339()
            adv["accepted"] = status == "validated"
            self._json(200, {"ok": True, "advisory_id": advisory_id, "status": status})
            log_event("advisory_feedback", advisory_id=advisory_id, status=status, feedback=feedback)
            return
        self._json(404, {"error": "not found"})

    def log_message(self, fmt, *args):
        pass

    def _authorize(self):
        expected = f"Bearer {API_KEY}"
        if self.headers.get("Authorization") != expected:
            self._json(401, {"error": "unauthorized"})
            return False
        return True

    def _handle_list_advisories(self, parsed):
        query = parse_qs(parsed.query)
        requested_status = query.get("status", [""])[0]
        requested_hardware = query.get("hardware", [""])[0]
        adv = STATE["advisory"]
        items = []
        if (not requested_status or adv["status"] == requested_status) and (
            not requested_hardware or adv["target_hardware"] == requested_hardware
        ):
            if requested_status == "pending" and adv["status"] == "pending":
                adv["status"] = "delivered"
                adv["delivered_at"] = now_rfc3339()
            items = [adv]
        self._json(200, items)
        log_event("list_advisories", query=query, returned=len(items), advisory_status=adv["status"])

    def _json(self, status, payload):
        body = json.dumps(payload).encode()
        self.send_response(status)
        self.send_header("Content-Type", "application/json")
        self.send_header("Content-Length", str(len(body)))
        self.end_headers()
        self.wfile.write(body)


def main():
    server = ThreadingHTTPServer(("127.0.0.1", PORT), Handler)
    print(json.dumps({"ts": now_rfc3339(), "kind": "server_start", "port": PORT}), flush=True)
    server.serve_forever()


if __name__ == "__main__":
    main()
