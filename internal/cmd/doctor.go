package cmd

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

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
	Long:  `Check auth status, database integrity, sync freshness, store permissions, and API connectivity.`,
	RunE:  runDoctor,
}

func runDoctor(cmd *cobra.Command, args []string) error {
	cfg := config.Get()
	configDirErr := config.EnsureDir(cfg)
	checks := []output.DoctorCheck{}
	checks = append(checks, permissionDoctorCheck(cfg))

	// Check 1: Config directory
	configCheck := output.DoctorCheck{
		Name:   "Config directory",
		Status: "ok",
	}
	if configDirErr != nil {
		configCheck.Status = "error"
		configCheck.Message = configDirErr.Error()
	} else {
		configCheck.Message = cfg.StoreDir
	}
	checks = append(checks, configCheck)

	checks = append(checks, executableDoctorChecks()...)

	// Check 2: Auth status
	creds, err := auth.LoadCredentials(cfg)
	checks = append(checks, authenticationDoctorCheck(creds, err))

	// Check 3: Database
	dbCheck := output.DoctorCheck{
		Name:   "Database",
		Status: "ok",
	}
	var store *db.Store
	store, err = db.Open(cfg)
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
		syncCheck = syncStateDoctorCheck(state, err, time.Now())
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

func authenticationDoctorCheck(creds *auth.Credentials, err error) output.DoctorCheck {
	check := output.DoctorCheck{Name: "Authentication", Status: "ok"}
	if err != nil {
		check.Status = "error"
		check.Message = "Not authenticated (run: slacli auth)"
		return check
	}
	if creds.IsExpired() {
		check.Status = "warning"
		check.Message = fmt.Sprintf("Token expired at %s (run: slacli auth --refresh)", creds.ExpiresAt)
		return check
	}
	if creds.ExpiresAt == "" {
		check.Message = "Token expiry is not set"
		return check
	}
	check.Message = fmt.Sprintf("Valid until %s", creds.ExpiresAt)
	return check
}

func syncStateDoctorCheck(state *db.SyncState, err error, now time.Time) output.DoctorCheck {
	check := output.DoctorCheck{Name: "Sync state", Status: "ok"}
	if err != nil || state == nil || state.LastSync == "" {
		check.Status = "warning"
		check.Message = "Never synced (run: slacli sync)"
		return check
	}
	lastSync, err := time.Parse(time.RFC3339, state.LastSync)
	if err != nil {
		check.Status = "warning"
		check.Message = "Last sync time is invalid (run: slacli sync)"
		return check
	}
	if age := now.Sub(lastSync); age > 24*time.Hour {
		check.Status = "warning"
		check.Message = fmt.Sprintf("Last sync is older than 24 hours (%s ago; run: slacli sync)", age.Round(time.Hour))
		return check
	}
	check.Message = fmt.Sprintf("Last sync: %s", state.LastSync)
	return check
}

func permissionDoctorCheck(cfg *config.Config) output.DoctorCheck {
	check := output.DoctorCheck{Name: "Store permissions", Status: "ok"}
	issues, err := config.CheckPermissions(cfg)
	if err != nil {
		check.Status = "error"
		check.Message = err.Error()
		return check
	}
	if len(issues) == 0 {
		check.Message = "Store permissions are secure (0700 directory, 0600 files)"
		return check
	}

	if err := config.RepairPermissions(cfg); err != nil {
		check.Status = "error"
		check.Message = fmt.Sprintf("Could not repair store permissions: %v", err)
		return check
	}
	remaining, err := config.CheckPermissions(cfg)
	if err != nil {
		check.Status = "error"
		check.Message = fmt.Sprintf("Could not verify repaired store permissions: %v", err)
		return check
	}
	if len(remaining) > 0 {
		check.Status = "warning"
		check.Message = "Store permissions still need attention after repair"
		return check
	}
	check.Message = fmt.Sprintf("Repaired permissions for %d store entries (0700 directory, 0600 files)", len(issues))
	return check
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
