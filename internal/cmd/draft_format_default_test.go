package cmd

import (
	"bytes"
	"encoding/json"
	"strings"
	"testing"

	"github.com/spf13/cobra"

	"slacli/internal/slack"
)

func TestDraftCommandsDefaultToNativeRichText(t *testing.T) {
	commands := []struct {
		name string
		cmd  *cobra.Command
	}{
		{name: "draft", cmd: draftCmd},
		{name: "drafts create", cmd: draftsCreateCmd},
		{name: "drafts edit", cmd: draftsEditCmd},
	}

	for _, tc := range commands {
		t.Run(tc.name, func(t *testing.T) {
			formatFlag := tc.cmd.Flags().Lookup("format")
			if formatFlag == nil {
				t.Fatal("format flag is missing")
			}
			if got := formatFlag.DefValue; got != slack.DraftFormatRichText {
				t.Fatalf("omitted --format default = %q, want %q", got, slack.DraftFormatRichText)
			}

			var help bytes.Buffer
			tc.cmd.SetOut(&help)
			tc.cmd.SetErr(&help)
			if err := tc.cmd.Help(); err != nil {
				t.Fatalf("help: %v", err)
			}
			if !strings.Contains(help.String(), "--format string") || !strings.Contains(help.String(), `draft format: literal or rich-text (default "rich-text")`) {
				t.Fatalf("help does not document native default:\n%s", help.String())
			}

			blocks, err := slack.DraftBlocks("**native by default**", formatFlag.DefValue)
			if err != nil {
				t.Fatalf("DraftBlocks() error = %v", err)
			}
			encoded, err := json.Marshal(blocks)
			if err != nil {
				t.Fatalf("marshal blocks: %v", err)
			}
			if !strings.Contains(string(encoded), `"style":{"bold":true}`) {
				t.Fatalf("omitted --format did not produce a bold native text node: %s", encoded)
			}
		})
	}
}
