// Command telex runs Telegram Explorer.
package main

import (
	"bufio"
	"context"
	"errors"
	"flag"
	"fmt"
	"log"
	"net/http"
	"os"
	"os/signal"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"time"

	webui "telegram-explorer/web"

	"telegram-explorer/internal/auth"
	"telegram-explorer/internal/files"
	"telegram-explorer/internal/sftpd"
	"telegram-explorer/internal/store"
	"telegram-explorer/internal/telegram"
	"telegram-explorer/internal/vault"
	"telegram-explorer/internal/web"
)

func env(key, fallback string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return fallback
}

func main() {
	if len(os.Args) > 1 && os.Args[1] == "restore" {
		if err := restore(os.Args[2:]); err != nil {
			log.Fatal(err)
		}
		return
	}
	if err := run(); err != nil {
		log.Fatal(err)
	}
}

func run() error {
	dataDir := env("DATA_DIR", "/data")
	if err := os.MkdirAll(dataDir, 0o700); err != nil {
		return err
	}
	st, err := store.Open(filepath.Join(dataDir, "telex.db"))
	if err != nil {
		return err
	}
	defer st.Close()

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	var backend files.Backend
	var tgClient *telegram.Client
	engine, err := files.New(st, nil, dataDir)
	if err != nil {
		return err
	}
	switch env("STORAGE_BACKEND", "telegram") {
	case "telegram":
		appID, err := strconv.Atoi(os.Getenv("TG_APP_ID"))
		if err != nil || os.Getenv("TG_APP_HASH") == "" {
			return errors.New("TG_APP_ID und TG_APP_HASH müssen gesetzt sein")
		}
		tgClient = &telegram.Client{AppID: appID, AppHash: os.Getenv("TG_APP_HASH"), Store: st, Vault: engine.Vault}
		backend = tgClient
	case "local":
		log.Print("ACHTUNG: lokaler Testspeicher aktiv, Dateien gehen nicht zu Telegram")
		if backend, err = files.NewLocalBackend(filepath.Join(dataDir, "local-backend")); err != nil {
			return err
		}
	default:
		return errors.New("STORAGE_BACKEND muss telegram oder local sein")
	}
	engine.Backend = backend

	manager := auth.New(st)
	sftpServer := &sftpd.Server{Addr: env("SFTP_ADDR", ":2222"), Engine: engine, Auth: manager}

	var once sync.Once
	onUnlock := func() {
		once.Do(func() {
			engine.Start(ctx)
			if tgClient != nil {
				go tgClient.Run(ctx)
			}
			go func() {
				if err := sftpServer.Run(ctx); err != nil {
					log.Printf("SFTP: %v", err)
				}
			}()
		})
	}

	server := &web.Server{Engine: engine, Auth: manager, UI: webui.FS(), OnUnlock: onUnlock, OnSFTPChange: sftpServer.Sync,
		TrustProxy: os.Getenv("TRUST_PROXY") == "1", SetupCode: auth.Token(9)}
	if tgClient != nil {
		server.Telegram = tgClient
	}
	if user, err := manager.User(); err != nil {
		return err
	} else if user == nil {
		log.Printf("Noch nicht eingerichtet. Einrichtungscode: %s", server.SetupCode)
	}

	httpServer := &http.Server{
		Addr:              env("HTTP_ADDR", ":8080"),
		Handler:           server.Handler(),
		ReadHeaderTimeout: 10 * time.Second,
		IdleTimeout:       2 * time.Minute,
	}
	go func() {
		<-ctx.Done()
		shutdown, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		httpServer.Shutdown(shutdown)
	}()
	log.Printf("Web lauscht auf %s", httpServer.Addr)
	if err := httpServer.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
		return err
	}
	return nil
}

// restore replaces the database with a backup taken from the storage channel.
func restore(args []string) error {
	fs := flag.NewFlagSet("restore", flag.ExitOnError)
	dataDir := fs.String("data", env("DATA_DIR", "/data"), "Datenverzeichnis")
	fs.Parse(args)
	if fs.NArg() != 1 {
		return errors.New("Aufruf: telex restore <backup.telexbk>")
	}
	target := filepath.Join(*dataDir, "telex.db")
	if _, err := os.Stat(target); err == nil {
		return fmt.Errorf("%s existiert bereits; zuerst sichern und entfernen", target)
	}
	backup, err := os.ReadFile(fs.Arg(0))
	if err != nil {
		return err
	}
	passphrase := os.Getenv("TELEX_PASSPHRASE")
	if passphrase == "" {
		fmt.Print("Passphrase: ")
		line, err := bufio.NewReader(os.Stdin).ReadString('\n')
		if err != nil {
			return err
		}
		passphrase = strings.TrimRight(line, "\r\n")
	}
	db, err := vault.OpenBackup(backup, passphrase)
	if err != nil {
		return err
	}
	if err := os.MkdirAll(*dataDir, 0o700); err != nil {
		return err
	}
	if err := os.WriteFile(target, db, 0o600); err != nil {
		return err
	}
	fmt.Println("Wiederhergestellt:", target)
	return nil
}
