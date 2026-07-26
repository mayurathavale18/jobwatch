# Jobs Filter Upgrade Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Upgrade the dashboard's job filtering: add a provider filter, broaden search to company/location text, add a date-range filter, and make status multi-select -- needed now that jobwatch tracks 28 companies across 6 providers instead of the original handful.

**Architecture:** Backend changes are additive to the existing `JobFilter`/`filterWhere` pattern in `internal/store/store.go`, threaded through `internal/web/api.go`'s query-param parsing exactly like the existing `status`/`company`/`q` params. Frontend changes are additive to `Jobs.tsx`'s existing filter-bar pattern (URL-synced state, one `<select>`/input per filter) -- status changes from a single `<select>` to a row of toggle buttons since it's now multi-select.

**Tech Stack:** Go (`database/sql`, stdlib `time`/`strings`/`strconv`), React/TypeScript (no new dependencies).

## Global Constraints

- `go test ./...` and `gofmt -l .` (empty) must pass throughout.
- Frontend: `cd frontend && npx tsc -b --noEmit && npx oxlint` must pass with no new errors. No frontend test runner exists in this repo (no vitest/jest configured) -- verification is type-check + lint + manual check against the dev server per this project's existing convention for UI changes.
- Date-range filtering uses `first_seen_at` (ingestion timestamp, `NOT NULL`, already indexed), not `posted_at` (nullable, inconsistently populated across providers).
- After merge to `master`, changes must be verified live against the deployed EC2 instance (`16.113.24.110`) and in-browser against `https://jobwatch.mayurathavale.com`, not just CI/build green.

---

### Task 1: Backend filter fields -- `Provider`, multi-`Statuses`, `Since`, broadened `Search`

**Files:**
- Modify: `internal/store/store.go` (`JobFilter` struct ~line 273-279, `filterWhere` ~line 282-299, add `Providers` method near `CompanyNames` ~line 385-400)
- Test: `internal/store/store_test.go` (extend `TestListJobsFilters`, add `TestListJobsFiltersProviderStatusesSinceAndBroadSearch`, `TestProviders`)

**Interfaces:**
- Produces: `JobFilter{Status string}` becomes `JobFilter{Statuses []string}` (breaking change to the struct, fixed up in Task 2's only caller). New fields `Provider string`, `Since string` (RFC3339, empty = no lower bound). New method `(*Store) Providers(ctx) ([]string, error)`.
- Consumes: nothing new.

- [ ] **Step 1: Write the failing tests**

In `internal/store/store_test.go`, replace `TestListJobsFilters` (the existing test still exercises `Company`/`Search` -- keep those assertions, just note `Search` now also matches company/location) and add new tests after it:

```go
func TestListJobsFilters(t *testing.T) {
	ctx := context.Background()
	s := newTestStore(t)

	j1 := sampleJob()
	j1.ExternalID = "1"
	j1.Title = "Backend Engineer"
	j1.CompanyName = "Stripe"

	j2 := sampleJob()
	j2.ExternalID = "2"
	j2.Title = "Frontend Engineer"
	j2.CompanyName = "Razorpay"

	for _, j := range []providers.Job{j1, j2} {
		tx, _ := s.BeginTx(ctx)
		if _, err := s.InsertJob(ctx, tx, j, StatusNew); err != nil {
			t.Fatalf("InsertJob: %v", err)
		}
		tx.Commit()
	}

	all, err := s.ListJobs(ctx, JobFilter{})
	if err != nil {
		t.Fatalf("ListJobs: %v", err)
	}
	if len(all) != 2 {
		t.Fatalf("expected 2 jobs, got %d", len(all))
	}

	byCompany, err := s.ListJobs(ctx, JobFilter{Company: "Stripe"})
	if err != nil {
		t.Fatalf("ListJobs by company: %v", err)
	}
	if len(byCompany) != 1 || byCompany[0].CompanyName != "Stripe" {
		t.Errorf("byCompany = %+v", byCompany)
	}

	bySearch, err := s.ListJobs(ctx, JobFilter{Search: "Frontend"})
	if err != nil {
		t.Fatalf("ListJobs by search: %v", err)
	}
	if len(bySearch) != 1 || bySearch[0].Title != "Frontend Engineer" {
		t.Errorf("bySearch = %+v", bySearch)
	}
}

func TestListJobsFiltersProviderStatusesSinceAndBroadSearch(t *testing.T) {
	ctx := context.Background()
	s := newTestStore(t)

	old := sampleJob()
	old.ExternalID = "old"
	old.Provider = "web3career"
	old.CompanyName = "Old Chain"
	old.Location = "Bengaluru, India"
	old.FirstSeenAt = time.Now().UTC().Add(-10 * 24 * time.Hour)

	recentGreenhouse := sampleJob()
	recentGreenhouse.ExternalID = "recent-gh"
	recentGreenhouse.Provider = "greenhouse"
	recentGreenhouse.CompanyName = "Brex"
	recentGreenhouse.Location = "Remote"
	recentGreenhouse.FirstSeenAt = time.Now().UTC()

	recentWeb3 := sampleJob()
	recentWeb3.ExternalID = "recent-w3"
	recentWeb3.Provider = "web3career"
	recentWeb3.CompanyName = "New Chain"
	recentWeb3.Location = "Remote"
	recentWeb3.FirstSeenAt = time.Now().UTC()

	for i, j := range []providers.Job{old, recentGreenhouse, recentWeb3} {
		tx, _ := s.BeginTx(ctx)
		status := StatusNew
		if i == 1 {
			status = StatusShortlisted
		}
		if _, err := s.InsertJob(ctx, tx, j, status); err != nil {
			t.Fatalf("InsertJob: %v", err)
		}
		tx.Commit()
	}

	byProvider, err := s.ListJobs(ctx, JobFilter{Provider: "web3career"})
	if err != nil {
		t.Fatalf("ListJobs by provider: %v", err)
	}
	if len(byProvider) != 2 {
		t.Fatalf("byProvider len = %d, want 2", len(byProvider))
	}

	byStatuses, err := s.ListJobs(ctx, JobFilter{Statuses: []string{string(StatusNew), string(StatusShortlisted)}})
	if err != nil {
		t.Fatalf("ListJobs by statuses: %v", err)
	}
	if len(byStatuses) != 3 {
		t.Fatalf("byStatuses len = %d, want 3 (all seeded jobs are new or shortlisted)", len(byStatuses))
	}

	since := time.Now().UTC().Add(-24 * time.Hour).Format(time.RFC3339)
	bySince, err := s.ListJobs(ctx, JobFilter{Since: since})
	if err != nil {
		t.Fatalf("ListJobs by since: %v", err)
	}
	if len(bySince) != 2 {
		t.Fatalf("bySince len = %d, want 2 (excludes the 10-day-old job)", len(bySince))
	}

	byLocationSearch, err := s.ListJobs(ctx, JobFilter{Search: "Bengaluru"})
	if err != nil {
		t.Fatalf("ListJobs by location search: %v", err)
	}
	if len(byLocationSearch) != 1 || byLocationSearch[0].CompanyName != "Old Chain" {
		t.Errorf("byLocationSearch = %+v, want the Bengaluru job matched via location text", byLocationSearch)
	}

	byCompanySearch, err := s.ListJobs(ctx, JobFilter{Search: "Brex"})
	if err != nil {
		t.Fatalf("ListJobs by company search: %v", err)
	}
	if len(byCompanySearch) != 1 || byCompanySearch[0].CompanyName != "Brex" {
		t.Errorf("byCompanySearch = %+v, want the Brex job matched via company_name text", byCompanySearch)
	}
}

func TestProviders(t *testing.T) {
	ctx := context.Background()
	s := newTestStore(t)

	j1 := sampleJob()
	j1.ExternalID = "1"
	j1.Provider = "greenhouse"

	j2 := sampleJob()
	j2.ExternalID = "2"
	j2.Provider = "web3career"

	j3 := sampleJob()
	j3.ExternalID = "3"
	j3.Provider = "greenhouse"

	for _, j := range []providers.Job{j1, j2, j3} {
		tx, _ := s.BeginTx(ctx)
		if _, err := s.InsertJob(ctx, tx, j, StatusNew); err != nil {
			t.Fatalf("InsertJob: %v", err)
		}
		tx.Commit()
	}

	got, err := s.Providers(ctx)
	if err != nil {
		t.Fatalf("Providers: %v", err)
	}
	if len(got) != 2 || got[0] != "greenhouse" || got[1] != "web3career" {
		t.Errorf("Providers = %v, want [greenhouse web3career] (distinct, sorted)", got)
	}
}
```

- [ ] **Step 2: Run tests to verify they fail**

Run: `go test ./internal/store/... -run 'TestListJobsFilters|TestProviders' -v`
Expected: FAIL to compile -- `JobFilter` has no field `Provider`/`Statuses`/`Since`, and `Store` has no method `Providers`.

- [ ] **Step 3: Implement the JobFilter/filterWhere changes**

In `internal/store/store.go`, replace:

```go
// JobFilter narrows ListJobs results.
type JobFilter struct {
	Status  string // exact match, empty = any
	Company string // exact match on company_name, empty = any
	Search  string // substring match on title, empty = any
	Limit   int    // 0 = unlimited
	Offset  int
}

// filterWhere builds the shared WHERE clause + args for ListJobs and CountJobs.
func filterWhere(f JobFilter) (string, []any) {
	query := ` WHERE 1=1`
	var args []any

	if f.Status != "" {
		query += ` AND status = ?`
		args = append(args, f.Status)
	}
	if f.Company != "" {
		query += ` AND company_name = ?`
		args = append(args, f.Company)
	}
	if f.Search != "" {
		query += ` AND title LIKE ? ESCAPE '\'`
		args = append(args, "%"+escapeLike(f.Search)+"%")
	}
	return query, args
}
```

with:

```go
// JobFilter narrows ListJobs results.
type JobFilter struct {
	Statuses []string // exact match, any of; empty = any status
	Provider string   // exact match, empty = any
	Company  string   // exact match on company_name, empty = any
	Search   string   // substring match across title/company_name/location, empty = any
	Since    string   // RFC3339; first_seen_at >= Since. Empty = no lower bound.
	Limit    int      // 0 = unlimited
	Offset   int
}

// filterWhere builds the shared WHERE clause + args for ListJobs and CountJobs.
func filterWhere(f JobFilter) (string, []any) {
	query := ` WHERE 1=1`
	var args []any

	if len(f.Statuses) > 0 {
		placeholders := strings.Repeat("?,", len(f.Statuses))
		placeholders = placeholders[:len(placeholders)-1]
		query += ` AND status IN (` + placeholders + `)`
		for _, s := range f.Statuses {
			args = append(args, s)
		}
	}
	if f.Provider != "" {
		query += ` AND provider = ?`
		args = append(args, f.Provider)
	}
	if f.Company != "" {
		query += ` AND company_name = ?`
		args = append(args, f.Company)
	}
	if f.Search != "" {
		query += ` AND (title LIKE ? ESCAPE '\' OR company_name LIKE ? ESCAPE '\' OR location LIKE ? ESCAPE '\')`
		like := "%" + escapeLike(f.Search) + "%"
		args = append(args, like, like, like)
	}
	if f.Since != "" {
		query += ` AND first_seen_at >= ?`
		args = append(args, f.Since)
	}
	return query, args
}
```

- [ ] **Step 4: Add the Providers method**

In `internal/store/store.go`, immediately after `CompanyNames` (~line 400), add:

```go
// Providers returns the distinct set of provider names present in jobs,
// for populating the dashboard filter bar.
func (s *Store) Providers(ctx context.Context) ([]string, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT DISTINCT provider FROM jobs ORDER BY provider`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var out []string
	for rows.Next() {
		var name string
		if err := rows.Scan(&name); err != nil {
			return nil, err
		}
		out = append(out, name)
	}
	return out, rows.Err()
}
```

- [ ] **Step 5: Run tests to verify they pass**

Run: `go test ./internal/store/... -v`
Expected: all PASS, including the pre-existing `TestListJobsPaginationAndCount` and `TestInsertAndDedupe` (unaffected by this change).

- [ ] **Step 6: Run full Go test suite and gofmt check**

Run: `go test ./... 2>&1 | tail -20`
Expected: FAIL at this point -- `internal/web/api.go` still constructs `JobFilter{Status: ...}`, which no longer compiles. This is expected; Task 2 fixes it. Confirm the *only* failure is a compile error in `internal/web` referencing the removed `Status` field, then proceed to Task 2 without committing yet (a broken build should never be committed as its own step).

---

### Task 2: Wire the new filters into the API

**Files:**
- Modify: `internal/web/api.go` (`handleAPIJobs`, `apiJobsResponse` struct)
- Test: `internal/web/api_test.go` (add `TestHandleAPIJobsFiltersByProviderStatusesAndDays`)

**Interfaces:**
- Consumes: `store.JobFilter{Statuses, Provider, Since}` (Task 1), `store.Providers(ctx)` (Task 1).
- Produces: `apiJobsResponse.Providers []string` (new field, `json:"providers"`). Query params: `?provider=`, `?status=new,shortlisted` (comma-separated), `?days=1|7|30` (any positive int).

- [ ] **Step 1: Write the failing test**

Add to `internal/web/api_test.go` (after `TestHandleAPIJobsPaginates`):

```go
func TestHandleAPIJobsFiltersByProviderStatusesAndDays(t *testing.T) {
	srv, st := newTestServer(t)
	ctx := context.Background()

	old := providers.Job{
		Provider: "web3career", CompanySlug: "old-chain", CompanyName: "Old Chain",
		ExternalID: "old", Title: "Backend Engineer", Location: "Remote",
		URL: "https://example.com/old", FirstSeenAt: time.Now().UTC().Add(-10 * 24 * time.Hour), Raw: []byte(`{}`),
	}
	recent := providers.Job{
		Provider: "greenhouse", CompanySlug: "brex", CompanyName: "Brex",
		ExternalID: "recent", Title: "Backend Engineer", Location: "Remote",
		URL: "https://example.com/recent", FirstSeenAt: time.Now().UTC(), Raw: []byte(`{}`),
	}

	for i, j := range []providers.Job{old, recent} {
		tx, err := st.BeginTx(ctx)
		if err != nil {
			t.Fatalf("BeginTx: %v", err)
		}
		status := store.StatusNew
		if i == 0 {
			status = store.StatusShortlisted
		}
		if _, err := st.InsertJob(ctx, tx, j, status); err != nil {
			t.Fatalf("InsertJob: %v", err)
		}
		tx.Commit()
	}

	// Provider filter.
	req := httptest.NewRequest(http.MethodGet, "/api/jobs?provider=web3career", nil)
	w := httptest.NewRecorder()
	srv.Handler().ServeHTTP(w, req)
	var resp apiJobsResponse
	if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if resp.TotalFiltered != 1 || len(resp.Jobs) != 1 || resp.Jobs[0].CompanyName != "Old Chain" {
		t.Fatalf("provider filter resp = %+v", resp)
	}
	if len(resp.Providers) != 2 {
		t.Errorf("Providers = %v, want 2 distinct providers regardless of active filter", resp.Providers)
	}

	// Multi-status filter (comma-separated).
	req = httptest.NewRequest(http.MethodGet, "/api/jobs?status=new,shortlisted", nil)
	w = httptest.NewRecorder()
	srv.Handler().ServeHTTP(w, req)
	if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if resp.TotalFiltered != 2 {
		t.Fatalf("multi-status resp = %+v, want both jobs (new + shortlisted)", resp)
	}

	// Days filter: only the recent job falls within the last 1 day.
	req = httptest.NewRequest(http.MethodGet, "/api/jobs?days=1", nil)
	w = httptest.NewRecorder()
	srv.Handler().ServeHTTP(w, req)
	if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if resp.TotalFiltered != 1 || len(resp.Jobs) != 1 || resp.Jobs[0].CompanyName != "Brex" {
		t.Fatalf("days filter resp = %+v", resp)
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./internal/web/... -run TestHandleAPIJobsFiltersByProviderStatusesAndDays -v`
Expected: FAIL to compile (`internal/web/api.go` still references the removed `JobFilter.Status` field from Task 1).

- [ ] **Step 3: Implement the API wiring**

In `internal/web/api.go`, replace:

```go
type apiJobsResponse struct {
	Jobs          []apiJob       `json:"jobs"`
	Companies     []string       `json:"companies"`
	StatusCounts  map[string]int `json:"statusCounts"`
	TotalJobs     int            `json:"totalJobs"`
	LastPoll      *apiPollRun    `json:"lastPoll"`
	Statuses      []string       `json:"statuses"`
	Page          int            `json:"page"`
	PageSize      int            `json:"pageSize"`
	TotalFiltered int            `json:"totalFiltered"`
}
```

with:

```go
type apiJobsResponse struct {
	Jobs          []apiJob       `json:"jobs"`
	Companies     []string       `json:"companies"`
	Providers     []string       `json:"providers"`
	StatusCounts  map[string]int `json:"statusCounts"`
	TotalJobs     int            `json:"totalJobs"`
	LastPoll      *apiPollRun    `json:"lastPoll"`
	Statuses      []string       `json:"statuses"`
	Page          int            `json:"page"`
	PageSize      int            `json:"pageSize"`
	TotalFiltered int            `json:"totalFiltered"`
}
```

Then replace:

```go
	filter := store.JobFilter{
		Status:  q.Get("status"),
		Company: q.Get("company"),
		Search:  q.Get("q"),
		Limit:   jobsPageSize,
		Offset:  (page - 1) * jobsPageSize,
	}
```

with:

```go
	filter := store.JobFilter{
		Company: q.Get("company"),
		Provider: q.Get("provider"),
		Search:  q.Get("q"),
		Limit:   jobsPageSize,
		Offset:  (page - 1) * jobsPageSize,
	}
	if statusParam := q.Get("status"); statusParam != "" {
		filter.Statuses = strings.Split(statusParam, ",")
	}
	if days, err := strconv.Atoi(q.Get("days")); err == nil && days > 0 {
		filter.Since = time.Now().UTC().Add(-time.Duration(days) * 24 * time.Hour).Format(time.RFC3339)
	}
```

`api.go` already imports `strconv`; add `"time"` to its import block (alongside the existing `encoding/json`, `log/slog`, `net/http`, `os`, `os/exec`, `path/filepath`, `strconv`, `strings`).

Then find where `companies, err := s.store.CompanyNames(ctx)` is called and add immediately after it:

```go
	providersList, err := s.store.Providers(ctx)
	if err != nil {
		httpError(w, "listing providers", err)
		return
	}
```

Finally, in the `writeJSON(w, apiJobsResponse{...})` call, add `Providers: providersList,` alongside the existing `Companies: companies,` line.

- [ ] **Step 4: Run test to verify it passes**

Run: `go test ./internal/web/... -run TestHandleAPIJobsFiltersByProviderStatusesAndDays -v`
Expected: PASS

- [ ] **Step 5: Run full Go test suite and gofmt check**

Run: `go test ./... && gofmt -l .`
Expected: all PASS, `gofmt -l .` empty

- [ ] **Step 6: Commit**

```bash
git add internal/store/store.go internal/store/store_test.go internal/web/api.go internal/web/api_test.go
git commit -m "Add provider/multi-status/date-range filters and broaden search to backend"
```

---

### Task 3: Frontend API client types

**Files:**
- Modify: `frontend/src/api.ts`

**Interfaces:**
- Produces: `JobsResponse.providers: string[]`, `JobFilters.provider?: string`, `JobFilters.status?: string` (comma-joined, unchanged type but new semantics), `JobFilters.days?: number`.

- [ ] **Step 1: Update the types and fetchJobs**

In `frontend/src/api.ts`, replace:

```typescript
export interface JobsResponse {
  jobs: Job[]
  companies: string[]
  statusCounts: Record<string, number>
  totalJobs: number
  lastPoll: PollRun | null
  statuses: string[]
  page: number
  pageSize: number
  totalFiltered: number
}
```

with:

```typescript
export interface JobsResponse {
  jobs: Job[]
  companies: string[]
  providers: string[]
  statusCounts: Record<string, number>
  totalJobs: number
  lastPoll: PollRun | null
  statuses: string[]
  page: number
  pageSize: number
  totalFiltered: number
}
```

Then replace:

```typescript
export interface JobFilters {
  status?: string
  company?: string
  q?: string
  page?: number
}

export async function fetchJobs(filters: JobFilters): Promise<JobsResponse> {
  const params = new URLSearchParams()
  if (filters.status) params.set('status', filters.status)
  if (filters.company) params.set('company', filters.company)
  if (filters.q) params.set('q', filters.q)
  if (filters.page && filters.page > 1) params.set('page', String(filters.page))
  const qs = params.toString()
  const res = await fetch(`/api/jobs${qs ? `?${qs}` : ''}`)
  if (!res.ok) throw new Error(`GET /api/jobs: ${res.status}`)
  return res.json()
}
```

with:

```typescript
export interface JobFilters {
  status?: string
  provider?: string
  company?: string
  q?: string
  days?: number
  page?: number
}

export async function fetchJobs(filters: JobFilters): Promise<JobsResponse> {
  const params = new URLSearchParams()
  if (filters.status) params.set('status', filters.status)
  if (filters.provider) params.set('provider', filters.provider)
  if (filters.company) params.set('company', filters.company)
  if (filters.q) params.set('q', filters.q)
  if (filters.days) params.set('days', String(filters.days))
  if (filters.page && filters.page > 1) params.set('page', String(filters.page))
  const qs = params.toString()
  const res = await fetch(`/api/jobs${qs ? `?${qs}` : ''}`)
  if (!res.ok) throw new Error(`GET /api/jobs: ${res.status}`)
  return res.json()
}
```

- [ ] **Step 2: Type-check**

Run: `cd frontend && npx tsc -b --noEmit`
Expected: FAIL -- `Jobs.tsx` doesn't yet supply `provider`/`days` and doesn't yet read `data.providers` (Task 4 fixes this). Confirm the only errors are in `Jobs.tsx`, not `api.ts` itself.

- [ ] **Step 3: Commit**

```bash
git add frontend/src/api.ts
git commit -m "Add provider/days fields to frontend API client types"
```

---

### Task 4: Frontend filter UI

**Files:**
- Modify: `frontend/src/pages/Jobs.tsx`
- Modify: `frontend/src/index.css` (status-pill styles)

**Interfaces:**
- Consumes: `JobFilters`, `JobsResponse` (Task 3).

- [ ] **Step 1: Update Jobs.tsx's filter state and URL sync**

Replace:

```typescript
function filtersFromLocation(): { status: string; company: string; q: string } {
  const params = new URLSearchParams(window.location.search)
  return {
    status: params.get('status') ?? '',
    company: params.get('company') ?? '',
    q: params.get('q') ?? '',
  }
}
```

with:

```typescript
function filtersFromLocation(): { status: string; provider: string; company: string; q: string; days: number } {
  const params = new URLSearchParams(window.location.search)
  return {
    status: params.get('status') ?? '',
    provider: params.get('provider') ?? '',
    company: params.get('company') ?? '',
    q: params.get('q') ?? '',
    days: Number(params.get('days')) || 0,
  }
}
```

Replace the `useEffect` URL-sync block:

```typescript
  useEffect(() => {
    const params = new URLSearchParams()
    if (filters.status) params.set('status', filters.status)
    if (filters.company) params.set('company', filters.company)
    if (filters.q) params.set('q', filters.q)
    if (page > 1) params.set('page', String(page))
    const qs = params.toString()
    window.history.replaceState(null, '', qs ? `/?${qs}` : '/')

    fetchJobs({ ...filters, page }).then(setData).catch(console.error)
  }, [filters, page])

  function updateFilters(patch: Partial<{ status: string; company: string; q: string }>) {
    setFilters((f) => ({ ...f, ...patch }))
    setPage(1)
  }
```

with:

```typescript
  useEffect(() => {
    const params = new URLSearchParams()
    if (filters.status) params.set('status', filters.status)
    if (filters.provider) params.set('provider', filters.provider)
    if (filters.company) params.set('company', filters.company)
    if (filters.q) params.set('q', filters.q)
    if (filters.days) params.set('days', String(filters.days))
    if (page > 1) params.set('page', String(page))
    const qs = params.toString()
    window.history.replaceState(null, '', qs ? `/?${qs}` : '/')

    fetchJobs({ ...filters, page }).then(setData).catch(console.error)
  }, [filters, page])

  function updateFilters(patch: Partial<{ status: string; provider: string; company: string; q: string; days: number }>) {
    setFilters((f) => ({ ...f, ...patch }))
    setPage(1)
  }

  function toggleStatus(status: string) {
    const current = filters.status ? filters.status.split(',') : []
    const next = current.includes(status)
      ? current.filter((s) => s !== status)
      : [...current, status]
    updateFilters({ status: next.join(',') })
  }
```

- [ ] **Step 2: Replace the status `<select>` with toggle pills, add provider and date-range selects, update search placeholder**

Replace:

```typescript
      <div className="filters">
        <select
          value={filters.status}
          onChange={(e) => updateFilters({ status: e.target.value })}
        >
          <option value="">All statuses</option>
          {data.statuses.map((s) => (
            <option key={s} value={s}>
              {s}
            </option>
          ))}
        </select>
        <select
          value={filters.company}
          onChange={(e) => updateFilters({ company: e.target.value })}
        >
          <option value="">All companies</option>
          {data.companies.map((c) => (
            <option key={c} value={c}>
              {c}
            </option>
          ))}
        </select>
        <input
          type="text"
          placeholder="Search title..."
          value={filters.q}
          onChange={(e) => updateFilters({ q: e.target.value })}
        />
        <a href="/">Reset</a>
      </div>
```

with:

```typescript
      <div className="filters">
        <div className="status-pills">
          {data.statuses.map((s) => {
            const active = filters.status.split(',').includes(s)
            return (
              <button
                key={s}
                type="button"
                className={`status-pill${active ? ' active' : ''}`}
                onClick={() => toggleStatus(s)}
              >
                {s}
              </button>
            )
          })}
        </div>
        <select
          value={filters.provider}
          onChange={(e) => updateFilters({ provider: e.target.value })}
        >
          <option value="">All providers</option>
          {data.providers.map((p) => (
            <option key={p} value={p}>
              {p}
            </option>
          ))}
        </select>
        <select
          value={filters.company}
          onChange={(e) => updateFilters({ company: e.target.value })}
        >
          <option value="">All companies</option>
          {data.companies.map((c) => (
            <option key={c} value={c}>
              {c}
            </option>
          ))}
        </select>
        <select
          value={String(filters.days)}
          onChange={(e) => updateFilters({ days: Number(e.target.value) })}
        >
          <option value="0">All time</option>
          <option value="1">Last 24h</option>
          <option value="7">Last 7 days</option>
          <option value="30">Last 30 days</option>
        </select>
        <input
          type="text"
          placeholder="Search title, company, location..."
          value={filters.q}
          onChange={(e) => updateFilters({ q: e.target.value })}
        />
        <a href="/">Reset</a>
      </div>
```

- [ ] **Step 3: Add status-pill CSS**

In `frontend/src/index.css`, immediately after the `.filters select, .filters input[type='text'] { ... }` rule, add:

```css
.status-pills {
  display: flex;
  gap: 0.3rem;
  flex-wrap: wrap;
}
.status-pill {
  padding: 0.25rem 0.5rem;
  border: 1px solid var(--border);
  background: var(--bg);
  color: var(--muted);
  font: inherit;
  font-size: 0.8rem;
  cursor: pointer;
  border-radius: 0;
}
.status-pill:hover {
  border-color: var(--border-strong);
}
.status-pill.active {
  background: var(--accent);
  color: var(--bg);
  border-color: var(--accent);
}
```

(`--border-strong` is already used elsewhere in this file for `.notes-input:hover` -- confirms it's a defined variable in this theme.)

- [ ] **Step 4: Type-check and lint**

Run: `cd frontend && npx tsc -b --noEmit && npx oxlint`
Expected: both pass with no errors.

- [ ] **Step 5: Build and manually verify in the dev server**

```bash
cd frontend && npm run build
```
Expected: build succeeds.

Then start the dev server and manually verify in a browser (per this project's convention of checking UI changes against a running instance, not just a successful build):

```bash
cd /home/dev-mayur/jobwatch && source .env && go run ./cmd/jobwatch serve -config config.yaml &
```

Open `http://127.0.0.1:8787` and confirm:
- Status pills toggle on/off and combine (e.g. click "new" and "shortlisted" both active shows both).
- Provider dropdown filters to just that provider's jobs (try "web3career").
- Date-range dropdown narrows results (try "Last 24h").
- Search box matches company name and location text, not just title.
- URL reflects all active filters (e.g. `?status=new,shortlisted&provider=web3career&days=7&q=engineer`) and reloading the page preserves them.
- "Reset" clears everything back to `/`.

Stop the dev server (`kill %1` or Ctrl-C) once verified.

- [ ] **Step 6: Commit**

```bash
git add frontend/src/pages/Jobs.tsx frontend/src/index.css
git commit -m "Add provider filter, multi-select status pills, and date-range filter to dashboard UI"
```

---

### Task 5: Push, auto-deploy, and verify on EC2

**Files:** none (deploy + verification only)

**Interfaces:** none -- validates Tasks 1-4's committed changes against the live server.

- [ ] **Step 1: Run the full local test/build suite one more time**

Run: `go test ./... && gofmt -l . && cd frontend && npx tsc -b --noEmit && npx oxlint && npm run build`
Expected: all PASS

- [ ] **Step 2: Push to master**

```bash
git push origin master
```

- [ ] **Step 3: Watch the GitHub Actions run to completion**

Run: `gh run watch --exit-status $(gh run list --workflow=deploy.yml --limit 1 --json databaseId --jq '.[0].databaseId')`
Expected: run concludes with success.

- [ ] **Step 4: Verify the service is healthy**

```bash
ssh -i ~/.ssh/jobwatch-key.pem ubuntu@16.113.24.110 "sudo systemctl is-active jobwatch && curl -s -o /dev/null -w '%{http_code}\n' http://127.0.0.1:8787/api/jobs"
```
Expected: `active` and `200`.

- [ ] **Step 5: Verify the new API fields are live**

```bash
ssh -i ~/.ssh/jobwatch-key.pem ubuntu@16.113.24.110 "curl -s 'http://127.0.0.1:8787/api/jobs?provider=web3career&days=7' | python3 -c \"import json,sys; d=json.load(sys.stdin); print('providers:', d['providers']); print('totalFiltered:', d['totalFiltered'])\""
```
Expected: `providers` includes `web3career` among the full distinct list, `totalFiltered` reflects only recent web3career jobs.

- [ ] **Step 6: Spot-check the dashboard in a browser**

Open `https://jobwatch.mayurathavale.com` (Basic Auth) and repeat Task 4 Step 5's manual checks (status pills, provider dropdown, date range, broadened search, URL sync, Reset) against the live production data.
