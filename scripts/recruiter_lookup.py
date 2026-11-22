"""Conservative opt-in work email discovery for the Instahyre draft worker."""
from email.utils import getaddresses
from html import unescape
import json
import os
import re
from urllib.parse import quote, urlencode, urlsplit
from urllib.request import Request, urlopen

GMAIL = "https://gmail.googleapis.com/gmail/v1/users/me/messages"
BRAVE = "https://api.search.brave.com/res/v1/web/search"
EMAIL = re.compile(r"(?<![\w.+-])([a-z0-9][a-z0-9._+-]*@[a-z0-9.-]+\.[a-z]{2,})(?![\w-]|\.[\w-])", re.I)
PERSONAL = {"gmail.com", "googlemail.com", "yahoo.com", "outlook.com", "hotmail.com", "icloud.com",
            "proton.me", "protonmail.com", "aol.com", "live.com", "me.com", "ymail.com", "gmx.com", "mail.com"}
RELAY = {"instahyre.com", "tidyhire.app", "greenhouse.io", "lever.co", "ashbyhq.com", "workday.com",
         "myworkday.com", "myworkdayjobs.com", "linkedin.com", "gmail.com", "sendgrid.net", "amazonses.com",
         "jobs.lever.co", "indeed.com"}


def words(value):
    return re.findall(r"[^\W_]+", value.casefold())


def clean(value):
    return unescape(re.sub(r"<[^>]*>", " ", value))


def contains_phrase(text, phrase):
    return f" {' '.join(words(phrase))} " in f" {' '.join(words(text))} "


def relevant(text, company, role):
    company_words = words(company)
    if company_words and company_words[-1] in {"inc", "ltd", "llc", "limited"}:
        company_words.pop()
    role_words = words(role)
    specialization = [w for w in role_words if w not in {
        "sr", "senior", "jr", "junior", "lead", "engineer", "developer", "software",
        "development", "i", "ii", "iii", "1", "2", "3",
    }]
    role_matches = (
        all(word in words(text) for word in specialization)
        if specialization else contains_phrase(text, " ".join(role_words))
    )
    return bool(company_words and role_words and contains_phrase(text, " ".join(company_words)) and role_matches)


def work_address(address, name):
    if not EMAIL.fullmatch(address):
        return ""
    address = address.casefold()
    local, domain = address.rsplit("@", 1)
    if any(domain == blocked or domain.endswith("." + blocked) for blocked in PERSONAL | RELAY):
        return ""
    if local in {"hr", "jobs", "careers", "recruiting", "hiring", "support", "admin", "contact", "info"}:
        return ""
    if re.search(r"no[._-]?reply|do[._-]?not[._-]?reply|notification|bot", local):
        return ""
    # A named mailbox adds an independent check beyond a co-occurring name.
    first = words(name)[0]
    mailbox_name = words(local.split("+", 1)[0])
    return address if len(first) >= 3 and first in mailbox_name else ""


def get_json(url, headers, data=None):
    request = Request(url, headers=headers, data=data)
    with urlopen(request, timeout=6) as response:
        return json.load(response)


def public_candidates(name, company, role):
    key = os.environ.get("JOBWATCH_BRAVE_SEARCH_API_KEY")
    if not key:
        return set()
    role_query = " ".join(word for word in words(role)
                          if word not in {"sr", "senior", "jr", "junior", "i", "ii", "iii", "1", "2", "3"})
    query = urlencode({"q": f'"{name}" "{company}" "{role_query}" hiring', "count": 8,
                       "extra_snippets": "true"})
    data = get_json(BRAVE + "?" + query, {"X-Subscription-Token": key, "Accept": "application/json"})
    candidates = set()
    for item in data.get("web", {}).get("results", [])[:8]:
        url = urlsplit(item.get("url", ""))
        # Use attributable professional posts and profiles. Aggregators and
        # people-search pages can associate the right name with stale emails.
        if (url.scheme != "https" or not url.hostname or
            not (url.hostname == "linkedin.com" or url.hostname.endswith(".linkedin.com")) or
            not url.path.startswith(("/posts/", "/in/"))):
            continue
        title = clean(item.get("title", ""))
        if not contains_phrase(title, name):
            continue
        for snippet in [item.get("description", ""), *item.get("extra_snippets", [])]:
            content = clean(snippet)
            if not relevant(content, company, role) or not set(words(content)) & {"hiring", "apply", "resume", "cv"}:
                continue
            addresses = {address.casefold() for address in EMAIL.findall(content)}
            if len(addresses) == 1:
                candidate = work_address(next(iter(addresses)), name)
                if candidate:
                    candidates.add(candidate)
    return candidates


def gmail_candidates(name, company, role, client_id, client_secret, token_url):
    refresh = os.environ.get("JOBWATCH_GMAIL_LOOKUP_REFRESH_TOKEN")
    if not (refresh and client_id and client_secret):
        return set()
    body = urlencode({"client_id": client_id, "client_secret": client_secret,
                      "refresh_token": refresh, "grant_type": "refresh_token"}).encode()
    access = get_json(token_url, {"Content-Type": "application/x-www-form-urlencoded"}, body).get("access_token")
    if not access:
        return set()
    headers = {"Authorization": f"Bearer {access}"}
    q = urlencode({"q": f'newer_than:365d -in:drafts subject:"{" ".join(words(company))}" "{name}"',
                   "maxResults": 5})
    messages = get_json(GMAIL + "?" + q, headers).get("messages", [])
    metadata = urlencode([("format", "metadata"), ("metadataHeaders", "Subject"),
                          ("metadataHeaders", "From"), ("metadataHeaders", "To"),
                          ("metadataHeaders", "Authentication-Results")])
    candidates = set()
    for message in messages[:5]:
        result = get_json(GMAIL + "/" + quote(message["id"], safe="") + "?" + metadata, headers)
        fields = {}
        for entry in result.get("payload", {}).get("headers", []):
            fields.setdefault(entry["name"].lower(), []).append(entry["value"])
        subject = " ".join(fields.get("subject", []))
        if not relevant(subject, company, role):
            continue
        sent = "SENT" in result.get("labelIds", [])
        for display_name, address in getaddresses(fields.get("to" if sent else "from", [])):
            candidate = work_address(address, name)
            if not candidate or not contains_phrase(display_name, name):
                continue
            if not sent:
                domain = candidate.split("@", 1)[1]
                auth = " ".join(fields.get("authentication-results", [])).casefold()
                if not re.search(r"\bdmarc=pass\b[^;]*\bheader\.from=" + re.escape(domain) + r"(?=[;\s]|$)", auth):
                    continue
            candidates.add(candidate)
    return candidates


def find_work_email(name, company, role, *, client_id, client_secret, token_url):
    name = " ".join(words(name))
    if not name or len(name.split()[0]) < 3 or not company or not role:
        return ""
    matches = set()
    for source, args in (
        (public_candidates, (name, company, role)),
        (gmail_candidates, (name, company, role, client_id, client_secret, token_url)),
    ):
        try:
            matches.update(source(*args))
        except (OSError, KeyError, TypeError, ValueError):
            # An unavailable lookup must not block a draft or log secrets.
            continue
    return next(iter(matches)) if len(matches) == 1 else ""
