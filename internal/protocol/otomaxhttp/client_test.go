package otomaxhttp

import (
	"context"
	"io"
	"net/http"
	"strings"
	"testing"
)

type roundTripFunc func(*http.Request) (*http.Response, error)

func (f roundTripFunc) RoundTrip(request *http.Request) (*http.Response, error) { return f(request) }

func TestClientRendersQueryWithoutLeakingSecretInError(t *testing.T) {
	client, err := NewClient("https://provider.example", &http.Client{Transport: roundTripFunc(func(request *http.Request) (*http.Response, error) {
		if got := request.URL.Query().Get("apikey"); got != "secret-key" {
			t.Fatalf("apikey=%q", got)
		}
		return &http.Response{StatusCode: http.StatusOK, Body: io.NopCloser(strings.NewReader("ok")), Header: make(http.Header)}, nil
	})})
	if err != nil {
		t.Fatal(err)
	}
	_, err = client.Execute(context.Background(), Endpoint{Method: http.MethodGet, Path: "/v1/order", Query: map[string]string{"apikey": "{{secret.apikey}}"}}, TemplateValues{Secrets: map[string]string{"apikey": "secret-key"}})
	if err != nil {
		t.Fatalf("execute: %v", err)
	}
	_, err = client.Execute(context.Background(), Endpoint{Method: http.MethodGet, Path: "/v1/order", Query: map[string]string{"apikey": "{{secret.apikey}}"}}, TemplateValues{})
	if err == nil || strings.Contains(err.Error(), "secret-key") {
		t.Fatalf("expected masked configuration failure, err=%v", err)
	}
}
