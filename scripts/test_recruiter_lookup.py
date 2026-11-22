import recruiter_lookup as lookup


def test_public_hiring_match_requires_attribution_and_role(monkeypatch):
    monkeypatch.setenv("JOBWATCH_BRAVE_SEARCH_API_KEY", "example")
    monkeypatch.setattr(lookup, "get_json", lambda *args: {"web": {"results": [
        {"url": "https://www.linkedin.com/posts/manish-singh_company-hiring",
         "title": "Manish Singh on LinkedIn",
         "description": "Capslock Marketplaces is hiring a Backend Engineer. Apply at manish@voiceclub.app."},
        {"url": "https://www.linkedin.com/posts/another-person",
         "title": "Another Person on LinkedIn",
         "description": "Capslock Marketplaces is hiring a Backend Engineer. Apply at manish@other.org."},
        {"url": "https://www.linkedin.com/in/manish",
         "title": "Manish Singh",
         "description": "Capslock Marketplaces is hiring a Designer. Apply at manish@other.org."},
        {"url": "https://directory.example.org/manish",
         "title": "Manish Singh",
         "description": "Capslock Marketplaces is hiring a Backend Engineer. Apply at manish@other.org."},
    ]}})
    assert lookup.public_candidates("manish", "Capslock Marketplaces", "Sr. Backend Engineer") == {
        "manish@voiceclub.app"
    }


def test_gmail_requires_matching_headers_and_rejects_relay(monkeypatch):
    monkeypatch.setenv("JOBWATCH_GMAIL_LOOKUP_REFRESH_TOKEN", "example")

    def response(url, headers, data=None):
        if data is not None:
            return {"access_token": "test"}
        if url.startswith(lookup.GMAIL + "?"):
            return {"messages": [{"id": candidate} for candidate in ("direct", "relay", "failed", "wrong-job")]}
        key = url.split("?")[0].rsplit("/", 1)[1]
        address, auth, subject = {
            "direct": ("Manish Singh <manish@voiceclub.app>", "dmarc=pass header.from=voiceclub.app",
                       "Sr. Backend Engineer at Capslock Marketplaces"),
            "relay": ("Manish Singh <manish@reply.instahyre.com>", "dmarc=pass header.from=reply.instahyre.com",
                      "Sr. Backend Engineer at Capslock Marketplaces"),
            "failed": ("Manish Singh <manish@example.org>", "dmarc=fail header.from=example.org",
                       "Sr. Backend Engineer at Capslock Marketplaces"),
            "wrong-job": ("Manish Singh <manish@example.org>", "dmarc=pass header.from=example.org",
                          "Designer at Capslock Marketplaces"),
        }[key]
        return {"payload": {"headers": [{"name": k, "value": v} for k, v in {
            "Subject": subject, "From": address, "Authentication-Results": auth,
        }.items()]}}

    monkeypatch.setattr(lookup, "get_json", response)
    assert lookup.gmail_candidates("manish", "Capslock Marketplaces", "Sr. Backend Engineer",
                                   "client", "secret", "https://oauth.example.org") == {"manish@voiceclub.app"}


def test_unavailable_and_conflicting_sources(monkeypatch):
    def find():
        return lookup.find_work_email("Manish", "Capslock Marketplaces", "Backend Engineer",
                                      client_id="", client_secret="", token_url="")

    monkeypatch.setattr(lookup, "public_candidates", lambda *_: {"manish@voiceclub.app"})
    monkeypatch.setattr(lookup, "gmail_candidates", lambda *_: {"manish@example.org"})
    assert find() == ""

    def unavailable(*_):
        raise OSError("Offline")

    monkeypatch.setattr(lookup, "gmail_candidates", unavailable)
    assert find() == "manish@voiceclub.app"
