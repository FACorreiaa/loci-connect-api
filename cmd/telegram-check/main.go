// Command telegram-check proves that this deployment can actually talk to
// Telegram.
//
// Every test in internal/domain/messaging/telegram runs against a stand-in for
// the Bot API, which proves the adapter is correct and cannot prove a bot works.
// The difference is invisible from a green build, and this is the command that
// closes it: it asks Telegram who the token belongs to and how it is delivering
// updates, then compares that with how Loci is configured.
//
// Read-only by default. It changes nothing at Telegram, so it is safe to run
// against production; --send-to is the one flag that writes, and it says so.
//
// It reads TELEGRAM_* straight from the environment rather than through
// config.Load, which also validates JWT secrets, provider keys and the weather
// licence. A token check that fails on an unrelated variable is a token check
// that answers the wrong question.
package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"time"

	"github.com/joho/godotenv"

	"github.com/FACorreiaa/loci-connect-api/internal/domain/messaging/telegram"
	"github.com/FACorreiaa/loci-connect-api/pkg/config"
)

func main() {
	if err := run(os.Args[1:]); err != nil {
		fmt.Fprintln(os.Stderr, "telegram-check:", err)
		os.Exit(1)
	}
}

func run(args []string) error {
	fs := flag.NewFlagSet("telegram-check", flag.ContinueOnError)
	sendTo := fs.String("send-to", "", "chat id to send a test message to (writes; find it by messaging the bot first)")
	timeout := fs.Duration("timeout", 20*time.Second, "how long to allow for the whole check")
	fs.Usage = func() {
		fmt.Fprint(os.Stderr, `usage: telegram-check [flags]

Verifies TELEGRAM_BOT_TOKEN against the real Bot API and compares Telegram's
delivery mode with TELEGRAM_WEBHOOK_SECRET. Read-only unless --send-to is given.

flags:
`)
		fs.PrintDefaults()
	}
	if err := fs.Parse(args); err != nil {
		return err
	}

	_ = godotenv.Load()

	cfg := config.MessagingConfig{
		TelegramBotToken:      strings.TrimSpace(os.Getenv("TELEGRAM_BOT_TOKEN")),
		TelegramBotHandle:     strings.TrimSpace(os.Getenv("TELEGRAM_BOT_HANDLE")),
		TelegramWebhookSecret: strings.TrimSpace(os.Getenv("TELEGRAM_WEBHOOK_SECRET")),
	}
	if cfg.TelegramBotToken == "" {
		return errors.New("TELEGRAM_BOT_TOKEN is not set, so there is nothing to check; get a token from @BotFather")
	}

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	ctx, cancel := context.WithTimeout(ctx, *timeout)
	defer cancel()

	client := telegram.NewClient(cfg.TelegramBotToken, nil)

	// 1. Who does this token belong to?
	account, err := client.GetMe(ctx)
	if err != nil {
		return fmt.Errorf("getMe failed, so the token cannot reach Telegram: %w", err)
	}
	fmt.Printf("token ok        @%s (id %s)\n", account.Username, account.ID)

	// The configured handle is cosmetic — it is what Settings links people to
	// — but a mismatch sends people to the wrong bot, which looks exactly like
	// the link code being broken.
	if handle := strings.TrimPrefix(cfg.TelegramBotHandle, "@"); handle != "" && !strings.EqualFold(handle, account.Username) {
		fmt.Printf("MISMATCH        TELEGRAM_BOT_HANDLE is %q but the token belongs to @%s;\n"+
			"                Settings would send people to the wrong bot\n", cfg.TelegramBotHandle, account.Username)
	}

	// 2. Where does Telegram think it should deliver updates?
	hook, err := client.GetWebhookInfo(ctx)
	if err != nil {
		return fmt.Errorf("getWebhookInfo failed: %w", err)
	}
	reportDeliveryMode(cfg, hook)

	// 3. Optional write.
	if *sendTo != "" {
		if err := client.SendMessage(ctx, *sendTo, "Loci is connected. This is a test message from telegram-check."); err != nil {
			return fmt.Errorf("sendMessage to %s failed: %w", *sendTo, err)
		}
		fmt.Printf("send ok         a test message was delivered to chat %s\n", *sendTo)
	}

	fmt.Println("\nThe token works and the Bot API answered. What this does NOT prove:")
	fmt.Println("  - that a person can link an account (Settings > Connections > Get a link code)")
	fmt.Println("  - that a real reply arrives (needs one real conversation)")
	return nil
}

// reportDeliveryMode compares what Loci is configured to do with what Telegram
// is actually set up to do.
//
// This is the check worth having. The two modes are mutually exclusive at
// Telegram's end — while a webhook is registered, getUpdates is refused outright
// — so a leftover webhook makes polling look broken for a reason nothing in the
// polling path can see, and a missing webhook makes production look dead while
// every test still passes.
func reportDeliveryMode(cfg config.MessagingConfig, hook telegram.WebhookInfo) {
	switch {
	case cfg.UsesWebhook() && hook.Set():
		fmt.Printf("delivery ok     webhook mode, Telegram posts to %s\n", hook.URL)

	case cfg.UsesWebhook() && !hook.Set():
		fmt.Println("PROBLEM         TELEGRAM_WEBHOOK_SECRET is set, so Loci serves a webhook and does not poll,")
		fmt.Println("                but Telegram has no webhook registered. Nothing will ever arrive.")
		fmt.Println("                Fix: call setWebhook (see docs/telegram-setup.md).")

	case !cfg.UsesWebhook() && hook.Set():
		fmt.Printf("PROBLEM         Telegram still has a webhook registered (%s), but Loci is in polling mode.\n", hook.URL)
		fmt.Println("                Telegram refuses getUpdates while a webhook exists, so every poll fails.")
		fmt.Println("                Fix: call deleteWebhook, or set TELEGRAM_WEBHOOK_SECRET to run in webhook mode.")

	default:
		fmt.Println("delivery ok     polling mode, and Telegram has no webhook registered")
	}

	// Independent of the mode: these are Telegram telling you deliveries are
	// failing, and they are the fastest answer to "why is nothing arriving".
	if hook.PendingUpdateCount > 0 {
		fmt.Printf("                %d update(s) queued at Telegram\n", hook.PendingUpdateCount)
	}
	if hook.LastErrorMessage != "" {
		when := "unknown time"
		if hook.LastErrorDate > 0 {
			when = time.Unix(hook.LastErrorDate, 0).Format(time.RFC3339)
		}
		fmt.Printf("                last delivery error (%s): %s\n", when, hook.LastErrorMessage)
	}
}
