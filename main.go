package main

import (
	"log"
	"net/http"
	"net/http/httputil"
	"net/url"
)

type RateLimiter interface {
	Allow(clientID string) bool
}

var temp RateLimiter = (*MockLimiter)(nil)

type MockLimiter struct{}	


func startBackendService() {
	mux := http.NewServeMux()
	mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		w.Write([]byte("Hello from the Backend Service!\n"))
	})

	log.Println("Dummy backend running on http://localhost:8081")
	log.Fatal(http.ListenAndServe(":8081", mux))
}

func startGateway(targetURL string) {
	// Parse the target URL ( backend)
	backendURL, err := url.Parse(targetURL)
	if err != nil {
		log.Fatal("Invalid backend URL:", err)
	}

	// Go's built-in reverse proxy does all the heavy lifting
	proxy := httputil.NewSingleHostReverseProxy(backendURL)

	gatewayMux := http.NewServeMux()

	// This handler is our gateway's front door. 
	// Right now it blindly proxies. In Step 2, we will add rate limiting here.
	gatewayMux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		log.Printf("[Gateway] Received request: %s %s -> Forwarding", r.Method, r.URL.Path)
		proxy.ServeHTTP(w, r)
	})

	log.Println("API Gateway running on http://localhost:8082")
	log.Fatal(http.ListenAndServe(":8082", gatewayMux))
}

func main() {
	// Start our dummy backend in a separate goroutine so it runs concurrently
	go startBackendService()

	// Start the Gateway on the main thread
	startGateway("http://localhost:8081")
}