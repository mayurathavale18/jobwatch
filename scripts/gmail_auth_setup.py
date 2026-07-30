#!/usr/bin/env python3
"""
One-time local OAuth2 flow for jobwatch's Gmail draft-outreach feature.

Usage: python3 scripts/gmail_auth_setup.py

Prerequisites (see docs/superpowers/specs/2026-07-27-founder-outreach-design.md):
  1. A Google Cloud Console project with the Gmail API enabled.
  2. An OAuth 2.0 Client ID, type "Desktop app" -- gives client_id + client_secret.
  3. OAuth consent screen in Testing mode, your email added as a test user,
     scope gmail.compose only.

Set GMAIL_CLIENT_ID and GMAIL_CLIENT_SECRET as environment variables (or
edit them in below) before running. This script opens your browser once,
you approve access, and it prints a refresh_token to paste into .env as
GMAIL_REFRESH_TOKEN -- the unattended cron pipeline uses that token
forever after (until revoked), no repeated browser flow needed.
"""
import http.server
import json
import os
import sys
import threading
import urllib.parse
import webbrowser
from urllib.request import urlopen, Request

CLIENT_ID = os.environ.get("GMAIL_CLIENT_ID", "")
CLIENT_SECRET = os.environ.get("GMAIL_CLIENT_SECRET", "")
REDIRECT_PORT = 8765
REDIRECT_URI = f"http://localhost:{REDIRECT_PORT}"
SCOPE = "https://www.googleapis.com/auth/gmail.compose"

_auth_code = {"value": None}


class _CallbackHandler(http.server.BaseHTTPRequestHandler):
    def do_GET(self):
        params = urllib.parse.parse_qs(urllib.parse.urlparse(self.path).query)
        _auth_code["value"] = params.get("code", [None])[0]
        self.send_response(200)
        self.end_headers()
        self.wfile.write(b"Auth complete -- you can close this tab and return to the terminal.")

    def log_message(self, format, *args):
        pass  # silence the default request logging


def main():
    if not CLIENT_ID or not CLIENT_SECRET:
        print("ERROR: set GMAIL_CLIENT_ID and GMAIL_CLIENT_SECRET env vars first.", file=sys.stderr)
        sys.exit(1)

    auth_url = "https://accounts.google.com/o/oauth2/v2/auth?" + urllib.parse.urlencode({
        "client_id": CLIENT_ID,
        "redirect_uri": REDIRECT_URI,
        "response_type": "code",
        "scope": SCOPE,
        "access_type": "offline",
        "prompt": "consent",
    })

    server = http.server.HTTPServer(("localhost", REDIRECT_PORT), _CallbackHandler)
    thread = threading.Thread(target=server.handle_request)
    thread.start()

    print(f"Opening browser for consent: {auth_url}")
    webbrowser.open(auth_url)
    thread.join(timeout=120)

    code = _auth_code["value"]
    if not code:
        print("ERROR: did not receive an auth code within 120s.", file=sys.stderr)
        sys.exit(1)

    body = urllib.parse.urlencode({
        "client_id": CLIENT_ID,
        "client_secret": CLIENT_SECRET,
        "code": code,
        "redirect_uri": REDIRECT_URI,
        "grant_type": "authorization_code",
    }).encode("utf-8")
    req = Request(
        "https://oauth2.googleapis.com/token", data=body, method="POST",
        headers={"Content-Type": "application/x-www-form-urlencoded"},
    )
    with urlopen(req, timeout=15) as resp:
        data = json.loads(resp.read().decode("utf-8"))

    refresh_token = data.get("refresh_token")
    if not refresh_token:
        print(f"ERROR: no refresh_token in response: {data}", file=sys.stderr)
        sys.exit(1)

    print("\nSuccess. Add this to .env (both laptop and EC2):\n")
    print(f"export GMAIL_REFRESH_TOKEN={refresh_token}")


if __name__ == "__main__":
    main()
