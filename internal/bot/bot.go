package bot

import (
	"context"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"time"

	tele "gopkg.in/telebot.v4"

	"gh.tarampamp.am/video-dl-bot/internal/filestorage"
	"gh.tarampamp.am/video-dl-bot/internal/whitelist"
	ytdlp "gh.tarampamp.am/video-dl-bot/internal/yt-dlp"
)

// Emojis used for user interaction feedback.
const (
	emojiBadRequest  = "💩" // emoji to react with when the user provided a bad request
	emojiDownloading = "🫡" // emoji to react with while downloading
	emojiUploading   = "🚀" // emoji to react with while uploading
)

// Chat actions to simulate activity status.
const (
	actDownloadingVideo = tele.RecordingVideo
	actUploadingVideo   = tele.UploadingVideo
	actDownloadingAudio = tele.RecordingAudio
	actUploadingAudio   = tele.UploadingAudio
)

type (
	// Bot wraps the Telegram bot client.
	Bot struct {
		cookiesFile            string // path to the cookies file (if any)
		jsRuntimes             string // JavaScript runtimes for yt-dlp (e.g., "node", "bun", "deno", "quickjs")
		maxConcurrentDownloads uint   // maximum number of concurrent downloads allowed
		whitelist              *whitelist.Store
		adminIDs               map[int64]struct{}

		log    *slog.Logger
		client *tele.Bot
	}

	// Option defines a functional option type for customizing the Bot.
	Option func(*Bot)
)

// WithLogger sets a custom logger for the Bot instance.
func WithLogger(log *slog.Logger) Option { return func(b *Bot) { b.log = log } }

// WithCookiesFile sets the path to a cookies file, used by yt-dlp for authenticated downloads.
func WithCookiesFile(path string) Option { return func(b *Bot) { b.cookiesFile = path } }

// WithJSRuntimes configures the JavaScript runtimes for yt-dlp, allowing support for sites that require JS execution.
func WithJSRuntimes(runtimes string) Option { return func(b *Bot) { b.jsRuntimes = runtimes } }

// WithMaxConcurrentDownloads limits the number of concurrent downloads the bot can handle.
func WithMaxConcurrentDownloads(n uint) Option {
	return func(b *Bot) { b.maxConcurrentDownloads = max(1, min(100, n)) } //nolint:mnd
}

// WithWhitelist sets the whitelist store used for access control.
func WithWhitelist(store *whitelist.Store) Option { return func(b *Bot) { b.whitelist = store } }

// WithAdminIDs sets Telegram user IDs that can manage the whitelist.
func WithAdminIDs(ids []int64) Option {
	return func(b *Bot) {
		b.adminIDs = make(map[int64]struct{}, len(ids))
		for _, id := range ids {
			b.adminIDs[id] = struct{}{}
		}
	}
}

// NewBot creates and returns a new instance of Bot.
func NewBot(ctx context.Context, token string, opts ...Option) (*Bot, error) {
	const pollerTimeout = 10 * time.Second // default timeout for the long poller

	var bot = Bot{ // set default values
		log: slog.Default(),
	}

	for _, opt := range opts {
		opt(&bot)
	}

	if bot.whitelist == nil {
		return nil, fmt.Errorf("whitelist store is required")
	}

	if len(bot.adminIDs) == 0 {
		return nil, fmt.Errorf("at least one admin id is required")
	}

	client, err := tele.NewBot(tele.Settings{
		Token:  token,
		Poller: &tele.LongPoller{Timeout: pollerTimeout},
		OnError: func(err error, c tele.Context) {
			bot.log.Error(
				"telegram client error",
				slog.String("error", err.Error()),
				slog.String("sender_name", c.Sender().FirstName),
				slog.String("sender_id", fmt.Sprintf("%d", c.Sender().ID)),
			)
		},
	})
	if err != nil {
		return nil, err
	}

	bot.client = client

	var lim = make(Limiter, bot.maxConcurrentDownloads)

	client.Handle("/start", bot.withAccess(bot.handleStartCommand()))
	client.Handle("test", bot.withAccess(bot.handleTestCommand()))
	client.Handle("/allow", bot.handleAllowCommand())
	client.Handle("/deny", bot.handleDenyCommand())
	client.Handle("/users", bot.handleUsersCommand())
	client.Handle("/whoami", bot.handleWhoamiCommand())

	var msgHandler = bot.withAccess(bot.handleMessages(ctx, lim))

	for _, event := range [...]string{tele.OnText, tele.OnForward, tele.OnReply} {
		client.Handle(event, msgHandler)
	}

	return &bot, nil
}

// Start begins polling updates from Telegram. Blocks until context is canceled.
func (b *Bot) Start(ctx context.Context) {
	var stopped = make(chan struct{})

	go func() {
		defer close(stopped)

		<-ctx.Done()
		b.client.Stop()
	}()

	b.client.Start()

	<-stopped
}

// handleStartCommand returns a handler for the "/start" command.
func (b *Bot) handleStartCommand() tele.HandlerFunc {
	return func(c tele.Context) (err error) {
		return b.reply(c.Message(), fmt.Sprintf(`Hello %s! I can help you download videos and audio from hundreds of websites.

Send me a video URL to download the video.
Prefix a URL with "mp3" or "audio" to download MP3 instead.

Examples:
- https://www.youtube.com/watch?v=dQw4w9WgXcQ
- mp3 https://www.youtube.com/watch?v=dQw4w9WgXcQ`,
			c.Sender().FirstName,
		))
	}
}

// handleTestCommand returns a handler for a simple "test" command.
func (b *Bot) handleTestCommand() tele.HandlerFunc {
	return func(c tele.Context) (err error) {
		return b.reply(
			c.Message(),
			"Send me a video URL or prefix it with mp3/audio for audio download.",
		)
	}
}

// handleMessages processes incoming user messages and attempts to download media content.
func (b *Bot) handleMessages(pCtx context.Context, lim Limiter) tele.HandlerFunc { //nolint:funlen
	const errWrongMessageReplyMd2 = "Please provide a valid link\\." +
		"\n" +
		"\n" +
		"Examples:\n" +
		"\\- `https://www\\.youtube\\.com/watch?v=dQw4w9WgXcQ`\n" +
		"\\- `mp3 https://www\\.youtube\\.com/watch?v=dQw4w9WgXcQ`\n" +
		"\n" +
		"You can also share a link to an Instagram reel, TikTok video, or any other video you'd like to download\\. " +
		"Hundreds of sites are supported, so feel free to give it a try\\!"

	return func(c tele.Context) error {
		ctx, cancel := context.WithCancel(pCtx)
		defer cancel()

		var (
			user, userMsg = c.Sender(), c.Message()
			mediaType, userURL, userURLErr = ParseMediaRequest(c.Text())
		)

		if userURLErr != nil {
			_ = b.react(user, userMsg, emojiBadRequest)

			b.log.Info("received invalid link from user",
				slog.String("sender_name", user.FirstName),
				slog.Int64("sender_id", user.ID),
				slog.String("message_text", c.Text()),
			)

			return b.reply(userMsg, errWrongMessageReplyMd2, &tele.SendOptions{
				ParseMode:             tele.ModeMarkdownV2,
				DisableWebPagePreview: true,
				DisableNotification:   true,
			})
		}

		isAudio := mediaType == MediaAudio

		b.log.Info("received media download request",
			slog.String("sender_name", user.FirstName),
			slog.Int64("sender_id", user.ID),
			slog.String("media_url", userURL.String()),
			slog.Bool("is_audio", isAudio),
		)

		if err := lim.Acquire(ctx); err != nil {
			return err
		}
		defer lim.Release()

		defer func() { _ = b.clearReactions(user, userMsg) }()

		var (
			downloadAction tele.ChatAction
			uploadAction   tele.ChatAction
		)

		if isAudio {
			downloadAction = actDownloadingAudio
			uploadAction = actUploadingAudio
		} else {
			downloadAction = actDownloadingVideo
			uploadAction = actUploadingVideo
		}

		_ = b.react(user, userMsg, emojiDownloading)
		stopDownloadingAction := b.setChatAction(ctx, user, downloadAction)

		defer stopDownloadingAction()

		var ytDlpOpts []ytdlp.Option

		if b.cookiesFile != "" {
			ytDlpOpts = append(ytDlpOpts, ytdlp.WithCookiesFile(b.cookiesFile))
		}

		if b.jsRuntimes != "" {
			ytDlpOpts = append(ytDlpOpts, ytdlp.WithJSRuntimes(b.jsRuntimes))
		}

		var (
			dl    *ytdlp.Downloaded
			dlErr error
		)

		if isAudio {
			dl, dlErr = ytdlp.DownloadAudio(ctx, userURL.String(), ytDlpOpts...)
		} else {
			dl, dlErr = ytdlp.Download(ctx, userURL.String(), ytDlpOpts...)
		}

		if dlErr != nil {
			mediaLabel := "video"
			if isAudio {
				mediaLabel = "audio"
			}

			b.log.Error("failed to download media",
				slog.String("error", dlErr.Error()),
				slog.String("sender_name", user.FirstName),
				slog.Int64("sender_id", user.ID),
				slog.String("media_url", userURL.String()),
				slog.Bool("is_audio", isAudio),
			)

			return b.reply(userMsg, fmt.Sprintf("❌ Failed to download %s", mediaLabel))
		}

		stopDownloadingAction()

		stat, statErr := os.Stat(dl.Filepath)
		if statErr != nil {
			b.log.Error("failed to stat downloaded media file",
				slog.String("error", statErr.Error()),
				slog.String("file_path", dl.Filepath),
				slog.String("sender_name", user.FirstName),
				slog.Int64("sender_id", user.ID),
				slog.String("media_url", userURL.String()),
			)

			return b.reply(userMsg, "❌ Downloaded file not available")
		}

		b.log.Debug("successfully downloaded media",
			slog.String("file_path", dl.Filepath),
			slog.String("sender_name", user.FirstName),
			slog.Int64("sender_id", user.ID),
			slog.String("media_url", userURL.String()),
			slog.Int64("file_size", stat.Size()),
			slog.Bool("is_audio", isAudio),
		)

		defer func() { _ = os.Remove(dl.Filepath) }()

		fp, fpErr := os.Open(dl.Filepath)
		if fpErr != nil {
			return fpErr
		}

		defer func() { _ = fp.Close() }()

		_ = b.react(user, userMsg, emojiUploading)
		stopUploadingAction := b.setChatAction(ctx, user, uploadAction)

		defer stopUploadingAction()

		var fileSizeMb = float64(stat.Size()) / 1024 / 1024 // file size in MB

		if fileSizeMb <= 50 { //nolint:mnd
			if isAudio {
				if err := b.replyWithAudio(userMsg, tele.Audio{File: tele.FromReader(fp)}); err != nil {
					b.log.Error("failed to upload audio to Telegram",
						slog.String("error", err.Error()),
						slog.Int64("file_size", stat.Size()),
						slog.String("sender_name", user.FirstName),
						slog.Int64("sender_id", user.ID),
						slog.String("media_url", userURL.String()),
					)

					return b.reply(userMsg, fmt.Sprintf(
						"❌ Failed to send audio (%.2f MB): %s",
						fileSizeMb,
						err.Error(),
					))
				}
			} else if err := b.replyWithVideo(userMsg, tele.Video{File: tele.FromReader(fp)}); err != nil {
				b.log.Error("failed to upload video to Telegram",
					slog.String("error", err.Error()),
					slog.Int64("file_size", stat.Size()),
					slog.String("sender_name", user.FirstName),
					slog.Int64("sender_id", user.ID),
					slog.String("media_url", userURL.String()),
				)

				return b.reply(userMsg, fmt.Sprintf(
					"❌ Failed to send video (%.2f MB): %s",
					fileSizeMb,
					err.Error(),
				))
			}
		} else {
			fileName := fmt.Sprintf("video%s", filepath.Ext(dl.Filepath))
			linkLabel := fmt.Sprintf("🚀 Download video (%.2f MB)", fileSizeMb)
			readyText := fmt.Sprintf(
				"[Your video](%s) is ready for download _\\(the link will expire in a couple of days\\)_:",
				userURL.String(),
			)

			if isAudio {
				fileName = fmt.Sprintf("audio%s", filepath.Ext(dl.Filepath))
				linkLabel = fmt.Sprintf("🚀 Download audio (%.2f MB)", fileSizeMb)
				readyText = fmt.Sprintf(
					"[Your audio](%s) is ready for download _\\(the link will expire in a couple of days\\)_:",
					userURL.String(),
				)
			}

			fileURL, urlErr := filestorage.UploadToFileBin(ctx, fp, fileName)
			if urlErr != nil {
				b.log.Error("failed to upload media file to file hosting",
					slog.String("error", urlErr.Error()),
					slog.Int64("file_size", stat.Size()),
					slog.String("sender_name", user.FirstName),
					slog.Int64("sender_id", user.ID),
					slog.String("media_url", userURL.String()),
				)

				return b.reply(userMsg, "❌ Failed to upload file to file hosting")
			}

			return b.replyWithLink(
				userMsg,
				readyText,
				linkLabel,
				fileURL,
				&tele.SendOptions{
					ParseMode:             tele.ModeMarkdownV2,
					DisableWebPagePreview: true,
				},
			)
		}

		stopUploadingAction()

		return nil
	}
}

// reply attempts to reply to a message; if the message is not found (e.g. deleted), sends a new message.
func (b *Bot) reply(to *tele.Message, msg string, opts ...any) (err error) {
	_, err = b.client.Reply(to, msg, opts...)
	if err != nil {
		_, err = b.client.Send(to.Sender, msg, opts...)
	}

	return
}

// replyWithVideo sends a video file either as a reply or a fresh message.
func (b *Bot) replyWithVideo(to *tele.Message, v tele.Video) (err error) {
	_, err = b.client.Reply(to, &v)
	if err != nil {
		_, err = b.client.Send(to.Sender, &v)
	}

	return
}

// replyWithAudio sends an audio file either as a reply or a fresh message.
func (b *Bot) replyWithAudio(to *tele.Message, a tele.Audio) (err error) {
	_, err = b.client.Reply(to, &a)
	if err != nil {
		_, err = b.client.Send(to.Sender, &a)
	}

	return
}

// replyWithLink sends a message with an inline download button.
func (b *Bot) replyWithLink(to *tele.Message, msgText, linkText, linkUrl string, opts ...any) (err error) {
	var markup = tele.ReplyMarkup{
		ResizeKeyboard: true,
		InlineKeyboard: [][]tele.InlineButton{
			{{
				Text: linkText,
				URL:  linkUrl,
			}},
		},
	}

	_, err = b.client.Reply(to, msgText, append(opts, &markup)...)
	if err != nil {
		_, err = b.client.Send(to.Sender, msgText, append(opts, &markup)...)
	}

	return
}

// react adds emoji reactions to a message.
func (b *Bot) react(to tele.Recipient, msg tele.Editable, emoji ...string) error {
	var reactions = make([]tele.Reaction, len(emoji))

	for i, e := range emoji {
		reactions[i] = tele.Reaction{
			Type:  tele.ReactionTypeEmoji,
			Emoji: e,
		}
	}

	return b.client.React(to, msg, tele.Reactions{Reactions: reactions})
}

// clearReactions removes all reactions from a message.
func (b *Bot) clearReactions(to tele.Recipient, msg tele.Editable) error {
	return b.client.React(to, msg, tele.Reactions{Reactions: []tele.Reaction{}})
}

// setChatAction periodically sends a chat action (e.g., typing). Returns a function to stop the action.
func (b *Bot) setChatAction(ctx context.Context, user *tele.User, action tele.ChatAction) (stop func()) {
	ctx, stop = context.WithCancel(ctx)

	const interval = 4*time.Second + 500*time.Millisecond

	go func() {
		defer stop()

		if ctx.Err() != nil {
			return
		}

		if err := b.client.Notify(user, action); err != nil {
			return
		}

		ticker := time.NewTicker(interval)
		defer ticker.Stop()

		for {
			select {
			case <-ctx.Done():
				return

			case <-ticker.C:
				if err := ctx.Err(); err != nil {
					return
				}

				if err := b.client.Notify(user, action); err != nil {
					return
				}
			}
		}
	}()

	return
}
