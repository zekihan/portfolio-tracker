# Browser regressions

`bootstrap.js` is a dependency-free browser-console regression against the running app.

1. Build and start the server with a disposable database.
2. Open `/holdings?currency=TRY`, wait for page load, and execute `bootstrap.js` in the browser console.
3. Repeat on `/holdings?currency=USD` and `/holdings?currency=EUR`.
4. Expect a result with `themeTogglesOnce: true`, `themeRestored: true`, and the requested currency. Assertion failures throw errors.

Disable the browser cache or hard-reload after changing `app.js` so the test exercises the current asset. The regression toggles the theme twice and restores its original value. It does not modify portfolio data.

`dashboard.js` runs on `/` and verifies that the obsolete unexported initialization call is absent and both charts initialize through the normal bootstrap within five seconds.

`allocation-currency.js` compares the refreshed Asset Type chart total with the real API's selected-currency total. Run it on `/?currency=TRY`, `/?currency=USD`, and `/?currency=EUR` with a nonzero portfolio and valid cached FX. The test changes the selected allocation view to Asset Type, without mutating holdings.

`allocation-views.js` exercises the public chart loader for Asset Type, Basket, Location, and Currency and compares each chart total with the corresponding API allocation. Run in TRY, USD, and EUR using nonzero holdings with basket and location fields populated. It tests chart data loading/rendering, not tab click handling.

`allocation-tabs.js` clicks each actual allocation button in TRY, USD, and EUR, verifies the chart redraw and totals, and checks active/ARIA selection and absence of JSON in the canvas. Use a populated fixture with basket and location values.

`portfolio-tabs.js` clicks every portfolio time-range tab in TRY, USD, and EUR, comparing point values and labels with the history API and checking active/ARIA selection. Seed snapshots 15, 60, 120, and 240 days ago so every range has a different point count. It rejects fixtures that cannot distinguish the four ranges.

`chart-refresh-selection.js` runs on a freshly loaded dashboard. It dispatches the real `portfolio-updated` event, verifies default views remain selected, then clicks non-default tabs and requires a subsequent event to redraw both charts without resetting the selections. Run in TRY, USD, and EUR.

`dropdown-swaps.js` runs on a fresh holdings page containing a holding. It verifies toggle behavior before and after three dispatched `htmx:afterSwap` events, then verifies delegated handling of a newly inserted dropdown and outside-click closure. It does not change persisted holdings.

`table-sort-swaps.js` checks the ascending/descending/none direction control cycle before and after three real HTMX GET swaps, including a newly inserted table. Run on a fresh holdings page. This regression covers direction state and indicators, not row ordering; it performs no persisted writes.

`table-order.js` verifies actual ascending/descending ordering for names, tickers, quantities, current prices, and estimates, then restores the original order on the third click. It uses temporary cloned rows with localized numeric display text and price status labels to require raw-value sorting. Run on a populated holdings page; the clone is removed afterward and no persistence is changed.

`inline-text.js` opens basket, location, and notes editors on a populated holdings page, verifies spaces, commas, and currency symbols survive, and checks Escape restores the text. It uses DOM-only fixtures, cancels without saving, and restores the original cells.

`inline-rejection.js` verifies that an empty-name PATCH rejected by the real API restores the original display and leaves the holding unchanged on re-read. Run only against a disposable populated fixture.

`inline-save.js` saves all seven editable fields through the real UI and API, checks unrelated fields remain unchanged, exercises empty text and numeric zero, and restores the fixture through PATCH in a finally block. Run ONLY against a disposable populated holdings database; it performs real writes.

Inline saves use `PATCH /api/holdings/:id` for name, ticker, basket, location, notes, quantity, and est_price. Omitted/null fields remain unchanged; empty text clears optional fields, but an empty name is rejected. Numeric values must be nonnegative. A successful update returns HTTP 204, a missing holding HTTP 404, and an empty or invalid patch HTTP 400. Full edit modals also use PATCH, submitting only fields changed since the form loaded; asset_type, price_provider, instrument_id, and base_currency are supported with the same validation as full updates. Unchanged modal saves issue no request. The existing full-record PUT contract is unchanged.

`inline-lifecycle.js` checks that Enter followed by blur makes exactly one real PATCH request and Escape followed by blur makes none. It re-reads persistence and restores fixture notes in a finally block. Run ONLY on a disposable populated holdings database.

`currency-switcher.js` runs in two phases on a disposable fixture: first run clicks the next currency (TRY → USD → EUR → TRY); after navigation, run it again to verify persisted settings, page currency, and page path, then restore the original setting. Repeat from each currency to test all three transitions. If verification fails, the second run still restores the setting in a finally block. It performs real settings writes.

`pull-refresh.js` dispatches real touch events on a fresh page scrolled to the top. It requires one refresh POST for a deliberate pull and none for an initial tap, three subsequent taps, or a short pull. The refresh request is stubbed with a failed response to prevent navigation and provider requests; an error toast is expected. It does not simulate physical device gestures or change persisted holdings.

`pull-cancel.js` dispatches touchstart/move/cancel and requires the visual offset to reset with no refresh, even if touchend follows. It also verifies a subsequent deliberate pull still works. Run on a fresh page scrolled to the top; the refresh POST is stubbed to prevent writes and navigation. It exercises handlers rather than physical device gestures.

The Go HTTP rendering tests separately ensure each page contains only the automatic `app.js` bootstrap. Browser regressions are not currently run by `go test` or CI.
