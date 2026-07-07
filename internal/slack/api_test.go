package slack

import (
	"io"
	"net/http"
	"strings"
	"testing"
)

type roundTripFunc func(*http.Request) (*http.Response, error)

func (f roundTripFunc) RoundTrip(req *http.Request) (*http.Response, error) {
	return f(req)
}

func TestSearchUsersPagesAndFilters(t *testing.T) {
	var requests []string
	client := &http.Client{
		Transport: roundTripFunc(func(req *http.Request) (*http.Response, error) {
			requests = append(requests, req.URL.RawQuery)

			if req.URL.Path != "/api/users.list" {
				t.Fatalf("expected users.list request, got %s", req.URL.Path)
			}

			switch req.URL.Query().Get("cursor") {
			case "":
				return jsonHTTPResponse(`{
					"ok": true,
					"members": [
						{"id":"U1","name":"alice","real_name":"Alice Adams","profile":{"email":"alice@example.com","display_name":"Alice"}}
					],
					"response_metadata": {"next_cursor":"next"}
				}`), nil
			case "next":
				return jsonHTTPResponse(`{
					"ok": true,
					"members": [
						{"id":"U2","name":"carol","real_name":"Carol Chen","profile":{"email":"carol@example.com","display_name":"Product QA"}}
					],
					"response_metadata": {"next_cursor":""}
				}`), nil
			default:
				t.Fatalf("unexpected cursor %q", req.URL.Query().Get("cursor"))
			}
			return nil, nil
		}),
	}

	api := NewAPI(client)
	users, err := api.SearchUsers("qa", 10)
	if err != nil {
		t.Fatalf("SearchUsers failed: %v", err)
	}

	if len(users) != 1 {
		t.Fatalf("expected 1 user, got %d", len(users))
	}
	if users[0].ID != "U2" {
		t.Fatalf("expected U2, got %s", users[0].ID)
	}
	if len(requests) != 2 {
		t.Fatalf("expected 2 paged requests, got %d", len(requests))
	}
}

func TestSearchUsersSkipsDeletedAndStopsAtLimit(t *testing.T) {
	calls := 0
	client := &http.Client{
		Transport: roundTripFunc(func(req *http.Request) (*http.Response, error) {
			calls++
			return jsonHTTPResponse(`{
				"ok": true,
				"members": [
					{"id":"U1","name":"alice_old","real_name":"Alice Old","deleted":true,"profile":{"email":"alice.old@example.com","display_name":"Alice Old"}},
					{"id":"U2","name":"alice","real_name":"Alice Adams","profile":{"email":"alice@example.com","display_name":"Alice"}},
					{"id":"U3","name":"alice2","real_name":"Alice Baker","profile":{"email":"alice.baker@example.com","display_name":"Alice B"}}
				],
				"response_metadata": {"next_cursor":"unused"}
			}`), nil
		}),
	}

	api := NewAPI(client)
	users, err := api.SearchUsers("@alice", 1)
	if err != nil {
		t.Fatalf("SearchUsers failed: %v", err)
	}

	if len(users) != 1 {
		t.Fatalf("expected 1 user, got %d", len(users))
	}
	if users[0].ID != "U2" {
		t.Fatalf("expected first active match U2, got %s", users[0].ID)
	}
	if calls != 1 {
		t.Fatalf("expected search to stop after limit, got %d calls", calls)
	}
}

func jsonHTTPResponse(body string) *http.Response {
	return &http.Response{
		StatusCode: http.StatusOK,
		Header:     make(http.Header),
		Body:       io.NopCloser(strings.NewReader(body)),
	}
}
