package bot_test

import (
	"testing"

	"gh.tarampamp.am/video-dl-bot/internal/bot"
)

func TestParseMediaRequest(t *testing.T) {
	t.Parallel()

	const sampleURL = "https://www.youtube.com/watch?v=dQw4w9WgXcQ"

	for name, tc := range map[string]struct {
		giveText    string
		wantType    bot.MediaType
		wantURL     string
		expectError bool
	}{
		"video url": {
			giveText: sampleURL,
			wantType: bot.MediaVideo,
			wantURL:  sampleURL,
		},
		"mp3 prefix": {
			giveText: "mp3 " + sampleURL,
			wantType: bot.MediaAudio,
			wantURL:  sampleURL,
		},
		"audio prefix uppercase": {
			giveText: "AUDIO " + sampleURL,
			wantType: bot.MediaAudio,
			wantURL:  sampleURL,
		},
		"mp3 prefix without url": {
			giveText:    "mp3 not-a-url",
			wantType:    bot.MediaAudio,
			expectError: true,
		},
		"invalid url": {
			giveText:    "hello world",
			wantType:    bot.MediaVideo,
			expectError: true,
		},
	} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			mediaType, userURL, err := bot.ParseMediaRequest(tc.giveText)

			if tc.expectError {
				if err == nil {
					t.Fatal("expected error, got nil")
				}

				return
			}

			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}

			if mediaType != tc.wantType {
				t.Fatalf("expected media type %v, got %v", tc.wantType, mediaType)
			}

			if userURL == nil || userURL.String() != tc.wantURL {
				t.Fatalf("expected URL %q, got %v", tc.wantURL, userURL)
			}
		})
	}
}
