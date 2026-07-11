package bot

import (
	"net/url"
	"strings"
)

// MediaType describes the requested download format.
type MediaType int

const (
	MediaVideo MediaType = iota
	MediaAudio
)

// ParseMediaRequest detects optional mp3/audio prefixes and extracts the target URL.
func ParseMediaRequest(text string) (MediaType, *url.URL, error) {
	trimmed := strings.TrimSpace(text)
	lower := strings.ToLower(trimmed)

	mediaType := MediaVideo

	for _, prefix := range [...]string{"mp3 ", "audio "} {
		if strings.HasPrefix(lower, prefix) {
			mediaType = MediaAudio
			trimmed = strings.TrimSpace(trimmed[len(prefix):])

			break
		}
	}

	userURL, err := ExtractLink(trimmed)
	if err != nil {
		return mediaType, nil, err
	}

	return mediaType, userURL, nil
}
