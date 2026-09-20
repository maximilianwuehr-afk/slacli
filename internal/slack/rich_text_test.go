package slack

import (
	"encoding/json"
	"reflect"
	"strings"
	"testing"
)

func TestDraftBlocksRichTextBuildsNativeFormatting(t *testing.T) {
	input := "Paragraph with **bold** and _italic_.\n\n- first\n  - nested\n1. numbered\n\nSee <https://example.test/docs?q=x#FAQ|FAQ>."

	blocks, err := DraftBlocks(input, DraftFormatRichText)
	if err != nil {
		t.Fatalf("DraftBlocks() error = %v", err)
	}
	if len(blocks) != 1 || blocks[0]["type"] != "rich_text" {
		t.Fatalf("unexpected blocks: %#v", blocks)
	}

	elements := asMaps(blocks[0]["elements"])
	if len(elements) != 5 {
		t.Fatalf("native element count = %d, want 5: %#v", len(elements), elements)
	}

	section := asMaps(elements[0]["elements"])
	if !hasStyledText(section, "bold", "bold") {
		t.Fatalf("bold node missing: %#v", section)
	}
	if !hasStyledText(section, "italic", "italic") {
		t.Fatalf("italic node missing: %#v", section)
	}

	if elements[1]["type"] != "rich_text_list" || elements[1]["style"] != "bullet" || intValue(elements[1]["indent"]) != 0 {
		t.Fatalf("top-level bullet list malformed: %#v", elements[1])
	}
	if elements[2]["type"] != "rich_text_list" || intValue(elements[2]["indent"]) != 1 {
		t.Fatalf("nested bullet list malformed: %#v", elements[2])
	}
	if elements[3]["type"] != "rich_text_list" || elements[3]["style"] != "ordered" {
		t.Fatalf("ordered list malformed: %#v", elements[3])
	}

	linkSection := asMaps(elements[4]["elements"])
	if !containsLink(linkSection, "FAQ", "https://example.test/docs?q=x#FAQ") {
		t.Fatalf("labelled link missing: %#v", linkSection)
	}

	readback := DraftTextFromBlocks(blocks)
	for _, want := range []string{"Paragraph with bold and italic.", "- first", "  - nested", "1. numbered", "<https://example.test/docs?q=x#FAQ|FAQ>"} {
		if !strings.Contains(readback, want) {
			t.Errorf("readback %q does not contain %q", readback, want)
		}
	}
}

func TestDraftBlocksLiteralPreservesText(t *testing.T) {
	input := "**literal**\n- still literal\n<https://example.test/#FAQ|label>"

	blocks, err := DraftBlocks(input, DraftFormatLiteral)
	if err != nil {
		t.Fatalf("DraftBlocks() error = %v", err)
	}
	if got := DraftTextFromBlocks(blocks); got != input {
		t.Fatalf("literal readback = %q, want %q", got, input)
	}
	section := asMaps(asMaps(blocks[0]["elements"])[0]["elements"])
	if containsLink(section, "label", "https://example.test/#FAQ") {
		t.Fatal("literal format unexpectedly parsed a link")
	}
}

func TestDraftBlocksEscapesFormattingMarkers(t *testing.T) {
	input := `\*literal\* \_text\_ \<https://example.test/#FAQ|label\>`
	blocks, err := DraftBlocks(input, DraftFormatRichText)
	if err != nil {
		t.Fatalf("DraftBlocks() error = %v", err)
	}
	if got := DraftTextFromBlocks(blocks); got != `*literal* _text_ <https://example.test/#FAQ|label>` {
		t.Fatalf("escaped readback = %q", got)
	}
	data, err := json.Marshal(blocks)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(data), `"type":"link"`) {
		t.Fatalf("escaped input produced a link node: %s", data)
	}
}

func TestDraftTextFromBlocksRendersNestedRichText(t *testing.T) {
	blocks := []map[string]interface{}{{
		"type": "rich_text",
		"elements": []interface{}{
			map[string]interface{}{
				"type":   "rich_text_list",
				"style":  "ordered",
				"indent": float64(0),
				"elements": []interface{}{
					map[string]interface{}{"type": "rich_text_section", "elements": []interface{}{
						map[string]interface{}{"type": "text", "text": "one"},
					}},
				},
			},
			map[string]interface{}{
				"type":   "rich_text_list",
				"style":  "bullet",
				"indent": float64(1),
				"elements": []interface{}{
					map[string]interface{}{"type": "rich_text_section", "elements": []interface{}{
						map[string]interface{}{"type": "link", "text": "FAQ", "url": "https://example.test/#FAQ"},
					}},
				},
			},
		},
	}}

	want := "1. one\n  - <https://example.test/#FAQ|FAQ>"
	if got := DraftTextFromBlocks(blocks); got != want {
		t.Fatalf("nested readback = %q, want %q", got, want)
	}
}

func hasStyledText(elements []map[string]interface{}, style, text string) bool {
	for _, element := range elements {
		if element["type"] != "text" || element["text"] != text {
			continue
		}
		styles, _ := element["style"].(map[string]interface{})
		if styles[style] == true {
			return true
		}
	}
	return false
}

func containsLink(elements []map[string]interface{}, text, url string) bool {
	for _, element := range elements {
		if element["type"] == "link" && element["text"] == text && element["url"] == url {
			return true
		}
	}
	return false
}

func TestAsMapsSupportsDecodedAndNativeSlices(t *testing.T) {
	values := []map[string]interface{}{{"type": "text"}}
	if got := asMaps(values); !reflect.DeepEqual(got, values) {
		t.Fatalf("asMaps(native) = %#v", got)
	}
}
