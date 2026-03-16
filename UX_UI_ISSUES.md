# UX / UI Issues

Captured by browsing `http://localhost:4000` with the config at `~/.config/glitchgate/config.yaml`.
Issues are grouped by scope (cross-cutting first, then per-page) and tagged with rough severity.

**Severity key:** `[crit]` blocks use / accessibility · `[high]` significant friction · `[med]` polish / consistency · `[low]` minor

---

## Cross-cutting issues (all pages)

### Navigation & header

- `[high]` Active nav item is only indicated by bold + color — no underline, background, or `aria-current` attribute. Hard to tell current location at a glance.
- `[med]` `"admin"` label appears as plain inline text between nav links and the Logout button. It reads as a stray label, not an account control. Should be a user menu or at minimum clearly grouped and labeled.
- `[med]` "Logout" is an outlined pill button while every nav item is a plain link — inconsistent control hierarchy. Logout action should be visually distinct (destructive affordance) but not styled differently from primary navigation for no reason.
- `[low]` `"glitchgate"` brand text is very small relative to nav items.

### Accessibility

- `[crit]` No visible keyboard focus outlines on interactive elements (inputs, buttons, links, table rows). Keyboard and assistive-tech users cannot track focus.
- `[high]` Color contrast for many secondary elements (light-gray labels, placeholder text, axis dates, muted badges, table separators) likely fails WCAG AA (4.5:1 for normal text, 3:1 for large text).
- `[high]` Color is the only state indicator in several places (red for "bad", blue for "active", green/red for status). Not accessible to color-blind users — needs redundant text or icon cues.
- `[med]` Small tap targets (chevron toggles, outlined delete buttons, nav items) are below the recommended 44 × 44 px minimum for touch/pointer users.

### Typography & number formatting

- `[med]` Currency values are formatted inconsistently across pages: some show 2 decimal places (`$708.08`), others show 4 (`$178.8130`), with no thousands separators. Use 2 decimal places and `$1,234.56` formatting consistently.
- `[med]` Em-dash `—` is used for missing/null values throughout. Screen readers may announce it as "dash." Use `N/A` with an `aria-label` or a `title` tooltip explaining why the value is absent.
- `[low]` Capitalization is inconsistent: nav items are Title Case, some column headers are ALL CAPS, some are Sentence case, badge labels vary.

### Data context

- `[high]` No timeframe label on aggregate numbers. Users see `$708.08 TOTAL COST` but don't know if that's today, 30 days, or all time. Add a visible time-range label to every stat.
- `[med]` No timezone label on timestamps at page/column-header level. Rows show `ET` but the header or filter field does not, and there's no user preference.

---

## Dashboard (`/`)

- `[high]` "Provider Subsidy" shown in **red** — red conventionally signals an error or deficit. If this is a positive value (money saved), use a neutral or green color. If it can be negative, label it clearly.
- `[high]` Bar chart has no y-axis, no gridlines, and no scale. Bars are only interpretable via the tiny labels above them. Add an axis, gridlines, and hover tooltips.
- `[high]` No time-range control. The chart shows recent days but there is no way to change the window.
- `[med]` `"$/MTOK"` abbreviation is not universally understood. Add a hover tooltip or inline "(per million tokens)".
- `[med]` "Top Models" list has no column headers — the dollar column is ambiguous without a label.
- `[med]` `"Errors 2 (0.4%)"` uses red color only with no icon or text label — inaccessible to color-blind users.
- `[med]` "Budget Status" section shows only a `"Manage budgets"` link with no budget data visible inline — low discoverability and low utility on the dashboard.
- `[low]` `"View full cost analysis →"` and `"View all →"` links are placed inconsistently (differing margins/alignments) relative to their sections.

---

## Request Logs (`/ui/logs`)

- `[high]` No "Clear filters" / "Reset" button. Once filters are applied there is no obvious way to return to the unfiltered view.
- `[high]` Status column shows raw HTTP codes (`200`) as plain text. Non-200 statuses blend in — should use color + text badge (e.g. green `200 OK`, red `429 Rate limited`).
- `[high]` Full key prefixes are visible in plaintext with no masking, copy affordance, or privacy hint. Consider truncating with click-to-reveal or copy-to-clipboard.
- `[med]` Auto-refresh indicator (`"Auto-refreshing (10s) Pause"`) looks like static text, not an interactive control. Needs a toggle button with clear on/off state.
- `[med]` Date inputs have no calendar picker icon, no format enforcement, and no timezone context. Placeholder `mm/dd/yyyy` conflicts with the `ET` timestamp format shown in the table.
- `[med]` `"Key Prefix"` filter label is vague — is it an exact match, prefix, or substring search? Add helper text.
- `[med]` "SSE" badge in the Model column lacks a tooltip. Users unfamiliar with server-sent events won't know what it means.
- `[med]` Numeric columns (In Tokens, Out Tokens, Latency, Cost) are left-aligned. Numbers should be right-aligned for fast comparison.
- `[med]` `"View"` row action is a small plain-text link with no icon — unclear whether it opens a modal, new page, or side panel.
- `[low]` No export affordance (CSV / JSON). Common expectation for a log table.
- `[low]` Pagination shows only `"Page 1 of 223 Next"` — no jump-to-page, no rows-per-page control, no first/last links.

---

## Costs (`/ui/costs`)

- `[high]` "Group By" and "Model" filters are side-by-side with no explanation of how they interact. The relationship between grouping dimension and the model filter is unclear.
- `[high]` Chart bars and the inline token-bars in the table have no scale or axis — they provide shape information only, not magnitude.
- `[med]` `"Update"` button has low visual weight relative to its importance. It should be a clearly primary action. There is no indicator that filters are "dirty" (changed but not applied).
- `[med]` Model name prefixes (`cm/`, `s/`, `a/`) are opaque jargon without a legend or tooltip.
- `[med]` "Token Details" accordion strip looks like a disabled area (faded background + faint chevron) — affordance that it is interactive is very weak.
- `[low]` No table footer totals to cross-check the displayed `TOTAL COST` aggregate.

---

## Budgets (`/ui/budgets`)

- `[high]` Empty state message `"No budgets configured yet."` offers no CTA or explanation. The Create form is physically above it, but the message should include a direct link/button.
- `[high]` No success, error, or loading feedback after form submission — users don't know if the action worked.
- `[high]` No confirmation or explanation of what a budget actually enforces (hard block vs. alert?). Users may create budgets without understanding the consequences.
- `[med]` `"Scope"` and `"Period"` have no help text or tooltips. `"Global"` is the only visible option — users don't know what other scopes exist.
- `[med]` `"Limit ($)"` input has no placeholder, step hint, or example value. Currency is hard-coded — no indication of what currency.
- `[med]` `"Create"` button generic label. Use `"Create Budget"` for clarity.
- `[med]` Form layout: three controls in a wide horizontal row with the button far right. Controls are visually far apart; a vertical stacked form with the button below the last field would be more accessible and scannable.
- `[low]` No cancel / reset option on the form.

---

## Providers (`/ui/providers`)

- `[high]` Provider Subsidy value displayed in red — same issue as Dashboard (color semantics are backwards for a positive savings figure).
- `[high]` "Cumulative Provider Subsidy" chart: bars are labeled individually but it's unclear whether each bar is a cumulative-to-date value or a daily delta. The title says "Cumulative" but each bar has its own dollar amount. Needs an explicit axis label.
- `[high]` Chart has no y-axis, scale, gridlines, or hover tooltips.
- `[med]` `"Token vs Subscription +431.2%"` — percentage with no baseline explanation, and no indication whether higher is good or bad.
- `[med]` Provider type names (`openai_responses`, `anthropic`) are raw internal identifiers. Consider user-friendly display names with the technical ID in a tooltip.
- `[med]` `"$/MTOK"` abbreviation used again without explanation (same as Dashboard).
- `[med]` Numeric columns in the breakdown table are left-aligned — should be right-aligned.
- `[low]` "Rate Breakdown" collapsible strip: chevron points right with no visible expanded/collapsed state — users won't know whether to click it or what it will do.

---

## Models (`/ui/models`)

- `[high]` Expand/collapse affordance is a small triangle with an ambiguous click target — unclear whether clicking the triangle, the model name link, or the row itself triggers expansion vs. navigation.
- `[med]` `"anthropic (anthropic)"` — provider name is duplicated redundantly in the Provider column.
- `[med]` Wildcard patterns (`a/*`, `c/*`) are unexplained jargon — no tooltip or legend.
- `[med]` `"1 fallback"` vs `"3 fallbacks"` labeling is grammatically inconsistent; pluralization is correct but style varies. Consider a consistent format like `"Fallbacks: 3"`.
- `[med]` `"Pricing $/MTok"` column abbreviation unexplained (same MTok issue as other pages).
- `[med]` Total Spend column has no time-frame label — is it all-time, 30-day, billing period?
- `[low]` No inline row actions (edit, disable, delete). No indication of how to manage a model entry.
- `[low]` No pagination or row count indicator if the model list grows.

---

## Keys (`/ui/keys`)

- `[crit]` No confirmation dialog before deleting a key — deletion is immediate and irreversible. Add a confirmation step.
- `[high]` No feedback that a newly created key is shown only once. If the key secret is shown post-creation, users need an explicit warning and a copy button.
- `[high]` No copy-to-clipboard affordance for key prefix values.
- `[med]` `"New Key Label"` label is ambiguous — it reads as if it's labeling the input field rather than describing what to type. Use `"Key label"` with a placeholder like `"e.g. Production Claude Code"`.
- `[med]` `"Delete"` button styling (thin outline) does not visually communicate a destructive action. Destructive actions should be styled distinctly (e.g. red button) to set the right expectation.
- `[med]` Column header `"Prefix"` is unclear — `"Key Prefix / ID"` or a tooltip would help.
- `[med]` `"Created"` column header lacks timezone note; rows show `ET` but header does not.
- `[low]` No search, filter, or sort on the keys list.
- `[low]` No pagination for long key lists.

---

## Audit Log (`/ui/audit`)

- `[high]` No `"Actor / User"` column — an audit log that doesn't show who performed the action is missing its most critical field.
- `[high]` No free-text search (by GUID, key name, team, etc.). Only date range + action type are filterable.
- `[med]` Action values are raw internal identifiers (`master_key.login`, `key_created`). Display user-friendly labels with the internal code in a tooltip or secondary line.
- `[med]` `"Detail"` column contains heterogeneous content (limits, GUIDs, descriptions) in a plain-text dump. Should format by type: links for GUIDs, badges for status values, code-formatted strings for keys.
- `[med]` No row expansion or drill-down to see structured audit detail.
- `[med]` No export (CSV / JSON) for compliance use cases.
- `[med]` Date format in the filter placeholders (`mm/dd/yyyy`) doesn't match the table display format (`Mar 16 1:10 AM`).
- `[low]` No pagination controls or total entry count shown.
- `[low]` No "last updated" or live/static data indicator.

---

## Users (`/ui/users`)

- `[high]` Empty state `"No users found."` with no invite/add button, no explanation, no next steps. A global admin seeing this page should be able to immediately act.
- `[high]` No indication of whether the empty state is due to a permissions issue or genuinely no users. These require different actions.
- `[med]` No column headers, search, filter, or sort affordances are hinted even in empty state — users have no mental model of what the table will contain.
- `[low]` Same nav/header issues as all other pages.

---

## Teams (`/ui/teams`)

- `[high]` Same empty state problem as Users: `"No teams yet. Create one above."` is positioned far from the form and provides no in-context help.
- `[med]` Placeholders are used as labels (`"e.g. Engineering"`, `"Short description"`). When the user types, the label disappears — fails accessibility best practices. Use persistent `<label>` elements above the inputs.
- `[med]` `"Create Team"` button is enabled with empty inputs and gives no validation feedback on submit.
- `[med]` `"Team Name"` is not marked as required (no asterisk or `aria-required`).
- `[med]` No explanation of what a Team is or how it relates to Keys and Budgets.
- `[low]` No success/failure feedback after form submission.

---

## Recommended priority order

| Priority | Area | Issues to fix first |
|----------|------|---------------------|
| 1 | **Accessibility** | Focus outlines, contrast ratios, color-only indicators, placeholder-as-label pattern |
| 2 | **Destructive actions** | Confirm-before-delete on Keys; feedback on all form submissions |
| 3 | **Audit log actor** | Add "Who" column — core audit log requirement |
| 4 | **Number formatting** | Consistent 2-decimal currency, right-aligned numeric columns, timeframe labels on all aggregates |
| 5 | **Charts** | Add y-axis / scale / hover tooltips to all bar charts; clarify cumulative vs. daily |
| 6 | **Empty states** | Add actionable CTAs to Users, Teams, Budgets empty states |
| 7 | **Filter UX** | Reset/clear button on all filter bars; dirty-state indicator on "Update" button |
| 8 | **Navigation** | Strong active-state indicator; fix `"admin"` affordance; keyboard focus ring |
