// Command mock runs the Hyperlift mock API as a standalone HTTP server. It uses
// the same contract-accurate http.Handler as the test suite, so the CLI can run
// end to end against a local endpoint:
//
//	terminal 1: make mock
//	terminal 2: HYPERLIFT_BASE_URL=http://localhost:8080 \
//	            HYPERLIFT_API_KEY=demo HYPERLIFT_API_SECRET=demo \
//	            ./bin/hyperlift apps get app_a1b2c3
package main

import (
	"flag"
	"fmt"
	"net/http"
	"os"
	"strings"
	"time"

	"github.com/nccloud/hyperlift-cli/internal/testapi"
)

// EnvAddr replaces the default listen address when -addr is absent.
const EnvAddr = "HYPERLIFT_MOCK_ADDR"

const defaultAddr = ":8080"

func main() {
	os.Exit(run())
}

func run() int {
	addr := flag.String("addr", "", "listen address (default \""+defaultAddr+"\", or $"+EnvAddr+")")

	flag.Parse()

	listenAddr := resolveAddr(*addr)

	srv := &http.Server{
		Addr:              listenAddr,
		Handler:           testapi.Server(),
		ReadHeaderTimeout: 10 * time.Second,
	}

	fmt.Printf("hyperlift mock API listening on http://localhost%s\n", displayHost(listenAddr))
	fmt.Printf("seeded application ids: app_a1b2c3, app_d4e5f6\n")
	fmt.Printf("example: HYPERLIFT_BASE_URL=http://localhost%s HYPERLIFT_API_KEY=demo HYPERLIFT_API_SECRET=demo ./bin/hyperlift apps get app_a1b2c3\n", displayHost(listenAddr))

	if err := srv.ListenAndServe(); err != nil {
		fmt.Fprintf(os.Stderr, "mock server: %v\n", err)
		return 1
	}

	return 0
}

// resolveAddr picks the listen address. The -addr flag wins, then
// $HYPERLIFT_MOCK_ADDR, then the default.
func resolveAddr(flagAddr string) string {
	if flagAddr != "" {
		return flagAddr
	}

	if v := os.Getenv(EnvAddr); v != "" {
		return v
	}

	return defaultAddr
}

// displayHost turns a listen address into the suffix to show after
// "http://localhost". For example ":8080" stays ":8080", and "0.0.0.0:9000"
// becomes ":9000".
func displayHost(addr string) string {
	if i := strings.LastIndexByte(addr, ':'); i >= 0 {
		return addr[i:]
	}

	return ":" + addr
}
