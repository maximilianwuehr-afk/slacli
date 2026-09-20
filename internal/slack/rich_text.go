package slack

import (
	"fmt"
	"net/url"
	"regexp"
	"strconv"
	"strings"
)

const (
	DraftFormatLiteral  = "literal"
	DraftFormatRichText = "rich-text"
)

var orderedListPrefix = regexp.MustCompile(`^(\d+)[.)]\s+`)

// DraftBlocks builds the native rich-text blocks sent to Slack for a draft.
// Literal is the backwards-compatible format. Rich-text parses only the small
// formatting grammar documented by the CLI; it is not a general Markdown parser.
func DraftBlocks(text, format string) ([]map[string]interface{}, error) {
	switch strings.ToLower(strings.TrimSpace(format)) {
	case "", DraftFormatLiteral:
		return literalDraftBlocks(text), nil
	case DraftFormatRichText, "rich_text", "rich":
		return richTextDraftBlocks(text), nil
	default:
		return nil, fmt.Errorf("unsupported draft format %q (use %q or %q)", format, DraftFormatLiteral, DraftFormatRichText)
	}
}

func literalDraftBlocks(text string) []map[string]interface{} {
	return []map[string]interface{}{{
		"type": "rich_text",
		"elements": []map[string]interface{}{{
			"type": "rich_text_section",
			"elements": []map[string]interface{}{{
				"type": "text",
				"text": text,
			}},
		}},
	}}
}

func richTextDraftBlocks(text string) []map[string]interface{} {
	lines := strings.Split(text, "\n")
	elements := make([]map[string]interface{}, 0, len(lines))
	section := make([]map[string]interface{}, 0, 4)

	flushSection := func() {
		if len(section) == 0 {
			return
		}
		elements = append(elements, map[string]interface{}{
			"type":     "rich_text_section",
			"elements": section,
		})
		section = nil
	}

	for i := 0; i < len(lines); {
		if item, ok := parseListLine(lines[i]); ok {
			flushSection()
			style, indent := item.style, item.indent
			items := []map[string]interface{}{}
			for i < len(lines) {
				next, ok := parseListLine(lines[i])
				if !ok || next.style != style || next.indent != indent {
					break
				}
				items = append(items, map[string]interface{}{
					"type":     "rich_text_section",
					"elements": parseInline(next.text),
				})
				i++
			}
			elements = append(elements, map[string]interface{}{
				"type":     "rich_text_list",
				"style":    style,
				"indent":   indent,
				"elements": items,
			})
			continue
		}

		section = append(section, parseInline(lines[i])...)
		if i < len(lines)-1 {
			section = append(section, map[string]interface{}{"type": "text", "text": "\n"})
		}
		i++
	}
	flushSection()

	if len(elements) == 0 {
		elements = append(elements, map[string]interface{}{
			"type":     "rich_text_section",
			"elements": []map[string]interface{}{{"type": "text", "text": ""}},
		})
	}
	return []map[string]interface{}{{"type": "rich_text", "elements": elements}}
}

type listLine struct {
	style  string
	indent int
	text   string
}

func parseListLine(line string) (listLine, bool) {
	leading := 0
	for leading < len(line) {
		switch line[leading] {
		case ' ':
			leading++
		case '\t':
			leading += 2
		default:
			goto prefix
		}
	}

prefix:
	trimmed := strings.TrimLeft(line, " \t")
	if len(trimmed) >= 2 && (trimmed[0] == '-' || trimmed[0] == '+' || trimmed[0] == '*') && trimmed[1] == ' ' {
		return listLine{style: "bullet", indent: leading / 2, text: trimmed[2:]}, true
	}
	if match := orderedListPrefix.FindStringSubmatch(trimmed); match != nil {
		prefixLen := len(match[0])
		return listLine{style: "ordered", indent: leading / 2, text: trimmed[prefixLen:]}, true
	}
	return listLine{}, false
}

func parseInline(input string) []map[string]interface{} {
	var result []map[string]interface{}
	var plain strings.Builder
	flushPlain := func() {
		if plain.Len() == 0 {
			return
		}
		result = append(result, map[string]interface{}{"type": "text", "text": plain.String()})
		plain.Reset()
	}

	for i := 0; i < len(input); {
		if input[i] == '\\' && i+1 < len(input) && isEscapable(input[i+1]) {
			plain.WriteByte(input[i+1])
			i += 2
			continue
		}

		if link, end, ok := parseSlackLink(input, i); ok {
			flushPlain()
			result = append(result, link)
			i = end
			continue
		}
		if link, end, ok := parseMarkdownLink(input, i); ok {
			flushPlain()
			result = append(result, link)
			i = end
			continue
		}

		marker, style, ok := inlineStyleAt(input, i)
		if ok {
			if end := findUnescaped(input, marker, i+len(marker)); end >= 0 {
				flushPlain()
				inner := parseInline(input[i+len(marker) : end])
				result = append(result, applyStyle(inner, style)...)
				i = end + len(marker)
				continue
			}
		}

		plain.WriteByte(input[i])
		i++
	}
	flushPlain()
	return result
}

func isEscapable(ch byte) bool {
	switch ch {
	case '\\', '*', '_', '<', '>', '[', ']', '(', ')':
		return true
	default:
		return false
	}
}

func parseSlackLink(input string, start int) (map[string]interface{}, int, bool) {
	if input[start] != '<' {
		return nil, start, false
	}
	endRel := strings.IndexByte(input[start+1:], '>')
	if endRel < 0 {
		return nil, start, false
	}
	end := start + 1 + endRel + 1
	value := input[start+1 : end-1]
	parts := strings.SplitN(value, "|", 2)
	if len(parts) != 2 || !isDraftURL(parts[0]) {
		return nil, start, false
	}
	return map[string]interface{}{
		"type": "link",
		"url":  parts[0],
		"text": parts[1],
	}, end, true
}

func parseMarkdownLink(input string, start int) (map[string]interface{}, int, bool) {
	if input[start] != '[' {
		return nil, start, false
	}
	labelEndRel := strings.IndexByte(input[start+1:], ']')
	if labelEndRel < 0 {
		return nil, start, false
	}
	labelEnd := start + 1 + labelEndRel + 1
	if labelEnd >= len(input) || input[labelEnd] != '(' {
		return nil, start, false
	}
	urlEndRel := strings.IndexByte(input[labelEnd+1:], ')')
	if urlEndRel < 0 {
		return nil, start, false
	}
	end := labelEnd + 1 + urlEndRel + 1
	href := input[labelEnd+1 : end-1]
	if !isDraftURL(href) {
		return nil, start, false
	}
	return map[string]interface{}{
		"type": "link",
		"url":  href,
		"text": input[start+1 : labelEnd-1],
	}, end, true
}

func isDraftURL(value string) bool {
	parsed, err := url.Parse(value)
	if err != nil || parsed.Scheme == "" {
		return false
	}
	return parsed.Scheme == "http" || parsed.Scheme == "https" || parsed.Scheme == "mailto"
}

func inlineStyleAt(input string, start int) (string, string, bool) {
	if strings.HasPrefix(input[start:], "**") {
		return "**", "bold", true
	}
	if strings.HasPrefix(input[start:], "__") {
		return "__", "bold", true
	}
	if input[start] == '*' && (start+1 >= len(input) || input[start+1] != ' ') {
		return "*", "italic", true
	}
	if input[start] == '_' {
		return "_", "italic", true
	}
	return "", "", false
}

func findUnescaped(input, marker string, start int) int {
	for index := start; index <= len(input)-len(marker); index++ {
		if input[index] == '\\' {
			index++
			continue
		}
		if strings.HasPrefix(input[index:], marker) {
			return index
		}
	}
	return -1
}

func applyStyle(elements []map[string]interface{}, style string) []map[string]interface{} {
	for _, element := range elements {
		styles, _ := element["style"].(map[string]interface{})
		if styles == nil {
			styles = map[string]interface{}{}
		}
		styles[style] = true
		element["style"] = styles
	}
	return elements
}

// DraftTextFromBlocks renders native rich-text blocks into a loss-aware CLI
// representation. Links retain both their label and destination.
func DraftTextFromBlocks(blocks []map[string]interface{}) string {
	var out strings.Builder
	for index, block := range blocks {
		if index > 0 && out.Len() > 0 && !strings.HasSuffix(out.String(), "\n") {
			out.WriteByte('\n')
		}
		if block["type"] != "rich_text" {
			continue
		}
		renderRichTextElements(block["elements"], &out)
	}
	return out.String()
}

func renderRichTextElements(value interface{}, out *strings.Builder) {
	for _, element := range asMaps(value) {
		switch element["type"] {
		case "rich_text_section", "rich_text_quote":
			renderRichTextElements(element["elements"], out)
		case "rich_text_list":
			renderList(element, out)
		case "text":
			if text, ok := element["text"].(string); ok {
				out.WriteString(text)
			}
		case "link":
			urlValue, _ := element["url"].(string)
			label, _ := element["text"].(string)
			if label == "" {
				out.WriteString("<" + urlValue + ">")
			} else {
				out.WriteString("<" + urlValue + "|" + label + ">")
			}
		case "emoji":
			if name, ok := element["name"].(string); ok {
				out.WriteString(":" + name + ":")
			}
		case "user":
			if userID, ok := element["user_id"].(string); ok {
				out.WriteString("<@" + userID + ">")
			}
		case "channel":
			if channelID, ok := element["channel_id"].(string); ok {
				out.WriteString("<#" + channelID + ">")
			}
		}
	}
}

func renderList(element map[string]interface{}, out *strings.Builder) {
	style, _ := element["style"].(string)
	indent := intValue(element["indent"])
	for index, item := range asMaps(element["elements"]) {
		if out.Len() > 0 && !strings.HasSuffix(out.String(), "\n") {
			out.WriteByte('\n')
		}
		out.WriteString(strings.Repeat("  ", indent))
		if style == "ordered" {
			out.WriteString(strconv.Itoa(index + 1))
			out.WriteString(". ")
		} else {
			out.WriteString("- ")
		}
		renderRichTextElements(item["elements"], out)
	}
}

func intValue(value interface{}) int {
	switch number := value.(type) {
	case int:
		return number
	case int64:
		return int(number)
	case float64:
		return int(number)
	default:
		return 0
	}
}

func asMaps(value interface{}) []map[string]interface{} {
	switch values := value.(type) {
	case []map[string]interface{}:
		return values
	case []interface{}:
		maps := make([]map[string]interface{}, 0, len(values))
		for _, value := range values {
			if item, ok := value.(map[string]interface{}); ok {
				maps = append(maps, item)
			}
		}
		return maps
	default:
		return nil
	}
}
