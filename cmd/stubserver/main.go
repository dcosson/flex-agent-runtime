package main

import (
	"flag"
	"fmt"
	"log"
	"net/http"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/anthropics/flex-agent-runtime/internal/ai/testutil/stubserver"
)

func main() {
	addr := flag.String("addr", ":9090", "listen address")
	fixtureDir := flag.String("fixtures", os.Getenv("STUBSERVER_FIXTURE_DIR"), "fixture directory (default: $STUBSERVER_FIXTURE_DIR or ./testdata/fixtures)")
	flag.Parse()

	if *fixtureDir == "" {
		*fixtureDir = "./testdata/fixtures"
	}

	fixtures, err := loadFixtures(*fixtureDir)
	if err != nil {
		log.Fatalf("load fixtures: %v", err)
	}

	sseHandler := stubserver.NewHandler(stubserver.WithFixtureFunc(func(r *http.Request) string {
		name := strings.TrimSpace(r.Header.Get("X-Fixture"))
		if name == "" {
			name = "default"
		}
		if fixture, ok := fixtures[name]; ok {
			return fixture
		}
		return fixtures["default"]
	}))

	jsonHandler := stubserver.NewHandler(stubserver.WithJSONFixtureFunc(func(r *http.Request) string {
		name := strings.TrimSpace(r.Header.Get("X-Fixture"))
		if name == "" {
			// Default JSON fixture based on path.
			switch {
			case strings.Contains(r.URL.Path, "batchEmbedContents"):
				name = "google-embedding"
			case strings.Contains(r.URL.Path, "/embed"):
				name = "cohere-embedding"
			default:
				name = "openai-embedding"
			}
		}
		if fixture, ok := fixtures[name]; ok {
			return fixture
		}
		return `{"error":"fixture not found"}`
	}))

	mux := http.NewServeMux()
	mux.HandleFunc("/health", func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		_, _ = fmt.Fprintln(w, "ok")
	})
	// Embedding endpoints serve JSON.
	mux.Handle("/embeddings", jsonHandler)
	mux.Handle("/v1/embeddings", jsonHandler)
	mux.Handle("/v2/embed", jsonHandler)
	// Google embedding uses dynamic paths — match with a handler func.
	mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		if strings.Contains(r.URL.Path, "batchEmbedContents") || strings.Contains(r.URL.Path, "/embed") {
			jsonHandler.ServeHTTP(w, r)
			return
		}
		sseHandler.ServeHTTP(w, r)
	})

	log.Printf("stubserver listening on %s with %d fixtures from %s", *addr, len(fixtures), *fixtureDir)
	log.Fatal(http.ListenAndServe(*addr, mux))
}

func loadFixtures(dir string) (map[string]string, error) {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil, err
	}

	fixtures := make(map[string]string, len(entries))
	ordered := make([]string, 0, len(entries))
	for _, entry := range entries {
		if entry.IsDir() {
			continue
		}
		path := filepath.Join(dir, entry.Name())
		data, err := os.ReadFile(path)
		if err != nil {
			return nil, err
		}
		name := entry.Name()
		content := string(data)
		fixtures[name] = content
		fixtures[strings.TrimSuffix(name, filepath.Ext(name))] = content
		ordered = append(ordered, name)
	}
	if len(fixtures) == 0 {
		return nil, fmt.Errorf("no fixture files found in %s", dir)
	}
	if _, ok := fixtures["default"]; !ok {
		sort.Strings(ordered)
		first := ordered[0]
		fixtures["default"] = fixtures[first]
	}

	return fixtures, nil
}
