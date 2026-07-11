package bot

import (
	"errors"
	"fmt"
	"strconv"
	"strings"

	tele "gopkg.in/telebot.v4"
)

func (b *Bot) isAdmin(userID int64) bool {
	_, ok := b.adminIDs[userID]

	return ok
}

func (b *Bot) isAllowed(userID int64) bool {
	if b.isAdmin(userID) {
		return true
	}

	return b.whitelist.IsAllowed(userID)
}

func (b *Bot) withAccess(handler tele.HandlerFunc) tele.HandlerFunc {
	return func(c tele.Context) error {
		if !b.isAllowed(c.Sender().ID) {
			return nil
		}

		return handler(c)
	}
}

func (b *Bot) withAdmin(handler tele.HandlerFunc) tele.HandlerFunc {
	return func(c tele.Context) error {
		if !b.isAdmin(c.Sender().ID) {
			return nil
		}

		return handler(c)
	}
}

func (b *Bot) resolveTargetUser(c tele.Context) (int64, string, error) {
	if len(c.Args()) > 0 {
		userID, err := strconv.ParseInt(strings.TrimSpace(c.Args()[0]), 10, 64)
		if err != nil {
			return 0, "", fmt.Errorf("invalid user id: %s", c.Args()[0])
		}

		return userID, "", nil
	}

	if c.Message().ReplyTo != nil && c.Message().ReplyTo.Sender != nil {
		sender := c.Message().ReplyTo.Sender

		return sender.ID, sender.Username, nil
	}

	return 0, "", errors.New("provide a user id or reply to the user's message")
}

func (b *Bot) handleAllowCommand() tele.HandlerFunc {
	return b.withAdmin(func(c tele.Context) error {
		targetID, username, err := b.resolveTargetUser(c)
		if err != nil {
			return b.reply(c.Message(), "❌ "+err.Error())
		}

		if err := b.whitelist.Add(targetID, username); err != nil {
			return b.reply(c.Message(), fmt.Sprintf("❌ Failed to add user: %s", err.Error()))
		}

		return b.reply(c.Message(), fmt.Sprintf("✅ User %d added to whitelist", targetID))
	})
}

func (b *Bot) handleDenyCommand() tele.HandlerFunc {
	return b.withAdmin(func(c tele.Context) error {
		targetID, _, err := b.resolveTargetUser(c)
		if err != nil {
			return b.reply(c.Message(), "❌ "+err.Error())
		}

		if b.isAdmin(targetID) {
			return b.reply(c.Message(), "❌ Cannot remove an admin from the whitelist")
		}

		if err := b.whitelist.Remove(targetID); err != nil {
			return b.reply(c.Message(), fmt.Sprintf("❌ Failed to remove user: %s", err.Error()))
		}

		return b.reply(c.Message(), fmt.Sprintf("✅ User %d removed from whitelist", targetID))
	})
}

func (b *Bot) handleUsersCommand() tele.HandlerFunc {
	return b.withAdmin(func(c tele.Context) error {
		users := b.whitelist.List()
		if len(users) == 0 {
			return b.reply(c.Message(), "Whitelist is empty.")
		}

		var lines = make([]string, 0, len(users))
		for _, user := range users {
			line := fmt.Sprintf("• %d", user.ID)
			if user.Username != "" {
				line += " (@" + user.Username + ")"
			}

			lines = append(lines, line)
		}

		return b.reply(c.Message(), "Whitelisted users:\n"+strings.Join(lines, "\n"))
	})
}

func (b *Bot) handleWhoamiCommand() tele.HandlerFunc {
	return b.withAdmin(func(c tele.Context) error {
		return b.reply(c.Message(), fmt.Sprintf("Your Telegram ID: %d", c.Sender().ID))
	})
}
