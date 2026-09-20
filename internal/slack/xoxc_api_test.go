package slack

import (
	"encoding/json"
	"io"
	"mime"
	"mime/multipart"
	"net/http"
	"net/url"
	"strings"
	"testing"
)

type draftRoundTripper func(*http.Request) (*http.Response, error)

func (f draftRoundTripper) RoundTrip(request *http.Request) (*http.Response, error) {
	return f(request)
}

func draftResponse(body string) *http.Response {
	return &http.Response{
		StatusCode: http.StatusOK,
		Header:     make(http.Header),
		Body:       io.NopCloser(strings.NewReader(body)),
	}
}

func TestListDraftsRendersRichTextWithoutDroppingNodes(t *testing.T) {
	blocks := []map[string]interface{}{{
		"type": "rich_text",
		"elements": []map[string]interface{}{
			{"type": "rich_text_section", "elements": []map[string]interface{}{
				{"type": "text", "text": "Paragraph\n\n"},
				{"type": "text", "text": "Bold", "style": map[string]interface{}{"bold": true}},
				{"type": "text", "text": " and "},
				{"type": "text", "text": "italic", "style": map[string]interface{}{"italic": true}},
				{"type": "link", "text": "FAQ", "url": "https://example.test/docs?q=x#FAQ"},
			}},
			{"type": "rich_text_list", "style": "bullet", "indent": 0, "elements": []map[string]interface{}{
				{"type": "rich_text_section", "elements": []map[string]interface{}{{"type": "text", "text": "first"}}},
			}},
			{"type": "rich_text_list", "style": "ordered", "indent": 1, "elements": []map[string]interface{}{
				{"type": "rich_text_section", "elements": []map[string]interface{}{{"type": "text", "text": "nested"}}},
			}},
		},
	}}
	response := map[string]interface{}{
		"ok": true,
		"drafts": []map[string]interface{}{{
			"id":              "Dr123",
			"date_created":    int64(1700000000),
			"is_deleted":      false,
			"is_sent":         false,
			"blocks":          blocks,
			"destinations":    []map[string]interface{}{{"channel_id": "C123", "thread_ts": "1700.42"}},
			"attachments":     []map[string]interface{}{{"id": "A123"}},
			"file_ids":        []string{"F123"},
			"client_msg_id":   "client-123",
			"last_updated_ts": "1700000001.123456",
		}},
	}
	body, err := json.Marshal(response)
	if err != nil {
		t.Fatal(err)
	}
	client := &http.Client{Transport: draftRoundTripper(func(request *http.Request) (*http.Response, error) {
		if !strings.HasSuffix(request.URL.Path, "/drafts.list") {
			t.Fatalf("unexpected endpoint %s", request.URL.Path)
		}
		return draftResponse(string(body)), nil
	})}

	drafts, err := NewXoxcAPI(client, "finn-mobility", "xoxc-test").ListDrafts()
	if err != nil {
		t.Fatalf("ListDrafts() error = %v", err)
	}
	if len(drafts) != 1 {
		t.Fatalf("draft count = %d, want 1", len(drafts))
	}
	draft := drafts[0]
	for _, want := range []string{
		"Paragraph",
		"<https://example.test/docs?q=x#FAQ|FAQ>",
		"- first",
		"  1. nested",
	} {
		if !strings.Contains(draft.Text, want) {
			t.Errorf("draft text %q does not contain %q", draft.Text, want)
		}
	}
	if draft.ChannelID != "C123" || draft.ThreadTS != "1700.42" {
		t.Fatalf("destination = %s / %s", draft.ChannelID, draft.ThreadTS)
	}
	if len(draft.Blocks) != 1 || len(draft.Destinations) != 1 || len(draft.FileIDs) != 1 {
		t.Fatalf("raw metadata was not retained: %#v", draft)
	}
	if draft.ClientMsgID != "client-123" || draft.LastUpdatedTS != "1700000001.123456" {
		t.Fatalf("optimistic concurrency metadata lost: %#v", draft)
	}
}

func TestSaveDraftWithBlocksUpdateRetainsDestinationAndMetadata(t *testing.T) {
	blocks, err := DraftBlocks("Updated **bold** <https://example.test/#FAQ|FAQ>", DraftFormatRichText)
	if err != nil {
		t.Fatal(err)
	}
	metadataResponse := `{"ok":true,"drafts":[{"id":"Dr123","last_updated_ts":"1700000001.123456","client_msg_id":"client-123","destinations":[{"channel_id":"C123","thread_ts":"1700.42","user_ids":["U123"]}],"attachments":[{"id":"A123"}],"file_ids":["F123"]}]}`
	var captured *http.Request
	client := &http.Client{Transport: draftRoundTripper(func(request *http.Request) (*http.Response, error) {
		switch {
		case strings.HasSuffix(request.URL.Path, "/drafts.list"):
			return draftResponse(metadataResponse), nil
		case strings.HasSuffix(request.URL.Path, "/auth.test"):
			return draftResponse(`{"ok":true,"user_id":"U1","user":"max","team_id":"T1","team":"FINN"}`), nil
		case strings.HasSuffix(request.URL.Path, "/drafts.create"):
			captured = request
			return draftResponse(`{"ok":true,"draft":{"id":"Dr123"}}`), nil
		default:
			t.Fatalf("unexpected endpoint %s", request.URL.Path)
			return nil, nil
		}
	})}

	gotID, err := NewXoxcAPI(client, "finn-mobility", "xoxc-test").SaveDraftWithBlocks("C999", "Updated", "ignored-thread", "Dr123", blocks)
	if err != nil {
		t.Fatalf("SaveDraftWithBlocks() error = %v", err)
	}
	if gotID != "Dr123" {
		t.Fatalf("draft ID = %q", gotID)
	}
	if captured == nil {
		t.Fatal("drafts.create request was not captured")
	}

	if err := captured.ParseMultipartForm(1 << 20); err != nil {
		t.Fatalf("parse multipart: %v", err)
	}
	form := url.Values(captured.MultipartForm.Value)
	if form.Get("draft_id") != "Dr123" || form.Get("client_msg_id") != "client-123" || form.Get("client_last_updated_ts") != "1700000001.123456" {
		t.Fatalf("identity/concurrency fields = %#v", form)
	}
	if form.Get("destinations") != `[{"channel_id":"C123","thread_ts":"1700.42"}]` {
		t.Fatalf("destination was not retained: %s", form.Get("destinations"))
	}
	if form.Get("attachments") != `[{"id":"A123"}]` || form.Get("file_ids") != `["F123"]` {
		t.Fatalf("attachments/file_ids were not retained: %#v", form)
	}
	if !strings.Contains(form.Get("blocks"), `"type":"link"`) || !strings.Contains(form.Get("blocks"), `#FAQ`) {
		t.Fatalf("formatted blocks were not sent: %s", form.Get("blocks"))
	}
}

func TestSaveDraftWithBlocksMultipartIsActuallyMultipart(t *testing.T) {
	client := &http.Client{Transport: draftRoundTripper(func(request *http.Request) (*http.Response, error) {
		if strings.HasSuffix(request.URL.Path, "/auth.test") {
			return draftResponse(`{"ok":true,"team_id":"T1"}`), nil
		}
		if !strings.HasSuffix(request.URL.Path, "/drafts.create") {
			t.Fatalf("unexpected endpoint %s", request.URL.Path)
		}
		mediaType, params, err := mime.ParseMediaType(request.Header.Get("Content-Type"))
		if err != nil || mediaType != "multipart/form-data" || params["boundary"] == "" {
			t.Fatalf("content type = %q (%v)", request.Header.Get("Content-Type"), err)
		}
		reader := multipart.NewReader(request.Body, params["boundary"])
		foundBlocks := false
		for {
			part, nextErr := reader.NextPart()
			if nextErr == io.EOF {
				break
			}
			if nextErr != nil {
				t.Fatalf("read multipart field: %v", nextErr)
			}
			if part.FormName() == "blocks" {
				foundBlocks = true
				break
			}
		}
		if !foundBlocks {
			t.Fatal("blocks multipart field was missing")
		}
		return draftResponse(`{"ok":true,"draft":{"id":"Dr-new"}}`), nil
	})}
	blocks, _ := DraftBlocks("literal", DraftFormatLiteral)
	if _, err := NewXoxcAPI(client, "finn-mobility", "xoxc-test").SaveDraftWithBlocks("C123", "literal", "", "", blocks); err != nil {
		t.Fatalf("SaveDraftWithBlocks() error = %v", err)
	}
}

func TestNormalizeDraftDestinationsKeepsAcceptedDestinationShape(t *testing.T) {
	got := normalizeDraftDestinations([]map[string]interface{}{
		{"channel_id": "C123", "thread_ts": "1700.42", "user_ids": []interface{}{"U123"}},
		{"channel_id": "D123", "user_ids": []interface{}{"U123"}},
	})
	if _, ok := got[0]["user_ids"]; ok {
		t.Fatalf("thread destination still has user_ids: %#v", got[0])
	}
	if got[0]["channel_id"] != "C123" || got[0]["thread_ts"] != "1700.42" {
		t.Fatalf("thread destination changed: %#v", got[0])
	}
	if got[1]["channel_id"] != nil || got[1]["thread_ts"] != nil {
		t.Fatalf("DM destination retained conflicting fields: %#v", got[1])
	}
	if _, ok := got[1]["user_ids"]; !ok {
		t.Fatalf("DM destination lost user_ids: %#v", got[1])
	}
}
