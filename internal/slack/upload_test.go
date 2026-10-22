package slack

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestUploadFileExternal(t *testing.T) {
	for _, thread := range []string{"", "1792490377.447259"} {
		t.Run("thread="+thread, func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "clip.mp4")
			data := "\x00video bytes\xff"
			if err := os.WriteFile(path, []byte(data), 0600); err != nil {
				t.Fatal(err)
			}
			calls := []string{}
			original := http.DefaultTransport
			t.Cleanup(func() { http.DefaultTransport = original })
			http.DefaultTransport = roundTripFunc(func(req *http.Request) (*http.Response, error) {
				calls = append(calls, "bytes")
				if req.URL.String() != "https://files.slack.com/upload/test" || req.Method != "POST" {
					t.Fatalf("unexpected upload %s %s", req.Method, req.URL)
				}
				if req.Header.Get("Authorization") != "" {
					t.Fatal("token sent to signed upload URL")
				}
				got, err := io.ReadAll(req.Body)
				if err != nil || string(got) != data || req.ContentLength != int64(len(data)) {
					t.Fatalf("incorrect bytes or length: %q / %d / %v", got, req.ContentLength, err)
				}
				return jsonHTTPResponse("OK"), nil
			})
			api := NewAPI(&http.Client{Transport: roundTripFunc(func(req *http.Request) (*http.Response, error) {
				calls = append(calls, req.URL.Path)
				var p map[string]interface{}
				if strings.HasSuffix(req.URL.Path, "getUploadURLExternal") {
					if err := req.ParseForm(); err != nil {
						t.Fatal(err)
					}
					p = map[string]interface{}{"filename": req.Form.Get("filename"), "length": req.Form.Get("length")}
				} else if err := json.NewDecoder(req.Body).Decode(&p); err != nil {
					t.Fatal(err)
				}
				if req.Method != "POST" {
					t.Fatal(req.Method)
				}
				switch req.URL.Path {
				case "/api/files.getUploadURLExternal":
					if p["filename"] != "clip.mp4" || p["length"] != fmt.Sprint(len(data)) {
						t.Fatalf("bad request: %#v", p)
					}
					return jsonHTTPResponse(`{"ok":true,"file_id":"F1","upload_url":"https://files.slack.com/upload/test"}`), nil
				case "/api/files.completeUploadExternal":
					if p["channel_id"] != "D1" || p["initial_comment"] != "one comment" {
						t.Fatalf("bad destination/comment: %#v", p)
					}
					if thread == "" {
						if _, ok := p["thread_ts"]; ok {
							t.Fatal("unexpected thread")
						}
					} else if p["thread_ts"] != thread {
						t.Fatal("missing thread")
					}
					files := p["files"].([]interface{})
					f := files[0].(map[string]interface{})
					if len(files) != 1 || f["id"] != "F1" || f["title"] != "clip.mp4" {
						t.Fatalf("bad files: %#v", files)
					}
					return jsonHTTPResponse(`{"ok":true,"files":[{"id":"F1","permalink":"https://slack.com/file/F1"}]}`), nil
				default:
					t.Fatalf("unexpected API call %s", req.URL.Path)
					return nil, nil
				}
			})})
			got, err := api.UploadFile("D1", path, "one comment", thread)
			if err != nil {
				t.Fatal(err)
			}
			if got.FileID != "F1" || got.FileName != "clip.mp4" || got.Permalink != "https://slack.com/file/F1" {
				t.Fatalf("bad result: %#v", got)
			}
			if strings.Join(calls, ",") != "/api/files.getUploadURLExternal,bytes,/api/files.completeUploadExternal" {
				t.Fatal(calls)
			}
		})
	}
}

func TestUploadFileFailures(t *testing.T) {
	for _, tc := range []struct {
		name, allocation, completion string
		status                       int
		want                         string
		calls                        int
	}{
		{"allocation rejected", `{"ok":false,"error":"missing_scope"}`, "", 200, "missing_scope", 1},
		{"allocation malformed", `invalid`, "", 200, "files.getUploadURLExternal", 1},
		{"missing URL", `{"ok":true,"file_id":"F1"}`, "", 200, "missing file ID", 1},
		{"insecure URL", `{"ok":true,"file_id":"F1","upload_url":"http://example.com"}`, "", 200, "invalid HTTPS", 1},
		{"byte upload rejected", "", "", 500, "upload bytes: HTTP 500", 1},
		{"completion rejected", "", `{"ok":false,"error":"channel_not_found"}`, 200, "channel_not_found", 2},
		{"completion malformed", "", `invalid`, 200, "files.completeUploadExternal", 2},
		{"completion empty", "", `{"ok":true,"files":[]}`, 200, "uploaded file missing", 2},
	} {
		t.Run(tc.name, func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "test.txt")
			if err := os.WriteFile(path, []byte("test"), 0600); err != nil {
				t.Fatal(err)
			}
			orig := http.DefaultTransport
			t.Cleanup(func() { http.DefaultTransport = orig })
			http.DefaultTransport = roundTripFunc(func(*http.Request) (*http.Response, error) {
				r := jsonHTTPResponse("status")
				r.StatusCode = tc.status
				return r, nil
			})
			calls := 0
			api := NewAPI(&http.Client{Transport: roundTripFunc(func(req *http.Request) (*http.Response, error) {
				calls++
				if strings.HasSuffix(req.URL.Path, "getUploadURLExternal") {
					b := tc.allocation
					if b == "" {
						b = `{"ok":true,"file_id":"F1","upload_url":"https://files.slack.com/upload/test"}`
					}
					return jsonHTTPResponse(b), nil
				}
				if strings.HasSuffix(req.URL.Path, "completeUploadExternal") {
					return jsonHTTPResponse(tc.completion), nil
				}
				return nil, fmt.Errorf("unexpected method")
			})})
			_, err := api.UploadFile("D1", path, "", "")
			if err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("got %v, want %s", err, tc.want)
			}
			if calls != tc.calls {
				t.Fatalf("got %d API calls, want %d", calls, tc.calls)
			}
		})
	}
}
