"""Local-only MaaS API-key validation stub for the Compose development stack."""

import json
from http.server import BaseHTTPRequestHandler, HTTPServer


class Handler(BaseHTTPRequestHandler):
    def do_POST(self):  # noqa: N802
        length = int(self.headers.get("Content-Length", "0"))
        try:
            body = json.loads(self.rfile.read(length) or b"{}")
        except json.JSONDecodeError:
            body = {}
        valid = body.get("key") == "local-noy-key"
        response = {
            "valid": valid,
            "username": "nitzikow@redhat.com" if valid else "",
            "groups": [],
        }
        payload = json.dumps(response).encode()
        self.send_response(200)
        self.send_header("Content-Type", "application/json")
        self.send_header("Content-Length", str(len(payload)))
        self.end_headers()
        self.wfile.write(payload)

    def do_GET(self):  # noqa: N802
        self.send_response(200)
        self.send_header("Content-Type", "text/plain")
        self.end_headers()
        self.wfile.write(b"local MaaS stub\n")

    def log_message(self, format, *args):
        return


HTTPServer(("0.0.0.0", 8081), Handler).serve_forever()
