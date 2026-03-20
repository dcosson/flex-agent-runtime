package restapi

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestDoJSON_RoundTrip(t *testing.T) {
	var gotAuth string
	var gotBody map[string]any

	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotAuth = r.Header.Get("Authorization")
		if r.Header.Get("Content-Type") != "application/json" {
			t.Fatalf("Content-Type = %q, want application/json", r.Header.Get("Content-Type"))
		}
		if err := json.NewDecoder(r.Body).Decode(&gotBody); err != nil {
			t.Fatalf("decode: %v", err)
		}
		_, _ = w.Write([]byte(`{"result":"ok"}`))
	}))
	defer ts.Close()

	c := Client{BaseURL: ts.URL, AuthToken: "tok-123", HTTPClient: ts.Client()}
	var out struct {
		Result string `json:"result"`
	}
	if err := c.DoJSON(context.Background(), http.MethodPost, "/test", map[string]any{"key": "val"}, &out); err != nil {
		t.Fatalf("DoJSON() error: %v", err)
	}
	if gotAuth != "Bearer tok-123" {
		t.Fatalf("Authorization = %q, want Bearer tok-123", gotAuth)
	}
	if gotBody["key"] != "val" {
		t.Fatalf("body = %v, want key=val", gotBody)
	}
	if out.Result != "ok" {
		t.Fatalf("result = %q, want ok", out.Result)
	}
}

func TestDoJSON_ErrorStatus(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusBadRequest)
		_, _ = w.Write([]byte(`bad request`))
	}))
	defer ts.Close()

	c := Client{BaseURL: ts.URL, HTTPClient: ts.Client()}
	err := c.DoJSON(context.Background(), http.MethodGet, "/fail", nil, nil)
	if err == nil {
		t.Fatal("DoJSON() succeeded, want error")
	}
}

func TestDoJSON_NilBody(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Content-Type") != "" {
			t.Fatalf("Content-Type should be empty for nil body, got %q", r.Header.Get("Content-Type"))
		}
		_, _ = w.Write([]byte(`{}`))
	}))
	defer ts.Close()

	c := Client{BaseURL: ts.URL, HTTPClient: ts.Client()}
	if err := c.DoJSON(context.Background(), http.MethodDelete, "/del", nil, nil); err != nil {
		t.Fatalf("DoJSON() error: %v", err)
	}
}

func TestCloneLabels(t *testing.T) {
	if got := CloneLabels(nil); got != nil {
		t.Fatalf("CloneLabels(nil) = %v, want nil", got)
	}
	if got := CloneLabels(map[string]string{}); got != nil {
		t.Fatalf("CloneLabels(empty) = %v, want nil", got)
	}
	in := map[string]string{"a": "1", "b": "2"}
	out := CloneLabels(in)
	if out["a"] != "1" || out["b"] != "2" {
		t.Fatalf("CloneLabels = %v, want a=1 b=2", out)
	}
	out["a"] = "changed"
	if in["a"] != "1" {
		t.Fatal("CloneLabels did not make a copy")
	}
}
