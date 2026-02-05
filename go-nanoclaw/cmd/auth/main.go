// Package main provides the WhatsApp authentication command.
package main

import (
	"context"
	"fmt"
	"os"

	_ "github.com/mattn/go-sqlite3" // SQLite driver

	"github.com/nanoclaw/go-nanoclaw/internal/config"
	"github.com/nanoclaw/go-nanoclaw/internal/logger"
	"github.com/nanoclaw/go-nanoclaw/internal/whatsapp"
)

var log = logger.WithComponent("auth")

func main() {
	fmt.Println("NanoClaw WhatsApp Authentication")
	fmt.Println("=================================")
	fmt.Println()

	// Load configuration
	cfg := config.Load()

	// Create auth handler
	auth, err := whatsapp.NewAuth(cfg)
	if err != nil {
		log.Fatal().Err(err).Msg("Failed to initialize auth")
		os.Exit(1)
	}
	defer auth.Close()

	// Check if already logged in
	if auth.IsLoggedIn() {
		fmt.Println("You are already logged in!")
		fmt.Println()
		fmt.Println("To log out and re-authenticate, run with --logout flag")
		fmt.Println("or delete the store directory:", cfg.AuthDir())
		return
	}

	// Run authentication
	ctx := context.Background()
	if err := auth.Run(ctx); err != nil {
		log.Fatal().Err(err).Msg("Authentication failed")
		os.Exit(1)
	}
}
