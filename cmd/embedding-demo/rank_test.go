package main

import (
	"math"
	"testing"
)

func TestCosineSimilarity(t *testing.T) {
	a := []float32{1, 0}
	b := []float32{1, 0}
	c := []float32{0, 1}

	if got := cosineSimilarity(a, b); math.Abs(got-1) > 1e-9 {
		t.Fatalf("identical cosine=%v want 1", got)
	}
	if got := cosineSimilarity(a, c); math.Abs(got) > 1e-9 {
		t.Fatalf("orthogonal cosine=%v want 0", got)
	}
}

func TestRankBySimilarity(t *testing.T) {
	query := []float32{1, 0}
	docs := []embeddedText{
		{Text: "A", Vector: []float32{1, 0}},
		{Text: "B", Vector: []float32{0.5, 0.5}},
		{Text: "C", Vector: []float32{0, 1}},
	}

	ranked := rankBySimilarity(query, docs)
	if len(ranked) != 3 {
		t.Fatalf("len=%d want 3", len(ranked))
	}
	if ranked[0].Text != "A" {
		t.Fatalf("top=%q want A", ranked[0].Text)
	}
	if ranked[2].Text != "C" {
		t.Fatalf("bottom=%q want C", ranked[2].Text)
	}
	if ranked[0].Score < ranked[1].Score || ranked[1].Score < ranked[2].Score {
		t.Fatalf("scores not monotonic: %+v", ranked)
	}
}
