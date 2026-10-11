# Browser regressions

`bootstrap.js` is a dependency-free browser-console regression against the running app.

1. Build and start the server with a disposable database.
2. Open `/holdings?currency=TRY`, wait for page load, and execute `bootstrap.js` in the browser console.
3. Repeat on `/holdings?currency=USD`.
4. Expect a result with `themeTogglesOnce: true`, `themeRestored: true`, and the requested currency. Assertion failures throw errors.

Disable the browser cache or hard-reload after changing `app.js` so the test exercises the current asset. The regression toggles the theme twice and restores its original value. It does not modify portfolio data.

The Go HTTP rendering tests separately ensure each page contains only the automatic `app.js` bootstrap. Browser regressions are not currently run by `go test` or CI.
