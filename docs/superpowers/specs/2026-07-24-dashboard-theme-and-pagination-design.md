# Techy dashboard theme + jobs-table pagination

Date: 2026-07-24
Status: approved, ready for implementation planning

## Problem

The dashboard (`frontend/`) uses default system fonts, rounded corners, and
plain-text status colors — it doesn't match the "techy/code-like" look
Mayur asked for last session, referencing the Hermes agent dashboard
(`http://127.0.0.1:9119/sessions`, screenshot reviewed): dark teal
background, monospace/blocky uppercase headers, sharp-edged bordered
buttons and status pills, bright green accent for active/positive states.

Separately, `Jobs.tsx` fetches and renders every row returned by
`GET /api/jobs` with no limit — `store.ListJobs` has no `LIMIT`/`OFFSET`.
As jobwatch keeps polling every 15 minutes across multiple companies for
months, the jobs table has no ceiling and will keep growing unbounded in
both DB query cost and page render size.

## Goals

- Retheme the dashboard (`frontend/src/index.css` and the components that
  reference its classes) to a techy/monospace look, in both dark and light
  variants (the app currently respects OS `color-scheme: light dark`; keep
  that, don't force dark-only).
- Add server-side pagination to the jobs table: fixed page size of 25,
  Prev/Next navigation, `page` reflected in the URL like the existing
  `status`/`company`/`q` filters.

## Non-goals

- No layout restructure — nav stays the existing top-tab bar (`Nav.tsx`),
  not converted into a Hermes-style left sidebar. The app has only two
  pages (Jobs, Cron); a sidebar is unwarranted scope.
- No background grain/noise texture — visually part of the Hermes
  reference but not core to "techy," skipped for now.
- No pagination on the Cron page (`Cron.tsx`) — its list is a small fixed
  set of configured cron jobs, not an unbounded table.
- No user-configurable page size — fixed at 25.

## Design

### 1. Theme (`frontend/src/index.css`)

- Define CSS custom properties for both `light` and `dark` under
  `@media (prefers-color-scheme: ...)` (matching the existing
  `color-scheme: light dark` approach — no manual toggle):
  - `--bg`, `--fg`, `--border`, `--accent` (bright green, e.g. `#3ddc84`),
    `--warn` (amber), `--danger` (red), `--muted`.
  - Dark values approximate the Hermes reference (near-black teal bg,
    teal-tinted borders). Light values are a paler/inverted equivalent of
    the same hue family, not a separate design.
- Body font switches from the current system-sans stack to
  `ui-monospace, 'SFMono-Regular', 'JetBrains Mono', Consolas, monospace`
  everywhere (currently only `.detail` uses monospace). No external font
  files/CDN — stays offline-safe, consistent with the rest of this
  single-binary-deployed tool.
- `h1` and section-style headers: uppercase, `letter-spacing`, bold —
  achieves the blocky Hermes-header look without a separate display font.
- Sharp corners: `border-radius: 0` on `button`, `.badge`, `table`,
  `.stats`, form inputs/selects — replaces today's rounded/soft styling.
- `.badge-*` (Cron page) and `.status-*` (Jobs page) become bordered boxes
  (border + background tint + uppercase text) instead of plain colored
  text, using the new `--accent`/`--warn`/`--danger`/`--muted` variables
  per status.
- `nav.tabs`: uppercase labels, accent-colored underline on `.active`
  (replaces current `border-bottom: 2px solid currentColor` with the
  accent variable).
- All existing hardcoded hex colors in `index.css` (`#8883`, `#c0392b`,
  `#16a085`, etc.) are replaced by the new custom properties so dark/light
  stay in sync from one set of rules.

### 2. Backend pagination (`internal/store/store.go`, `internal/web/api.go`)

- `store.JobFilter` gains `Limit int` and `Offset int`.
- `ListJobs` appends `LIMIT ? OFFSET ?` to the existing query when
  `Limit > 0` (keeps the function usable unpaginated for any other
  caller, though today `handleAPIJobs` is the only one).
- New `Store.CountJobs(ctx, f JobFilter) (int, error)`: same `WHERE`
  clause as `ListJobs` (status/company/search), `SELECT COUNT(*)`, no
  limit — gives the filtered total for page-count math. Kept separate
  from the existing unfiltered `StatusCounts` (that one drives the
  always-unfiltered stats bar and must not change).
- `handleAPIJobs`: parses `page` (default 1, min 1) and applies a fixed
  `pageSize = 25`; computes `offset = (page-1) * pageSize`; calls
  `ListJobs` with `Limit/Offset` set and `CountJobs` with the same filter
  (unlimited) for the total. `apiJobsResponse` gains `Page int`,
  `PageSize int`, `TotalFiltered int` (distinct from the existing
  unfiltered `TotalJobs`).

### 3. Frontend pagination (`frontend/src/api.ts`, `frontend/src/pages/Jobs.tsx`)

- `JobFilters` gains `page?: number`. `JobsResponse` gains `page`,
  `pageSize`, `totalFiltered`.
- `fetchJobs` includes `page` in the query string when set.
- `Jobs.tsx`: new `page` state, read from/written to the URL the same way
  `filtersFromLocation`/the existing `useEffect` already do for
  status/company/q (extend the same `URLSearchParams` read/write, not a
  parallel mechanism).
- Changing `status`, `company`, or `q` resets `page` to 1 (new filter
  results may not have as many pages as the current one).
- Below the table: `Page {page} of {totalPages}` (`totalPages =
  Math.max(1, Math.ceil(totalFiltered / pageSize))`) plus Prev/Next
  buttons, disabled at the respective bound. The whole control is hidden
  when `totalPages <= 1`.

## Testing

- **Go** (`internal/store`): `ListJobs` test asserting `Limit`/`Offset`
  correctly slices a multi-row fixture (ordering already covered by
  existing tests, this just adds windowing); `CountJobs` test asserting
  it matches filter semantics identically to `ListJobs` (same filtered
  set size regardless of `Limit`).
- **Go** (`internal/web`): `handleAPIJobs` test asserting default
  `page=1`, `pageSize=25` behavior, correct `totalFiltered` when filters
  narrow the set, and that an out-of-range `page` (e.g. past the last
  page) returns an empty `jobs` array rather than erroring.
- **Frontend**: no existing test harness for this page (consistent with
  the prior JD-input-fix spec) — verified manually via
  `webapp-testing`/browser check: page-state round-trips through the URL,
  Prev/Next disable correctly at bounds, filter changes reset to page 1,
  and the light/dark theme renders correctly in both `prefers-color-scheme`
  states.
