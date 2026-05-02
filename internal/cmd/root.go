package cmd

import (
	"fmt"
	"os"

	"github.com/spf13/cobra"
	"github.com/spf13/viper"

	"slacli/internal/config"
	"slacli/internal/output"
)

var (
	cfgFile   string
	storeDir  string
	jsonOut   bool
	plainOut  bool
	quiet     bool
	verbose   bool
	noColor   bool
	Version   = "dev"
	BuildDate = "unknown"
)

var rootCmd = &cobra.Command{
	Use:     "slacli",
	Short:   "Agent-native Slack CLI with local FTS, sync, and messaging",
	Version: Version,
	Long: `slacli is a command-line interface for Slack designed for AI agents.

It provides local full-text search over synced messages, channel management,
and messaging capabilities with JSON output for machine consumption.`,
	PersistentPreRunE: func(cmd *cobra.Command, args []string) error {
		// Skip config init for help/version
		if cmd.Name() == "help" || cmd.Name() == "version" {
			return nil
		}

		// Initialize config
		if err := config.Init(storeDir); err != nil {
			return fmt.Errorf("config init: %w", err)
		}

		// Setup output formatter
		output.Setup(output.Options{
			JSON:    jsonOut,
			Plain:   plainOut,
			Quiet:   quiet,
			Verbose: verbose,
			NoColor: noColor || os.Getenv("NO_COLOR") != "",
		})

		return nil
	},
	SilenceUsage:  true,
	SilenceErrors: true,
}

func Execute() error {
	return rootCmd.Execute()
}

func init() {
	cobra.OnInitialize(initConfig)

	// Global flags
	rootCmd.PersistentFlags().StringVar(&cfgFile, "config", "", "config file (default $HOME/.slacli/config.json)")
	rootCmd.PersistentFlags().StringVar(&storeDir, "store", "", "data directory (default $HOME/.slacli)")
	rootCmd.PersistentFlags().BoolVar(&jsonOut, "json", false, "output in JSON format")
	rootCmd.PersistentFlags().BoolVar(&plainOut, "plain", false, "output in plain text (no formatting)")
	rootCmd.PersistentFlags().BoolVarP(&quiet, "quiet", "q", false, "suppress non-essential output")
	rootCmd.PersistentFlags().BoolVarP(&verbose, "verbose", "v", false, "enable debug output")
	rootCmd.PersistentFlags().BoolVar(&noColor, "no-color", false, "disable colored output")

	// Bind flags to viper
	cobra.CheckErr(viper.BindPFlag("store", rootCmd.PersistentFlags().Lookup("store")))
	cobra.CheckErr(viper.BindPFlag("output.json", rootCmd.PersistentFlags().Lookup("json")))
	cobra.CheckErr(viper.BindPFlag("output.plain", rootCmd.PersistentFlags().Lookup("plain")))

	// Add subcommands
	rootCmd.AddCommand(versionCmd)
	rootCmd.AddCommand(authCmd)
	rootCmd.AddCommand(syncCmd)
	rootCmd.AddCommand(channelsCmd)
	rootCmd.AddCommand(messagesCmd)
	rootCmd.AddCommand(mentionsCmd)
	rootCmd.AddCommand(sendCmd)
	rootCmd.AddCommand(draftCmd)
	rootCmd.AddCommand(draftsCmd)
	rootCmd.AddCommand(usersCmd)
	rootCmd.AddCommand(dbCmd)
	rootCmd.AddCommand(doctorCmd)
	rootCmd.AddCommand(configCmd)
	rootCmd.AddCommand(reactCmd)
	rootCmd.AddCommand(reactionsCmd)
	rootCmd.AddCommand(markCmd)
	rootCmd.AddCommand(remindersCmd)
}

func initConfig() {
	if cfgFile != "" {
		viper.SetConfigFile(cfgFile)
	} else {
		home, err := os.UserHomeDir()
		if err != nil {
			return
		}
		viper.AddConfigPath(home + "/.slacli")
		viper.SetConfigType("json")
		viper.SetConfigName("config")
	}

	viper.SetEnvPrefix("SLACLI")
	viper.AutomaticEnv()

	// Read config file (ignore errors - file may not exist)
	_ = viper.ReadInConfig()
}

var versionCmd = &cobra.Command{
	Use:   "version",
	Short: "Print version information",
	Run: func(cmd *cobra.Command, args []string) {
		fmt.Printf("slacli %s (built %s)\n", Version, BuildDate)
	},
}
