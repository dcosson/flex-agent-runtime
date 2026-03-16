package main

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"os"
	"sort"
	"strings"
	"time"

	"github.com/spf13/cobra"

	"h2-agent-runtime/internal/ai"
	"h2-agent-runtime/internal/ai/provider/cohere"
	"h2-agent-runtime/internal/ai/provider/google"
	"h2-agent-runtime/internal/ai/provider/openai"
)

const (
	embedOpenAI = "openai"
	embedGoogle = "google"
	embedCohere = "cohere"
)

type stringList []string

func (s *stringList) String() string { return strings.Join(*s, ",") }
func (s *stringList) Type() string   { return "stringSlice" }
func (s *stringList) Set(v string) error {
	v = strings.TrimSpace(v)
	if v == "" {
		return nil
	}
	*s = append(*s, v)
	return nil
}

func main() {
	if err := newRootCmd().Execute(); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}

func newRootCmd() *cobra.Command {
	root := &cobra.Command{
		Use:   "embedding-demo",
		Short: "Embed documents and rank by query similarity",
		Long: strings.Join([]string{
			"Embed documents and rank by query similarity.",
			"",
			"API key env vars:",
			"  OPENAI_API_KEY",
			"  GOOGLE_API_KEY",
			"  COHERE_API_KEY",
		}, "\n"),
	}
	root.SilenceUsage = true
	root.CompletionOptions.DisableDefaultCmd = false

	root.AddCommand(newRankCmd())
	root.InitDefaultCompletionCmd()
	return root
}

func newRankCmd() *cobra.Command {
	var texts stringList
	query := ""
	modelID := strings.TrimSpace(os.Getenv("EMBEDDING_MODEL"))
	timeout := 60 * time.Second

	cmd := &cobra.Command{
		Use:   "rank",
		Short: "Embed query + texts and print cosine-similarity ranking",
		Long: strings.Join([]string{
			"Embed a query and document texts, then print cosine-similarity ranking.",
			"",
			"API key env vars:",
			"  OPENAI_API_KEY",
			"  GOOGLE_API_KEY",
			"  COHERE_API_KEY",
		}, "\n"),
		RunE: func(_ *cobra.Command, _ []string) error {
			return runRank(texts, query, modelID, timeout)
		},
	}

	cmd.Flags().Var(&texts, "text", "document text (repeatable)")
	cmd.Flags().StringVar(&query, "query", "", "query text")
	cmd.Flags().StringVar(&modelID, "model", modelID, "embedding model ID")
	cmd.Flags().DurationVar(&timeout, "timeout", timeout, "request timeout")
	return cmd
}

func runRank(texts []string, query, modelID string, timeout time.Duration) error {
	available := registerEmbeddingProviders()
	if len(available) == 0 {
		return fmt.Errorf("no API keys found; set one of: OPENAI_API_KEY, GOOGLE_API_KEY, COHERE_API_KEY")
	}

	selectedModel, selectedProvider, autoSelected, err := resolveEmbeddingModel(modelID, available)
	if err != nil {
		return err
	}
	modelID = selectedModel

	model, ok := ai.GetEmbeddingModel(modelID)
	if !ok {
		return fmt.Errorf("unknown embedding model %q", modelID)
	}
	if !contains(available, model.Provider) {
		return fmt.Errorf("model %q requires provider %q but key is not set; available providers: %s", modelID, model.Provider, strings.Join(available, ", "))
	}

	if len(texts) == 0 {
		stdinTexts, err := readTextsFromStdinIfPiped()
		if err != nil {
			return fmt.Errorf("read stdin: %w", err)
		}
		texts = append(texts, stdinTexts...)
	}

	if strings.TrimSpace(query) == "" {
		return errors.New("-query is required")
	}
	if len(texts) == 0 {
		return errors.New("provide at least one -text or pipe newline-delimited texts on stdin")
	}

	if autoSelected {
		fmt.Printf("model not specified; defaulting to %s (provider=%s)\n", selectedModel, selectedProvider)
	}
	fmt.Printf("available providers: %s\n", strings.Join(available, ", "))

	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()

	allTexts := append(append([]string{}, texts...), query)
	resp, err := ai.Embed(ctx, modelID, ai.EmbeddingRequest{Texts: allTexts})
	if err != nil {
		return fmt.Errorf("embed: %w", err)
	}
	if len(resp.Embeddings) != len(texts)+1 {
		return fmt.Errorf("unexpected embedding count: got %d want %d", len(resp.Embeddings), len(texts)+1)
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
	return nil
}

func registerEmbeddingProviders() []string {
	available := make([]string, 0, 3)

	if key := strings.TrimSpace(os.Getenv("OPENAI_API_KEY")); key != "" {
		openai.RegisterEmbedding(openai.Config{
			APIKey:  key,
			BaseURL: os.Getenv("OPENAI_BASE_URL"),
		}, "cmd-embedding-demo")
		available = append(available, embedOpenAI)
	}

	if key := strings.TrimSpace(os.Getenv("GOOGLE_API_KEY")); key != "" {
		google.RegisterEmbedding(google.Config{
			APIKey:  key,
			BaseURL: os.Getenv("GOOGLE_BASE_URL"),
			Version: os.Getenv("GOOGLE_API_VERSION"),
		}, "cmd-embedding-demo")
		available = append(available, embedGoogle)
	}

	if key := strings.TrimSpace(os.Getenv("COHERE_API_KEY")); key != "" {
		cohere.RegisterEmbedding(cohere.Config{
			APIKey:  key,
			BaseURL: os.Getenv("COHERE_BASE_URL"),
		}, "cmd-embedding-demo")
		available = append(available, embedCohere)
	}

	sort.Strings(available)
	return available
}

func resolveEmbeddingModel(modelID string, available []string) (string, string, bool, error) {
	modelID = strings.TrimSpace(modelID)
	if modelID != "" {
		model, ok := ai.GetEmbeddingModel(modelID)
		if !ok {
			return "", "", false, fmt.Errorf("unknown embedding model %q", modelID)
		}
		return modelID, model.Provider, false, nil
	}

	// Pick first available provider in precedence order.
	for _, provider := range []string{embedCohere, embedGoogle, embedOpenAI} {
		if !contains(available, provider) {
			continue
		}
		if model := defaultModelForProvider(provider); model != "" {
			return model, provider, true, nil
		}
	}
	return "", "", false, fmt.Errorf("no embedding model available for providers: %s", strings.Join(available, ", "))
}

func defaultModelForProvider(provider string) string {
	var preferred string
	switch provider {
	case embedOpenAI:
		preferred = "text-embedding-3-small"
	case embedGoogle:
		preferred = "gemini-embedding-001"
	case embedCohere:
		preferred = "embed-v4.0"
	}
	if preferred != "" {
		if model, ok := ai.GetEmbeddingModel(preferred); ok && model.Provider == provider {
			return preferred
		}
	}
	models := ai.ListEmbeddingModelsByProvider(provider)
	if len(models) == 0 {
		return ""
	}
	return models[0].ID
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

func contains(items []string, target string) bool {
	for _, item := range items {
		if item == target {
			return true
		}
	}
	return false
}
