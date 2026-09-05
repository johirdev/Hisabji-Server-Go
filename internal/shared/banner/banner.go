// Package banner prints a colorful startup banner to the terminal so it's
// immediately obvious the server is up, which port it's on, which env it's
// running in, and whether the DB connected — without needing any extra
// dependency (plain ANSI escape codes).
//
// Place this file at: internal/shared/banner/banner.go
package banner

import (
	"fmt"
	"os"
	"time"
)

// ANSI color/style codes.
const (
	reset  = "\033[0m"
	bold   = "\033[1m"
	dim    = "\033[2m"
	red    = "\033[31m"
	green  = "\033[32m"
	yellow = "\033[33m"
	blue   = "\033[34m"
	purple = "\033[35m"
	cyan   = "\033[36m"
	white  = "\033[97m"
)

// Info is everything the banner needs to render.
type Info struct {
	AppName   string
	Port      string
	Env       string // "development" | "staging" | "production"
	DBOk      bool
	StartedAt time.Time
}

// noColor lets you disable colors (e.g. CI logs, piping to a file)
// by setting NO_COLOR=1 in the environment. https://no-color.org
func noColor() bool {
	return os.Getenv("NO_COLOR") != ""
}

// Print writes the banner to stdout.
func Print(info Info) {
	if noColor() {
		printPlain(info)
		return
	}

	envColor := green
	envLabel := "DEVELOPMENT"
	switch info.Env {
	case "production":
		envColor, envLabel = red, "PRODUCTION"
	case "staging":
		envColor, envLabel = yellow, "STAGING"
	case "development", "":
		envColor, envLabel = cyan, "DEVELOPMENT"
	default:
		envColor, envLabel = purple, info.Env
	}

	dbLine := fmt.Sprintf("%s✔ connected%s", green, reset)
	if !info.DBOk {
		dbLine = fmt.Sprintf("%s✘ not connected%s", red, reset)
	}

	divider := blue + "═══════════════════════════════════════════════════" + reset
	appName := info.AppName
	if appName == "" {
		appName = "Hisabji API"
	}

	fmt.Println()
	fmt.Println(divider)
	fmt.Printf("  %s%s🚀  %s is up and running!%s\n", bold, green, appName, reset)
	fmt.Println(divider)
	fmt.Printf("  %s🌐  Port       :%s  %shttp://localhost:%s%s\n", white, reset, bold, info.Port, reset)
	fmt.Printf("  %s🏷️   Environment:%s  %s%s%s\n", white, reset, envColor, envLabel, reset)
	fmt.Printf("  %s🗄️   Database   :%s  %s\n", white, reset, dbLine)
	fmt.Printf("  %s🕐  Started    :%s  %s%s%s\n", white, reset, dim, info.StartedAt.Format("2006-01-02 15:04:05"), reset)
	fmt.Println(divider)
	fmt.Println()
}

// printPlain is the same banner with no ANSI codes, for NO_COLOR environments.
func printPlain(info Info) {
	dbLine := "connected"
	if !info.DBOk {
		dbLine = "NOT connected"
	}
	appName := info.AppName
	if appName == "" {
		appName = "Hisabji API"
	}
	fmt.Println()
	fmt.Println("=====================================================")
	fmt.Printf("  %s is up and running!\n", appName)
	fmt.Println("=====================================================")
	fmt.Printf("  Port        : http://localhost:%s\n", info.Port)
	fmt.Printf("  Environment : %s\n", info.Env)
	fmt.Printf("  Database    : %s\n", dbLine)
	fmt.Printf("  Started     : %s\n", info.StartedAt.Format("2006-01-02 15:04:05"))
	fmt.Println("=====================================================")
	fmt.Println()
}
