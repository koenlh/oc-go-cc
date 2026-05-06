package debuglog

import "fmt"

const DefaultPreviewLimit = 4096

func PreviewBytes(body []byte) string {
	return PreviewString(string(body))
}

func PreviewString(body string) string {
	if len(body) <= DefaultPreviewLimit {
		return body
	}
	return fmt.Sprintf("%s...(truncated %d bytes)", body[:DefaultPreviewLimit], len(body)-DefaultPreviewLimit)
}
