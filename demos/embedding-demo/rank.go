package main

import (
	"math"
	"sort"
)

type embeddedText struct {
	Text   string
	Vector []float32
}

type scoredText struct {
	Text  string
	Score float64
}

func cosineSimilarity(a, b []float32) float64 {
	if len(a) == 0 || len(b) == 0 {
		return 0
	}
	n := len(a)
	if len(b) < n {
		n = len(b)
	}
	var dot, na, nb float64
	for i := range n {
		av := float64(a[i])
		bv := float64(b[i])
		dot += av * bv
		na += av * av
		nb += bv * bv
	}
	if na == 0 || nb == 0 {
		return 0
	}
	return dot / (math.Sqrt(na) * math.Sqrt(nb))
}

func rankBySimilarity(query []float32, docs []embeddedText) []scoredText {
	out := make([]scoredText, len(docs))
	for i, doc := range docs {
		out[i] = scoredText{
			Text:  doc.Text,
			Score: cosineSimilarity(query, doc.Vector),
		}
	}
	sort.SliceStable(out, func(i, j int) bool {
		return out[i].Score > out[j].Score
	})
	return out
}
