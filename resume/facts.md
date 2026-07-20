# Mayur Athavale — Verified Work Facts
> Source of truth for all resume tailoring. Anything not in this file must NOT appear on a resume.
> `[FILL: ...]` = known gap, only Mayur can supply. Estimates are fine if marked "~" or "estimated".

## Identity
- SDE-I (full-time) at Zotok.ai, Jan 2025 – Present. Location: Hyderabad, Telangana, India.
- B.Tech, College of Engineering, Pune (COEP), 2020–2024.
- Contacts/links: as in master.tex (Hyderabad, LinkedIn, GitHub, Medium, mayurathavale.com, ssh portfolio).

## Ownership summary
- Owns 5 production services end-to-end at Zotok.ai, each from POC to scaled production: AI Copilot, Conversation Threader (ingestion), Temporal Agents, API Gateway, LiteLLM Proxy.
- [FILL: which system at Zotok does ONLY Mayur understand / get paged for? One sentence — this is the ownership story.]

---

# Services (per-service blocks)

## 1. AI Copilot (agentic chatbot product)
**Stack:** Python 3, LangGraph, FastAPI, Poetry, OpenSearch, Redis, PostgreSQL (RDS), AWS (ECS via Terraform), Azure Foundry model deployments.
**What it does:** Agentic AI copilot for B2B users; conversational surface over business data (threads, messages, sheets, tally).
**My role:** Built entire product and its 3 dependent services from POC to production deployment.
**Details:**
- Multi-agent orchestration with LangGraph; custom agent flows per client.
- RAG with hybrid retrieval: OpenSearch KNN (vector) + BM25.
- 4 streaming strategies: SSE, AG-UI, LangGraph multi-mode; SSE event streams drive dynamic agent-generated UI components on the frontend.
- Multiple-model switching feature (user/plan selectable models).
- Full onboarding flow: auth → workspace creation → WAHA (WhatsApp) connection setup.
- Google Sheets and Tally connectors including auth flows.
**Metrics:**
- Drove 200%+ increase in daily sales leads (existing verified claim).
- 5k + live DAUs on copilot product.
- 100k+ live conversations handled on copilot product every day.
**Stories:**
- [FILL: one production incident on copilot — what broke, how you debugged, what changed after?]
- [FILL: one architecture decision you made and the alternative you rejected, e.g. why LangGraph, why hybrid retrieval?]

## 2. Conversation Threader (ingestion pipeline)
**Stack:** Spark jobs, S3 Tables, cron-windowed batch, vector embeddings, OpenSearch indexes. [FILL: language/runtime for spark jobs — PySpark?]
**What it does:** Extracts user messages from S3 Tables on scheduled windows, groups messages → threads → query_types, embeds key fields, stores embeddings + full message/thread data in OpenSearch. Backbone for copilot's search_threads and search_messages tools.
**My role:** Built the pipeline end-to-end.
**Metrics:**
- 10k+ messages per 20min time window
- 10M+ index size / doc count
- 1-2s pipeline latency per window.
**Stories:**
- [FILL: hardest data-quality or scale problem here — dedup? grouping accuracy? spark tuning?]

## 3. Temporal Agents (scheduled automation)
**Stack:** Temporal, Python 3, Poetry (service later migrated to TypeScript — I did the migration), WAHA server (WhatsApp), OpenSearch, Google Sheets API.
**What it does:** Scheduled agents pick last-X-minutes of messages from clients' WhatsApp groups (via WAHA), search OpenSearch, extract data per a guidance prompt into connected Google Sheets.
**My role:** Built agents and schedules; migrated the temporal service from Python to TypeScript.
**Metrics:**
- 20+ WhatsApp groups / clients monitored
- 72 runs per day, processing 1000+ messages per tennant group.
**Stories:**
- [FILL: why migrate temporal service to TypeScript — what drove the decision?]

## 4. API Gateway
**Stack:** Go, Gin (gin-gonic), KrakenD. (NOTE: "Gin" — typo "Jin" corrected.)
**What it does:** Routes traffic across 12 microservices; auth middleware, rate limiting, request routing; full KrakenD configuration.
**My role:** Wrote the entire service; concurrency patterns and performance tuning for high-throughput SLAs.
**Metrics:**
- 1k RPS, 10K under load
- ~250-300ms of P99 latency with 100ms of P95 latency
**Stories:**
- [FILL: one performance-tuning fix — what was slow, what you changed, before/after?]

## 5. LiteLLM Proxy (model-call governance)
**Stack:** LiteLLM (self-hosted), own infra setup.
**What it does:** Common proxy layer for ALL model calls across the platform: token tracking + plan-based budget enforcement.
**My role:** Built and self-hosted; own the infra.
**Metrics:**
- 100M tokens per day flow thought litellm
- 5 services connect to the Azure models throguh litellm with custom budget exemts and enforcements based on their source with more than 5k DAU.
**Stories:**
- [FILL: why self-hosted LiteLLM vs managed alternative — the tradeoff you weighed?]

---

# Cross-cutting work

## Infrastructure & DevOps
- Full AWS infra via Terraform for 5 services: ECS (Fargate), ECR, EC2, S3, S3 Tables, Redshift, Athena, RDS Postgres, Aurora MySQL, EventBridge, ElastiCache (Redis, Valkey), SQS, SNS, DynamoDB, Secrets Manager, Lambda, API Gateway, Route 53, VPC, CloudFront, IAM, Amplify, CloudWatch, CloudTrail.
- Azure Foundry model deployments.
- Custom deployment scripts; GitHub Actions CI/CD for all services. Release cycles cut by 35% via automated integration/functional test quality gates (existing verified claim).
- Docker + docker-compose local dev/testing setup.
- Zero-downtime database migrations on production PostgreSQL (RDS).
- CloudWatch metrics + structured logging for production observability.

## Event-driven systems
- Pipelines on SQS, SNS, DynamoDB Streams, webhooks (WhatsApp automation): idempotency, retries, dead-letter processing, partial-failure recovery, reconciliation for missed/delayed events.

## Multi-tenant API layer
- Hasura + NestJS proxy: dynamic schema generation per tenant, query guards, business-logic middleware, role-based access enforcement, strict input validation.

## Frontend
- Offline-first PWA (React 18, Vite): custom caching + cache-invalidation strategies.
- Custom priority-queue library for sequential task execution (API 1 → 2 → 3) with offline cart sync.
- Live chat message rendering with Centrifuge.
- Dynamic multi-screen web-app tour (deeply customized react-joyride).
- Custom edge-rendering architecture from scratch (no external deps): live edge connection + position updates during node drag, edge identification for node connection. [FILL: what product feature is this — flow builder? canvas?]
- Migrated Webpack module-federation microfrontends → Vite with custom build runtime in NX: build times/latencies down 70%, dev build + local startup down 80% (with HMR).
- Mobile apps: React Native, Expo, Java (Android + iOS). [FILL: shipped to stores? which app/feature?]

## Testing & quality
- Unit + integration tests for all 5 services.
- Built custom test service with MCP support for the dev team.
- Custom code & PR review agents; commit validation with Husky + bash.

## AI tooling & docs
- LLM-WIKI: custom repository- and workspace-specific skills enabling good code generation and documentation even on cheaper models.

## Developer tooling
- PR management CLI (team utility; saves ~7 GUI clicks per PR on GitHub). [FILL: team size using it?]

---

# Personal projects (outside work)
- **PR Manager** (Go, GitHub APIs, GitHub Actions): CLI automating PR workflows — reviewer assignment, merge queuing, status checks, cross-platform binary releases via Actions.
- **Terminal Portfolio** (Go, BubbleTea, Wish, Lipgloss, AWS EC2): SSH-accessible TUI (ssh portfolio.mayurathavale.com), multi-tab navigation, SQLite visitor analytics, live presence tracking; GitHub Actions hot-deploy via SCP + systemd.
- **Technical writing**: Medium articles on system security, cloud infra, networking incl. self-hosted VPN tunneling on AWS.

---

# Skills inventory (honesty tiers)
> production = built/operated in prod | used = hands-on but limited | familiar = concepts only, DO NOT claim hands-on

**Languages**
- production: Go, Python, TypeScript, JavaScript, SQL, Bash
- used: Java (React Native mobile work)

**Backend/frameworks**
- production: FastAPI, LangGraph, NestJS, Node.js, Gin, KrakenD, Temporal, GraphQL (Hasura), REST, SSE/streaming, webhooks
- familiar: tRPC [FILL: confirm — production or familiar?]

**Data/queues/search**
- production: PostgreSQL, Aurora MySQL, Redis, Valkey, DynamoDB, OpenSearch (KNN+BM25), SQS, SNS, DynamoDB Streams, EventBridge, S3 Tables, Spark (threader jobs)
- used: Redshift, Athena, RabbitMQ [FILL: RabbitMQ — where was it used? confirm tier], MongoDB, Firebase [FILL: confirm tier for these two]
- familiar: Kafka (known, not used much at Zotok)

**Infra/DevOps**
- production: AWS (full list above), Terraform, Docker, GitHub Actions, CloudWatch observability, Azure Foundry
- used: GCP [FILL: what specifically on GCP?], Kubernetes [FILL: honest tier — resume says production workloads, confirm what you actually ran]

**Frontend**
- production: React 18, Vite, NX, Module Federation, PWA/offline-first, react-joyride, Centrifuge, React Native + Expo
- used: Next.js 14 [FILL: confirm tier — production or side projects?]

**AI/LLM**
- production: LangGraph, RAG (hybrid retrieval), multi-agent orchestration, LiteLLM, prompt-driven extraction agents, MCP
- used: LangChain [FILL: confirm — LangGraph yes, but LangChain itself?]

---

# Adjacent Skills ( soft hand on these skills )
- Databases : cassandra
- GCP : BigQuery
- Automation : Ansible, Jenkins
- AI/ML : PyTorch, TensorFlow, Scikit-learn
- Languages: Java, Kotlin
- Frameworks: Spring, Spring Boot

# Interview stories bank (fill over time, one paragraph each)
- [FILL: production incident story — detection, debugging, fix, prevention]
- [FILL: disagreement/pushback story — technical decision you argued for]
- [FILL: failure/rollback story — something that didn't work and what you learned]
- [FILL: collaboration story — who you unblocked or mentored]
- [FILL: scale story — a limit you hit and engineered around]
