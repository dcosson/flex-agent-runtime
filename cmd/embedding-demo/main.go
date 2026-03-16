package main

import (
	"bufio"
	"context"
	"errors"
	"flag"
	"fmt"
	"os"
	"strings"
	"time"

	"h2-agent-runtime/internal/ai"
	"h2-agent-runtime/internal/ai/provider/cohere"
	"h2-agent-runtime/internal/ai/provider/google"
	"h2-agent-runtime/internal/ai/provider/openai"
)

type stringList []string

func (s *stringList) String() string { return strings.Join(*s, ",") }
func (s *stringList) Set(v string) error {
	v = strings.TrimSpace(v)
	if v == "" {
		return nil
	}
	*s = append(*s, v)
	return nil
}

func main() {
	registerEmbeddingProviders()

	var texts stringList
	query := ""
	modelID := envOrDefault("EMBEDDING_MODEL", "text-embedding-3-small")
	timeout := flag.Duration("timeout", 60*time.Second, "request timeout")
	flag.Var(&texts, "text", "document text (repeatable)")
	flag.StringVar(&query, "query", "", "query text")
	flag.StringVar(&modelID, "model", modelID, "embedding model ID")
	flag.Parse()

	if len(texts) == 0 {
		stdinTexts, err := readTextsFromStdinIfPiped()
		if err != nil {
			fatalf("read stdin: %v", err)
		}
		texts = append(texts, stdinTexts...)
	}

	if strings.TrimSpace(query) == "" {
		fatalf("-query is required")
	}
	if len(texts) == 0 {
		fatalf("provide at least one -text or pipe newline-delimited texts on stdin")
	}

	ctx, cancel := context.WithTimeout(context.Background(), *timeout)
	defer cancel()

	resp, err := ai.Embed(ctx, modelID, ai.EmbeddingRequest{Texts: append(append([]string{}, []string(texts)...), query)})
	if err != nil {
		fatalf("embed: %v", err)
	}
	if len(resp.Embeddings) != len(texts)+1 {
		fatalf("unexpected embedding count: got %d want %d", len(resp.Embeddings), len(texts)+1)
	}

	docs := make([]embeddedText, 0, len(texts))
	for i, text := range texts {
		docs = append(docs, embeddedText{Text: text, Vector: resp.Embeddings[i].Values})
	}
	queryVector := resp.Embeddings[len(resp.Embeddings)-1].Values
	ranked := rankBySimilarity(queryVector, docs)

	fmt.Printf("embedding-demo model=%s docs=%d\n", resp.Model, len(texts))
	fmt.Printf("query> %s\n", query)
	for i, r := range ranked {
		fmt.Printf("%d. score=%0.6f text=%q\n", i+1, r.Score, r.Text)
	}
}

func registerEmbeddingProviders() {
	openai.RegisterEmbedding(openai.Config{
		APIKey:  os.Getenv("OPENAI_API_KEY"),
		BaseURL: os.Getenv("OPENAI_BASE_URL"),
	}, "cmd-embedding-demo")

	google.RegisterEmbedding(google.Config{
		APIKey:  os.Getenv("GOOGLE_API_KEY"),
		BaseURL: os.Getenv("GOOGLE_BASE_URL"),
		Version: os.Getenv("GOOGLE_API_VERSION"),
	}, "cmd-embedding-demo")

	cohere.RegisterEmbedding(cohere.Config{
		APIKey:  os.Getenv("COHERE_API_KEY"),
		BaseURL: os.Getenv("COHERE_BASE_URL"),
	}, "cmd-embedding-demo")
}

func readTextsFromStdinIfPiped() ([]string, error) {
	fi, err := os.Stdin.Stat()
	if err != nil {
		return nil, err
	}
	if fi.Mode()&os.ModeCharDevice != 0 {
		return nil, nil
	}

	scanner := bufio.NewScanner(os.Stdin)
	out := make([]string, 0)
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if line != "" {
			out = append(out, line)
		}
	}
	if err := scanner.Err(); err != nil {
		return nil, err
	}
	if len(out) == 0 {
		return nil, errors.New("stdin is empty")
	}
	return out, nil
}

func envOrDefault(name, fallback string) string {
	v := strings.TrimSpace(os.Getenv(name))
	if v == "" {
		return fallback
	}
	return v
}

func fatalf(format string, args ...any) {
	fmt.Fprintf(os.Stderr, format+"\n", args...)
	os.Exit(1)
}
