package mediagen

import (
	"bufio"
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"mime"
	"net/http"
	"net/url"
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
	statusURL, err := t.statusStreamURL(rc, submit.StatusURL)
	if err != nil {
		return result, err
	}
	responseURL, err := t.authenticatedURL(rc, submit.ResponseURL)
	if err != nil {
		return result, err
	}
	status, err := waitFalCompletion(ctx, client, statusURL, "Key "+rc.token, submit.RequestID)
	if err != nil {
		return result, err
	}
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
}

type queueStatus struct {
	Status    string          `json:"status"`
	Error     json.RawMessage `json:"error"`
	RequestID string          `json:"request_id"`
}

// Fal keeps /status/stream open until COMPLETED. Build the path on the
// already trusted URL, preserving escaped path segments and any query.
func (t *Tool) statusStreamURL(rc runtimeConfig, raw string) (string, error) {
	trusted, err := t.authenticatedURL(rc, raw)
	if err != nil {
		return "", err
	}
	u, err := url.Parse(trusted)
	if err != nil {
		return "", errors.New("invalid video status URL")
	}
	escaped := strings.TrimRight(u.EscapedPath(), "/") + "/stream"
	path, err := url.PathUnescape(escaped)
	if err != nil {
		return "", errors.New("invalid video status path")
	}
	u.Path = path
	u.RawPath = escaped
	return t.authenticatedURL(rc, u.String())
}

const maxFalStatusStreamBytes int64 = 16 * maxMetadataBytes

// One context-bound connection, with no reconnect, status polling or retry.
// Only complete SSE frames may finish the request; an early EOF is uncertain.
func waitFalCompletion(ctx context.Context, client *http.Client, endpoint, auth, requestID string) (queueStatus, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint, nil)
	if err != nil {
		return queueStatus{}, errors.New("invalid video status stream request")
	}
	req.Header.Set("Authorization", auth)
	req.Header.Set("Accept", "text/event-stream")
	resp, err := client.Do(req)
	if err != nil {
		if ctx.Err() != nil {
			return queueStatus{}, ctx.Err()
		}
		return queueStatus{}, errors.New("video status stream request failed (no automatic retry)")
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return queueStatus{}, fmt.Errorf("video status stream returned HTTP %d (body withheld; no automatic retry)", resp.StatusCode)
	}
	contentType, _, err := mime.ParseMediaType(resp.Header.Get("Content-Type"))
	if err != nil || contentType != "text/event-stream" {
		return queueStatus{}, errors.New("video provider did not return a status event stream")
	}
	if resp.ContentLength > maxFalStatusStreamBytes {
		return queueStatus{}, errors.New("video status stream exceeds byte limit")
	}
	reader := &io.LimitedReader{R: &contextReader{ctx: ctx, reader: resp.Body}, N: maxFalStatusStreamBytes + 1}
	scanner := bufio.NewScanner(reader)
	scanner.Buffer(make([]byte, 4096), int(maxMetadataBytes)+1)
	var data []byte
	for scanner.Scan() {
		if err := ctx.Err(); err != nil {
			return queueStatus{}, err
		}
		if reader.N == 0 {
			return queueStatus{}, errors.New("video status stream exceeds byte limit")
		}
		line := scanner.Bytes()
		if len(line) == 0 {
			if len(data) == 0 {
				continue
			}
			var status queueStatus
			if err := json.Unmarshal(data, &status); err != nil {
				return queueStatus{}, errors.New("video provider returned invalid status JSON")
			}
			data = data[:0]
			if status.RequestID != "" && status.RequestID != requestID {
				return queueStatus{}, errors.New("status request_id does not match submitted job")
			}
			switch status.Status {
			case "IN_QUEUE", "IN_PROGRESS":
				if providerError(status.Error) {
					return queueStatus{}, errors.New("video provider reported generation failure (details withheld)")
				}
			case "COMPLETED":
				return status, nil
			default:
				return queueStatus{}, errors.New("video provider returned an unknown queue status")
			}
			continue
		}
		if !bytes.HasPrefix(line, []byte("data:")) {
			continue // comments, event/id/retry and unknown fields are inert
		}
		value := line[len("data:"):]
		if len(value) > 0 && value[0] == ' ' {
			value = value[1:]
		}
		if int64(len(data)+len(value)+1) > maxMetadataBytes {
			return queueStatus{}, errors.New("video status event exceeds byte limit")
		}
		data = append(data, value...)
		data = append(data, '\n')
	}
	if err := ctx.Err(); err != nil {
		return queueStatus{}, err
	}
	if reader.N == 0 {
		return queueStatus{}, errors.New("video status stream exceeds byte limit")
	}
	if scanner.Err() != nil {
		return queueStatus{}, errors.New("video status stream failed (no automatic retry)")
	}
	return queueStatus{}, errors.New("video status stream ended before completion (no automatic retry)")
}

func providerError(raw json.RawMessage) bool {
	s := strings.TrimSpace(string(raw))
	return s != "" && s != "null" && s != `""`
}
