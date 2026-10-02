package mediagen

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"regexp"
	"strings"
	"time"

	"supercli/internal/tools/core"
)

func (t *Tool) generateImage(ctx context.Context, client *http.Client, rc runtimeConfig, payload map[string]any) (core.Result, error) {
	var response struct {
		Data []struct {
			Base64 string `json:"b64_json"`
		} `json:"data"`
		Error json.RawMessage `json:"error"`
	}
	// Base64 grows by 4/3; cap the envelope as well as the decoded output.
	limit := ((rc.maxBytes+2)/3)*4 + maxMetadataBytes
	if err := requestJSON(ctx, client, http.MethodPost, rc.endpoint, "Bearer "+rc.token, payload, limit, &response); err != nil {
		return core.Result{}, fmt.Errorf("image submission/result: %w; no retry was sent; remote generation may still finish and incur charges", err)
	}
	if providerError(response.Error) {
		return core.Result{}, errors.New("image provider reported generation failure (details withheld)")
	}
	if len(response.Data) != 1 || response.Data[0].Base64 == "" {
		return core.Result{}, errors.New("image provider must return exactly one b64_json image")
	}
	return t.saveOutput(ctx, base64.NewDecoder(base64.StdEncoding, strings.NewReader(response.Data[0].Base64)), rc.maxBytes)
}

var requestIDPattern = regexp.MustCompile(`^[A-Za-z0-9_-]{1,200}$`)

type queueSubmission struct {
	RequestID   string          `json:"request_id"`
	StatusURL   string          `json:"status_url"`
	ResponseURL string          `json:"response_url"`
	CancelURL   string          `json:"cancel_url"`
	Error       json.RawMessage `json:"error"`
}

func (t *Tool) generateVideo(ctx context.Context, client *http.Client, rc runtimeConfig, payload map[string]any) (result core.Result, err error) {
	var submit queueSubmission
	if err = requestJSON(ctx, client, http.MethodPost, rc.endpoint, "Key "+rc.token, payload, maxMetadataBytes, &submit); err != nil {
		return result, fmt.Errorf("video submission outcome may be unknown: %w; no resubmission was attempted", err)
	}
	if providerError(submit.Error) {
		return result, errors.New("video provider rejected generation (details withheld)")
	}
	if !requestIDPattern.MatchString(submit.RequestID) {
		return result, errors.New("video provider returned no valid request_id; submission outcome may be unknown")
	}
	// Validate the cancel recipient independently, so another malformed URL does
	// not prevent a safe best-effort cancellation of an already accepted job.
	cancelURL, cancelErr := t.authenticatedURL(rc, submit.CancelURL)
	completed := false
	defer func() {
		if err == nil {
			return
		}
		cancellation := "remote cancellation was not attempted because its URL was untrusted"
		if !completed && cancelErr == nil {
			cancelCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 3*time.Second)
			cancelCallErr := requestJSON(cancelCtx, client, http.MethodPut, cancelURL, "Key "+rc.token, nil, maxMetadataBytes, nil)
			cancel()
			if cancelCallErr == nil {
				cancellation = "remote cancellation requested; the provider may still finish and charge for in-progress work"
			} else {
				cancellation = "remote cancellation could not be confirmed; the provider may still finish and charge"
			}
		} else if completed {
			cancellation = "the provider job already completed"
		}
		err = fmt.Errorf("request %s: %w; %s", submit.RequestID, err, cancellation)
	}()
	if cancelErr != nil {
		return result, cancelErr
	}
	statusURL, err := t.authenticatedURL(rc, submit.StatusURL)
	if err != nil {
		return result, err
	}
	responseURL, err := t.authenticatedURL(rc, submit.ResponseURL)
	if err != nil {
		return result, err
	}
	for {
		var status struct {
			Status    string          `json:"status"`
			Error     json.RawMessage `json:"error"`
			RequestID string          `json:"request_id"`
		}
		if err = requestJSON(ctx, client, http.MethodGet, statusURL, "Key "+rc.token, nil, maxMetadataBytes, &status); err != nil {
			return result, err
		}
		if status.RequestID != "" && status.RequestID != submit.RequestID {
			return result, errors.New("status request_id does not match submitted job")
		}
		switch status.Status {
		case "IN_QUEUE", "IN_PROGRESS":
			if providerError(status.Error) {
				return result, errors.New("video provider reported generation failure (details withheld)")
			}
		case "COMPLETED":
			completed = true
			if providerError(status.Error) {
				return result, errors.New("video generation failed at provider (details withheld)")
			}
			var response struct {
				Video struct {
					URL string `json:"url"`
				} `json:"video"`
				Error json.RawMessage `json:"error"`
			}
			if err = requestJSON(ctx, client, http.MethodGet, responseURL, "Key "+rc.token, nil, maxMetadataBytes, &response); err != nil {
				return result, err
			}
			if providerError(response.Error) {
				return result, errors.New("video result reported generation failure (details withheld)")
			}
			var download string
			download, err = t.downloadURL(rc, response.Video.URL)
			if err != nil {
				return result, err
			}
			var req *http.Request
			req, err = http.NewRequestWithContext(ctx, http.MethodGet, download, nil)
			if err != nil {
				return result, errors.New("invalid video download request")
			}
			// The CDN receives no provider credential, cookies or other custom headers.
			var resp *http.Response
			downloadClient := *client
			downloadClient.Jar = nil
			resp, err = downloadClient.Do(req)
			if err != nil {
				if ctx.Err() != nil {
					return result, ctx.Err()
				}
				return result, errors.New("video download failed")
			}
			defer resp.Body.Close()
			if resp.StatusCode != http.StatusOK {
				return result, fmt.Errorf("video download returned HTTP %d", resp.StatusCode)
			}
			if resp.ContentLength > rc.maxBytes {
				return result, errors.New("video download exceeds byte limit")
			}
			return t.saveOutput(ctx, resp.Body, rc.maxBytes)
		default:
			return result, errors.New("video provider returned an unknown queue status")
		}
		timer := time.NewTimer(rc.poll)
		select {
		case <-ctx.Done():
			timer.Stop()
			return result, ctx.Err()
		case <-timer.C:
		}
	}
}
func providerError(raw json.RawMessage) bool {
	s := strings.TrimSpace(string(raw))
	return s != "" && s != "null" && s != `""`
}
