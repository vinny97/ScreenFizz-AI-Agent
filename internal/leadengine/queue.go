package leadengine

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
)

const leadStatusQueued = "QUEUED"

// QueueResult summarizes a queueing run.
type QueueResult struct {
	Queued int
}

// QueueReadyLeads updates up to 100 READY_TO_EMAIL leads to QUEUED, oldest first.
func (c *Client) QueueReadyLeads(ctx context.Context) (QueueResult, error) {
	leads, err := c.listLeadsForQueue(ctx)
	if err != nil {
		return QueueResult{}, err
	}

	result := QueueResult{}
	for _, lead := range leads {
		if err := c.updateLeadStatus(ctx, lead.ID, leadStatusQueued); err != nil {
			return QueueResult{}, err
		}
		result.Queued++
	}
	return result, nil
}

func (c *Client) listLeadsForQueue(ctx context.Context) ([]qualificationLead, error) {
	query := url.Values{}
	query.Set("select", "id")
	query.Set("status", "eq."+leadStatusReadyToEmail)
	query.Set("order", "created_at.asc")
	requestURL := c.baseURL + "/rest/v1/leads?" + query.Encode()

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, requestURL, nil)
	if err != nil {
		return nil, fmt.Errorf("create queued leads request: %w", err)
	}
	c.setSupabaseHeaders(req)
	req.Header.Set("Prefer", "count=exact")
	req.Header.Set("Range", "0-99")

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("list queued leads: %w", err)
	}
	body, readErr := io.ReadAll(io.LimitReader(resp.Body, 16<<20))
	closeErr := resp.Body.Close()
	if err := errors.Join(readErr, closeErr); err != nil {
		return nil, fmt.Errorf("read queued leads: %w", err)
	}
	if resp.StatusCode != http.StatusOK && resp.StatusCode != http.StatusPartialContent {
		return nil, fmt.Errorf("list queued leads: Supabase returned %s: %s", resp.Status, strings.TrimSpace(string(body)))
	}

	var leads []qualificationLead
	if err := json.Unmarshal(body, &leads); err != nil {
		return nil, fmt.Errorf("decode queued leads: %w", err)
	}
	return leads, nil
}
