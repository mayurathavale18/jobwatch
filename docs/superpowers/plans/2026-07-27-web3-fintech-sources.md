# Web3/Fintech Sources Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Add web3/crypto and fintech job sources to jobwatch: a new `web3career` provider hitting web3.career's official Jobs API, plus 19 fintech/crypto companies added to existing Greenhouse/Ashby providers with zero new code.

**Architecture:** One new search-aggregator provider (`internal/providers/web3career.go`, same shape as the existing `remoteok.go`/`wwr.go`: one `Fetch` call returns jobs across many real companies, each carrying its own `CompanySlug`/`CompanyName`). Everything else is `config.yaml` additions consumed by providers that already exist. All fetched jobs flow through the existing `Passes()` title/location filter and the Task E eligibility fixes (word-boundary matching, years-of-experience cap) automatically -- no new filter code needed.

**Tech Stack:** Go (`net/http`, `encoding/json` stdlib only -- no new dependencies).

## Global Constraints

- web3.career API Terms of Service (from their signup confirmation email): must use `apply_url` verbatim as the job's URL, unmodified (no added query params), with a followable (non-nofollow) link. This provider stores `apply_url` directly as `Job.URL` and never rewrites it -- satisfied by construction.
- The web3.career API's response shape is empirically inconsistent: passing `remote=true` or `show_description=false` has been observed (via live curl testing against the real token) to drop the `id`, `company`, and `date_epoch` fields entirely and change the wrapper array's length. **Only `token` and `limit` may be sent as query parameters** -- verified in Task 1 to keep the full field set intact.
- `WEB3CAREER_API_TOKEN` is a secret: never write its literal value into any file tracked by git (this plan included -- steps reference `<TOKEN>` as a placeholder, never the real value). It goes into `.env` files only (local `/home/dev-mayur/jobwatch/.env` and EC2 `/opt/jobwatch/.env`), which are gitignored.
- `go test ./...` and `gofmt -l .` (empty) must pass throughout.
- After merge to `master`, changes must be verified live against the deployed EC2 instance (`16.113.24.110`), not just CI green.

---

### Task 1: `web3career` Go provider

**Files:**
- Create: `internal/providers/web3career.go`
- Create: `internal/providers/testdata/web3career/sample.json`
- Test: `internal/providers/providers_test.go` (add `TestParseWeb3Career`)

**Interfaces:**
- Produces: `Web3CareerAPITokenEnv` (const, `"WEB3CAREER_API_TOKEN"`), `NewWeb3Career(client *http.Client) *Web3Career`, `(*Web3Career).Fetch(ctx, company) ([]Job, error)`, `parseWeb3CareerJSON(data []byte) ([]Job, error)` (testable pure function, mirrors `parseRemoteOKJSON`).
- Consumes: `Job` struct, `slugify` helper (both already in `internal/providers/remoteok.go`, same package).

- [ ] **Step 1: Write the fixture**

Create `internal/providers/testdata/web3career/sample.json`:

```json
[
  "Web3 Jobs API https://web3.career\nURL params:\nremote=true (show only remote jobs),\nlimit=100 ...\nAPI Terms of Service: Please link back using apply_url (with follow, and without nofollow!) and mention web3.career as a source, so we get traffic back from your site. If you do not we'll have to suspend API access.",
  [
    {
      "id": 151862,
      "date": "Sat, 25 Jul 2026 06:46:26 +0100",
      "date_epoch": 1784958386,
      "is_remote": false,
      "country": "india",
      "city": "bengaluru",
      "title": "Backend Engineer",
      "company": "Acme Chain",
      "location": " KA Bengaluru IN",
      "apply_url": "https://web3.career/r/aaaa1111",
      "tags": ["backend", "solidity"]
    },
    {
      "id": 151863,
      "date": "Sat, 25 Jul 2026 06:46:26 +0100",
      "date_epoch": 1784958400,
      "is_remote": true,
      "country": "united-states",
      "city": "san-francisco",
      "title": "Smart Contract Engineer",
      "company": "Widget DAO",
      "location": " CA San Francisco US",
      "apply_url": "https://web3.career/r/bbbb2222",
      "tags": ["solidity", "rust"]
    },
    {
      "id": 151864,
      "date": "Sat, 25 Jul 2026 06:46:26 +0100",
      "date_epoch": 1784958420,
      "is_remote": true,
      "country": "remote",
      "city": "remote",
      "title": "Full Stack Engineer",
      "company": "Chain Labs",
      "location": "Remote",
      "apply_url": "https://web3.career/r/cccc3333",
      "tags": ["fullstack"]
    },
    {
      "id": 0,
      "date": "Sat, 25 Jul 2026 06:46:26 +0100",
      "date_epoch": 1784958430,
      "is_remote": false,
      "title": "Malformed Entry",
      "company": "Nobody",
      "location": "",
      "apply_url": "",
      "tags": []
    }
  ]
]
```

- [ ] **Step 2: Write the failing test**

Add to `internal/providers/providers_test.go` (after `TestParseWeWorkRemotely`, before `TestSlugify`):

```go
func TestParseWeb3Career(t *testing.T) {
	data := readFixture(t, "testdata/web3career/sample.json")

	jobs, err := parseWeb3CareerJSON(data)
	if err != nil {
		t.Fatalf("parseWeb3CareerJSON: %v", err)
	}
	if len(jobs) != 3 {
		t.Fatalf("expected 3 jobs (malformed entry with empty apply_url and id=0 skipped), got %d", len(jobs))
	}

	j := jobs[0]
	if j.Provider != "web3career" {
		t.Errorf("Provider = %q, want web3career", j.Provider)
	}
	if j.CompanySlug != "acme-chain" || j.CompanyName != "Acme Chain" {
		t.Errorf("company fields wrong: slug=%q name=%q", j.CompanySlug, j.CompanyName)
	}
	if j.ExternalID != "151862" {
		t.Errorf("ExternalID = %q, want 151862", j.ExternalID)
	}
	if j.Title != "Backend Engineer" {
		t.Errorf("Title = %q", j.Title)
	}
	if j.URL != "https://web3.career/r/aaaa1111" {
		t.Errorf("URL = %q, want apply_url verbatim", j.URL)
	}
	if j.Location != " KA Bengaluru IN" {
		t.Errorf("Location = %q, want unmodified (is_remote=false)", j.Location)
	}
	if j.PostedAt == nil || j.PostedAt.Unix() != 1784958386 {
		t.Errorf("PostedAt = %v, want parsed from date_epoch 1784958386", j.PostedAt)
	}

	// is_remote=true but location text doesn't say "remote" -- must be
	// appended so the existing locations_include filter catches it.
	j2 := jobs[1]
	if j2.Location != " CA San Francisco US (Remote)" {
		t.Errorf("Location = %q, want \"(Remote)\" appended for is_remote job with non-remote-sounding location text", j2.Location)
	}

	// is_remote=true and location text already says "Remote" -- must not
	// be double-appended.
	j3 := jobs[2]
	if j3.Location != "Remote" {
		t.Errorf("Location = %q, want unchanged (already says Remote)", j3.Location)
	}
}
```

- [ ] **Step 3: Run test to verify it fails**

Run: `go test ./internal/providers/... -run TestParseWeb3Career -v`
Expected: FAIL with `undefined: parseWeb3CareerJSON`

- [ ] **Step 4: Implement the provider**

Create `internal/providers/web3career.go`:

```go
package providers

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"strconv"
	"strings"
	"time"

	"jobwatch/internal/config"
)

// Web3CareerAPITokenEnv names the environment variable holding the API
// token issued by web3.career's free Jobs API signup. See
// https://web3.career/web3-jobs-api.
const Web3CareerAPITokenEnv = "WEB3CAREER_API_TOKEN"

// Web3Career fetches postings from web3.career's official Jobs API. Like
// RemoteOK and We Work Remotely, this is a search aggregator: one Fetch
// call returns jobs across many different real companies, each carrying
// its own CompanySlug/CompanyName rather than the config entry's.
//
// The API's response shape is inconsistent across query parameter
// combinations -- passing remote=true or show_description=false has been
// observed (via live testing against a real token) to both change the
// wrapper array's length AND silently drop fields (id, company,
// date_epoch) this provider depends on. To stay robust to that, Fetch
// requests only token+limit and locates the jobs list by taking the
// *last* element of the top-level response array, rather than assuming
// a fixed index.
type Web3Career struct {
	Client   *http.Client
	BaseURL  string
	APIToken string
}

func NewWeb3Career(client *http.Client) *Web3Career {
	if client == nil {
		client = http.DefaultClient
	}
	return &Web3Career{
		Client:   client,
		BaseURL:  "https://web3.career/api/v1",
		APIToken: os.Getenv(Web3CareerAPITokenEnv),
	}
}

type web3CareerJob struct {
	ID        int64  `json:"id"`
	DateEpoch int64  `json:"date_epoch"`
	IsRemote  bool   `json:"is_remote"`
	Title     string `json:"title"`
	Company   string `json:"company"`
	Location  string `json:"location"`
	ApplyURL  string `json:"apply_url"`
}

func (w *Web3Career) Fetch(ctx context.Context, _ config.Company) ([]Job, error) {
	if w.APIToken == "" {
		return nil, fmt.Errorf("web3career: %s env var not set", Web3CareerAPITokenEnv)
	}

	reqURL := fmt.Sprintf("%s?token=%s&limit=100", w.BaseURL, w.APIToken)
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, reqURL, nil)
	if err != nil {
		return nil, err
	}

	resp, err := w.Client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("web3career: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("web3career: unexpected status %d", resp.StatusCode)
	}

	var raw []json.RawMessage
	if err := json.NewDecoder(resp.Body).Decode(&raw); err != nil {
		return nil, fmt.Errorf("web3career: decode: %w", err)
	}

	return parseWeb3Career(raw)
}

func parseWeb3CareerJSON(data []byte) ([]Job, error) {
	var raw []json.RawMessage
	if err := json.Unmarshal(data, &raw); err != nil {
		return nil, fmt.Errorf("web3career: decode: %w", err)
	}
	return parseWeb3Career(raw)
}

func parseWeb3Career(raw []json.RawMessage) ([]Job, error) {
	if len(raw) == 0 {
		return nil, fmt.Errorf("web3career: empty response array")
	}

	var jobsRaw []json.RawMessage
	if err := json.Unmarshal(raw[len(raw)-1], &jobsRaw); err != nil {
		return nil, fmt.Errorf("web3career: decode jobs list: %w", err)
	}

	now := time.Now().UTC()
	jobs := make([]Job, 0, len(jobsRaw))
	for _, jr := range jobsRaw {
		var j web3CareerJob
		if err := json.Unmarshal(jr, &j); err != nil {
			return nil, fmt.Errorf("web3career: decode entry: %w", err)
		}
		if j.ApplyURL == "" || j.ID == 0 {
			continue
		}

		var postedAt *time.Time
		if j.DateEpoch > 0 {
			t := time.Unix(j.DateEpoch, 0).UTC()
			postedAt = &t
		}

		// web3.career's own location text often omits the word "remote"
		// even when is_remote is true (e.g. "CA San Francisco US") --
		// left as-is, that silently fails every locations_include filter
		// for a posting that's exactly what it's looking for.
		location := j.Location
		if j.IsRemote && !strings.Contains(strings.ToLower(location), "remote") {
			if strings.TrimSpace(location) == "" {
				location = "Remote"
			} else {
				location = location + " (Remote)"
			}
		}

		jobs = append(jobs, Job{
			Provider:    "web3career",
			CompanySlug: slugify(j.Company),
			CompanyName: j.Company,
			ExternalID:  strconv.FormatInt(j.ID, 10),
			Title:       j.Title,
			Location:    location,
			URL:         j.ApplyURL,
			PostedAt:    postedAt,
			FirstSeenAt: now,
			Raw:         json.RawMessage(jr),
		})
	}
	return jobs, nil
}
```

- [ ] **Step 5: Run test to verify it passes**

Run: `go test ./internal/providers/... -run TestParseWeb3Career -v`
Expected: PASS

- [ ] **Step 6: Run full Go test suite and gofmt check**

Run: `go test ./... && gofmt -l .`
Expected: all PASS, `gofmt -l .` prints nothing

- [ ] **Step 7: Commit**

```bash
git add internal/providers/web3career.go internal/providers/providers_test.go internal/providers/testdata/web3career/sample.json
git commit -m "Add web3career provider for web3.career's official Jobs API"
```

---

### Task 2: Wire `web3career` into config validation and the provider registry

**Files:**
- Modify: `internal/config/config.go` (`validProviders` map and error message, ~line 76-90)
- Modify: `internal/providers/provider.go` (`ForName` switch and doc comment, ~line 40-58)

**Interfaces:**
- Consumes: `providers.NewWeb3Career` (Task 1).
- Produces: `providers.ForName("web3career")` returns a `*Web3Career`; `config.Load` accepts `provider: web3career` in `config.yaml`.

- [ ] **Step 1: Update config.go's provider allowlist**

In `internal/config/config.go`, replace:

```go
	validProviders := map[string]bool{
		"greenhouse": true, "lever": true, "ashby": true, "workday": true,
		"remoteok": true, "wwr": true,
	}
```

with:

```go
	validProviders := map[string]bool{
		"greenhouse": true, "lever": true, "ashby": true, "workday": true,
		"remoteok": true, "wwr": true, "web3career": true,
	}
```

And replace:

```go
			return fmt.Errorf("companies[%d] (%s): unsupported provider %q (want greenhouse, lever, ashby, workday, remoteok, or wwr)", i, co.Name, co.Provider)
```

with:

```go
			return fmt.Errorf("companies[%d] (%s): unsupported provider %q (want greenhouse, lever, ashby, workday, remoteok, wwr, or web3career)", i, co.Name, co.Provider)
```

- [ ] **Step 2: Update provider.go's ForName**

In `internal/providers/provider.go`, replace:

```go
// ForName returns the Provider implementation for the given provider name
// ("greenhouse", "lever", "ashby", "workday", "remoteok", "wwr"), as
// configured in config.yaml.
func ForName(name string) Provider {
	switch name {
	case "greenhouse":
		return NewGreenhouse(nil)
	case "lever":
		return NewLever(nil)
	case "ashby":
		return NewAshby(nil)
	case "workday":
		return NewWorkday(nil)
	case "remoteok":
		return NewRemoteOK(nil)
	case "wwr":
		return NewWeWorkRemotely(nil)
	default:
		return nil
	}
}
```

with:

```go
// ForName returns the Provider implementation for the given provider name
// ("greenhouse", "lever", "ashby", "workday", "remoteok", "wwr",
// "web3career"), as configured in config.yaml.
func ForName(name string) Provider {
	switch name {
	case "greenhouse":
		return NewGreenhouse(nil)
	case "lever":
		return NewLever(nil)
	case "ashby":
		return NewAshby(nil)
	case "workday":
		return NewWorkday(nil)
	case "remoteok":
		return NewRemoteOK(nil)
	case "wwr":
		return NewWeWorkRemotely(nil)
	case "web3career":
		return NewWeb3Career(nil)
	default:
		return nil
	}
}
```

- [ ] **Step 3: Run full Go test suite and gofmt check**

Run: `go test ./... && gofmt -l .`
Expected: all PASS

- [ ] **Step 4: Commit**

```bash
git add internal/config/config.go internal/providers/provider.go
git commit -m "Wire web3career into provider registry and config validation"
```

---

### Task 3: Expand config.yaml with web3career and 19 fintech/crypto companies

**Files:**
- Modify: `config.yaml`

**Interfaces:** none -- pure data, consumed by Task 1/2's already-wired providers.

- [ ] **Step 1: Add the new companies**

In `config.yaml`, find the `companies:` list and its last entry (`We Work Remotely`):

```yaml
  - name: "We Work Remotely"
    provider: wwr
    slug: wwr
```

Add immediately after it:

```yaml
  - name: "Web3Career"
    provider: web3career
    slug: web3career
  - name: "Brex"
    provider: greenhouse
    slug: brex
  - name: "Affirm"
    provider: greenhouse
    slug: affirm
  - name: "Chime"
    provider: greenhouse
    slug: chime
  - name: "Marqeta"
    provider: greenhouse
    slug: marqeta
  - name: "Robinhood"
    provider: greenhouse
    slug: robinhood
  - name: "Coinbase"
    provider: greenhouse
    slug: coinbase
  - name: "Gemini"
    provider: greenhouse
    slug: gemini
  - name: "Binance"
    provider: greenhouse
    slug: binance
  - name: "Bitpanda"
    provider: greenhouse
    slug: bitpanda
  - name: "OKX"
    provider: greenhouse
    slug: okx
  - name: "Bybit"
    provider: greenhouse
    slug: bybit
  - name: "ConsenSys"
    provider: greenhouse
    slug: consensys
  - name: "Plaid"
    provider: ashby
    slug: plaid
  - name: "Ramp"
    provider: ashby
    slug: ramp
  - name: "Mercury"
    provider: ashby
    slug: mercury
  - name: "Deel"
    provider: ashby
    slug: deel
  - name: "Alchemy"
    provider: ashby
    slug: alchemy
  - name: "Circle"
    provider: ashby
    slug: circle
  - name: "Airwallex"
    provider: ashby
    slug: airwallex
```

- [ ] **Step 2: Validate the YAML loads correctly**

Run: `go run ./cmd/jobwatch -h > /dev/null && go test ./internal/config/... -v`
Expected: no parse errors; existing config tests still PASS (they use their own inline YAML fixtures, not this file, but this confirms the binary still builds and `config.Load` logic is unaffected).

Then explicitly load the real file to confirm it parses:

```bash
cat > /tmp/check_config.go <<'EOF'
package main

import (
	"fmt"
	"os"

	"jobwatch/internal/config"
)

func main() {
	cfg, err := config.Load("config.yaml")
	if err != nil {
		fmt.Fprintln(os.Stderr, "load failed:", err)
		os.Exit(1)
	}
	fmt.Printf("loaded %d companies\n", len(cfg.Companies))
}
EOF
mkdir -p tmp_check_config && mv /tmp/check_config.go tmp_check_config/main.go
go run ./tmp_check_config
rm -rf tmp_check_config
```

Expected: `loaded 28 companies` (8 existing entries + 20 new: web3career + 19 fintech/crypto).

- [ ] **Step 3: Commit**

```bash
git add config.yaml
git commit -m "Add web3career and 19 fintech/crypto companies to config.yaml"
```

---

### Task 4: Wire the real API token locally and confirm a live end-to-end fetch

**Files:**
- Modify: `/home/dev-mayur/jobwatch/.env` (NOT part of this worktree/repo -- lives only in the main checkout, gitignored)

**Interfaces:** none -- this task validates Tasks 1-3 against the real, live web3.career API using the real token already obtained from their signup email.

- [ ] **Step 1: Add the token to the local .env**

In `/home/dev-mayur/jobwatch/.env`, add a new line following the existing `export KEY=value` format used by `JOBWATCH_TG_TOKEN`/`OPENCODE_API_KEY`:

```
export WEB3CAREER_API_TOKEN=<TOKEN>
```

(Use the real token value from the web3.career signup confirmation email -- never write it into a git-tracked file.)

- [ ] **Step 2: Run a real poll cycle locally and confirm web3career jobs land**

```bash
cd /home/dev-mayur/jobwatch
source .env
go run ./cmd/jobwatch poll -config config.yaml
sqlite3 jobwatch.db "SELECT provider, COUNT(*) FROM jobs WHERE provider='web3career' GROUP BY provider;"
sqlite3 jobwatch.db "SELECT title, company_name, location, status FROM jobs WHERE provider='web3career' LIMIT 5;"
```

Expected: the `web3career` count query returns at least 1 row with count > 0 (assuming the API currently has any postings passing `Passes()`'s include/exclude/location filters -- if it returns 0, check the poll command's log output for a `web3career` fetch error before assuming failure, since a 0-match result against current filters is possible but a fetch *error* is not acceptable).

- [ ] **Step 3: Spot-check one fetched job's data quality**

```bash
sqlite3 jobwatch.db "SELECT url FROM jobs WHERE provider='web3career' LIMIT 1;"
```

Confirm the URL starts with `https://web3.career/r/` (the apply_url redirect, stored verbatim per the API's Terms of Service) and, opened in a browser or via `curl -sL -o /dev/null -w '%{url_effective}\n' <url>`, redirects to a real company careers/job page.

---

### Task 5: Push, auto-deploy, and verify on EC2

**Files:** none (deploy + verification only)

**Interfaces:** none -- validates Tasks 1-4's committed changes against the live server, and adds the token to the EC2 `.env` (Task 4 only covered local).

- [ ] **Step 1: Run the full local test suite one more time**

Run: `go test ./... && gofmt -l .`
Expected: all PASS, `gofmt -l .` empty

- [ ] **Step 2: Push to master**

```bash
git push origin master
```

This triggers `.github/workflows/deploy.yml`.

- [ ] **Step 3: Watch the GitHub Actions run to completion**

Run: `gh run watch --exit-status $(gh run list --workflow=deploy.yml --limit 1 --json databaseId --jq '.[0].databaseId')`
Expected: run concludes with success.

- [ ] **Step 4: Add the token to the EC2 .env**

```bash
ssh -i ~/.ssh/jobwatch-key.pem ubuntu@16.113.24.110 "sudo bash -c 'echo \"export WEB3CAREER_API_TOKEN=<TOKEN>\" >> /opt/jobwatch/.env && chown jobwatch:jobwatch /opt/jobwatch/.env'"
```

(Substitute the real token. This command only appends to a file already `chmod 600`-protected and owned by the `jobwatch` user -- confirm that permission is unchanged afterward with `ssh ... "sudo ls -la /opt/jobwatch/.env"`.)

- [ ] **Step 5: Restart the service so it picks up the new .env line**

```bash
ssh -i ~/.ssh/jobwatch-key.pem ubuntu@16.113.24.110 "sudo systemctl restart jobwatch && sleep 2 && sudo systemctl is-active jobwatch"
```
Expected: `active`

- [ ] **Step 6: Verify config.yaml landed correctly on the box**

```bash
ssh -i ~/.ssh/jobwatch-key.pem ubuntu@16.113.24.110 "grep -c 'provider: web3career\|provider: ashby\|provider: greenhouse' /opt/jobwatch/config.yaml"
diff <(md5sum config.yaml | cut -d' ' -f1) <(ssh -i ~/.ssh/jobwatch-key.pem ubuntu@16.113.24.110 "sudo md5sum /opt/jobwatch/config.yaml" | cut -d' ' -f1) && echo MATCH
```

- [ ] **Step 7: Run a live poll on the box and confirm web3career jobs land in the prod DB**

```bash
ssh -i ~/.ssh/jobwatch-key.pem ubuntu@16.113.24.110 "sudo -u jobwatch bash -lc '/opt/jobwatch/scripts/poll-wrapper.sh'"
ssh -i ~/.ssh/jobwatch-key.pem ubuntu@16.113.24.110 "sudo -u jobwatch sqlite3 /opt/jobwatch/jobwatch.db \"SELECT provider, COUNT(*) FROM jobs GROUP BY provider ORDER BY 2 DESC;\""
```

Expected: `poll cycle complete` with `companies_ok` covering all newly-added companies and `errors: null` (or, if any single new company legitimately has zero current postings, that's fine -- only a fetch *error* for web3career/greenhouse/ashby company entries is a real problem); the provider breakdown includes `web3career` and the new company names' postings show up under their respective `greenhouse`/`ashby` provider counts.

- [ ] **Step 8: Spot-check the dashboard**

Open `https://jobwatch.mayurathavale.com` (Basic Auth) and confirm new jobs from Brex/Ramp/Plaid/etc. and web3career show up in the jobs list.
