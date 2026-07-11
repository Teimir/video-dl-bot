package cli

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"syscall"

	"gh.tarampamp.am/video-dl-bot/internal/bot"
	"gh.tarampamp.am/video-dl-bot/internal/cli/cmd"
	"gh.tarampamp.am/video-dl-bot/internal/logger"
	"gh.tarampamp.am/video-dl-bot/internal/version"
	"gh.tarampamp.am/video-dl-bot/internal/whitelist"
)

//go:generate go run ./generate/readme.go

// App represents the CLI application structure.
type App struct {
	cmd cmd.Command
	opt struct {
		PidFile       string
		DoHealthcheck bool

		BotToken               string
		CookiesFile            string
		JSRuntimes             string
		MaxConcurrentDownloads uint
		AdminIDs               string
		WhitelistFile          string
	}
}

func parseAdminIDs(raw string) ([]int64, error) {
	parts := strings.Split(raw, ",")
	ids := make([]int64, 0, len(parts))

	for _, part := range parts {
		part = strings.TrimSpace(part)
		if part == "" {
			continue
		}

		id, err := strconv.ParseInt(part, 10, 64)
		if err != nil {
			return nil, fmt.Errorf("invalid admin id %q", part)
		}

		ids = append(ids, id)
	}

	if len(ids) == 0 {
		return nil, errors.New("at least one admin id is required")
	}

	return ids, nil
}

// NewApp initializes a new CLI application instance.
func NewApp(name string) *App { //nolint:funlen,gocognit,gocyclo
	var app = App{
		cmd: cmd.Command{
			Name:        name,
			Description: "This is a video download bot that allows you to download videos not leaving Telegram.",
			Version:     version.Version(),
		},
	}

	app.opt.MaxConcurrentDownloads = 5
	app.opt.WhitelistFile = "./whitelist.json"

	var (
		logLevelFlag = cmd.Flag[string]{
			Names:   []string{"log-level"},
			Usage:   "Logging level (" + strings.Join(logger.LevelStrings(), "/") + ")",
			EnvVars: []string{"LOG_LEVEL"},
			Default: logger.InfoLevel.String(),
			Validator: func(_ *cmd.Command, v string) error {
				if _, err := logger.ParseLevel(v); err != nil {
					return fmt.Errorf("invalid log level: %w", err)
				}

				return nil
			},
		}
		logFormatFlag = cmd.Flag[string]{
			Names:   []string{"log-format"},
			Usage:   "Logging format (" + strings.Join(logger.FormatStrings(), "/") + ")",
			EnvVars: []string{"LOG_FORMAT"},
			Default: logger.ConsoleFormat.String(),
			Validator: func(_ *cmd.Command, v string) error {
				if _, err := logger.ParseFormat(v); err != nil {
					return fmt.Errorf("invalid log format: %w", err)
				}

				return nil
			},
		}
		botTokenFlag = cmd.Flag[string]{
			Names:   []string{"bot-token", "t"},
			Usage:   "Telegram bot token",
			EnvVars: []string{"BOT_TOKEN"},
			Default: app.opt.BotToken,
			Validator: func(_ *cmd.Command, v string) error {
				if v == "" {
					return fmt.Errorf("telegram bot token is required")
				}

				if len(v) < 10 || len(v) > 100 {
					return fmt.Errorf("telegram bot token must be between 10 and 100 characters long")
				}

				if !regexp.MustCompile(`^[0-9]{8,10}:[a-zA-Z0-9_-]{35}$`).MatchString(v) {
					return fmt.Errorf("telegram bot token is invalid")
				}

				return nil
			},
		}
		cookiesFileFlag = cmd.Flag[string]{
			Names:   []string{"cookies-file", "c"},
			Usage:   "Path to the file with cookies (netscape-formatted) for the bot (optional)",
			EnvVars: []string{"COOKIES_FILE"},
			Default: app.opt.CookiesFile,
			Validator: func(_ *cmd.Command, v string) error {
				if v != "" {
					if stat, err := os.Stat(v); err != nil {
						return fmt.Errorf("failed to access cookies file: %w", err)
					} else if stat.IsDir() {
						return fmt.Errorf("cookies file path cannot be a directory")
					}
				}

				return nil
			},
		}
		jsRuntimesFlag = cmd.Flag[string]{
			Names:   []string{"js-runtimes"},
			Usage:   "JavaScript runtimes for yt-dlp (e.g. 'node', 'node:/path/to/node', 'bun', 'deno', 'quickjs')",
			EnvVars: []string{"JS_RUNTIMES"},
			Default: app.opt.JSRuntimes,
			Validator: func(_ *cmd.Command, v string) error {
				if v == "" {
					return nil
				}

				for _, char := range v {
					if char == '"' || char == '\'' || char == ';' || char == '&' || char == '|' {
						return fmt.Errorf("js runtimes cannot contain quotes, semicolons, or shell operators")
					}
				}

				return nil
			},
		}
		maxConcurrentDownloadsFlag = cmd.Flag[uint]{
			Names:   []string{"max-concurrent-downloads", "m"},
			Usage:   "Maximum number of concurrent downloads",
			EnvVars: []string{"MAX_CONCURRENT_DOWNLOADS"},
			Default: app.opt.MaxConcurrentDownloads,
			Validator: func(_ *cmd.Command, v uint) error {
				if v < 1 || v > 100 {
					return fmt.Errorf("maximum number of concurrent downloads must be between 1 and 100")
				}

				return nil
			},
		}
		adminIDsFlag = cmd.Flag[string]{
			Names:   []string{"admin-ids"},
			Usage:   "Comma-separated Telegram user IDs of bot admins",
			EnvVars: []string{"ADMIN_IDS"},
			Default: app.opt.AdminIDs,
			Validator: func(_ *cmd.Command, v string) error {
				if _, err := parseAdminIDs(v); err != nil {
					return err
				}

				return nil
			},
		}
		whitelistFileFlag = cmd.Flag[string]{
			Names:   []string{"whitelist-file"},
			Usage:   "Path to the JSON file with whitelisted Telegram user IDs",
			EnvVars: []string{"WHITELIST_FILE"},
			Default: app.opt.WhitelistFile,
			Validator: func(_ *cmd.Command, v string) error {
				if v == "" {
					return errors.New("whitelist file path is required")
				}

				return nil
			},
		}
		pidFileFlag = cmd.Flag[string]{
			Names:   []string{"pid-file"},
			Usage:   "Path to the file where the process ID will be stored",
			EnvVars: []string{"PID_FILE"},
			Default: app.opt.PidFile,
			Validator: func(_ *cmd.Command, _ string) error {
				if app.opt.PidFile == "" {
					return nil
				}

				if _, err := os.Stat(app.opt.PidFile); err != nil {
					if os.IsNotExist(err) {
						return nil
					}
				}

				return errors.New("pid file path already exists (another instance may be running), or the path is invalid")
			},
		}
		healthcheckFlag = cmd.Flag[bool]{
			Names: []string{"healthcheck"},
			Usage: "Check the health of the bot (useful for Docker/K8s healthcheck; pid file must be set) and exit",
		}
	)

	app.cmd.Flags = []cmd.Flagger{
		&logLevelFlag,
		&logFormatFlag,
		&botTokenFlag,
		&cookiesFileFlag,
		&jsRuntimesFlag,
		&maxConcurrentDownloadsFlag,
		&adminIDsFlag,
		&whitelistFileFlag,
		&pidFileFlag,
		&healthcheckFlag,
	}

	app.cmd.Action = func(ctx context.Context, c *cmd.Command, args []string) error {
		var (
			logLevel, _  = logger.ParseLevel(*logLevelFlag.Value)
			logFormat, _ = logger.ParseFormat(*logFormatFlag.Value)
		)

		log, logErr := logger.New(logLevel, logFormat)
		if logErr != nil {
			return logErr
		}

		setIfFlagIsSet(&app.opt.PidFile, pidFileFlag)
		setIfFlagIsSet(&app.opt.DoHealthcheck, healthcheckFlag)
		setIfFlagIsSet(&app.opt.BotToken, botTokenFlag)
		setIfFlagIsSet(&app.opt.CookiesFile, cookiesFileFlag)
		setIfFlagIsSet(&app.opt.JSRuntimes, jsRuntimesFlag)
		setIfFlagIsSet(&app.opt.MaxConcurrentDownloads, maxConcurrentDownloadsFlag)
		setIfFlagIsSet(&app.opt.AdminIDs, adminIDsFlag)
		setIfFlagIsSet(&app.opt.WhitelistFile, whitelistFileFlag)

		if app.opt.DoHealthcheck {
			if app.opt.PidFile == "" {
				return errors.New("pid file must be set for healthcheck")
			}

			b, err := os.ReadFile(app.opt.PidFile)
			if err != nil {
				return fmt.Errorf("failed to read pid file: %w", err)
			}

			pid, err := strconv.Atoi(string(b))
			if err != nil {
				return fmt.Errorf("invalid pid in file %s: %w", app.opt.PidFile, err)
			}

			if err = syscall.Kill(pid, syscall.Signal(0)); err != nil {
				return errors.New("process is not running")
			}

			log.Info("healthcheck successful", slog.Int("pid", pid), slog.String("pid_file", app.opt.PidFile))

			return nil
		}

		if app.opt.PidFile != "" {
			if _, err := os.Stat(app.opt.PidFile); err == nil {
				return fmt.Errorf("pid file already exists: %s (another instance may be running)", app.opt.PidFile)
			}

			if err := os.WriteFile(app.opt.PidFile, []byte(strconv.Itoa(os.Getpid())), 0o644); err != nil { //nolint:gosec,mnd
				return fmt.Errorf("failed to write PID file: %w", err)
			}

			log.Info("pid file created", "path", app.opt.PidFile)

			defer func() { _ = os.Remove(app.opt.PidFile) }()
		}

		if app.opt.CookiesFile != "" {
			content, rErr := os.ReadFile(app.opt.CookiesFile)
			if rErr != nil {
				return fmt.Errorf("failed to read cookies file: %w", rErr)
			}

			tmpDir, tmpDirErr := os.MkdirTemp("", "cookies-*")
			if tmpDirErr != nil {
				return fmt.Errorf("failed to create temporary directory for cookies: %w", tmpDirErr)
			}

			defer func() { _ = os.RemoveAll(tmpDir) }()

			tmpCookiesFile := filepath.Join(tmpDir, "cookies.txt")

			if err := os.WriteFile(tmpCookiesFile, content, 0o600); err != nil { //nolint:mnd,gosec
				return err
			}

			app.opt.CookiesFile = tmpCookiesFile
		}

		return app.run(ctx, log)
	}

	return &app
}

// setIfFlagIsSet assigns a flag value to target if the flag is set and non-nil.
func setIfFlagIsSet[T cmd.FlagType](target *T, source cmd.Flag[T]) {
	if target == nil || source.Value == nil || !source.IsSet() {
		return
	}

	*target = *source.Value
}

// Run starts the CLI command execution.
func (a *App) Run(ctx context.Context, args []string) error { return a.cmd.Run(ctx, args) }

// Help returns the CLI help message.
func (a *App) Help() string { return a.cmd.Help() }

// run contains the main bot initialization and event loop.
func (a *App) run(ctx context.Context, log *slog.Logger) error {
	adminIDs, err := parseAdminIDs(a.opt.AdminIDs)
	if err != nil {
		return err
	}

	whitelistStore, err := whitelist.NewStore(a.opt.WhitelistFile)
	if err != nil {
		return fmt.Errorf("failed to initialize whitelist: %w", err)
	}

	var botOpts = []bot.Option{
		bot.WithLogger(log.With("source", "telebot")),
		bot.WithMaxConcurrentDownloads(a.opt.MaxConcurrentDownloads),
		bot.WithWhitelist(whitelistStore),
		bot.WithAdminIDs(adminIDs),
	}

	if a.opt.CookiesFile != "" {
		botOpts = append(botOpts, bot.WithCookiesFile(a.opt.CookiesFile))
	} else {
		log.Warn("no cookies file provided, some sites may not work without it")
	}

	if a.opt.JSRuntimes != "" {
		botOpts = append(botOpts, bot.WithJSRuntimes(a.opt.JSRuntimes))
		log.Info("custom JavaScript runtimes provided for yt-dlp", "runtimes", a.opt.JSRuntimes)
	} else {
		log.Info("no custom JavaScript runtimes provided, yt-dlp defaults will be used")
	}

	b, err := bot.NewBot(ctx, a.opt.BotToken, botOpts...)
	if err != nil {
		return fmt.Errorf("failed to create bot: %w", err)
	}

	log.Info("starting bot", slog.String("whitelist_file", a.opt.WhitelistFile))

	b.Start(ctx)

	log.Info("bot stopped")

	return nil
}
