// Command createadmin creates (or updates) a super admin account.
//
//	go run ./cmd/createadmin -username admin -email admin@example.com
//
// The password comes from -password, then ADMIN_PASSWORD, then a line on
// stdin. Other settings (DATABASE_URL, REDIS_URL, ...) load like cmd/app.
package main

import (
	"bufio"
	"context"
	"errors"
	"flag"
	"fmt"
	"os"
	"strings"

	"go.uber.org/zap"

	"github.com/DevOpsXLab/iblog-monolith-go/app"
	"github.com/DevOpsXLab/iblog-monolith-go/config"
)

func main() {
	logCfg := zap.NewDevelopmentConfig()
	logCfg.Level = zap.NewAtomicLevelAt(zap.WarnLevel)
	logCfg.DisableStacktrace = true
	zap.ReplaceGlobals(zap.Must(logCfg.Build()))
	if err := run(); err != nil {
		fmt.Fprintln(os.Stderr, "createadmin:", err)
		os.Exit(1)
	}
}

func run() error {
	username := flag.String("username", "", "super admin username (required)")
	email := flag.String("email", "", "super admin email (required)")
	password := flag.String("password", "", "password; prefer ADMIN_PASSWORD or stdin to keep it out of shell history")
	flag.Parse()
	if *username == "" || *email == "" {
		flag.Usage()
		return errors.New("-username and -email are required")
	}

	pw := *password
	if pw == "" {
		pw = os.Getenv("ADMIN_PASSWORD")
	}
	if pw == "" {
		fmt.Fprint(os.Stderr, "password: ")
		line, err := bufio.NewReader(os.Stdin).ReadString('\n')
		if err != nil && line == "" {
			return fmt.Errorf("read password: %w", err)
		}
		pw = strings.TrimRight(line, "\r\n")
	}
	if pw == "" {
		return errors.New("empty password")
	}

	cfg, err := config.Load()
	if err != nil {
		return err
	}
	// app.New seeds the admin from these and runs migrations; no worker.
	cfg.AdminUsername, cfg.AdminEmail, cfg.AdminPassword = *username, *email, pw
	cfg.Worker = false

	a, err := app.New(context.Background(), cfg)
	if err != nil {
		return err
	}
	a.Close()
	fmt.Printf("super admin %q ready\n", *username)
	return nil
}
