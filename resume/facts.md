# Mayur Athavale — Verified Work Facts
> Source of truth for all resume tailoring. Anything not in this file must NOT appear on a resume.
> `[FILL: ...]` = known gap, only Mayur can supply. Estimates must come from a supplied value or identified measurements and remain marked "~" or "estimated"; do not guess to fill a gap.
> Updated 2026-10-10 from Mayur's latest supplied resume, current project source, and recorded work from September/October. Resume-reported metrics are candidate claims, not independently re-measured here.
> Implemented/tested, published, deployed, and verified live are different evidence states. Preserve the stated state when selecting a claim. Never infer a result or a measurement from a feature's existence.

## Evidence and tailoring rules
- Latest candidate-supplied resume: `C:/Users/zonot/Downloads/mayur_athavale_resume.pdf` (read 2026-10-10). This is the source for the current headline metrics and agent-bridge/hermes-openwa descriptions.
- Current project evidence: `jobwatch/HANDOFF.md`, `internal/poller/poller.go`, `internal/store/store.go`, `scripts/tailor_resume.py`; companion `agent-bridge/package.json` and README; `hermes-openwa/README.md`. Local source confirms implementation; it does not prove the current deployed version or npm release.
- Recent work references below are recorded September/October implementation and validation evidence. Deployment statements apply to those recorded checks, not a fresh production audit on 2026-10-10.
- Values already supplied in this file but absent from the latest resume are retained with their original scope. Where measurement method or current applicability is unknown, retain a `[FILL]` question rather than upgrade the claim.
- Compensation, notice dates, location eligibility and desired roles belong in `resume/preferences.md`, not achievement facts. An existing offer is not current salary. Reconfirm time-sensitive preferences before applying.
- Current automation limitation: `FACTS_MD_PATH` is declared in `scripts/tailor_resume.py` but is not read by the current pipeline. Skills, experience bullets and projects are hardcoded there; the judge receives the generated resume and `preferences.md`. This file is the editorial authority, not an enforced runtime lookup yet.

## Identity
- SDE-I (full-time) at Zotok.ai, Jan 2025 – Present. Location: Hyderabad, Telangana, India.
- B.Tech, College of Engineering, Pune (COEP), 2020–2024.
- Contacts/links: as in master.tex (Hyderabad, LinkedIn, GitHub, Medium, mayurathavale.com, ssh portfolio).

## Ownership summary
- Owns 5 production services end-to-end at Zotok.ai, each from POC to scaled production: AI Copilot, Conversation Threader (ingestion), Temporal Agents, API Gateway, LiteLLM Proxy.
- Ownership evidence: end-to-end Copilot delivery, scheduled extraction service migration, API gateway implementation, and self-hosted LiteLLM operation. Also contributed across Copilot UI/backend, Rewards Vault billing and admin/onboarding systems (see Recent work).
- [FILL: actual on-call responsibilities and escalation process. Do not claim sole ownership or exclusive knowledge without confirmation.]

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
- Workflows developed with sales, business and customer-facing teams drove a 200%+ increase in daily sales leads (latest resume claim; preserve collaborative attribution).
- 5k+ daily active users on the Copilot product (latest resume claim).
- 100k+ conversations/day on the Copilot product (latest resume claim; do not relabel this as HTTP requests, simultaneous users, or model calls).
**Stories:**
- Recorded production incident: Copilot admin Accounts returned HTTP 500 because the startup secret loader overwrote the configured database/schema with an absent secret field. Traced `ER_NO_DB_ERROR: No database selected` to configuration rather than the new owner lookup; patched the loader to retain configured defaults and added a regression test. PR #21303; implementation/tests evidenced, post-deploy verification not recorded in that session. No downtime or MTTR estimate available.
- Recorded architecture decision: keep exact financial/customer clarification memory deterministic and workspace-scoped; a remembered customer must still belong to the current authorized shortlist. Broader semantic memory was a separate feature-flagged implementation, not permission to silently choose a customer (see Recent work).

## 2. Conversation Threader (ingestion pipeline)
**Stack:** Spark jobs, S3 Tables, cron-windowed batch, vector embeddings, OpenSearch indexes. [FILL: language/runtime for spark jobs — PySpark?]
**What it does:** Extracts user messages from S3 Tables on scheduled windows, groups messages → threads → query_types, embeds key fields, stores embeddings + full message/thread data in OpenSearch. Backbone for copilot's search_threads and search_messages tools.
**My role:** Built the pipeline end-to-end.
**Metrics:**
- 10k+ messages per 20min time window
- 10M+ index size / doc count
- 1-2s pipeline latency per window (historical candidate-supplied value; not independently timed here). [FILL: what segment this measures, and how 10k+ messages/window, 10M+ indexed docs and this latency were measured. Do not imply a full Spark batch cold-start completes in 1-2s without evidence.]
**Stories:**
- [FILL: hardest data-quality or scale problem here — dedup? grouping accuracy? spark tuning?]

## 3. Temporal Agents (scheduled automation)
**Stack:** Temporal, Python 3, Poetry (service later migrated to TypeScript — I did the migration), WAHA server (WhatsApp), OpenSearch, Google Sheets API.
**What it does:** Scheduled agents pick last-X-minutes of messages from clients' WhatsApp groups (via WAHA), search OpenSearch, extract data per a guidance prompt into connected Google Sheets.
**My role:** Built agents and schedules; migrated the temporal service from Python to TypeScript.
**Metrics:**
- 20+ WhatsApp groups / clients monitored
- 72 runs/day; 1k+ messages per tenant (latest resume wording). [FILL: per-run versus per-day message count; do not multiply into an unsupported daily total.]
**Stories:**
- [FILL: why migrate temporal service to TypeScript — what drove the decision?]

## 4. API Gateway
**Stack:** Go, Gin (gin-gonic), KrakenD. (NOTE: "Gin" — typo "Jin" corrected.)
**What it does:** Routes traffic across 12 microservices; auth middleware, rate limiting, request routing; full KrakenD configuration.
**My role:** Wrote the entire service; concurrency patterns and performance tuning for high-throughput SLAs.
**Metrics:**
- 1k RPS steady; 10k RPS under load; 100ms P95 latency (latest resume). The 10k RPS figure is a load-test result, not sustained production traffic. [FILL: test hardware, concurrency, duration, request mix and which load the P95 belongs to.]
- ~250-300ms P99 latency (historical candidate-supplied value, absent from latest resume; measurement conditions unconfirmed).
**Stories:**
- [FILL: one performance-tuning fix — what was slow, what you changed, before/after?]

## 5. LiteLLM Proxy (model-call governance)
**Stack:** LiteLLM (self-hosted), own infra setup.
**What it does:** Common proxy layer for ALL model calls across the platform: token tracking + plan-based budget enforcement.
**My role:** Built and self-hosted; own the infra.
**Metrics:**
- 100M tokens/day across 5 services (latest resume; token throughput is distinct from request count and concurrent throughput).
- Per-source token tracking and plan-based budget enforcement; platform model access through Azure Foundry. The 5k+ DAU figure describes Copilot adoption, not a separate measured LiteLLM user population.
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

## Recent work (September-October 2026; evidence states preserved)
- **Copilot customer resolution and clarification:** shared the authenticated Finance resolver with AR while carrying seller credentials and workspace scope; unresolved/ambiguous identities require clarification. Added backward-compatible multi-select for general Finance questions while keeping customer identity single-select. References: backend PR #20796, frontend PR #16569; focused tests and TypeScript checks recorded. Do not claim reduced error rates without measurement.
- **Copilot memory:** implemented exact workspace-scoped clarification memory and a separate LangMem/Postgres/pgvector semantic-memory path with correction/reset controls and feature flags. Reference: PR #21055, focused tests recorded. Semantic rollout required a canary and was not confirmed live by the available evidence; do not describe it as broadly deployed.
- **Billing and payments:** implemented custom ZoP top-up metadata/amount validation, association with the active subscription, configurable minimum top-up, and frontend/admin support. Preserved audited, idempotent admin crediting and payment/ledger separation. References: backend PR #21188 and frontend PR #16726. Recorded validation: 59 backend + 7 frontend tests and a production metadata-migration dry run rolled back; these are implementation checks, not proof the complete release shipped.
- **Payment verification:** a recorded production INR 1 test payment produced captured-payment records, successful payment-link webhook logs and exactly one 1-ZoP ledger credit. This is an end-to-end test result, not payment volume/revenue. Webhook-secret precedence was still an unresolved follow-up in that record; do not claim complete billing/security remediation.
- **Admin/onboarding:** persisted completed signup milestones across retries/reconnects and arbitrary query parameters under a separate object; enriched account ownership from the correct User identity; added clickable detail access and normalized Indian-mobile search. References: frontend #16788/#16793, backend #21300/#21312. Tests/PRs evidenced; final authenticated production/UI verification pending in that record.

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
- **agent-bridge** (TypeScript, Node.js; published on npm per latest resume): zero-runtime-dependency WhatsApp bridge for Claude Code, Codex and Command Code. Implemented model/harness switching, named threads, file exchange/private context imports, plan/write modes, chat approvals for supported harnesses, HMAC verification, deduplication and a configuration dashboard. Local package confirms no runtime dependency block; Node/Python harness CLIs and the OpenWA gateway are external prerequisites, not eliminated dependencies. Recorded deployment/release checks exist; current registry/deployment not rechecked for this update. Do not invent adoption/download counts or promise identical approval support for every harness.
- **hermes-openwa** (Python): Nous Hermes Agent platform plugin reusing an existing OpenWA session without re-pairing. Implemented raw-byte webhook signature verification, self-chat defaults, deduplication/echo gates and an ack-first worker queue. Latest resume reports live end-to-end verification and 45 tests across Python 3.11-3.13; current README confirms transport/adapter separation and shared-session design. The 45-test figure is the supplied validation snapshot, not a freshly rerun suite or proof of durable queue storage.
- **PR Manager** (Go, GitHub APIs, GitHub Actions): CLI automating PR workflows — reviewer assignment, merge queuing, status checks, cross-platform binary releases via Actions.
- **Terminal Portfolio** (Go, BubbleTea, Wish, Lipgloss): SSH-accessible TUI (ssh portfolio.mayurathavale.com), multi-tab navigation, SQLite visitor analytics, live presence tracking. Originally AWS EC2 + GitHub Actions SCP/systemd deploy; since Aug 2026 self-hosted on a personal k3s cluster.
- **jobwatch** (Go, Python, React, SQLite, k3s): personal job-search agent, operated since Jul 2026. Latest resume reports 50+ ATS boards/aggregators; source supports Greenhouse, Ashby, Lever, Workday, RemoteOK, WWR, Remotive and web3.career. Includes LLM verdict/preference-fit scoring, one-page LaTeX generation from curated facts-derived templates, Telegram notifications/reply commands, Gmail outreach drafts and a dashboard composer, and LinkedIn-connection matching. SQLite WAL stores jobs; a single Telegram worker atomically persists updates with the offset and processes dashboard actions through an events queue. GitHub Actions CD deploys to a shared k3s PVC. Do not claim automatic job applications, a proven ATS pass probability, fully runtime-enforced truth checking, or a measured increase in interview/offer conversion.
- **Personal k3s cluster** (Contabo VPS, Docker, GitHub Actions, WireGuard): latest resume reports consolidation of 4 services from 3 AWS EC2 instances onto one VPS, reducing monthly hosting costs by approximately 80%. Historical cluster included Jobwatch, Terminal Portfolio and Vaultwarden; later records include OpenWA/agent-bridge in `agent-stack`. The 4-service figure describes the migration, not a current count of pods or deployments. Traefik ingress, cert-manager TLS, local-path PVCs and CronJobs. [FILL: before/after monthly bills and included services; do not turn ~80% into exact currency savings.]
- **Technical writing**: Medium articles on system security, cloud infra, networking incl. self-hosted VPN tunneling on AWS.

## Planned projects / current learning (exclude from shipped-experience claims)
- **CollateralFi:** previously planned Solana/Anchor DeFi lending design; absent from the latest resume. Implementation/completion not evidenced. Do not insert into tailored project sections or use it to claim hands-on Rust, Anchor, Solana or oracle experience.
- **Ethereum learning:** raw JSON-RPC/transaction-receipt exercises and discussion of a light-client-backed settlement observer. This is learning/design work, not a deployed Ethereum service, Solidity production experience, or independent transaction-verification implementation.
- **Laya:** local experiment in `C:/Users/zonot/OneDrive/Desktop/laya-test`; the minimal script loads `convaiinnovations/laya` and asks one refund-classification question. Running an example is not evidence of Jobwatch domain accuracy, trained-model ownership, calibrated predictions, or VPS capacity.

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
- used: SQLite (deployed personal projects), Redshift, Athena, MongoDB/Firebase/Firestore (named in latest resume; specific projects and scope still to confirm), RabbitMQ [FILL: where was it used? confirm tier]
- familiar: Kafka (known, not used much at Zotok)

**Infra/DevOps**
- production: AWS (full list above), Terraform, Docker, GitHub Actions, CloudWatch observability, Azure Foundry
- used: GCP Firebase/Firestore (named in latest resume; application and scale still to confirm), Kubernetes/k3s (personal deployed services with ingress, TLS, CronJobs and PVCs; not claimed as employer production Kubernetes), WireGuard (personal infra)

**Frontend**
- production: React 18, Vite, NX, Module Federation, PWA/offline-first, react-joyride, Centrifuge, React Native + Expo
- used: Next.js 14 [FILL: confirm tier — production or side projects?]

**AI/LLM**
- production: LangGraph, RAG (hybrid retrieval), multi-agent orchestration, LiteLLM, prompt-driven extraction agents, MCP
- used: LangChain (named in latest resume; specific direct-library work versus use through LangGraph still to confirm), Claude Code, Codex and Command Code harness integrations (agent-bridge), Hermes Agent/OpenWA plugin integration

---

# Adjacent Skills ( soft hand on these skills )
- Databases : cassandra
- GCP : BigQuery
- Automation : Ansible, Jenkins
- AI/ML : PyTorch, TensorFlow, Scikit-learn
- Languages: Java, Kotlin
- Frameworks: Spring, Spring Boot

# Interview stories bank (fill over time, one paragraph each)
- **Production incident:** Accounts HTTP 500 -> inspected production logs -> isolated missing database/schema configuration in secret-loader assignment -> preserved static defaults and added a regression test (#21303). Post-deploy recovery timing is unknown; do not invent an MTTR or users impacted.
- **Architecture/tradeoff:** exact customer/financial choices remain deterministic, authorized and workspace-scoped; broader semantic memory runs as a separate feature-flagged path. Explain identity correctness versus semantic convenience, not a claim that vector search is always inappropriate.
- **Failure/rollback:** top-up metadata migration was tested in a production transaction and rolled back. Initial collation failures required binary comparisons; a second dry-run pass produced zero changes and retained the existing ledger credit. This was a dry run, not a destructive production rollback or a completed release.
- **Collaboration:** partnered with sales, business and customer-facing teams on Copilot workflows (latest resume). [FILL: one concrete interaction, your decision, and the observed outcome; no invented mentoring/team-size claim.]
- **Reliability:** Jobwatch replaced periodic Telegram sync with one long-poll consumer and atomic update/offset persistence in SQLite; dashboard actions use the same durable queue. Recorded queued-action pickup was ~1.2s in a September deployment check, not a general latency SLA or newly measured benchmark.
- **Scale:** Go gateway routed 12 services at 1k steady RPS / 10k under load; LiteLLM handled 100M tokens/day across 5 services (latest resume). [FILL: a specific bottleneck, before/after data, and your change; the headline numbers alone do not establish a tuning story.]
