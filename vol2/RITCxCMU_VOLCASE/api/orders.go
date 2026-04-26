package api

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"strings"
)

func PlaceOrder(ctx context.Context, baseURL, apiKey string, o Order) error {
	// Build the URL exactly as your format shows
	url := fmt.Sprintf("%s/orders?ticker=%s&type=%s&quantity=%d&action=%s",
		baseURL, o.Ticker, o.Type, o.Quantity, o.Action)

	req, _ := http.NewRequestWithContext(ctx, "POST", url, nil)
	if apiKey != "" {
		req.Header.Set("X-API-Key", apiKey)
	}

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()

	// always print HTTP status
	fmt.Println("status:", resp.Status)

	// if not success, read body and return it as part of error
	if resp.StatusCode >= 300 {
		body, _ := io.ReadAll(resp.Body) // ignore read error
		return fmt.Errorf("order failed %s: %s", resp.Status, strings.TrimSpace(string(body)))
	}

	return nil
}
