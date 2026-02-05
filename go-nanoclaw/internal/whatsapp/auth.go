// Package whatsapp provides WhatsApp connectivity using whatsmeow.
package whatsapp

import (
	"context"
	"fmt"
	"os"
	"os/signal"
	"path/filepath"
	"syscall"

	"go.mau.fi/whatsmeow"
	"go.mau.fi/whatsmeow/store/sqlstore"
	waLog "go.mau.fi/whatsmeow/util/log"

	"github.com/skip2/go-qrcode"

	"github.com/nanoclaw/go-nanoclaw/internal/config"
)

// Auth handles WhatsApp authentication via QR code.
type Auth struct {
	cfg       *config.Config
	client    *whatsmeow.Client
	container *sqlstore.Container
}

// NewAuth creates a new auth handler.
func NewAuth(cfg *config.Config) (*Auth, error) {
	// Set up logging
	dbLog := waLog.Noop

	// Create store directory
	storeDir := cfg.AuthDir()
	if err := os.MkdirAll(storeDir, 0755); err != nil {
		return nil, fmt.Errorf("failed to create store directory: %w", err)
	}

	// Open SQLite store for credentials
	dbPath := filepath.Join(storeDir, "whatsapp.db")
	container, err := sqlstore.New("sqlite3", "file:"+dbPath+"?_foreign_keys=on", dbLog)
	if err != nil {
		return nil, fmt.Errorf("failed to create store: %w", err)
	}

	// Get or create device store
	deviceStore, err := container.GetFirstDevice()
	if err != nil {
		return nil, fmt.Errorf("failed to get device: %w", err)
	}

	// Create client
	client := whatsmeow.NewClient(deviceStore, dbLog)

	return &Auth{
		cfg:       cfg,
		client:    client,
		container: container,
	}, nil
}

// Close closes the auth handler.
func (a *Auth) Close() error {
	return a.container.Close()
}

// Run runs the authentication process.
func (a *Auth) Run(ctx context.Context) error {
	// Check if already logged in
	if a.client.Store.ID != nil {
		fmt.Println("Already logged in as:", a.client.Store.ID.String())
		fmt.Println("To re-authenticate, delete the auth store and try again.")
		return nil
	}

	// Get QR channel
	qrChan, _ := a.client.GetQRChannel(ctx)

	// Connect
	if err := a.client.Connect(); err != nil {
		return fmt.Errorf("failed to connect: %w", err)
	}
	defer a.client.Disconnect()

	fmt.Println("Scan the QR code with WhatsApp to authenticate:")
	fmt.Println()

	// Set up signal handling
	sigChan := make(chan os.Signal, 1)
	signal.Notify(sigChan, syscall.SIGINT, syscall.SIGTERM)

	// Wait for QR code or login
	for {
		select {
		case <-ctx.Done():
			return ctx.Err()

		case <-sigChan:
			fmt.Println("\nAuthentication cancelled")
			return nil

		case evt := <-qrChan:
			if evt.Event == "code" {
				// Print QR code to terminal
				a.printQRCode(evt.Code)
			} else if evt.Event == "success" {
				fmt.Println()
				fmt.Println("Authentication successful!")
				fmt.Println("You can now run the main application.")
				return nil
			} else if evt.Event == "timeout" {
				fmt.Println("QR code timed out, generating new one...")
			}
		}
	}
}

// printQRCode prints a QR code to the terminal.
func (a *Auth) printQRCode(code string) {
	// Generate QR code
	qr, err := qrcode.New(code, qrcode.Medium)
	if err != nil {
		fmt.Println("Error generating QR code:", err)
		fmt.Println("Raw code:", code)
		return
	}

	// Print to terminal using Unicode blocks
	art := qr.ToSmallString(false)
	fmt.Println(art)
}

// IsLoggedIn checks if the client is logged in.
func (a *Auth) IsLoggedIn() bool {
	return a.client.Store.ID != nil
}

// Logout logs out the current session.
func (a *Auth) Logout() error {
	if a.client.Store.ID == nil {
		return fmt.Errorf("not logged in")
	}

	return a.client.Logout()
}
