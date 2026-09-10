package renderers

import (
	"net/url"
	"strings"
	"unicode"

	"github.com/jingkaihe/kodelet/pkg/types/tools"
)

// ImageAttachmentLines renders one compact, user-facing line per image. baseURL
// is the client's connected server address, used only for relative image links.
func ImageAttachmentLines(result tools.StructuredToolResult, baseURL string) []string {
	var lines []string
	label := "Generated image"
	if result.ToolName == "view_image" {
		label = "Viewed image"
	}
	for _, attachment := range result.Attachments {
		if attachment.Type != "image" {
			continue
		}
		if attachment.Error != "" {
			lines = append(lines, "Image unavailable - "+strings.Join(strings.Fields(attachment.Error), " "))
			continue
		}
		if imageURL := imageAttachmentURL(attachment, baseURL); imageURL != "" {
			lines = append(lines, label+" - "+imageURL)
		} else {
			lines = append(lines, "Image unavailable")
		}
	}
	return lines
}

func imageAttachmentURL(attachment tools.ToolAttachment, baseURL string) string {
	if attachment.ArtifactID == "" || attachment.ShortCode == "" || len(attachment.ShortCode) > 128 {
		return ""
	}
	for _, ch := range attachment.ShortCode {
		if !strings.ContainsRune("abcdefghijklmnopqrstuvwxyzABCDEFGHIJKLMNOPQRSTUVWXYZ0123456789_-", ch) {
			return ""
		}
	}
	imagePath := "/i/" + attachment.ShortCode
	value := attachment.ViewURL
	if value == "" {
		value = imagePath
	}
	if strings.IndexFunc(value, func(ch rune) bool {
		return unicode.IsSpace(ch) || unicode.IsControl(ch) || unicode.Is(unicode.Cf, ch)
	}) >= 0 {
		return ""
	}
	parsed, err := url.Parse(value)
	if err != nil ||
		parsed.User != nil ||
		parsed.RawQuery != "" ||
		parsed.Fragment != "" ||
		!strings.HasSuffix(parsed.Path, imagePath) {
		return ""
	}
	if parsed.IsAbs() {
		if (parsed.Scheme != "https" && parsed.Scheme != "http") || parsed.Host == "" {
			return ""
		}
		return parsed.String()
	}
	if parsed.Host != "" || parsed.Path != imagePath {
		return ""
	}
	if baseURL != "" {
		base, err := url.Parse(baseURL)
		if err == nil &&
			(base.Scheme == "http" || base.Scheme == "https") &&
			base.Host != "" &&
			base.User == nil &&
			base.RawQuery == "" &&
			base.Fragment == "" {
			base.Path = strings.TrimRight(base.Path, "/") + imagePath
			base.RawPath = ""
			return base.String()
		}
	}
	return parsed.String()
}
