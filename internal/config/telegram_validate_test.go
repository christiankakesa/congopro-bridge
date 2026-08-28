package config

import (
	"strings"
	"testing"
)

func TestValidateTelegram_UnsetStaysValid(t *testing.T) {
	c := &Config{}
	if err := c.ValidateTelegram(); err != nil {
		t.Fatalf("unset must be valid (feature disabled): %v", err)
	}
	if c.TelegramEnabled() {
		t.Fatal("unset must not report enabled")
	}
}

func TestValidateTelegram_AllOrNothing(t *testing.T) {
	for _, c := range []*Config{
		{TelegramBotToken: "1234567890:AAFtoken"},
		{TelegramChatID: "-1001234"},
	} {
		if err := c.ValidateTelegram(); err == nil {
			t.Errorf("half-configured %+v must be rejected", c)
		}
	}
}

func TestValidateTelegram_RejectsMalformed(t *testing.T) {
	cases := []struct {
		name  string
		token string
		chat  string
		want  string // substring of the error
	}{
		{"token without colon", "AAFtokenonly", "-100", "BotFather"},
		{"token with non-digit bot id", "abc:AAFtoken", "-100", "BotFather"},
		{"chat id not an integer", "1234567890:AAFtoken", "@mychannel", "chat id"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			c := &Config{TelegramBotToken: tc.token, TelegramChatID: tc.chat}
			err := c.ValidateTelegram()
			if err == nil {
				t.Fatal("want error, got nil")
			}
			if !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("error %q missing hint %q", err, tc.want)
			}
		})
	}
}

func TestValidateTelegram_AcceptsWellFormed(t *testing.T) {
	for _, chat := range []string{"123456", "-1001234567890"} {
		c := &Config{TelegramBotToken: "1234567890:AAFtoken", TelegramChatID: chat}
		if err := c.ValidateTelegram(); err != nil {
			t.Errorf("chat %q rejected: %v", chat, err)
		}
		if !c.TelegramEnabled() {
			t.Error("configured must report enabled")
		}
	}
}
