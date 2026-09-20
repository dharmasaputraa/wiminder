package api

import (
	"context"
	"encoding/json"
	"testing"

	"wiminder/internal/config"
	"wiminder/internal/secret"
	"wiminder/internal/store"
)

func devSeedConfig(botToken, chatID, firstAdmin string) config.Config {
	return config.Config{
		AuthMode:                config.AuthDev,
		AdminEmails:             map[string]bool{firstAdmin: true, "zz@fallback.dev": true},
		DevSeedTelegramBotToken: botToken,
		DevSeedTelegramChatID:   chatID,
	}
}

func devSeedChannels(t *testing.T, st *store.Store) []store.Channel {
	t.Helper()
	chans, err := st.ListChannels(context.Background(), "")
	if err != nil {
		t.Fatal(err)
	}
	return chans
}

func devSeedTelegramChannels(chans []store.Channel) []store.Channel {
	var out []store.Channel
	for _, ch := range chans {
		if ch.Type == "telegram" && ch.Name == "Telegram (dev)" {
			out = append(out, ch)
		}
	}
	return out
}

func TestSeedDevTelegramCreatesOnce(t *testing.T) {
	st, err := store.OpenInMemory()
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	if err := st.Migrate(); err != nil {
		t.Fatal(err)
	}
	key := secret.DeriveKey("test-secret-at-least-16-ch")
	cfg := devSeedConfig("tok1", "chat1", "a@x.dev")

	for i := range 3 {
		if _, err := SeedDevTelegram(context.Background(), st, key, cfg); err != nil {
			t.Fatalf("run %d: %v", i, err)
		}
	}
	if got := devSeedTelegramChannels(devSeedChannels(t, st)); len(got) != 1 {
		t.Fatalf("want exactly 1 dev channel after repeated seeding, got %d", len(got))
	}
}

func TestSeedDevTelegramUpsertsWhenAdminEmailChanges(t *testing.T) {
	st, err := store.OpenInMemory()
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	if err := st.Migrate(); err != nil {
		t.Fatal(err)
	}
	key := secret.DeriveKey("test-secret-at-least-16-ch")

	if _, err := SeedDevTelegram(context.Background(), st, key, devSeedConfig("tok1", "chat1", "a@x.dev")); err != nil {
		t.Fatal(err)
	}

	// First admin email changes (e.g. project rename) and credentials rotate:
	// the existing dev channel must be updated, not duplicated.
	if _, err := SeedDevTelegram(context.Background(), st, key, devSeedConfig("tok2", "chat2", "b@x.dev")); err != nil {
		t.Fatal(err)
	}

	chans := devSeedTelegramChannels(devSeedChannels(t, st))
	if len(chans) != 1 {
		t.Fatalf("want exactly 1 dev channel after admin email change, got %d", len(chans))
	}
	u, err := st.GetOrCreateUser(context.Background(), "b@x.dev", "b@x.dev", nil)
	if err != nil {
		t.Fatal(err)
	}
	if chans[0].OwnerID != u.ID {
		t.Fatalf("dev channel owner = user %s, want new first admin %q (user %s)", chans[0].OwnerID, "b@x.dev", u.ID)
	}
	raw, err := secret.Decrypt(key, chans[0].ConfigEnc)
	if err != nil {
		t.Fatal(err)
	}
	var creds map[string]string
	if err := json.Unmarshal(raw, &creds); err != nil {
		t.Fatal(err)
	}
	if creds["bot_token"] != "tok2" || creds["chat_id"] != "chat2" {
		t.Fatalf("dev channel credentials not refreshed: got %v", creds)
	}
}

func TestSeedDevTelegramCollapsesExistingDuplicates(t *testing.T) {
	st, err := store.OpenInMemory()
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	if err := st.Migrate(); err != nil {
		t.Fatal(err)
	}
	key := secret.DeriveKey("test-secret-at-least-16-ch")
	ctx := context.Background()
	cfg := devSeedConfig("tok1", "chat1", "b@x.dev")
	admin, err := st.GetOrCreateUser(ctx, "b@x.dev", "b@x.dev", cfg.AdminEmails)
	if err != nil {
		t.Fatal(err)
	}
	stale, err := st.GetOrCreateUser(ctx, "old@x.dev", "old@x.dev", nil)
	if err != nil {
		t.Fatal(err)
	}
	// Broken state left by the old per-user check: two dev channels.
	for _, owner := range []string{stale.ID, admin.ID} {
		if _, err := st.CreateChannel(ctx, owner, "telegram", "Telegram (dev)", []byte("junk")); err != nil {
			t.Fatal(err)
		}
	}

	if _, err := SeedDevTelegram(ctx, st, key, cfg); err != nil {
		t.Fatal(err)
	}

	chans := devSeedTelegramChannels(devSeedChannels(t, st))
	if len(chans) != 1 {
		t.Fatalf("want duplicates collapsed to 1 dev channel, got %d", len(chans))
	}
	if chans[0].OwnerID != admin.ID {
		t.Fatalf("kept channel owner = user %s, want current admin (user %s)", chans[0].OwnerID, admin.ID)
	}
	raw, err := secret.Decrypt(key, chans[0].ConfigEnc)
	if err != nil {
		t.Fatal(err)
	}
	if !json.Valid(raw) || len(raw) == 0 {
		t.Fatalf("kept channel credentials not refreshed: %q", raw)
	}
}
