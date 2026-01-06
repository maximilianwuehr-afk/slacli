package cmd

import (
	"bufio"
	"fmt"
	"os"
	"strings"

	"github.com/spf13/cobra"

	"slacli/internal/auth"
	"slacli/internal/config"
	"slacli/internal/output"
	"slacli/internal/slack"
)

var sendCmd = &cobra.Command{
	Use:   "send <channel> [text]",
	Short: "Send a message to a channel or DM",
	Long: `Send a message to a channel or DM.

Channel can be:
  - #channel-name
  - @username
  - user@email.com (for DM)
  - Channel ID (C...)`,
	Args: cobra.RangeArgs(1, 2),
	RunE: runSend,
}

var (
	sendThread string
	sendFile   string
	sendStdin  bool
)

func init() {
	sendCmd.Flags().StringVar(&sendThread, "thread", "", "reply to thread (message timestamp)")
	sendCmd.Flags().StringVar(&sendFile, "file", "", "attach file")
	sendCmd.Flags().BoolVar(&sendStdin, "stdin", false, "read message from stdin")
}

func runSend(cmd *cobra.Command, args []string) error {
	cfg := config.Get()

	client, err := auth.GetClient(cfg)
	if err != nil {
		return fmt.Errorf("auth required: %w", err)
	}

	channel := args[0]
	var text string

	if sendStdin {
		// Read from stdin
		var lines []string
		scanner := bufio.NewScanner(os.Stdin)
		for scanner.Scan() {
			lines = append(lines, scanner.Text())
		}
		if err := scanner.Err(); err != nil {
			return fmt.Errorf("read stdin: %w", err)
		}
		text = strings.Join(lines, "\n")
	} else if len(args) > 1 {
		text = args[1]
	} else if sendFile == "" {
		return fmt.Errorf("message text required (use --stdin to read from stdin)")
	}

	api := slack.NewAPI(client)

	// Resolve channel
	channelID, err := api.ResolveChannel(channel)
	if err != nil {
		return fmt.Errorf("resolve channel: %w", err)
	}

	// Upload file if specified
	if sendFile != "" {
		result, err := api.UploadFile(channelID, sendFile, text)
		if err != nil {
			return fmt.Errorf("upload file: %w", err)
		}
		output.Print(result)
		return nil
	}

	// Send message
	result, err := api.SendMessage(channelID, text, sendThread)
	if err != nil {
		return fmt.Errorf("send message: %w", err)
	}

	output.Print(result)
	return nil
}
