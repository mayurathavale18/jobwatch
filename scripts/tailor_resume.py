#!/usr/bin/env python3
"""
Jobwatch resume tailoring agent.
Generates tailored LaTeX resumes for unprocessed 'new' jobs, compiles to PDF,
verifies one page, updates tracker, and sends Telegram notifications.
"""

import json
import os
import re
import shutil
import sqlite3
import subprocess
import sys
import time
import html
from datetime import datetime
from urllib.request import urlopen, Request
from urllib.error import HTTPError, URLError
from pathlib import Path

# -----------------------------------------------------------------------------
# Paths and constants
# -----------------------------------------------------------------------------
HOME = Path.home()
# Resolved relative to this script's own location, not $HOME/jobwatch --
# that assumed the repo always sits one level under $HOME, true on a laptop
# checkout but false for the deployed jobwatch service user, whose $HOME
# *is* the repo root already (see deploy/setup.sh's useradd --home). Same
# bug class already fixed in the shell cron wrappers (see their own
# comments); this Python one was missed in that pass since it was grepped
# for "$HOME/jobwatch" in *.sh only.
REPO_ROOT = Path(__file__).resolve().parent.parent
DB_PATH = REPO_ROOT / "jobwatch.db"
MASTER_TEX_PATH = REPO_ROOT / "resume" / "master.tex"
FACTS_MD_PATH = REPO_ROOT / "resume" / "facts.md"
TAILORED_JSON_PATH = REPO_ROOT / "resume" / "tailored.json"
# Intentionally still $HOME-based, unlike the above: this is real output
# meant to land in whichever environment's own home directory (the user's
# ~/Documents on a laptop, the service user's home on the server), not a
# path inside the repo checkout itself.
OUTPUT_ROOT = HOME / "Documents" / "mayur-athavale-resume"
DATE_STR = datetime.now().strftime("%d-%m-%y")
TODAY_ISO = datetime.now().strftime("%Y-%m-%d")

MAX_PER_CYCLE = 8
DAILY_BUDGET = 20
RATE_LIMIT_SECONDS = 60

# Telegram config from environment
TG_TOKEN = os.environ.get("JOBWATCH_TG_TOKEN", "")
TG_CHAT = os.environ.get("JOBWATCH_TG_CHAT", "")

# OpenCode Go config (OpenAI-compatible gateway, https://opencode.ai/docs/zen).
# Used only for JD extraction fallback (Task 9), judging (Task 10), and
# bounded bullet rewording (Task 13) -- never for resume content selection.
OPENCODE_API_KEY = os.environ.get("OPENCODE_API_KEY", "")
OPENCODE_BASE_URL = "https://opencode.ai/zen/go/v1"
DEFAULT_OPENCODE_MODEL = "deepseek-v4-pro"

# -----------------------------------------------------------------------------
# Logging helpers
# -----------------------------------------------------------------------------
def log(msg):
    ts = datetime.now().strftime("%Y-%m-%d %H:%M:%S")
    print(f"[{ts}] {msg}", flush=True)

def slugify(text):
    s = text.lower().strip()
    s = re.sub(r"[^a-z0-9\s-]", "", s)
    s = re.sub(r"\s+", "-", s)
    s = re.sub(r"-+", "-", s)
    return s.strip("-")

def company_slug(company_name, url=""):
    name = company_name.lower().strip()
    if "databricks" in name:
        return "databricks"
    if "elastic" in name:
        return "elastic"
    if "stripe" in name:
        return "stripe"
    if "razorpay" in name:
        return "razorpay"
    if "cloudflare" in name:
        return "cloudflare"
    if "wells fargo" in name:
        return "wellsfargo"
    # Fallback: derive from URL domain
    m = re.search(r"https?://(?:www\.)?([^/]+)", url)
    if m:
        domain = m.group(1).split(".")[0]
        return slugify(domain)
    return slugify(name)

# -----------------------------------------------------------------------------
# JD fetching
# -----------------------------------------------------------------------------
def fetch_greenhouse_job(company_slug, gh_job_id):
    """Fetch full job description from Greenhouse public API."""
    api_url = f"https://boards-api.greenhouse.io/v1/boards/{company_slug}/jobs/{gh_job_id}"
    try:
        req = Request(api_url, headers={"User-Agent": "jobwatch-resume-tailor/1.0"})
        with urlopen(req, timeout=15) as resp:
            data = json.loads(resp.read().decode("utf-8"))
        content = html.unescape(data.get("content", ""))
        # Strip HTML
        text = re.sub(r"<script.*?</script>", "", content, flags=re.S | re.I)
        text = re.sub(r"<style.*?</style>", "", text, flags=re.S | re.I)
        text = re.sub(r"<[^>]+>", " ", text)
        text = re.sub(r"\s+", " ", text).strip()
        return {
            "title": data.get("title", ""),
            "company_name": data.get("company_name", ""),
            "content_text": text,
            "content_html": content,
            "location": data.get("location", {}).get("name", ""),
            "absolute_url": data.get("absolute_url", ""),
        }
    except Exception as e:
        log(f"WARN: Failed to fetch JD for {company_slug}/{gh_job_id}: {e}")
        return None

def html_to_jd_text(raw_html):
    """Strip a job posting page down to plausible JD body text: drop
    script/style/nav/header/footer blocks entirely (boilerplate, never JD
    content), convert block tags to newlines so paragraphs don't run
    together, strip remaining tags, decode entities, collapse whitespace.
    Lightweight and heuristic -- good enough for most static ATS pages,
    not a real DOM parser. See fetch_jd_generic for the fallback when this
    isn't enough (JS-rendered pages return almost nothing usable here).
    """
    text = raw_html
    for tag in ("script", "style", "nav", "header", "footer"):
        text = re.sub(rf"<{tag}[^>]*>.*?</{tag}>", " ", text, flags=re.S | re.I)
    text = re.sub(r"</(p|div|li|h[1-6])\s*>", "\n", text, flags=re.I)
    text = re.sub(r"<br\s*/?\s*>", "\n", text, flags=re.I)
    text = re.sub(r"<li[^>]*>", "\n- ", text, flags=re.I)
    text = re.sub(r"<[^>]+>", " ", text)
    text = html.unescape(text)
    text = re.sub(r"[ \t]+", " ", text)
    text = re.sub(r"\n\s*\n+", "\n\n", text)
    return text.strip()

# Below this many characters of scraped text, fetch_jd_generic treats the
# page as effectively empty (JS-rendered SPA shell, blocked fetch, etc)
# and falls back to the OpenCode Go LLM extraction path (see Task 9).
MIN_USABLE_JD_CHARS = 150

def fetch_jd_generic(url):
    """Fetch full JD text from an arbitrary (non-Greenhouse) job posting
    URL. Tries a free HTML scrape first; if that yields too little usable
    text (JS-rendered SPA shell, blocked fetch, etc), falls back to one
    OpenCode Go call asking it to extract the JD text from the raw HTML.
    Returns the same dict shape as fetch_greenhouse_job. Returns None only
    on a hard fetch failure -- a thin/empty result after both attempts
    still returns a dict (with whatever text was found), so the caller
    can flag "JD text unavailable" rather than treat it as a fetch error.
    """
    try:
        req = Request(url, headers={"User-Agent": "jobwatch-resume-tailor/1.0"})
        with urlopen(req, timeout=15) as resp:
            raw_html = resp.read().decode("utf-8", errors="replace")
    except Exception as e:
        log(f"WARN: Failed to fetch JD page for {url}: {e}")
        return None

    text = html_to_jd_text(raw_html)

    if len(text) < MIN_USABLE_JD_CHARS:
        log(f"JD scrape too thin ({len(text)} chars) for {url}; trying OpenCode extraction")
        llm_text = call_opencode(
            system_prompt=(
                "You extract job description text from raw HTML. Return ONLY the "
                "job description body text (responsibilities, requirements, "
                "qualifications) as plain text, no HTML, no commentary. If the HTML "
                "genuinely contains no job description content, return an empty string."
            ),
            user_content=raw_html[:20000],
        )
        if llm_text and len(llm_text.strip()) >= MIN_USABLE_JD_CHARS:
            text = llm_text.strip()

    return {
        "title": "",
        "company_name": "",
        "content_text": text,
        "content_html": raw_html,
        "location": "",
        "absolute_url": url,
    }

def call_opencode(system_prompt, user_content, model=DEFAULT_OPENCODE_MODEL, timeout=20):
    """Call OpenCode Go's OpenAI-compatible chat completions endpoint.
    Returns the assistant's raw message content, or None on any failure
    (missing key, timeout, non-200, malformed response) -- callers always
    have a non-LLM fallback and must never block on this.
    """
    if not OPENCODE_API_KEY:
        return None

    payload = json.dumps({
        "model": model,
        "messages": [
            {"role": "system", "content": system_prompt},
            {"role": "user", "content": user_content},
        ],
    }).encode("utf-8")

    try:
        req = Request(
            f"{OPENCODE_BASE_URL}/chat/completions",
            data=payload,
            method="POST",
            headers={
                "Authorization": f"Bearer {OPENCODE_API_KEY}",
                "Content-Type": "application/json",
            },
        )
        with urlopen(req, timeout=timeout) as resp:
            data = json.loads(resp.read().decode("utf-8"))
        return data["choices"][0]["message"]["content"]
    except Exception as e:
        log(f"WARN: OpenCode call failed: {e}")
        return None

RULE_BASED_REJECT_THRESHOLD = 0.4

def llm_judge(jd_text, resume_text, title, company):
    """Judge how a busy recruiter (5-10 seconds per resume, 100+ resumes
    to screen) would react to this resume against this JD: screen it
    forward, or reject-risk. Always returns a usable result -- falls back
    to a rule-based verdict (score_coverage threshold) on any LLM
    failure, timeout, or malformed response, tagged via "source" so the
    Telegram message can show which one produced it.
    """
    raw = call_opencode(
        system_prompt=(
            "You are a hiring manager screening resumes for a "
            f"{title} role at {company}. You see 100+ resumes and spend "
            "5-10 seconds on each. Given the job description and a "
            "candidate's resume text, decide: would you screen this "
            "resume forward for a closer look, or is it reject-risk? "
            "Respond with ONLY valid JSON, no markdown fences, no "
            "commentary, in this exact shape: "
            '{"verdict": "screen"|"reject_risk", '
            '"missing_keywords": ["keyword1", "keyword2"], '
            '"reason": "one sentence explaining the verdict"}'
        ),
        user_content=f"JOB DESCRIPTION:\n{jd_text}\n\nRESUME:\n{resume_text}",
    )

    if raw:
        try:
            parsed = json.loads(raw.strip().strip("`").removeprefix("json").strip())
            if parsed.get("verdict") in ("screen", "reject_risk") and isinstance(parsed.get("missing_keywords"), list):
                return {
                    "verdict": parsed["verdict"],
                    "missing_keywords": parsed["missing_keywords"],
                    "reason": str(parsed.get("reason", "")),
                    "source": "llm",
                }
        except (json.JSONDecodeError, AttributeError):
            log("WARN: llm_judge got malformed JSON from OpenCode, falling back to rule-based")

    jd_keywords = extract_keywords(jd_text)
    score, _covered, not_covered, not_truthful = score_coverage(jd_keywords, resume_text)
    verdict = "reject_risk" if score < RULE_BASED_REJECT_THRESHOLD else "screen"
    return {
        "verdict": verdict,
        "missing_keywords": not_covered + not_truthful,
        "reason": f"Rule-based: {score:.2f} keyword coverage (LLM unavailable).",
        "source": "rule_based",
    }

def extract_gh_job_id(url):
    m = re.search(r"gh_jid=(\d+)", url)
    if m:
        return m.group(1)
    return None

# -----------------------------------------------------------------------------
# Keyword extraction and coverage scoring
# -----------------------------------------------------------------------------
# Keyword groups derived from facts.md (production skills + common JD terms)
KEYWORD_CANDIDATES = {
    # Languages
    "go", "golang", "python", "typescript", "javascript", "java", "scala", "rust", "sql", "bash", "kotlin",
    # Backend / frameworks
    "rest", "rest api", "rest apis", "microservices", "fastapi", "nestjs", "node.js", "nodejs",
    "gin", "krakend", "api gateway", "api gateways", "temporal", "graphql", "hasura", "trpc",
    "sse", "streaming", "webhooks", "event-driven", "event driven architecture",
    "spring", "spring boot",
    # Data / queues / search
    "postgresql", "postgres", "mysql", "redis", "valkey", "dynamodb", "opensearch", "elasticsearch",
    "mongodb", "s3", "s3 tables", "spark", "sqs", "sns", "dynamodb streams", "eventbridge",
    "redshift", "athena", "rabbitmq", "kafka", "cassandra",
    # Infra / DevOps
    "aws", "gcp", "azure", "terraform", "docker", "kubernetes", "k8s", "github actions", "ci/cd",
    "ecs", "ec2", "rds", "lambda", "secrets manager", "cloudwatch", "cloudfront", "route 53",
    "vpc", "iam", "fargate", "bigquery", "ansible", "jenkins",
    # AI / LLM
    "langgraph", "langchain", "rag", "llm", "ai", "ml", "machine learning", "multi-agent",
    "agent", "agents", "agentic", "litellm", "mcp", "vector search", "embedding", "embeddings",
    "knn", "bm25", "hybrid retrieval", "pytorch", "tensorflow", "scikit-learn",
    # Frontend
    "react", "next.js", "nextjs", "vite", "nx", "react native", "expo", "pwa",
    "full stack", "fullstack", "frontend",
    # Concepts
    "distributed systems", "system design", "concurrency", "multi-tenant", "multitenant",
    "secure coding", "solid", "resiliency", "observability", "metrics", "logging", "tracing",
    "monitoring", "alerting", "dashboards", "reliability", "scalability", "performance",
    "idempotency", "retries", "dead-letter", "dlq", "prompt engineering",
    # Role-specific
    "forward deployed engineer", "fde", "solutions engineer", "solutions architect",
    "data engineer", "backend engineer", "software engineer", "sre", "site reliability",
    "security", "networking", "billing", "finance",
    # Recognized-only gaps (not in truthful_skills -- real, not fabricated)
    "payments", "risk", "fraud", "c++", "ai security", "prompt injection", "jailbreak",
}

def normalize_keyword(kw):
    return re.sub(r"\s+", " ", kw.lower().strip())

def extract_keywords(jd_text):
    """Extract JD keywords that appear in our known keyword set."""
    text = jd_text.lower()
    found = set()
    for kw in KEYWORD_CANDIDATES:
        # Use word boundaries for short terms
        if len(kw) <= 4:
            pattern = r"(?:^|[\s,;()\[\]{}|/])" + re.escape(kw) + r"(?:$|[\s,;()\[\]{}|/])"
        else:
            pattern = re.escape(kw)
        if re.search(pattern, text):
            found.add(kw)
    return sorted(found)

def keyword_in_text(kw, text):
    text = text.lower()
    if len(kw) <= 4:
        pattern = r"(?:^|[\s,;()\[\]{}|/])" + re.escape(kw) + r"(?:$|[\s,;()\[\]{}|/])"
    else:
        pattern = re.escape(kw)
    return bool(re.search(pattern, text))

# Skills Mayur truthfully has per facts.md. A JD keyword landing here but
# absent from the resume text is a real coverage gap worth closing (see
# inject_keyword_emphasis); one NOT in this set is a fabrication risk and
# must never be added to the resume, no matter how well it'd score.
TRUTHFUL_SKILLS = {
    "go", "golang", "python", "typescript", "javascript", "java", "sql", "bash",
    "rest", "rest api", "rest apis", "microservices", "fastapi", "nestjs", "node.js", "nodejs",
    "gin", "krakend", "api gateway", "api gateways", "temporal", "graphql", "hasura",
    "sse", "streaming", "webhooks", "event-driven", "event driven architecture",
    "postgresql", "postgres", "mysql", "redis", "valkey", "dynamodb", "opensearch",
    "mongodb", "s3", "s3 tables", "spark", "sqs", "sns", "dynamodb streams", "eventbridge",
    "redshift", "athena",
    "aws", "terraform", "docker", "github actions", "ci/cd",
    "ecs", "ec2", "rds", "lambda", "secrets manager", "cloudwatch", "fargate",
    "langgraph", "langchain", "rag", "llm", "ai", "ml", "machine learning", "multi-agent",
    "agent", "agents", "agentic", "litellm", "mcp", "vector search", "embedding", "embeddings",
    "knn", "bm25", "hybrid retrieval",
    "react", "next.js", "nextjs", "vite", "nx", "react native", "expo", "pwa",
    "full stack", "fullstack", "frontend", "prompt engineering",
    "distributed systems", "system design", "concurrency", "multi-tenant", "multitenant",
    "secure coding", "solid", "resiliency", "observability", "metrics", "logging", "tracing",
    "monitoring", "alerting", "reliability", "scalability", "performance",
    "idempotency", "retries",
    "forward deployed engineer", "fde", "solutions engineer", "solutions architect",
    "data engineer", "backend engineer", "software engineer", "sre", "site reliability",
    "security", "networking",
}

# Facts.md's "familiar" tier: concepts known, NOT hands-on production use.
# A JD keyword landing here is eligible for a resume claim, but only with
# hedging language ("exposure to", "familiarity with") -- never phrased as
# if hands-on. See facts.md's "Skills inventory (honesty tiers)" section.
FAMILIAR_SKILLS = {
    "kafka", "kubernetes", "k8s", "gcp", "langchain", "mongodb", "rabbitmq",
    "trpc", "firebase",
}

# Mayur's curated "soft hand" tools -- see facts.md's "Adjacent Skills"
# section, which this must be kept in sync with (same manual-sync pattern
# as TRUTHFUL_SKILLS itself). Not in facts.md's production/used/familiar
# tiers at all, but a fair hedged "exposure to" claim per his own judgment.
ADJACENT_SKILLS = {
    "cassandra", "bigquery", "ansible", "jenkins",
    "pytorch", "tensorflow", "scikit-learn",
    "kotlin", "spring", "spring boot",
}

def classify_keyword(kw):
    """Classify a JD keyword into one of four honesty tiers for resume
    claims: "direct" (facts.md production/used tier, claim plainly),
    "hedged_familiar" (facts.md familiar tier, claim only with hedge
    language), "hedged_adjacent" (Mayur's curated ADJACENT_SKILLS, claim
    only with hedge language), or "fabrication_risk" (nothing related at
    all -- never claimed).
    """
    if kw in TRUTHFUL_SKILLS:
        return "direct"
    if kw in FAMILIAR_SKILLS:
        return "hedged_familiar"
    if kw in ADJACENT_SKILLS:
        return "hedged_adjacent"
    return "fabrication_risk"

def score_coverage_tiered(keywords, resume_text):
    """Like score_coverage, but buckets keywords by honesty tier instead
    of a flat covered/not-covered split. Returns a dict with:
    - score: fraction of `keywords` actually present in resume_text
    - direct: direct-tier keywords present in resume_text
    - hedged: hedged-tier (familiar or adjacent) keywords present in
      resume_text
    - missing: keywords absent from resume_text, regardless of tier
      (includes fabrication_risk keywords, which should never be
      injected regardless of presence)
    """
    direct, hedged, missing = [], [], []
    for kw in keywords:
        present = keyword_in_text(kw, resume_text)
        tier = classify_keyword(kw)
        if present and tier == "direct":
            direct.append(kw)
        elif present and tier in ("hedged_familiar", "hedged_adjacent"):
            hedged.append(kw)
        else:
            missing.append(kw)
    score = (len(direct) + len(hedged)) / len(keywords) if keywords else 1.0
    return {"score": round(score, 2), "direct": direct, "hedged": hedged, "missing": missing}

# Display casing for keywords that might get injected into bullet text or
# the Additional Skills line (see inject_keyword_emphasis) -- KEYWORD_CANDIDATES
# and TRUTHFUL_SKILLS are stored lowercase for matching, but the resume text
# itself needs real casing (AWS, not Aws). Anything missing here falls back
# to .title(), which is fine for plain words but wrong for acronyms.
CANONICAL_CASE = {
    "go": "Go", "golang": "Golang", "python": "Python", "typescript": "TypeScript",
    "javascript": "JavaScript", "java": "Java", "sql": "SQL", "bash": "Bash",
    "rest": "REST", "rest api": "REST API", "rest apis": "REST APIs", "microservices": "Microservices",
    "fastapi": "FastAPI", "nestjs": "NestJS", "node.js": "Node.js", "nodejs": "Node.js",
    "gin": "Gin", "krakend": "KrakenD", "api gateway": "API Gateway", "api gateways": "API Gateways",
    "temporal": "Temporal", "graphql": "GraphQL", "hasura": "Hasura",
    "sse": "SSE", "streaming": "Streaming", "webhooks": "Webhooks", "event-driven": "Event-driven",
    "event driven architecture": "Event-driven Architecture",
    "postgresql": "PostgreSQL", "postgres": "Postgres", "mysql": "MySQL", "redis": "Redis",
    "valkey": "Valkey", "dynamodb": "DynamoDB", "opensearch": "OpenSearch", "mongodb": "MongoDB",
    "s3": "S3", "s3 tables": "S3 Tables", "spark": "Spark", "sqs": "SQS", "sns": "SNS",
    "dynamodb streams": "DynamoDB Streams", "eventbridge": "EventBridge",
    "redshift": "Redshift", "athena": "Athena",
    "aws": "AWS", "terraform": "Terraform", "docker": "Docker", "github actions": "GitHub Actions",
    "ci/cd": "CI/CD",
    "ecs": "ECS", "ec2": "EC2", "rds": "RDS", "lambda": "Lambda", "secrets manager": "Secrets Manager",
    "cloudwatch": "CloudWatch", "fargate": "Fargate",
    "langgraph": "LangGraph", "langchain": "LangChain", "rag": "RAG", "llm": "LLM", "ai": "AI",
    "ml": "ML", "machine learning": "Machine Learning", "multi-agent": "Multi-Agent",
    "agent": "Agent", "agents": "Agents", "agentic": "Agentic", "litellm": "LiteLLM", "mcp": "MCP",
    "vector search": "Vector Search", "embedding": "Embedding", "embeddings": "Embeddings",
    "knn": "KNN", "bm25": "BM25", "hybrid retrieval": "Hybrid Retrieval",
    "react": "React", "next.js": "Next.js", "nextjs": "Next.js", "vite": "Vite", "nx": "NX",
    "react native": "React Native", "expo": "Expo", "pwa": "PWA",
    "full stack": "Full Stack", "fullstack": "Fullstack", "frontend": "Frontend",
    "prompt engineering": "Prompt Engineering",
    "distributed systems": "Distributed Systems", "system design": "System Design",
    "concurrency": "Concurrency", "multi-tenant": "Multi-Tenant", "multitenant": "Multitenant",
    "secure coding": "Secure Coding", "solid": "SOLID", "resiliency": "Resiliency",
    "observability": "Observability", "metrics": "Metrics", "logging": "Logging", "tracing": "Tracing",
    "monitoring": "Monitoring", "alerting": "Alerting", "reliability": "Reliability",
    "scalability": "Scalability", "performance": "Performance",
    "idempotency": "Idempotency", "retries": "Retries",
    "forward deployed engineer": "Forward Deployed Engineer", "fde": "FDE",
    "solutions engineer": "Solutions Engineer", "solutions architect": "Solutions Architect",
    "data engineer": "Data Engineer", "backend engineer": "Backend Engineer",
    "software engineer": "Software Engineer", "sre": "SRE", "site reliability": "Site Reliability",
    "security": "Security", "networking": "Networking",
    "cassandra": "Cassandra", "bigquery": "BigQuery", "ansible": "Ansible", "jenkins": "Jenkins",
    "pytorch": "PyTorch", "tensorflow": "TensorFlow", "scikit-learn": "Scikit-learn",
    "kotlin": "Kotlin", "spring": "Spring", "spring boot": "Spring Boot",
}

def canonical_case(kw):
    return CANONICAL_CASE.get(kw, kw.title())

def score_coverage(keywords, resume_text):
    """Return coverage score and lists of covered/not-covered keywords."""
    covered = []
    not_covered = []
    not_truthful = []
    for kw in keywords:
        if keyword_in_text(kw, resume_text):
            covered.append(kw)
        elif kw in TRUTHFUL_SKILLS:
            not_covered.append(kw)
        else:
            not_truthful.append(kw)
    score = len(covered) / len(keywords) if keywords else 1.0
    return round(score, 2), covered, not_covered, not_truthful

# -----------------------------------------------------------------------------
# Resume generation
# -----------------------------------------------------------------------------
def load_master_tex():
    with open(MASTER_TEX_PATH, "r", encoding="utf-8") as f:
        return f.read()

def determine_focus(jd_text, title):
    """Determine the primary focus area from JD/title."""
    text = (title + " " + jd_text).lower()
    focus = []
    if any(k in text for k in ["ai engineer", "machine learning", "ml engineer", "llm", "rag", "langgraph", "agentic", "multi-agent"]):
        focus.append("ai_llm")
    if any(k in text for k in ["data engineer", "data & ai", "data and ai", "spark", "analytics", "data pipeline"]):
        focus.append("data")
    if any(k in text for k in ["observability", "monitoring", "metrics", "logging", "tracing", "cloudwatch"]):
        focus.append("observability")
    if any(k in text for k in ["networking", "network infrastructure", "network engineer"]):
        focus.append("networking")
    if any(k in text for k in ["security", "secure coding", "security analytics"]):
        focus.append("security")
    if any(k in text for k in ["forward deployed", "fde", "solutions engineer", "solutions architect", "customer facing", "customer-facing"]):
        focus.append("forward_deployed")
    if any(k in text for k in ["backend", "distributed systems", "microservices", "api gateway", "go", "golang"]):
        focus.append("backend")
    if any(k in text for k in ["billing", "finance", "payments"]):
        focus.append("billing_finance")
    if any(k in text for k in ["full stack", "fullstack", "frontend"]):
        focus.append("full_stack")
    if not focus:
        focus.append("backend")
    return focus

def _jd_first_order(skills_str, jd_keywords):
    """Reorder a category's comma-separated skill string so JD-matched
    items come first, preserving relative order within each group
    otherwise -- a keyword-forward ordering for a recruiter's 5-10 second
    scan, not a random shuffle."""
    skills = [s.strip() for s in skills_str.split(",")]
    matched = [s for s in skills if any(keyword_in_text(kw, s) for kw in jd_keywords)]
    unmatched = [s for s in skills if s not in matched]
    return ", ".join(matched + unmatched)


def build_skills_section(focus, jd_keywords):
    """Build the Technical Skills section, reordered by relevance."""
    # Base categories. Sub-technologies are flattened into the top-level
    # comma list rather than nested in parens (e.g. "AWS, ECS, EC2, ..."
    # not "AWS (ECS, EC2, ...)") -- ATS skill-taggers that split on
    # top-level commas only were reading the parenthetical form as one long
    # unmatched string instead of recognizing each technology as its own
    # keyword. Must match master.tex's Skills section structure (see its
    # own comment) -- this function overwrites that section wholesale for
    # every tailored resume, so drift here silently undoes that fix.
    categories = {
        "Languages": "Go, Python, TypeScript, JavaScript, SQL, Bash",
        "Backend": "REST APIs, Microservices, FastAPI, NestJS, Node.js, API Gateways, KrakenD, Event-driven Architecture, SQS, SNS, DynamoDB Streams, Temporal Workflows",
        "Databases": "PostgreSQL, MySQL, Redis, DynamoDB, OpenSearch, MongoDB",
        "Infrastructure": "AWS, ECS, EC2, RDS, SQS, SNS, DynamoDB, Lambda, Secrets Manager, CloudWatch, Terraform, Docker, Kubernetes, GitHub Actions",
        "AI/LLM": "LangGraph, LangChain, RAG Pipelines, OpenSearch, KNN, BM25, Multi-Agent Orchestration",
        "Frontend": "React, Next.js 14, TypeScript, GraphQL, tRPC",
        "Concepts": "System Design, Distributed Systems, Concurrency, Multi-Tenant Architecture, Secure Coding, SOLID, Resiliency Patterns",
    }

    # Add truthful skills mentioned in JD but not in base categories
    # e.g. Java for Stripe roles
    jd_text_lower = ", ".join(jd_keywords).lower()
    if "java" in jd_text_lower and "java" not in categories["Languages"].lower():
        categories["Languages"] = "Go, Python, TypeScript, JavaScript, Java, SQL, Bash"
    if "spark" in jd_text_lower:
        categories["Databases"] = "PostgreSQL, MySQL, Redis, DynamoDB, OpenSearch, MongoDB, Spark, S3 Tables"
    if "observability" in jd_text_lower or "monitoring" in jd_text_lower or "metrics" in jd_text_lower or "logging" in jd_text_lower:
        categories["Infrastructure"] = "AWS, ECS, EC2, RDS, SQS, SNS, DynamoDB, Lambda, Secrets Manager, CloudWatch, Terraform, Docker, Kubernetes, GitHub Actions, CloudWatch Metrics, Structured Logging"

    # Reorder categories based on focus
    order = []
    if "observability" in focus:
        order = ["Infrastructure", "Languages", "Backend", "Databases", "AI/LLM", "Concepts", "Frontend"]
    elif "ai_llm" in focus or "data" in focus:
        order = ["AI/LLM", "Languages", "Backend", "Databases", "Infrastructure", "Concepts", "Frontend"]
    elif "networking" in focus or "security" in focus:
        order = ["Backend", "Languages", "Infrastructure", "Databases", "AI/LLM", "Concepts", "Frontend"]
    elif "forward_deployed" in focus:
        order = ["AI/LLM", "Backend", "Languages", "Databases", "Infrastructure", "Concepts", "Frontend"]
    else:
        order = ["Languages", "Backend", "Infrastructure", "Databases", "AI/LLM", "Concepts", "Frontend"]

    # Limit to 6 categories if we need to save space (handled by caller via tight flag)
    lines = []
    for cat in order:
        lines.append(f"\\techSkill{{{cat}}}{{{_jd_first_order(categories[cat], jd_keywords)}}}")
    return "\n".join(lines), order

def build_experience_bullets(focus, jd_keywords, tight=False):
    """Return ordered list of experience bullets, most relevant first."""
    all_bullets = [
        {
            "text": "Built and owned a \\textbf{Go-based API gateway} routing traffic across \\textbf{12 microservices} --- implemented auth middleware, rate limiting, and request routing with full KrakenD configuration; applied concurrency patterns to handle high-throughput routing safely.",
            "focus": ["backend", "security", "distributed"],
        },
        {
            "text": "Designed \\textbf{event-driven automation pipelines} using SQS, SNS, DynamoDB Streams, and webhooks; modelled data flows for reliability, idempotency, and explicit failure-mode handling across async consumers.",
            "focus": ["backend", "distributed", "data"],
        },
        {
            "text": "Designed \\textbf{Temporal workflows with customized templates} to orchestrate agent-driven business processes; built APIs to deploy agents with \\textbf{customized guidance prompts}, enabling tenant-specific behavior.",
            "focus": ["ai_llm", "backend", "forward_deployed"],
        },
        {
            "text": "Extracted structured requirements from user \\textbf{WhatsApp messages} and synced them into attached \\textbf{Google Sheets}; triggered webhook events on manual cell updates to send templated WhatsApp messages or execute custom business logic.",
            "focus": ["data", "forward_deployed"],
        },
        {
            "text": "Owned \\textbf{AWS infrastructure via Terraform} (ECS Fargate, RDS, SQS, SNS, DynamoDB, Secrets Manager, Elasticache/Valkey); maintained GitHub Actions CI/CD with automated integration tests, cutting release cycles by \\textbf{35\\%}.",
            "focus": ["backend", "observability", "security"],
        },
        {
            "text": "Built a \\textbf{RAG pipeline} with OpenSearch KNN + BM25 hybrid retrieval, Redis + PostgreSQL (RDS) dual-layer storage, and CloudWatch metrics and structured logging for observability.",
            "focus": ["ai_llm", "data", "observability"],
        },
        {
            "text": "Architected an \\textbf{agentic AI copilot} (FastAPI + LangGraph) that increased daily sales leads by \\textbf{200\\%+}; implemented 4 streaming strategies (SSE, AG-UI, LangGraph multi-mode).",
            "focus": ["ai_llm", "forward_deployed"],
        },
        {
            "text": "Migrated \\textbf{Webpack module-federation microfrontends to Vite} with a custom build runtime in NX: build times/latencies down \\textbf{70\\%}, dev build + local startup down \\textbf{80\\%} (with HMR).",
            "focus": ["frontend", "full_stack"],
        },
    ]

    # Score each bullet by focus overlap
    def score(b):
        score = 0
        for f in focus:
            if f in b["focus"]:
                score += 3
            # Also give partial credit for distributed/security focus
            if f == "security" and "security" in b["text"].lower():
                score += 2
            if f == "observability" and "observability" in b["text"].lower():
                score += 2
            if f == "networking" and "routing" in b["text"].lower():
                score += 1
        # Keyword matches
        for kw in jd_keywords:
            if keyword_in_text(kw, b["text"]):
                score += 1
        return score

    scored = sorted(all_bullets, key=lambda b: score(b), reverse=True)

    # In tight mode, remove weakest bullet(s)
    if tight:
        # Keep top 5
        scored = scored[:5]
    else:
        # Keep top 6 or 7 depending on focus
        if "observability" in focus or len(focus) > 2:
            scored = scored[:6]
        else:
            scored = scored[:7]

    return [b["text"] for b in scored]

MAX_INJECTED_KEYWORDS = 3

def inject_keyword_emphasis(bullets, skills_section, jd_keywords):
    """Close JD-keyword coverage gaps without fabricating anything.

    Direct-tier gaps (facts.md production/used skills missing from the
    fixed bullet text) get folded in plainly, same as before. Hedged-tier
    gaps (facts.md familiar tier, or Mayur's curated ADJACENT_SKILLS) get
    folded in too, but only with explicit hedging language -- never
    phrased as hands-on. Fabrication-risk keywords
    (score_coverage_tiered's "missing" bucket that isn't hedge-eligible)
    are never touched here.
    """
    draft_text = skills_section + "\n" + "\n".join(bullets)
    result = score_coverage_tiered(jd_keywords, draft_text)

    # Direct-tier gaps: present in jd_keywords, not yet in draft_text, and
    # classify as "direct" (i.e. would have landed in old score_coverage's
    # not_covered bucket).
    direct_gaps = [kw for kw in jd_keywords
                   if not keyword_in_text(kw, draft_text) and classify_keyword(kw) == "direct"]
    hedged_gaps = [kw for kw in jd_keywords
                   if not keyword_in_text(kw, draft_text)
                   and classify_keyword(kw) in ("hedged_familiar", "hedged_adjacent")]

    if not direct_gaps and not hedged_gaps:
        return bullets, None

    bullets = list(bullets)
    inject_direct, remaining_direct = direct_gaps[:MAX_INJECTED_KEYWORDS], direct_gaps[MAX_INJECTED_KEYWORDS:]
    inject_hedged, remaining_hedged = hedged_gaps[:MAX_INJECTED_KEYWORDS], hedged_gaps[MAX_INJECTED_KEYWORDS:]

    if inject_direct and bullets:
        bolded = ", ".join(f"\\textbf{{{canonical_case(kw)}}}" for kw in inject_direct)
        bullets[0] = bullets[0].rstrip() + f" Also applies {bolded} in this work."

    if inject_hedged and bullets:
        hedged_list = ", ".join(canonical_case(kw) for kw in inject_hedged)
        bullets[0] = bullets[0].rstrip() + f" Has working exposure to {hedged_list} for adjacent needs."

    extra_skills_line = None
    remaining = remaining_direct + remaining_hedged
    if remaining:
        plain = ", ".join(canonical_case(kw) for kw in remaining)
        extra_skills_line = f"\\techSkill{{Additional Relevant Skills}}{{{plain}}}"

    return bullets, extra_skills_line

NUMBER_RE = re.compile(r"\d+(?:\.\d+)?%?")


def _significant_words(text):
    """Lowercase word tokens, excluding short/common filler words that
    don't carry meaning for a resemblance check (rewording naturally
    changes articles/prepositions; content words should survive)."""
    stopwords = {
        "a", "an", "the", "and", "or", "but", "for", "with", "using", "via",
        "to", "of", "in", "on", "at", "by", "as", "is", "was", "were", "be",
        "this", "that", "these", "those", "it", "its", "into",
    }
    text = re.sub(r"\\[a-zA-Z]+\{", " ", text)  # strip LaTeX command openers (\textbf{, \texttt{, etc)
    text = text.replace("}", " ")  # strip closing braces
    words = re.findall(r"[a-z0-9]+", text.lower())
    return {w for w in words if w not in stopwords and len(w) > 2}


def reword_is_safe(original, reworded, approved_keywords):
    """Mechanically validate an LLM-reworded bullet before it's ever used:
    every number/percentage in the original must appear unchanged in the
    reworded version, no KEYWORD_CANDIDATES term may appear in the
    reworded version unless it was already in the original OR is in
    approved_keywords, and the reworded text must retain meaningful
    word-level overlap with the original (rejects wholesale replacement
    with unrelated/hallucinated content). This is a code check, not the
    LLM's word -- the LLM cannot talk its way past it.
    """
    if len(reworded.strip()) < 10:
        return False

    original_numbers = set(NUMBER_RE.findall(original))
    reworded_numbers = set(NUMBER_RE.findall(reworded))
    if not original_numbers.issubset(reworded_numbers):
        return False

    approved = {kw.lower() for kw in approved_keywords}
    original_lower = original.lower()
    for candidate in KEYWORD_CANDIDATES:
        if keyword_in_text(candidate, reworded) and not keyword_in_text(candidate, original_lower):
            if candidate not in approved:
                return False

    # Balanced \textbf{...} braces -- an unbalanced brace would break the
    # LaTeX compile downstream.
    if reworded.count("{") != reworded.count("}"):
        return False

    # Reject wholesale replacement: the negative checks above only catch
    # NEW numbers/keywords, not a fabricated sentence that happens to
    # contain none -- a hallucinated unrelated bullet with no digits and
    # no tracked keywords would otherwise pass unconditionally. Require
    # meaningful word-level overlap with the original as a positive
    # resemblance signal.
    original_words = _significant_words(original)
    if original_words:
        reworded_words = _significant_words(reworded)
        overlap = len(original_words & reworded_words) / len(original_words)
        if overlap < 0.5:
            return False

    return True

def llm_reword_bullet(bullet_text, direct_keywords, hedged_keywords):
    """Reword one already-selected bullet's phrasing to naturally surface
    the given pre-approved keywords -- selection is untouched (this never
    picks which keywords are fair game, only how to phrase the ones it's
    handed). Falls back to the original bullet unchanged on any LLM
    failure or safety-check rejection (see reword_is_safe): a phrasing
    task, never a content-invention task.
    """
    approved = direct_keywords + hedged_keywords
    if not approved:
        return bullet_text

    direct_list = ", ".join(canonical_case(k) for k in direct_keywords) or "none"
    hedged_list = ", ".join(canonical_case(k) for k in hedged_keywords) or "none"

    reworded = call_opencode(
        system_prompt=(
            "You reword a single resume bullet point to naturally surface "
            "specific keywords for a job application, for a recruiter "
            "scanning resumes in 5-10 seconds. Rules, all mandatory: "
            "(1) Preserve every number and percentage from the original "
            "bullet exactly. "
            "(2) Do not invent any new action, tool, metric, or outcome "
            "not already in the original bullet. "
            f"(3) You may plainly state these keywords if relevant: {direct_list}. "
            f"(4) These keywords may ONLY appear with hedging language like "
            f"'exposure to' or 'working knowledge of', never as if hands-on: {hedged_list}. "
            "(5) Preserve any LaTeX \\textbf{...} markup structure (balanced braces). "
            "(6) Return ONLY the reworded bullet text, no commentary, no quotes."
        ),
        user_content=bullet_text,
    )

    if not reworded:
        return bullet_text

    reworded = reworded.strip().strip('"')
    if not reworded:
        return bullet_text

    if not reword_is_safe(bullet_text, reworded, approved):
        log("WARN: llm_reword_bullet output failed safety check, falling back to original")
        return bullet_text

    return reworded

def build_projects_section(focus, jd_keywords):
    """Build the Projects & Writing section."""
    projects = [
        "\\resumeItem{\\textbf{zo-ai Conversation Threader} (Python, OpenSearch, LangGraph, FastAPI) --- Built the OpenSearch-backed dataset backbone for a copilot service: clustered non-contiguous WhatsApp messages into chronological threads using temporal proximity and semantic similarity, classified each thread into query types (custom, fixed, or LLM-discovered), and indexed semantic embeddings (titleVector, descriptionVector, queryTypeVector) for hybrid search retrieval of actionable items.}",
        "\\resumeItem{\\textbf{\\extlink{https://github.com/mayurathavale18/pr-manager}{\\underline{PR Manager}}} (Go, GitHub APIs, GitHub Actions) --- CLI tool automating end-to-end PR workflows: reviewer assignment, merge queuing, status checks, and cross-platform binary releases via \\textbf{GitHub Actions CI/CD}.}",
        "\\resumeItem{\\textbf{\\extlink{https://github.com/mayurathavale18/terminal-portfolio}{\\underline{Terminal Portfolio}}} (Go, BubbleTea, Wish, Lipgloss, AWS EC2) --- SSH-accessible TUI at \\texttt{ssh portfolio.mayurathavale.com} with multi-tab navigation, SQLite visitor analytics, and live presence tracking. Deployed on \\textbf{AWS EC2} with \\textbf{GitHub Actions} hot-deploying via SCP and systemd.}",
        "\\resumeItem{\\textbf{Technical Writing} --- Published articles on system security, cloud infrastructure, and networking on \\extlink{https://mayatdev1569.medium.com/}{\\underline{Medium}}, including self-hosted VPN tunneling on AWS.}",
    ]

    # Reorder / tweak based on focus
    if "ai_llm" in focus or "data" in focus:
        projects[0] = "\\resumeItem{\\textbf{zo-ai Conversation Threader} (Python, OpenSearch, Spark, S3 Tables) --- Built the OpenSearch-backed dataset backbone for a copilot service: clustered non-contiguous WhatsApp messages into chronological threads using temporal proximity and semantic similarity, classified each thread into query types, and indexed semantic embeddings for hybrid search retrieval of actionable items.}"
    if "observability" in focus or "backend" in focus:
        # Keep default
        pass

    return "\n\\vspace{6pt}\n\n".join(projects)

def generate_resume(job, jd_data, tight=False):
    """Generate tailored LaTeX resume as string."""
    master = load_master_tex()
    jd_text = jd_data.get("content_text", "")
    title = jd_data.get("title") or job["title"]

    focus = determine_focus(jd_text, title)
    jd_keywords = extract_keywords(jd_text)

    skills_section, skill_order = build_skills_section(focus, jd_keywords)
    if tight and len(skill_order) > 6:
        # Reduce to 6 categories: drop Frontend usually
        keep = [c for c in skill_order if c != "Frontend"][:6]
        # Rebuild
        categories = {
            "Languages": "Go, Python, TypeScript, JavaScript, SQL, Bash",
            "Backend": "REST APIs, Microservices, FastAPI, NestJS, Node.js, API Gateways, KrakenD, Event-driven Architecture, SQS, SNS, DynamoDB Streams, Temporal Workflows",
            "Databases": "PostgreSQL, MySQL, Redis, DynamoDB, OpenSearch, MongoDB",
            "Infrastructure": "AWS, ECS, EC2, RDS, SQS, SNS, DynamoDB, Lambda, Secrets Manager, CloudWatch, Terraform, Docker, Kubernetes, GitHub Actions",
            "AI/LLM": "LangGraph, LangChain, RAG Pipelines, OpenSearch, KNN, BM25, Multi-Agent Orchestration",
            "Frontend": "React, Next.js 14, TypeScript, GraphQL, tRPC",
            "Concepts": "System Design, Distributed Systems, Concurrency, Multi-Tenant Architecture, Secure Coding, SOLID, Resiliency Patterns",
        }
        jd_text_lower = ", ".join(jd_keywords).lower()
        if "java" in jd_text_lower:
            categories["Languages"] = "Go, Python, TypeScript, JavaScript, Java, SQL, Bash"
        if "spark" in jd_text_lower:
            categories["Databases"] = "PostgreSQL, MySQL, Redis, DynamoDB, OpenSearch, MongoDB, Spark, S3 Tables"
        if "observability" in jd_text_lower or "monitoring" in jd_text_lower:
            categories["Infrastructure"] = "AWS, ECS, EC2, RDS, SQS, SNS, DynamoDB, Lambda, Secrets Manager, CloudWatch, Terraform, Docker, Kubernetes, GitHub Actions, CloudWatch Metrics, Structured Logging"
        skills_lines = [f"\\techSkill{{{cat}}}{{{categories[cat]}}}" for cat in keep]
        skills_section = "\n".join(skills_lines)

    bullets = build_experience_bullets(focus, jd_keywords, tight=tight)
    projects = build_projects_section(focus, jd_keywords)

    # Close truthful coverage gaps: inject up to MAX_INJECTED_KEYWORDS
    # missing-but-truthful JD keywords into the top bullet, and put any
    # overflow on an "Additional Relevant Skills" line -- skipped in tight
    # mode, since one page takes priority over closing the last few points
    # of coverage (see inject_keyword_emphasis's docstring).
    bullets, extra_skills_line = inject_keyword_emphasis(bullets, skills_section, jd_keywords)
    if extra_skills_line and not tight:
        skills_section = skills_section + "\n" + extra_skills_line

    # Replace skills section. Lookahead anchors on \section{Experience} only
    # (not the preceding \vspace, whose glue value is a master.tex layout
    # concern and shouldn't be duplicated/hardcoded here).
    skills_pattern = r"(\\section\{Technical Skills\}\n)(.*?)(?=\n\\vspace.*?\n\\section\{Experience\})"
    # Use a replacement *function*, not a string: re.sub() interprets
    # backslash escapes (\t, \n, ...) inside a string repl argument, so a
    # literal "\techSkill{...}" in skills_section silently became a tab
    # character followed by plain-text "echSkill{...}" -- a real, reproduced
    # corruption bug (every Skills line lost its \techSkill command). A
    # replacement function receives the string as-is, with no reprocessing.
    master = re.sub(skills_pattern, lambda m: m.group(1) + skills_section + "\n", master, flags=re.S)

    # Replace the entire experience item list content. Must search for
    # \resumeItemListStart starting *after* \section{Experience}: the bare
    # substring "\resumeItemListStart" also appears earlier, inside the
    # \newcommand{\resumeItemListStart}{...} macro *definition* -- searching
    # from the top of the document matched that definition instead of its
    # usage, and spliced the bullets into the middle of the macro itself,
    # corrupting it (produced a "Paragraph ended before \new@command was
    # complete" compile error).
    bullets_tex = "\n\n".join(f"\\resumeItem{{{b}}}" for b in bullets)
    exp_section_start = master.find("\\section{Experience}")
    exp_start = master.find("\\resumeItemListStart", exp_section_start)
    exp_end = master.find("\\resumeItemListEnd", exp_start) + len("\\resumeItemListEnd")
    new_exp = "\\resumeItemListStart\n\n" + bullets_tex + "\n\n\\resumeItemListEnd"
    master = master[:exp_start] + new_exp + master[exp_end:]

    # Replace projects section
    proj_start = master.find("\\section{Projects \\& Writing}")
    proj_list_start = master.find("\\resumeItemListStart", proj_start)
    proj_list_end = master.find("\\resumeItemListEnd", proj_list_start) + len("\\resumeItemListEnd")
    new_proj = "\\section{Projects \\& Writing}\n\\resumeItemListStart\n\n" + projects + "\n\n\\resumeItemListEnd"
    master = master[:proj_start] + new_proj + master[proj_list_end:]

    return master, focus, jd_keywords

# -----------------------------------------------------------------------------
# Compilation and verification
# -----------------------------------------------------------------------------
def compile_tex(tex_path):
    """Compile LaTeX with tectonic. Return (success, output)."""
    workdir = tex_path.parent
    cmd = ["tectonic", str(tex_path.name)]
    try:
        result = subprocess.run(
            cmd, cwd=workdir, capture_output=True, text=True, timeout=120
        )
        return result.returncode == 0, result.stdout + result.stderr
    except Exception as e:
        return False, str(e)

def get_page_count(pdf_path):
    """Return number of pages in PDF."""
    try:
        result = subprocess.run(
            ["pdfinfo", str(pdf_path)], capture_output=True, text=True, timeout=30
        )
        for line in result.stdout.splitlines():
            if line.startswith("Pages:"):
                return int(line.split(":")[1].strip())
    except Exception as e:
        log(f"WARN: pdfinfo failed: {e}")
    return None

# -----------------------------------------------------------------------------
# Telegram
# -----------------------------------------------------------------------------
def send_telegram(pdf_path, company, title, url, score, job_id, reply_to_message_id=None,
                   judgment=None, hedged_keywords=None, jd_unavailable=False):
    """Send Telegram notification with PDF document."""
    if not TG_TOKEN or not TG_CHAT:
        return False, "Telegram credentials not configured"

    lines = [f"{company} \u2014 {title}"]

    if judgment:
        verdict_label = "SCREEN \u2705" if judgment["verdict"] == "screen" else "REJECT-RISK \u26a0\ufe0f"
        source_note = "" if judgment["source"] == "llm" else " (rule-based, LLM unavailable)"
        lines.append(f"Verdict: {verdict_label}{source_note} \u2014 {judgment['reason']}")

    lines.append(f"Coverage: {score}/1.0")

    if hedged_keywords:
        lines.append(f"Hedged (adjacent/familiar): {', '.join(canonical_case(k) for k in hedged_keywords)}")

    if judgment and judgment.get("missing_keywords"):
        lines.append(f"Missing: {', '.join(judgment['missing_keywords'][:5])}")

    if jd_unavailable:
        lines.append("\u26a0\ufe0f JD text unavailable \u2014 coverage/verdict unreliable, resume generated from title only")

    lines.append(f"Apply: {url}")
    lines.append(f"#J{job_id}")
    caption = "\n".join(lines)

    cmd = [
        "curl", "-s", "-X", "POST",
        f"https://api.telegram.org/bot{TG_TOKEN}/sendDocument",
        "-F", f"chat_id={TG_CHAT}",
        "-F", f"document=@{pdf_path}",
        "-F", f"caption={caption}",
    ]
    if reply_to_message_id:
        cmd += ["-F", f"reply_to_message_id={reply_to_message_id}"]
    try:
        result = subprocess.run(cmd, capture_output=True, text=True, timeout=60)
        resp = json.loads(result.stdout)
        if resp.get("ok"):
            return True, None
        else:
            return False, f"Telegram API error: {resp.get('description', result.stdout)}"
    except Exception as e:
        return False, str(e)

# -----------------------------------------------------------------------------
# Main processing
# -----------------------------------------------------------------------------
def is_engineering_role(title, jd_text):
    """Skip clearly non-engineering roles that violate truth lock."""
    text = (title + " " + jd_text).lower()
    non_eng = [
        "account executive", "sales executive", "business development",
        "cloud billing associate", "billing operations", "billing analyst",
        "lead, cloud billing operations", "senior lead, cloud billing",
        "recruiter", "hr ", "human resources", "marketing", "finance manager",
        "accountant", "bookkeeper", "legal", "counsel", "paralegal",
        "office manager", "administrative", "executive assistant"
    ]
    for term in non_eng:
        if term in text:
            return False
    # Must contain an engineering keyword
    eng_terms = [
        "engineer", "engineering", "developer", "sde", "swe", "software",
        "data engineer", "ai engineer", "ml engineer", "backend", "frontend",
        "fullstack", "full stack", "devops", "sre", "site reliability",
        "infrastructure", "platform", "solutions engineer", "solutions architect",
        "forward deployed", "technical solutions", "systems engineer"
    ]
    return any(term in text for term in eng_terms)

def load_tailored():
    if TAILORED_JSON_PATH.exists():
        with open(TAILORED_JSON_PATH, "r", encoding="utf-8") as f:
            return json.load(f)
    return {}

def update_job_status(job_id, status):
    """Flip a job's status in jobwatch.db once tailoring has resolved it, so
    the dashboard stops showing it as 'new' forever (tailor_resume.py used to
    only ever read the jobs table -- all progress lived in tailored.json,
    completely decoupled from jobs.status). Guarded to WHERE status='new' so
    this never clobbers a status the user already set via the dashboard or a
    Telegram reply (applied/rejected/interview/offer/etc), including when
    rebuild_one() re-runs for a job the user already actioned.
    """
    conn = sqlite3.connect(DB_PATH)
    try:
        conn.execute("UPDATE jobs SET status = ? WHERE id = ? AND status = 'new'", (status, int(job_id)))
        conn.commit()
    finally:
        conn.close()

def save_tailored(data):
    TAILORED_JSON_PATH.parent.mkdir(parents=True, exist_ok=True)
    with open(TAILORED_JSON_PATH, "w", encoding="utf-8") as f:
        json.dump(data, f, indent=2, ensure_ascii=False)

def rebuild_one(job_id, reply_to_message_id=None):
    """Regenerate one job's tailored resume from scratch against the current
    master.tex, recompile, verify, and resend. This is the 'fix' Telegram
    reply command's implementation: it reuses the exact same relevance-scored
    generation path as the normal batch run (build_experience_bullets /
    build_projects_section already rank content by JD-focus match), rather
    than a separate naive-truncation post-processor -- so a "fix" always
    reflects the latest master.tex layout fixes, not a stale patched-in-place
    .tex file.
    """
    tailored = load_tailored()
    entry = tailored.get(job_id)
    if not entry:
        log(f"ERROR: job {job_id} not found in tracker")
        return False, f"Job {job_id} not found in tracker"

    company = entry.get("company", "Unknown")
    title = entry.get("title", "Unknown")
    url = entry.get("url", "")

    conn = sqlite3.connect(f"file:{DB_PATH}?mode=ro", uri=True)
    conn.row_factory = sqlite3.Row
    row = conn.execute("SELECT * FROM jobs WHERE id=?", (job_id,)).fetchone()
    conn.close()
    job = dict(row) if row else {"id": job_id, "company_name": company, "title": title, "url": url}

    gh_slug = company_slug(company, url)
    gh_id = extract_gh_job_id(url)
    jd_data = fetch_greenhouse_job(gh_slug, gh_id) if (gh_id and gh_slug) else None
    if not jd_data:
        jd_data = {
            "title": title, "company_name": company, "content_text": title,
            "content_html": "", "location": "", "absolute_url": url,
        }

    comp_slug = company_slug(company, url)
    title_slug = slugify(title)
    out_dir = OUTPUT_ROOT / comp_slug
    out_dir.mkdir(parents=True, exist_ok=True)
    base_name = f"mayur_athavale_resume_{comp_slug}_{title_slug}_{DATE_STR}"
    tex_path = out_dir / f"{base_name}.tex"
    pdf_path = out_dir / f"{base_name}.pdf"

    attempt = 0
    success = False
    final_error = None
    resume_text = ""
    jd_keywords = []

    while attempt < 2 and not success:
        attempt += 1
        tight = (attempt == 2)
        log(f"  Rebuild attempt {attempt} (tight={tight}) for job {job_id}...")

        tex_content, focus, jd_keywords = generate_resume(job, jd_data, tight=tight)
        with open(tex_path, "w", encoding="utf-8") as f:
            f.write(tex_content)

        ok, output = compile_tex(tex_path)
        if not ok:
            log(f"  Compile failed: {output[:500]}")
            final_error = output
            continue

        pages = get_page_count(pdf_path)
        log(f"  Pages: {pages}")
        if pages != 1:
            final_error = f"Page overflow: {pages} pages"
            continue

        resume_text = tex_content
        success = True

    if not success:
        log(f"  REBUILD FAILED for job {job_id}: {(final_error or '')[:200]}")
        tailored[job_id] = {**entry, "status": "failed", "error": final_error,
                             "tailored_date": datetime.now().isoformat()}
        save_tailored(tailored)
        return False, final_error

    score, covered, not_covered, not_truthful = score_coverage(jd_keywords, resume_text)
    log(f"  Coverage score: {score} ({len(covered)}/{len(jd_keywords)} keywords)")

    tailored[job_id] = {
        **entry,
        "pdf_path": str(pdf_path),
        "tex_path": str(tex_path),
        "coverage_score": score,
        "status": "done",
        "error": None,
        "tailored_date": datetime.now().isoformat(),
        "keywords": jd_keywords,
        "covered_keywords": covered,
        "not_covered_keywords": not_covered,
        "not_truthful_keywords": not_truthful,
    }
    save_tailored(tailored)

    tg_ok, tg_error = send_telegram(pdf_path, company, title, url, score, job_id,
                                     reply_to_message_id=reply_to_message_id)
    tailored[job_id]["telegram_error"] = None if tg_ok else tg_error
    save_tailored(tailored)
    if tg_ok:
        update_job_status(job_id, "shortlisted")
    return tg_ok, (None if tg_ok else tg_error)

def process_job(job, tailored):
    """Process one job dict from the jobs table: fetch JD (Greenhouse API,
    falling back to fetch_jd_generic for anything else), generate and
    compile the resume, score it, judge it, and send Telegram. Mutates and
    returns `tailored` (the job-id-keyed tracker dict) plus one of
    "sent"/"failed"/"skipped_non_eng". Shared by main()'s batch loop and
    the --job-id one-off path (see the bottom of this file) -- unlike
    rebuild_one(), this works for a job with NO pre-existing tracker
    entry, which a fresh manual submission always starts as.
    """
    jid = str(job["id"])
    company = job.get("company_name") or "Unknown"
    title = job.get("title") or "Unknown"
    url = job.get("url") or ""

    log(f"\n--- Processing ID {jid}: {company} — {title} ---")

    gh_slug = company_slug(company, url)
    gh_id = extract_gh_job_id(url)
    jd_data = None
    if gh_id and gh_slug:
        jd_data = fetch_greenhouse_job(gh_slug, gh_id)

    jd_unavailable = False
    if not jd_data:
        jd_data = fetch_jd_generic(url)
    if not jd_data or len(jd_data.get("content_text", "")) < MIN_USABLE_JD_CHARS:
        log(f"WARN: Could not fetch usable JD for {jid}; using title only")
        jd_unavailable = True
        jd_data = {
            "title": title, "company_name": company, "content_text": title,
            "content_html": "", "location": "", "absolute_url": url,
        }

    jd_text = jd_data.get("content_text", "")

    if not is_engineering_role(title, jd_text):
        log(f"SKIPPED (non-engineering): {title}")
        tailored[jid] = {
            "company": company, "title": title, "url": url,
            "pdf_path": None, "tex_path": None, "coverage_score": None,
            "status": "ignored",
            "error": "Non-engineering role; facts.md does not support claims. Skipped per truth lock.",
            "tailored_date": datetime.now().isoformat(),
        }
        save_tailored(tailored)
        update_job_status(jid, "ignored")
        return tailored, "skipped_non_eng"

    comp_slug = company_slug(company, url)
    title_slug = slugify(title)
    out_dir = OUTPUT_ROOT / comp_slug
    out_dir.mkdir(parents=True, exist_ok=True)
    base_name = f"mayur_athavale_resume_{comp_slug}_{title_slug}_{DATE_STR}"
    tex_path = out_dir / f"{base_name}.tex"
    pdf_path = out_dir / f"{base_name}.pdf"

    attempt = 0
    success = False
    final_error = None
    resume_text = ""
    jd_keywords = []

    while attempt < 2 and not success:
        attempt += 1
        tight = (attempt == 2)
        log(f"  Attempt {attempt} (tight={tight})...")

        tex_content, focus, jd_keywords = generate_resume(job, jd_data, tight=tight)
        with open(tex_path, "w", encoding="utf-8") as f:
            f.write(tex_content)

        ok, output = compile_tex(tex_path)
        if not ok:
            log(f"  Compile failed: {output[:500]}")
            final_error = output
            continue

        pages = get_page_count(pdf_path)
        log(f"  Pages: {pages}")
        if pages != 1:
            log(f"  PDF has {pages} pages; retrying with tighter content")
            final_error = f"Page overflow: {pages} pages"
            continue

        resume_text = tex_content
        success = True

    if not success:
        log(f"  FAILED after 2 attempts: {(final_error or '')[:200]}")
        tailored[jid] = {
            "company": company, "title": title, "url": url,
            "pdf_path": None, "tex_path": str(tex_path) if tex_path.exists() else None,
            "coverage_score": None, "status": "failed", "error": final_error,
            "tailored_date": datetime.now().isoformat(),
        }
        save_tailored(tailored)
        return tailored, "failed"

    tiered = score_coverage_tiered(jd_keywords, resume_text)
    score, covered, not_covered, not_truthful = score_coverage(jd_keywords, resume_text)
    log(f"  Coverage score: {score} ({len(covered)}/{len(jd_keywords)} keywords)")

    judgment = llm_judge(jd_text, resume_text, title, company)

    tailored[jid] = {
        "company": company, "title": title, "url": url,
        "pdf_path": str(pdf_path), "tex_path": str(tex_path),
        "coverage_score": score, "status": "done", "error": None,
        "tailored_date": datetime.now().isoformat(),
        "keywords": jd_keywords, "covered_keywords": covered,
        "not_covered_keywords": not_covered, "not_truthful_keywords": not_truthful,
        "hedged_keywords": tiered["hedged"],
        "verdict": judgment["verdict"], "verdict_source": judgment["source"],
        "verdict_reason": judgment["reason"], "missing_keywords": judgment["missing_keywords"],
        "jd_unavailable": jd_unavailable,
    }
    save_tailored(tailored)

    log(f"  Sending Telegram notification...")
    tg_ok, tg_error = send_telegram(pdf_path, company, title, url, score, jid, judgment=judgment,
                                     hedged_keywords=tiered["hedged"], jd_unavailable=jd_unavailable)
    if not tg_ok:
        log(f"  Telegram failed: {tg_error}")
        tailored[jid]["telegram_error"] = tg_error
        save_tailored(tailored)
        return tailored, "failed"

    tailored[jid]["telegram_error"] = None
    save_tailored(tailored)
    update_job_status(jid, "shortlisted")
    return tailored, "sent"


def main():
    log("=== Jobwatch Resume Tailoring Agent ===")

    # Validate tools
    if not shutil.which("tectonic"):
        log("ERROR: tectonic not found")
        sys.exit(1)
    if not shutil.which("pdfinfo"):
        log("ERROR: pdfinfo not found")
        sys.exit(1)

    # Daily budget check
    tailored = load_tailored()
    today_count = sum(
        1 for v in tailored.values()
        if v.get("tailored_date", "").startswith(TODAY_ISO) and v.get("status") == "done"
    )
    log(f"Already tailored today (done): {today_count}/{DAILY_BUDGET}")
    if today_count >= DAILY_BUDGET:
        log("Daily budget exhausted. Exiting.")
        sys.exit(0)

    # Query unprocessed jobs
    conn = sqlite3.connect(f"file:{DB_PATH}?mode=ro", uri=True)
    conn.row_factory = sqlite3.Row
    c = conn.cursor()
    c.execute("SELECT * FROM jobs WHERE status='new' ORDER BY id")
    rows = c.fetchall()
    conn.close()

    jobs = []
    for row in rows:
        job = dict(row)
        if str(job["id"]) not in tailored:
            jobs.append(job)

    log(f"Unprocessed new jobs: {len(jobs)}")
    if not jobs:
        log("No unprocessed jobs. Exiting.")
        sys.exit(0)

    # Determine how many to process this cycle
    remaining_budget = DAILY_BUDGET - today_count
    to_process = min(MAX_PER_CYCLE, remaining_budget, len(jobs))
    log(f"Will process up to {to_process} jobs this cycle")

    processed = 0
    failed = 0
    skipped_non_eng = 0

    for job in jobs[:to_process + 5]:  # allow buffer for non-eng skips
        if processed >= to_process:
            break

        tailored, outcome = process_job(job, tailored)

        if outcome == "sent":
            processed += 1
        elif outcome == "failed":
            failed += 1
        elif outcome == "skipped_non_eng":
            skipped_non_eng += 1

        if outcome == "sent" and processed < to_process:
            log(f"  Rate limit: sleeping {RATE_LIMIT_SECONDS}s...")
            time.sleep(RATE_LIMIT_SECONDS)

    log(f"\n=== Cycle complete ===")
    log(f"Processed: {processed}, Failed: {failed}, Skipped non-eng: {skipped_non_eng}")

if __name__ == "__main__":
    if len(sys.argv) > 1 and sys.argv[1] == "--rebuild":
        if len(sys.argv) < 3:
            print("Usage: tailor_resume.py --rebuild <job_id> [reply_to_message_id]", file=sys.stderr)
            sys.exit(1)
        _job_id = sys.argv[2]
        _reply_to = sys.argv[3] if len(sys.argv) > 3 else None
        _ok, _err = rebuild_one(_job_id, reply_to_message_id=_reply_to)
        if not _ok:
            print(f"REBUILD_FAILED: {_err}", file=sys.stderr)
            sys.exit(1)
        print("REBUILD_OK")
    elif len(sys.argv) > 1 and sys.argv[1] == "--job-id":
        if len(sys.argv) < 3:
            print("Usage: tailor_resume.py --job-id <job_id>", file=sys.stderr)
            sys.exit(1)
        _job_id = sys.argv[2]
        if not shutil.which("tectonic") or not shutil.which("pdfinfo"):
            print("ERROR: tectonic/pdfinfo not found", file=sys.stderr)
            sys.exit(1)
        _conn = sqlite3.connect(f"file:{DB_PATH}?mode=ro", uri=True)
        _conn.row_factory = sqlite3.Row
        _row = _conn.execute("SELECT * FROM jobs WHERE id=?", (int(_job_id),)).fetchone()
        _conn.close()
        if not _row:
            print(f"JOB_ID_NOT_FOUND: {_job_id}", file=sys.stderr)
            sys.exit(1)
        _tailored = load_tailored()
        _tailored, _outcome = process_job(dict(_row), _tailored)
        if _outcome == "failed":
            print(f"PROCESS_JOB_FAILED: {_tailored.get(_job_id, {}).get('error', 'unknown error')}", file=sys.stderr)
            sys.exit(1)
        print(f"PROCESS_JOB_{_outcome.upper()}")
    else:
        main()
