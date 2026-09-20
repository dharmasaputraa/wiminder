package api

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"

	"wiminder/internal/config"
	"wiminder/internal/secret"
	"wiminder/internal/store"
)

// SeedDevTelegram is a dev-only env seeder: with AUTH_MODE=dev and
// DEV_SEED_TELEGRAM_BOT_TOKEN + DEV_SEED_TELEGRAM_CHAT_ID set, it makes sure
// the first ADMIN_EMAILS entry owns a Telegram channel with those credentials,
// so a fresh dev database can deliver reminders without manual channel setup.
// Upsert semantics: the "Telegram (dev)" channel is unique across ALL users
// (its name is owned by the seeder) — an existing one gets its credentials and
// owner refreshed so env changes take effect on restart, instead of creating
// duplicates when the first admin email changes. Extra copies left over from
// older seeder versions are collapsed back down to one.
func SeedDevTelegram(ctx context.Context, st *store.Store, key []byte, cfg config.Config) (bool, error) {
	if cfg.AuthMode != config.AuthDev || cfg.DevSeedTelegramBotToken == "" || cfg.DevSeedTelegramChatID == "" {
		return false, nil
	}
	admin := ""
	for email := range cfg.AdminEmails {
		if admin == "" || email < admin {
			admin = email
		}
	}
	if admin == "" {
		return false, errors.New("DEV_SEED_TELEGRAM_* needs at least one ADMIN_EMAILS entry to own the channel")
	}
	u, err := st.GetOrCreateUser(ctx, admin, admin, cfg.AdminEmails)
	if err != nil {
		return false, fmt.Errorf("provision seed user: %w", err)
	}
	const name = "Telegram (dev)"
	all, err := st.ListChannels(ctx, "")
	if err != nil {
		return false, err
	}
	var keep *store.Channel
	var extras []string
	for i := range all {
		ch := all[i]
		if ch.Type != "telegram" || ch.Name != name {
			continue
		}
		switch {
		case keep == nil || ch.OwnerID == u.ID:
			if keep != nil {
				extras = append(extras, keep.ID)
			}
			keep = &all[i]
		default:
			extras = append(extras, ch.ID)
		}
	}
	raw, err := json.Marshal(map[string]string{
		"bot_token": cfg.DevSeedTelegramBotToken,
		"chat_id":   cfg.DevSeedTelegramChatID,
	})
	if err != nil {
		return false, err
	}
	enc, err := secret.Encrypt(key, raw)
	if err != nil {
		return false, err
	}
	if keep != nil {
		if err := st.UpdateChannelConfig(ctx, keep.ID, u.ID, enc); err != nil {
			return false, err
		}
		for _, id := range extras {
			if err := st.DeleteChannel(ctx, "", id); err != nil {
				return false, err
			}
			slog.Info("dev seed: duplicate telegram channel removed", "channel_id", id, "name", name)
		}
		if len(extras) > 0 {
			slog.Info("dev seed: telegram channel updated", "owner", admin, "name", name)
		}
		return true, nil
	}
	if _, err := st.CreateChannel(ctx, u.ID, "telegram", name, enc); err != nil {
		return false, err
	}
	slog.Info("dev seed: telegram channel created", "owner", admin, "name", name)
	return true, nil
}
