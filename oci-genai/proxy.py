#!/usr/bin/env python3
"""OCI GenAI local proxy -- OpenAI-compatible endpoint backed by OCI GenAI.

Starts a local server that accepts standard OpenAI API calls and forwards
them to OCI GenAI using OCI authentication from ~/.oci/config.

Prerequisites:
    pip install -r requirements.txt
    # Configure ~/.oci/config with your OCI credentials

Usage:
    python proxy.py                          # starts on port 9999
    OCI_PROXY_PORT=8888 python proxy.py      # custom port

Then configure PicoOraClaw with:
    "provider": "openai",
    "api_base": "http://localhost:9999/v1",
    "api_key": "oci-genai",
    "model": "meta.llama-3.3-70b-instruct"
"""

import json
import os
import sys
from http.server import HTTPServer, BaseHTTPRequestHandler

from oci_client import create_oci_client

PROXY_PORT = int(os.getenv("OCI_PROXY_PORT", "9999"))


class OCIProxyHandler(BaseHTTPRequestHandler):
    client = None

    def do_POST(self):
        if "/chat/completions" in self.path:
            content_length = int(self.headers.get("Content-Length", 0))
            body = json.loads(self.rfile.read(content_length))
            try:
                response = self.client.chat.completions.create(**body)
                result = response.model_dump()
                self._respond(200, result)
            except Exception as e:
                self._respond(
                    500,
                    {"error": {"message": str(e), "type": "oci_genai_error"}},
                )
        else:
            self._respond(404, {"error": {"message": "Not found"}})

    def do_GET(self):
        if "/models" in self.path:
            self._respond(200, {"object": "list", "data": []})
        elif "/health" in self.path:
            self._respond(200, {"status": "ok"})
        else:
            self._respond(404, {"error": {"message": "Not found"}})

    def _respond(self, code, data):
        self.send_response(code)
        self.send_header("Content-Type", "application/json")
        self.end_headers()
        self.wfile.write(json.dumps(data).encode())

    def log_message(self, format, *args):
        sys.stderr.write(f"[oci-proxy] {args[0]}\n")


def main():
    if not os.getenv("OCI_COMPARTMENT_ID"):
        print("ERROR: OCI_COMPARTMENT_ID environment variable is required.")
        print("Set it to your OCI compartment OCID.")
        sys.exit(1)

    client = create_oci_client()
    OCIProxyHandler.client = client

    server = HTTPServer(("0.0.0.0", PROXY_PORT), OCIProxyHandler)
    print(f"OCI GenAI proxy listening on http://localhost:{PROXY_PORT}/v1")
    print(f"  Region:      {os.getenv('OCI_REGION', 'us-chicago-1')}")
    print(f"  Profile:     {os.getenv('OCI_PROFILE', 'DEFAULT')}")
    print(f"  Compartment: {os.getenv('OCI_COMPARTMENT_ID', '')[:50]}...")
    print()
    print("Configure PicoOraClaw with:")
    print(f'  "api_base": "http://localhost:{PROXY_PORT}/v1"')
    print(f'  "api_key": "oci-genai"')
    print()
    try:
        server.serve_forever()
    except KeyboardInterrupt:
        print("\nShutting down.")
        server.shutdown()


if __name__ == "__main__":
    main()
