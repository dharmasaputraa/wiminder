package notify

import (
	"encoding/json"
	"fmt"

	"wiminder/internal/secret"
	"wiminder/internal/store"
)

// NewFromChannel decrypts the channel config and returns a concrete Notifier.
func NewFromChannel(ch store.Channel, key []byte) (Notifier, error) {
	plain, err := secret.Decrypt(key, ch.ConfigEnc)
	if err != nil {
		return nil, fmt.Errorf("decrypt channel config %q: %w", ch.Name, err)
	}
	switch ch.Type {
	case "gotify":
		var c GotifyConfig
		if err := json.Unmarshal(plain, &c); err != nil {
			return nil, err
		}
		return NewGotify(c), nil
	case "telegram":
		var c TelegramConfig
		if err := json.Unmarshal(plain, &c); err != nil {
			return nil, err
		}
		return NewTelegram(c), nil
	case "email":
		var c SMTPConfig
		if err := json.Unmarshal(plain, &c); err != nil {
			return nil, err
		}
		return NewSMTP(c), nil
	default:
		return nil, fmt.Errorf("unknown channel type: %q", ch.Type)
	}
}
