package anthropic

import (
	"context"

	"github.com/anthropics/anthropic-sdk-go"

	"github.com/jingkaihe/kodelet/pkg/logger"
	"github.com/jingkaihe/kodelet/pkg/vision"
)

// Image dimension limits for a Messages API request.
// https://platform.claude.com/docs/en/build-with-claude/vision#image-limits
const (
	anthropicMaxImageDimension         = 8000
	anthropicManyImageRequestThreshold = 20
	anthropicManyImageMaxDimension     = 3000
)

// fitRequestImages downscales base64 images in place so that each one fits the
// request's dimension limit and the per-image base64 size limit. Every image in
// the request counts toward the many-image threshold, including images resent
// from earlier turns and tool results, so an image accepted early in a
// conversation can make later requests fail. Updating the shared blocks keeps
// the fix in the saved conversation.
func fitRequestImages(ctx context.Context, messages []anthropic.MessageParam) {
	images := requestImages(messages)
	maxDimension := anthropicMaxImageDimension
	if len(images) > anthropicManyImageRequestThreshold {
		maxDimension = anthropicManyImageMaxDimension
	}

	for _, img := range images {
		source := img.Source.OfBase64
		if source == nil {
			continue
		}
		fitted, changed, err := vision.FitBase64Image(vision.Base64Image{
			MimeType: string(source.MediaType),
			Data:     source.Data,
		}, vision.ImageLimits{
			MaxDimension:  maxDimension,
			MaxBase64Size: vision.MaxAnthropicImageBase64Size,
		})
		if err != nil {
			logger.G(ctx).
				WithError(err).
				WithField("max_dimension", maxDimension).
				Warn("failed to fit image within Anthropic image limits")
			continue
		}
		if !changed {
			continue
		}
		source.MediaType = anthropic.Base64ImageSourceMediaType(fitted.MimeType)
		source.Data = fitted.Data
		logger.G(ctx).
			WithField("image_count", len(images)).
			WithField("max_dimension", maxDimension).
			Info("downscaled image to fit Anthropic image limits")
	}
}

func requestImages(messages []anthropic.MessageParam) []*anthropic.ImageBlockParam {
	var images []*anthropic.ImageBlockParam
	for _, message := range messages {
		for _, block := range message.Content {
			if block.OfImage != nil {
				images = append(images, block.OfImage)
			}
			if block.OfToolResult == nil {
				continue
			}
			for _, content := range block.OfToolResult.Content {
				if content.OfImage != nil {
					images = append(images, content.OfImage)
				}
			}
		}
	}
	return images
}
