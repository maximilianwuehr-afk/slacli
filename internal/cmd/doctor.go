package cmd

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	"github.com/spf13/cobra"

	"slacli/internal/auth"
	"slacli/internal/config"
	"slacli/internal/db"
	"slacli/internal/output"
	"slacli/internal/slack"
)

var doctorCmd = &cobra.Command{
	Use:   "doctor",
	Short: "Run diagnostics",
	Long:  `Check auth status, database integrity, sync state, and API connectivity.`,
	RunE:  runDoctor,
}

func runDoctor(cmd *cobra.Command, args []string) error {
	cfg := config.Get()
	checks := []output.DoctorCheck{}

	// Check 1: Config directory
	configCheck := output.DoctorCheck{
		Name:   "Config directory",
		Status: "ok",
	}
	if err := config.EnsureDir(cfg); err != nil {
		configCheck.Status = "error"
		configCheck.Message = err.Error()
	} else {
		configCheck.Message = cfg.StoreDir
	}
	checks = append(checks, configCheck)

	checks = append(checks, executableDoctorChecks()...)

	// Check 2: Auth status
	authCheck := output.DoctorCheck{
		Name:   "Authentication",
		Status: "ok",
	}
	creds, err := auth.LoadCredentials(cfg)
	if err != nil {
		authCheck.Status = "error"
		authCheck.Message = "Not authenticated (run: slacli auth)"
	} else if creds.IsExpired() {
		authCheck.Status = "warning"
		authCheck.Message = fmt.Sprintf("Token expired at %s (run: slacli auth --refresh)", creds.ExpiresAt)
	} else {
		authCheck.Message = fmt.Sprintf("Valid until %s", creds.ExpiresAt)
	}
	checks = append(checks, authCheck)

	// Check 3: Database
	dbCheck := output.DoctorCheck{
		Name:   "Database",
		Status: "ok",
	}
	store, err := db.Open(cfg)
	if err != nil {
		dbCheck.Status = "error"
		dbCheck.Message = err.Error()
	} else {
		stats, err := store.Stats()
		if err != nil {
			dbCheck.Status = "error"
			dbCheck.Message = err.Error()
		} else {
			dbCheck.Message = fmt.Sprintf("%d channels, %d messages, %d users (%.1f MB)",
				stats.ChannelCount, stats.MessageCount, stats.UserCount, float64(stats.SizeBytes)/(1024*1024))
		}
		_ = store.Close()
	}
	checks = append(checks, dbCheck)

	// Check 4: Sync state
	syncCheck := output.DoctorCheck{
		Name:   "Sync state",
		Status: "ok",
	}
	if store != nil {
		state, err := db.LoadSyncState(cfg)
		if err != nil {
			syncCheck.Status = "warning"
			syncCheck.Message = "Never synced (run: slacli sync)"
		} else {
			syncCheck.Message = fmt.Sprintf("Last sync: %s", state.LastSync)
		}
	}
	checks = append(checks, syncCheck)

	// Check 5: API connectivity
	apiCheck := output.DoctorCheck{
		Name:   "Slack API",
		Status: "ok",
	}
	if creds != nil && !creds.IsExpired() {
		client, _ := auth.GetClient(cfg)
		if client != nil {
			api := slack.NewAPI(client)
			if err := api.Test(); err != nil {
				apiCheck.Status = "error"
				apiCheck.Message = err.Error()
			} else {
				apiCheck.Message = "Connected"
			}
		}
	} else {
		apiCheck.Status = "skip"
		apiCheck.Message = "Skipped (not authenticated)"
	}
	checks = append(checks, apiCheck)

	output.Print(output.DoctorResult{Checks: checks})
	return nil
}

func executableDoctorChecks() []output.DoctorCheck {
	checks := []output.DoctorCheck{}

	current, err := os.Executable()
	if err != nil || current == "" {
		return []output.DoctorCheck{{
			Name:    "CLI executable",
			Status:  "warning",
			Message: "Could not determine current executable",
		}}
	}

	target := executableInstallTarget(current)
	version, versionErr := slacliVersionOutput(target)
	execCheck := output.DoctorCheck{
		Name:   "CLI executable",
		Status: "ok",
	}
	if versionErr != nil {
		execCheck.Status = "error"
		execCheck.Message = fmt.Sprintf("%s (%v)", target, versionErr)
	} else {
		execCheck.Message = fmt.Sprintf("%s (%s)", target, version)
	}
	checks = append(checks, execCheck)

	checks = append(checks, slacliAliasDoctorCheck(target))
	checks = append(checks, pathResolutionDoctorCheck(target))
	checks = append(checks, duplicateInstallsDoctorCheck(target, version))
	return checks
}

func slacliAliasDoctorCheck(target string) output.DoctorCheck {
	aliasPath := filepath.Join(filepath.Dir(target), executableName("slacli"))
	check := output.DoctorCheck{
		Name:   "CLI symlink",
		Status: "ok",
	}
	if samePath(aliasPath, target) {
		check.Message = "Current executable is slacli"
		return check
	}
	if sameExecutable(aliasPath, target) {
		check.Message = fmt.Sprintf("%s -> %s", aliasPath, target)
		return check
	}

	check.Status = "warning"
	if _, err := os.Lstat(aliasPath); os.IsNotExist(err) {
		check.Message = fmt.Sprintf("%s missing (run: slacli upgrade)", aliasPath)
	} else {
		check.Message = fmt.Sprintf("%s does not resolve to %s (run: slacli upgrade)", aliasPath, target)
	}
	return check
}

func pathResolutionDoctorCheck(target string) output.DoctorCheck {
	check := output.DoctorCheck{
		Name:   "CLI PATH",
		Status: "ok",
	}
	path, err := exec.LookPath(executableName("slacli"))
	if err != nil {
		check.Status = "warning"
		check.Message = "slacli not found on PATH"
		return check
	}
	if !sameExecutable(path, target) {
		check.Status = "warning"
		check.Message = fmt.Sprintf("PATH resolves slacli to %s, current target is %s", path, target)
		return check
	}
	check.Message = fmt.Sprintf("slacli resolves to %s", path)
	return check
}

func duplicateInstallsDoctorCheck(target, currentVersion string) output.DoctorCheck {
	check := output.DoctorCheck{
		Name:   "CLI installs",
		Status: "ok",
	}

	type installInfo struct {
		path    string
		version string
	}
	installs := []installInfo{}
	seen := map[string]bool{}
	for _, candidate := range slacliInstallCandidates() {
		absCandidate, err := filepath.Abs(candidate)
		if err != nil {
			absCandidate = candidate
		}
		if seen[absCandidate] {
			continue
		}
		seen[absCandidate] = true
		if !looksLikeSlacli(absCandidate) {
			continue
		}
		version, err := slacliVersionOutput(absCandidate)
		if err != nil {
			version = "unhealthy: " + err.Error()
		}
		installs = append(installs, installInfo{path: absCandidate, version: version})
	}

	if len(installs) == 0 {
		check.Status = "warning"
		check.Message = "No slacli/slack executable found on PATH"
		return check
	}

	stale := []string{}
	for _, install := range installs {
		if currentVersion != "" && install.version != currentVersion {
			stale = append(stale, fmt.Sprintf("%s (%s)", install.path, install.version))
		}
	}
	if len(stale) > 0 {
		check.Status = "warning"
		check.Message = fmt.Sprintf("Version mismatch; current target %s is %s; stale: %s", target, currentVersion, strings.Join(stale, "; "))
		return check
	}

	check.Message = fmt.Sprintf("%d slacli/slack PATH entries match %s", len(installs), currentVersion)
	return check
}
