package service

import (
	"errors"
	"io"
	"net/http"
	"strings"
	"testing"
)

type roundTripFunc func(*http.Request) (*http.Response, error)

func (f roundTripFunc) RoundTrip(req *http.Request) (*http.Response, error) {
	return f(req)
}

func TestCheckMLBBUsesUserAccountEndpoint(t *testing.T) {
	svc := &nicknameService{
		httpClient: &http.Client{Transport: roundTripFunc(func(req *http.Request) (*http.Response, error) {
			if req.Method != http.MethodPost {
				t.Fatalf("expected POST request, got %s", req.Method)
			}
			if req.URL.String() != "https://gopay.co.id/games/v1/order/user-account" {
				t.Fatalf("unexpected endpoint: %s", req.URL.String())
			}
			body, err := io.ReadAll(req.Body)
			if err != nil {
				t.Fatal(err)
			}
			want := `{"code":"MOBILE_LEGENDS","data":{"userId":"964740143","zoneId":"12822"}}`
			if string(body) != want {
				t.Fatalf("unexpected payload: %s", body)
			}

			return &http.Response{
				StatusCode: http.StatusOK,
				Body:       io.NopCloser(strings.NewReader(`{"message":"Success","data":{"countryOrigin":"id","username":"MìMï+Shú+Shú"}}`)),
				Header:     make(http.Header),
			}, nil
		})},
	}

	result, err := svc.checkMLBB("964740143", "12822")
	if err != nil {
		t.Fatal(err)
	}
	if result.Nickname != "MìMï+Shú+Shú" {
		t.Fatalf("unexpected nickname: %s", result.Nickname)
	}
}

func TestCheckMLBBDoesNotCreateFallbackNickname(t *testing.T) {
	svc := &nicknameService{
		httpClient: &http.Client{Transport: roundTripFunc(func(*http.Request) (*http.Response, error) {
			return nil, errors.New("network unavailable")
		})},
	}

	result, err := svc.checkMLBB("964740143", "12822")
	if err == nil {
		t.Fatalf("expected verification error, got result: %+v", result)
	}
}
