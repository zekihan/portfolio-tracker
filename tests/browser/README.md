# Browser regressions

`bootstrap.js` is a dependency-free browser-console regression against the running app.

1. Build and start the server with a disposable database.
2. Open `/holdings?currency=TRY`, wait for page load, and execute `bootstrap.js` in the browser console.
3. Repeat on `/holdings?currency=USD`.
4. Expect a result with `themeTogglesOnce: true`, `themeRestored: true`, and the requested currency. Assertion failures throw errors.

Disable the browser cache or hard-reload after changing `app.js` so the test exercises the current asset. The regression toggles the theme twice and restores its original value. It does not modify portfolio data.

`dashboard.js` runs on `/` and verifies that the obsolete unexported initialization call is absent and both charts initialize through the normal bootstrap within five seconds.

`allocation-currency.js` compares the refreshed Asset Type chart total with the real API's selected-currency total. Run it on both `/?currency=TRY` and `/?currency=USD` with a nonzero portfolio and valid cached FX. The test changes the selected allocation view to Asset Type, without mutating holdings.

`allocation-views.js` exercises the public chart loader for Asset Type, Basket, Location, and Currency and compares each chart total with the corresponding API allocation. Run in TRY and USD using nonzero holdings with basket and location fields populated. It tests chart data loading/rendering, not tab click handling.

`allocation-tabs.js` clicks each actual allocation button in TRY and USD, verifies the chart redraw and totals, and checks active/ARIA selection and absence of JSON in the canvas. Use a populated fixture with basket and location values.

The Go HTTP rendering tests separately ensure each page contains only the automatic `app.js` bootstrap. Browser regressions are not currently run by `go test` or CI.
