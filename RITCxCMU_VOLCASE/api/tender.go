package api

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
)

func GetTenders(ctx context.Context, baseURL, apiKey string) ([]Tender, error) {
	url := fmt.Sprintf("%s/tenders", baseURL)
	req, _ := http.NewRequestWithContext(ctx, "GET", url, nil)
	if apiKey != "" {
		req.Header.Set("X-API-Key", apiKey)
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	if resp.StatusCode >= 300 {
		body, _ := io.ReadAll(resp.Body)
		return nil, fmt.Errorf("get tenders failed %s: %s", resp.Status, string(body))
	}

	var tenders []Tender
	if err := json.NewDecoder(resp.Body).Decode(&tenders); err != nil {
		return nil, err
	}
	return tenders, nil
}

func AcceptTender(ctx context.Context, baseURL, apiKey string, tenderID int) error {
	url := fmt.Sprintf("%s/tenders/%d", baseURL, tenderID)
	req, _ := http.NewRequestWithContext(ctx, "POST", url, nil)
	if apiKey != "" {
		req.Header.Set("X-API-Key", apiKey)
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode >= 300 {
		body, _ := io.ReadAll(resp.Body)
		return fmt.Errorf("accept tender failed %s: %s", resp.Status, string(body))
	}
	return nil
}
