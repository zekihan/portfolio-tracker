package price

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

func TestFundProvidersShareMetadata(t *testing.T) {
	for _, test := range []struct {
		getter Getter
		name   string
	}{
		{name: "Pusula", getter: NewPusulaPortfoy()},
		{name: "Garanti", getter: NewGarantiBBVA()},
	} {
		t.Run(test.name, func(t *testing.T) {
			var funds, dates, pages, quotes atomic.Int32

			server := httptest.NewServer(metadataTestHandler(t, &funds, &dates, &pages, &quotes))
			t.Cleanup(server.Close)

			target, err := url.Parse(server.URL)
			if err != nil {
				t.Fatal(err)
			}

			providerTestClient(test.getter).Transport = &localProviderTransport{target: target}

			var workers sync.WaitGroup

			for range 10 {
				for _, code := range []string{"AAA", "BBB"} {
					workers.Go(func() { checkMetadataQuote(t, test.getter, code, test.name) })
				}
			}

			workers.Wait()

			checkFundMetadataTTL(t, test.getter)

			if test.name == "Pusula" && funds.Load() != 1 {
				t.Errorf("fund requests = %d", funds.Load())
			}

			if test.name == "Garanti" &&
				(dates.Load() != 1 || pages.Load() != 1 || quotes.Load() != 20) {
				t.Errorf(
					"date/page/quote requests = %d/%d/%d",
					dates.Load(),
					pages.Load(),
					quotes.Load(),
				)
			}
		})
	}
}

func TestSharedMetadataExpiryAndFailure(t *testing.T) {
	var cache sharedMetadata[string]

	var calls int

	fetch := func(context.Context) (string, error) {
		calls++

		return "value", nil
	}
	for range 2 {
		value, err := cache.get(t.Context(), time.Hour, fetch)
		if err != nil || value != "value" {
			t.Fatalf("get = %q, %v", value, err)
		}
	}

	if calls != 1 {
		t.Fatalf("calls = %d", calls)
	}

	cache.mu.Lock()
	cache.expires = time.Now().Add(-time.Second)
	cache.mu.Unlock()

	failure := errMalformedProviderResponse

	for range 2 {
		_, err := cache.get(
			t.Context(),
			time.Hour,

			func(context.Context) (string, error) {
				calls++

				return "", failure
			},
		)
		if !errors.Is(err, failure) {
			t.Fatalf("error = %v", err)
		}
	}

	_, err := cache.get(t.Context(), time.Hour, fetch)
	if err != nil {
		t.Fatal(err)
	}

	if calls != 4 {
		t.Fatalf("calls = %d", calls)
	}
}

func TestSharedMetadataCancellation(t *testing.T) {
	var cache sharedMetadata[string]

	started := make(chan struct{})
	canceled := make(chan struct{})
	ctx, cancel := context.WithCancel(t.Context())
	result := make(chan error, 1)

	go func() {
		_, err := cache.get(ctx, time.Hour, func(ctx context.Context) (string, error) {
			close(started)
			<-ctx.Done()
			close(canceled)

			return "", ctx.Err()
		})
		result <- err
	}()

	<-started
	cancel()

	err := <-result
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("error = %v", err)
	}

	select {
	case <-canceled:
	case <-time.After(time.Second):
		t.Fatal("request not canceled")
	}

	value, err := cache.get(
		t.Context(),
		time.Hour,
		func(context.Context) (string, error) { return "retry", nil },
	)
	if err != nil || value != "retry" {
		t.Fatalf("retry = %q, %v", value, err)
	}

	canceledCtx, stop := context.WithCancel(t.Context())
	stop()

	_, err = cache.get(
		canceledCtx,
		time.Hour,

		func(context.Context) (string, error) {
			t.Error("unexpected fetch")

			return "", nil
		},
	)
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("cached cancellation error = %v", err)
	}
}

func TestSharedMetadataCanceledLeaderDoesNotCancelFollower(t *testing.T) {
	var cache sharedMetadata[string]

	started, release := make(chan struct{}), make(chan struct{})
	ctx, cancel := context.WithCancel(t.Context())
	leader := make(chan error, 1)

	fetch := func(ctx context.Context) (string, error) {
		close(started)

		select {
		case <-release:
			return "shared", nil
		case <-ctx.Done():
			return "", ctx.Err()
		}
	}
	go func() { _, err := cache.get(ctx, time.Hour, fetch); leader <- err }()

	<-started

	follower := make(chan error, 1)

	go func() {
		value, err := cache.get(t.Context(), time.Hour, fetch)
		if err == nil && value != "shared" {
			t.Errorf("value = %q", value)
		}

		follower <- err
	}()

	deadline := time.NewTimer(time.Second)
	defer deadline.Stop()

	ticker := time.NewTicker(time.Millisecond)
	defer ticker.Stop()

	for {
		cache.mu.Lock()
		joined := cache.pending != nil && cache.pending.waiters == 2
		cache.mu.Unlock()

		if joined {
			break
		}

		select {
		case <-deadline.C:
			t.Fatal("follower did not join")
		case <-ticker.C:
		}
	}

	cancel()

	err := <-leader
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("leader error = %v", err)
	}

	close(release)

	err = <-follower
	if err != nil {
		t.Fatal(err)
	}
}

func metadataTestHandler(t *testing.T, funds, dates, pages, quotes *atomic.Int32) http.HandlerFunc {
	t.Helper()

	return func(w http.ResponseWriter, request *http.Request) {
		switch request.URL.Path {
		case "/api/funds":
			funds.Add(1)
			fmt.Fprint(
				w,
				`{"items":[{"abbreviation":"AAA","fundPrice":"12"},{"abbreviation":"BBB","fundPrice":"23"}]}`,
			)
		case "/webservice/lastdate":
			dates.Add(1)
			fmt.Fprint(w, `"2026-10-12"`)
		case "/fon-getirileri":
			pages.Add(1)
			fmt.Fprint(
				w,
				`<option data-code="AAA" data-currency="TRY"></option><option data-code="BBB" data-currency="USD"></option>`,
			)
		case "/webservice/funddailyvalues":
			quotes.Add(1)

			var payload struct {
				Code string `json:"code"`
			}

			err := json.NewDecoder(request.Body).Decode(&payload)
			if err != nil {
				t.Error(err)

				return
			}

			value := 12
			if payload.Code == "BBB" {
				value = 23
			}

			fmt.Fprintf(w, `{"data":{"Close":%d}}`, value)
		default:
			t.Errorf("unexpected path %s", request.URL.Path)
		}
	}
}

func checkMetadataQuote(t *testing.T, getter Getter, code, provider string) {
	t.Helper()

	quote, err := getter.GetPrice(t.Context(), code)
	if err != nil {
		t.Error(err)

		return
	}

	value, currency := 12.0, "TRY"
	if code == "BBB" {
		value = 23

		if provider == "Garanti" {
			currency = "USD"
		}
	}

	if quote.Value != value || quote.Currency != currency {
		t.Errorf("%s quote = %+v", code, quote)
	}
}

func TestSharedMetadataPanicAllowsRetry(t *testing.T) {
	var cache sharedMetadata[string]

	_, err := cache.get(t.Context(), time.Hour, func(context.Context) (string, error) {
		panic("provider bug")
	})
	if !errors.Is(err, errMetadataFetchPanic) {
		t.Fatalf("error = %v", err)
	}

	value, err := cache.get(
		t.Context(),
		time.Hour,
		func(context.Context) (string, error) { return "recovered", nil },
	)
	if err != nil || value != "recovered" {
		t.Fatalf("retry = %q, %v", value, err)
	}
}

func TestFundMetadataRejectsMalformedResponsesWithoutCaching(t *testing.T) {
	for _, test := range []struct {
		getter Getter
		name   string
		path   string
		body   string
	}{
		{getter: NewPusulaPortfoy(), name: "Pusula", path: "/api/funds", body: `{}`},
		{getter: NewGarantiBBVA(), name: "Garanti date", path: "/webservice/lastdate", body: `"invalid date"`},
		{getter: NewGarantiBBVA(), name: "Garanti page", path: "/fon-getirileri", body: `<html>unavailable</html>`},
	} {
		t.Run(test.name, func(t *testing.T) {
			var calls, funds, dates, pages, quotes atomic.Int32

			valid := metadataTestHandler(t, &funds, &dates, &pages, &quotes)
			server := httptest.NewServer(
				http.HandlerFunc(func(w http.ResponseWriter, request *http.Request) {
					if request.URL.Path == test.path && calls.Add(1) == 1 {
						fmt.Fprint(w, test.body)

						return
					}

					valid(w, request)
				}),
			)
			t.Cleanup(server.Close)

			target, err := url.Parse(server.URL)
			if err != nil {
				t.Fatal(err)
			}

			providerTestClient(test.getter).Transport = &localProviderTransport{target: target}

			_, err = test.getter.GetPrice(t.Context(), "AAA")
			if err == nil {
				t.Fatal("malformed metadata was accepted")
			}

			quote, err := test.getter.GetPrice(t.Context(), "AAA")
			if err != nil || quote.Value != 12 {
				t.Fatalf("retry = %+v, %v", quote, err)
			}
		})
	}
}

func TestGarantiCurrencyDoesNotMatchUnrelatedFund(t *testing.T) {
	body := []byte(`<option data-code="AAA" data-currency="TRY"></option>`)
	for _, code := range []string{"BBB", ""} {
		_, err := garantiCurrency(body, code)
		if err == nil {
			t.Errorf("unrelated code %q matched metadata", code)
		}
	}
}
