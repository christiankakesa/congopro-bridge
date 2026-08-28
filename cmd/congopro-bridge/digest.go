package main

import (
	"context"

	"github.com/jackc/pgx/v5/pgxpool"

	"congopro-bridge/internal/api"
	"congopro-bridge/internal/telegram"
)

// Daily staff digest, sent to the Telegram chat by `congopro-bridge -digest`
// under a systemd timer (deploy/systemd/congopro-bridge-digest.timer). A
// oneshot flag mode rather than an in-app ticker: Persistent=true covers
// downtime, a deploy restart can't eat the send, and the server keeps zero
// clock code. The gathering/formatting lives in internal/api (shared with
// the bot's /stats command).
func runDigest(ctx context.Context, pool *pgxpool.Pool, subs api.SubscriptionReader, tg telegram.Notifier) error {
	d, err := api.GatherDigest(ctx, pool, subs)
	if err != nil {
		return err
	}
	return tg.Send(ctx, api.FormatDigest(d))
}
