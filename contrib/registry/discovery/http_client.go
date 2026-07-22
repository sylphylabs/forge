package discovery

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
)

func (d *Discovery) getJSON(ctx context.Context, endpoint string, query url.Values, result any) error {
	return d.doJSON(ctx, http.MethodGet, endpoint, query, result)
}

func (d *Discovery) postJSON(ctx context.Context, endpoint string, query url.Values, result any) error {
	return d.doJSON(ctx, http.MethodPost, endpoint, query, result)
}

func (d *Discovery) doJSON(ctx context.Context, method, endpoint string, query url.Values, result any) error {
	u, err := url.Parse(endpoint)
	if err != nil {
		return fmt.Errorf("discovery: parse endpoint: %w", err)
	}
	values := u.Query()
	for key, entries := range query {
		for _, value := range entries {
			values.Add(key, value)
		}
	}
	u.RawQuery = values.Encode()

	req, err := http.NewRequestWithContext(ctx, method, u.String(), http.NoBody)
	if err != nil {
		return fmt.Errorf("discovery: create %s request: %w", method, err)
	}
	resp, err := d.httpClient.Do(req)
	if err != nil {
		return fmt.Errorf("discovery: execute %s request: %w", method, err)
	}
	defer resp.Body.Close()

	if resp.StatusCode < http.StatusOK || resp.StatusCode >= http.StatusMultipleChoices {
		_, _ = io.Copy(io.Discard, resp.Body)
		return fmt.Errorf("discovery: %s request returned %s", method, resp.Status)
	}
	if resp.StatusCode == http.StatusNoContent || result == nil {
		_, _ = io.Copy(io.Discard, resp.Body)
		return nil
	}
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return fmt.Errorf("discovery: read %s response: %w", method, err)
	}
	if err := json.Unmarshal(body, result); err != nil {
		return fmt.Errorf("discovery: decode %s response: %w", method, err)
	}
	return nil
}
