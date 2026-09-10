package client

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"

	runnerpayload "github.com/jingkaihe/kodelet/pkg/runner/protocol/payload"
	tooltypes "github.com/jingkaihe/kodelet/pkg/types/tools"
	"github.com/pkg/errors"
	"golang.org/x/sys/unix"
)

func contextWithRunnerArtifactResolver(ctx context.Context, peer Peer, runID, toolCallID string) context.Context {
	return tooltypes.ContextWithArtifactResolver(ctx, func(ctx context.Context, id string) (tooltypes.ToolAttachment, error) {
		if peer == nil {
			return tooltypes.ToolAttachment{}, errors.New("artifact storage is unavailable")
		}
		var result tooltypes.ToolAttachment
		err := peer.Call(ctx, runnerpayload.MethodArtifactResolve, runnerpayload.ArtifactRequest{
			RunID:      runID,
			ToolCallID: toolCallID,
			ArtifactID: id,
		}, &result)
		return result, err
	})
}

func (s *Service) ingestAttachments(ctx context.Context, peer Peer, run *activeRun, toolCallID string, result *runnerpayload.ToolResult) {
	attachments := append([]tooltypes.ToolAttachment(nil), result.Structured.Attachments...)
	if len(attachments) > runnerpayload.MaxToolAttachments {
		attachments = append(attachments[:runnerpayload.MaxToolAttachments], tooltypes.ToolAttachment{
			Type:  "image",
			Error: "additional attachments omitted: at most eight images are allowed per tool result",
		})
	}
	for i, attachment := range attachments {
		if attachment.Error != "" || (attachment.ArtifactID != "" && attachment.Path == "") {
			continue
		}
		uploaded, err := s.uploadAttachment(ctx, peer, run, toolCallID, attachment)
		if err != nil {
			attachment.ArtifactID, attachment.ShortCode, attachment.ViewURL = "", "", ""
			attachment.Error = "Image attachment could not be saved: " + err.Error()
			attachments[i] = attachment
		} else {
			attachments[i] = uploaded
		}
	}
	result.Structured.Attachments = attachments
}

func (s *Service) uploadAttachment(
	ctx context.Context,
	peer Peer,
	run *activeRun,
	toolCallID string,
	attachment tooltypes.ToolAttachment,
) (tooltypes.ToolAttachment, error) {
	if attachment.Type != "image" || strings.TrimSpace(attachment.Path) == "" || attachment.ArtifactID != "" {
		return tooltypes.ToolAttachment{}, errors.New("image attachment requires a local path and no artifactId")
	}
	if peer == nil || s.artifactBaseURL == "" {
		return tooltypes.ToolAttachment{}, errors.New("control-plane image uploads are unavailable")
	}
	path := attachment.Path
	if !filepath.IsAbs(path) {
		path = filepath.Join(run.manifest.WorkingDirectory, path)
	}
	// Reject FIFOs using the opened descriptor without waiting for a writer.
	file, err := os.OpenFile(path, os.O_RDONLY|unix.O_NONBLOCK, 0)
	if err != nil {
		return tooltypes.ToolAttachment{}, errors.Wrap(err, "failed to open attachment")
	}
	defer file.Close()
	info, err := file.Stat()
	if err != nil {
		return tooltypes.ToolAttachment{}, errors.Wrap(err, "failed to inspect attachment")
	}
	if !info.Mode().IsRegular() || info.Size() <= 0 || info.Size() > runnerpayload.MaxArtifactBytes {
		return tooltypes.ToolAttachment{}, errors.New("attachment must be a nonempty regular file of at most 32 MiB")
	}
	if attachment.Filename == "" {
		attachment.Filename = filepath.Base(path)
	}
	attachment.Path, attachment.ViewURL, attachment.ShortCode = "", "", ""
	var grant runnerpayload.ArtifactUploadGrant
	if err := peer.Call(ctx, runnerpayload.MethodArtifactUpload, runnerpayload.ArtifactRequest{
		RunID:      run.id,
		ToolCallID: toolCallID,
		Attachment: attachment,
	}, &grant); err != nil {
		return tooltypes.ToolAttachment{}, errors.Wrap(err, "failed to authorize image upload")
	}
	if grant.Token == "" {
		return tooltypes.ToolAttachment{}, errors.New("image upload authorization was empty")
	}
	request, err := http.NewRequestWithContext(ctx, http.MethodPut,
		strings.TrimRight(s.artifactBaseURL, "/")+runnerpayload.ArtifactUploadPath,
		io.LimitReader(file, runnerpayload.MaxArtifactBytes+1),
	)
	if err != nil {
		return tooltypes.ToolAttachment{}, errors.Wrap(err, "failed to construct image upload")
	}
	request.Header.Set("Authorization", "Bearer "+grant.Token)
	request.Header.Set("Content-Type", "application/octet-stream")
	client := &http.Client{
		Timeout: 2 * time.Minute,
		CheckRedirect: func(*http.Request, []*http.Request) error {
			return http.ErrUseLastResponse
		},
	}
	response, err := client.Do(request)
	if err != nil {
		return tooltypes.ToolAttachment{}, errors.Wrap(err, "failed to upload image")
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusCreated {
		return tooltypes.ToolAttachment{}, errors.Errorf("image upload returned HTTP %d", response.StatusCode)
	}
	var result tooltypes.ToolAttachment
	if err := json.NewDecoder(io.LimitReader(response.Body, 64*1024)).Decode(&result); err != nil {
		return tooltypes.ToolAttachment{}, errors.Wrap(err, "invalid image upload response")
	}
	if result.Type != "image" || result.ArtifactID == "" || result.ShortCode == "" {
		return tooltypes.ToolAttachment{}, errors.New("image upload returned no artifact reference")
	}
	return result, nil
}
