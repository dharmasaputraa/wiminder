# Notification channels

Channels are managed from the **Channels** page in the UI (configs are stored encrypted). Each channel can be tested with the **Test send** button.

## Gotify

Create an application in the Gotify UI → copy the **token**. Fill in the Base URL (`http://gotify:80` when using the `gotify` compose profile, or your own Gotify URL) and the token. Priority is optional (default 5).

## Telegram

Chat with [@BotFather](https://t.me/BotFather) → `/newbot` → copy the bot token. Send a message to the bot, then get the `chat_id` from `https://api.telegram.org/bot<TOKEN>/getUpdates` (or via @userinfobot). Fill in the bot token + chat_id.

## Email (SMTP)

A Gmail app password is recommended (`smtp.gmail.com:587`, user = Gmail address, password = 16-character app password), or a transactional SMTP relay. Fill in host, port, username, password, from, and the recipient list.

Note: sending email directly from a home IP (port 25) almost always lands in spam or gets blocked — always use a relay.
