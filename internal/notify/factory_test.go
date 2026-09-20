package notify

import (
	"testing"

	"wiminder/internal/secret"
	"wiminder/internal/store"
)

func TestNewFromChannel(t *testing.T) {
	key := secret.DeriveKey("super-secret-long-enough-16")

	enc := func(cfg string) []byte {
		b, err := secret.Encrypt(key, []byte(cfg))
		if err != nil {
			t.Fatal(err)
		}
		return b
	}

	n1, err := NewFromChannel(store.Channel{Type: "gotify", Name: "home",
		ConfigEnc: enc(`{"base_url":"http://g","token":"t"}`)}, key)
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := n1.(*Gotify); !ok {
		t.Errorf("gotify: %T", n1)
	}

	n2, err := NewFromChannel(store.Channel{Type: "telegram",
		ConfigEnc: enc(`{"bot_token":"b","chat_id":"c"}`)}, key)
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := n2.(*Telegram); !ok {
		t.Errorf("telegram: %T", n2)
	}

	n3, err := NewFromChannel(store.Channel{Type: "email",
		ConfigEnc: enc(`{"host":"h","port":587,"from":"a@b","to":["c@d"]}`)}, key)
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := n3.(*SMTP); !ok {
		t.Errorf("email: %T", n3)
	}

	if _, err := NewFromChannel(store.Channel{Type: " fax",
		ConfigEnc: enc(`{}`)}, key); err == nil {
		t.Error("unknown type must error")
	}
}
