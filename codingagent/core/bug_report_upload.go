// Ported from packages/coding-agent/src/core/bug-report-upload.ts (pi v1.5.2).

package core

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"mime/multipart"
	"net/http"
	"net/textproto"
	"net/url"
	"strings"

	"github.com/keejkrej/pi-go/internal/jsonx"
)

// UploadBugReportOptions configures UploadBugReport.
type UploadBugReportOptions struct {
	Token      *string
	GatewayUrl *string
	HttpClient *http.Client
}

// UploadBugReportResult contains the uploaded report ID returned by the Radius gateway.
type UploadBugReportResult struct {
	Id string `json:"id"`
}

type bruUploadResponse struct {
	Ok        *bool `json:"ok,omitzero"`
	BugReport *struct {
		Id *string `json:"id,omitzero"`
	} `json:"bug_report,omitzero"`
	Error       *string `json:"error,omitzero"`
	Description *string `json:"description,omitzero"`
}

// UploadBugReport uploads a BugReportBundle as multipart form data, anonymously or with a Radius token.
func UploadBugReport(ctx context.Context, bundle BugReportBundle, options ...*UploadBugReportOptions) (*UploadBugReportResult, error) {
	if ctx == nil {
		ctx = context.Background()
	}
	var opts UploadBugReportOptions
	if len(options) > 0 && options[0] != nil {
		opts = *options[0]
	}

	var body bytes.Buffer
	mw := multipart.NewWriter(&body)
	for _, f := range BugReportFiles(bundle) {
		h := make(textproto.MIMEHeader)
		h.Set("Content-Disposition", fmt.Sprintf(`form-data; name="%s"; filename="%s"`, f.Name, f.Name))
		h.Set("Content-Type", f.ContentType)
		part, err := mw.CreatePart(h)
		if err != nil {
			_ = mw.Close()
			return nil, fmt.Errorf("bug report upload failed: %w", err)
		}
		if _, err := io.WriteString(part, f.Data); err != nil {
			_ = mw.Close()
			return nil, fmt.Errorf("bug report upload failed: %w", err)
		}
	}
	if err := mw.Close(); err != nil {
		return nil, fmt.Errorf("bug report upload failed: %w", err)
	}

	gatewayBase := GetRadiusGatewayUrl()
	if opts.GatewayUrl != nil && strings.TrimSpace(*opts.GatewayUrl) != "" {
		gatewayBase = *opts.GatewayUrl
	}
	baseURL, err := url.Parse(gatewayBase)
	if err != nil {
		return nil, fmt.Errorf("bug report upload failed: %w", err)
	}
	endpointURL := baseURL.ResolveReference(&url.URL{Path: "/v1/bug-reports"})

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, endpointURL.String(), &body)
	if err != nil {
		return nil, fmt.Errorf("bug report upload failed: %w", err)
	}
	req.Header.Set("Content-Type", mw.FormDataContentType())
	if opts.Token != nil && *opts.Token != "" {
		req.Header.Set("Authorization", "Bearer "+*opts.Token)
	}

	client := opts.HttpClient
	if client == nil {
		client = http.DefaultClient
	}
	resp, err := client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("bug report upload failed: %w", err)
	}
	defer resp.Body.Close()

	respBytes, _ := io.ReadAll(resp.Body)
	var payload bruUploadResponse
	hasPayload := jsonx.Unmarshal(respBytes, &payload) == nil

	if resp.StatusCode >= 200 && resp.StatusCode < 300 && hasPayload &&
		payload.Ok != nil && *payload.Ok &&
		payload.BugReport != nil && payload.BugReport.Id != nil {
		return &UploadBugReportResult{Id: *payload.BugReport.Id}, nil
	}

	var detail string
	if hasPayload && (payload.Ok == nil || !*payload.Ok) {
		if payload.Description != nil && *payload.Description != "" {
			detail = *payload.Description
		} else if payload.Error != nil && *payload.Error != "" {
			detail = *payload.Error
		}
	}
	if detail == "" {
		if statusText := http.StatusText(resp.StatusCode); statusText != "" {
			detail = statusText
		} else if resp.Status != "" {
			detail = resp.Status
		} else {
			detail = fmt.Sprintf("%d", resp.StatusCode)
		}
	}
	return nil, fmt.Errorf("Bug report upload failed: %s", detail)
}
