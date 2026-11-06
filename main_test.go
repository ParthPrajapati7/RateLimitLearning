package main

import (
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"sync"
	"testing"
)

const backendBody = "Hello from the Backend Service!\n"

// --- Limiter tests ---------------------------------------------------------

func TestMockLimiterAlwaysAllows(t *testing.T) {
	limiter := &MockLimiter{}

	for _, client := range []string{"client-a", "client-b", ""} {
		for i := 0; i < 100; i++ {
			if !limiter.Allow(client) {
				t.Fatalf("MockLimiter denied request %d for client %q", i, client)
			}
		}
	}
}

// TestMockLimiterConcurrent hammers Allow from many goroutines.
// Run with `go test -race` — this becomes important once limiters hold state.
func TestMockLimiterConcurrent(t *testing.T) {
	limiter := &MockLimiter{}

	var wg sync.WaitGroup
	var mu sync.Mutex
	allowed := 0

	for i := 0; i < 50; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			if limiter.Allow("same-client") {
				mu.Lock()
				allowed++
				mu.Unlock()
			}
		}()
	}
	wg.Wait()

	if allowed != 50 {
		t.Fatalf("expected 50 allowed, got %d", allowed)
	}
}

// --- Backend tests ---------------------------------------------------------

func TestBackendHandler(t *testing.T) {
	req := httptest.NewRequest(http.MethodGet, "/", nil)
	rec := httptest.NewRecorder()

	newBackendHandler().ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected status 200, got %d", rec.Code)
	}
	if got := rec.Body.String(); got != backendBody {
		t.Fatalf("unexpected body: %q", got)
	}
}

// --- Gateway tests ---------------------------------------------------------

// newTestGateway spins up a real backend and a gateway pointing at it,
// both on random free ports. Servers are shut down when the test ends.
func newTestGateway(t *testing.T) *httptest.Server {
	t.Helper()

	backend := httptest.NewServer(newBackendHandler())
	t.Cleanup(backend.Close)

	backendURL, err := url.Parse(backend.URL)
	if err != nil {
		t.Fatalf("parse backend URL: %v", err)
	}

	gateway := httptest.NewServer(newGatewayHandler(backendURL, &MockLimiter{}))
	t.Cleanup(gateway.Close)
	return gateway
}

func TestGatewayProxiesToBackend(t *testing.T) {
	gateway := newTestGateway(t)

	resp, err := http.Get(gateway.URL + "/")
	if err != nil {
		t.Fatalf("request failed: %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		t.Fatalf("expected status 200, got %d", resp.StatusCode)
	}
	body, _ := io.ReadAll(resp.Body)
	if string(body) != backendBody {
		t.Fatalf("unexpected body: %q", body)
	}
}

func TestGatewayProxiesAnyPathAndMethod(t *testing.T) {
	gateway := newTestGateway(t)

	cases := []struct {
		method string
		path   string
	}{
		{http.MethodGet, "/"},
		{http.MethodGet, "/users/42"},
		{http.MethodPost, "/orders"},
		{http.MethodDelete, "/items/7"},
	}

	for _, tc := range cases {
		t.Run(tc.method+" "+tc.path, func(t *testing.T) {
			req, err := http.NewRequest(tc.method, gateway.URL+tc.path, nil)
			if err != nil {
				t.Fatalf("build request: %v", err)
			}
			resp, err := http.DefaultClient.Do(req)
			if err != nil {
				t.Fatalf("request failed: %v", err)
			}
			defer resp.Body.Close()

			if resp.StatusCode != http.StatusOK {
				t.Fatalf("expected status 200, got %d", resp.StatusCode)
			}
		})
	}
}

func TestGatewayReturnsBadGatewayWhenBackendDown(t *testing.T) {
	// Start and immediately stop a backend so its address refuses connections.
	backend := httptest.NewServer(newBackendHandler())
	backendURL, _ := url.Parse(backend.URL)
	backend.Close()

	gateway := httptest.NewServer(newGatewayHandler(backendURL, &MockLimiter{}))
	defer gateway.Close()

	resp, err := http.Get(gateway.URL + "/")
	if err != nil {
		t.Fatalf("request failed: %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusBadGateway {
		t.Fatalf("expected status 502, got %d", resp.StatusCode)
	}
}

func TestGatewayHandlesConcurrentRequests(t *testing.T) {
	gateway := newTestGateway(t)

	const n = 50
	var wg sync.WaitGroup
	errs := make(chan error, n)

	for i := 0; i < n; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			resp, err := http.Get(gateway.URL + "/")
			if err != nil {
				errs <- err
				return
			}
			io.Copy(io.Discard, resp.Body)
			resp.Body.Close()
			if resp.StatusCode != http.StatusOK {
				errs <- &statusError{resp.StatusCode}
			}
		}()
	}
	wg.Wait()
	close(errs)

	for err := range errs {
		t.Error(err)
	}
}

type statusError struct{ code int }

func (e *statusError) Error() string {
	return "unexpected status " + http.StatusText(e.code)
}
