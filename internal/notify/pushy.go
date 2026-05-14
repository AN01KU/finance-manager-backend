package notify

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"net/http"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/yanonymousV2/finance-manager-backend/internal/applog"
)

// Client sends push notifications via Pushy.
// If apiKey is empty, all methods are no-ops (notifications disabled).
type Client struct {
	apiKey     string
	httpClient *http.Client
	pool       *pgxpool.Pool
}

// New creates a Pushy notification client.
func New(apiKey string, pool *pgxpool.Pool) *Client {
	return &Client{
		apiKey: apiKey,
		httpClient: &http.Client{
			Timeout: 10 * time.Second,
		},
		pool: pool,
	}
}

// Enabled reports whether push notifications are configured.
func (c *Client) Enabled() bool {
	return c.apiKey != ""
}

// IOSNotification holds iOS-specific display options.
type IOSNotification struct {
	Title string `json:"title"`
	Body  string `json:"body"`
	Badge *int   `json:"badge,omitempty"`
	Sound string `json:"sound,omitempty"`
}

type pushRequest struct {
	To           interface{}      `json:"to"`
	Data         map[string]any   `json:"data"`
	Notification *IOSNotification `json:"notification,omitempty"`
}

type pushResponse struct {
	Success bool   `json:"success"`
	ID      string `json:"id"`
	Info    struct {
		Devices int      `json:"devices"`
		Failed  []string `json:"failed,omitempty"`
	} `json:"info"`
	Code  string `json:"code,omitempty"`
	Error string `json:"error,omitempty"`
}

// SendToUser sends a push notification to all devices registered by a user.
func (c *Client) SendToUser(ctx context.Context, userID uuid.UUID, data map[string]any, notification *IOSNotification) {
	if !c.Enabled() {
		return
	}

	tokens, err := c.getTokensForUser(ctx, userID)
	if err != nil || len(tokens) == 0 {
		return
	}

	go c.send(tokens, data, notification)
}

// SendToUsers sends a push notification to all devices registered by multiple users.
func (c *Client) SendToUsers(ctx context.Context, userIDs []uuid.UUID, data map[string]any, notification *IOSNotification) {
	if !c.Enabled() || len(userIDs) == 0 {
		return
	}

	tokens, err := c.getTokensForUsers(ctx, userIDs)
	if err != nil || len(tokens) == 0 {
		return
	}

	go c.send(tokens, data, notification)
}

func (c *Client) send(tokens []string, data map[string]any, notification *IOSNotification) {
	var to interface{} = tokens
	if len(tokens) == 1 {
		to = tokens[0]
	}

	req := pushRequest{
		To:           to,
		Data:         data,
		Notification: notification,
	}

	body, err := json.Marshal(req)
	if err != nil {
		slog.Error("failed to marshal push request", applog.KeyError, err)
		return
	}

	url := fmt.Sprintf("https://api.pushy.me/push?api_key=%s", c.apiKey)
	httpReq, err := http.NewRequest("POST", url, bytes.NewReader(body))
	if err != nil {
		slog.Error("failed to create push request", applog.KeyError, err)
		return
	}
	httpReq.Header.Set("Content-Type", "application/json")

	resp, err := c.httpClient.Do(httpReq)
	if err != nil {
		slog.Error("push request failed", applog.KeyError, err)
		return
	}
	defer resp.Body.Close() //nolint:errcheck

	var result pushResponse
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		slog.Error("failed to decode push response", applog.KeyError, err)
		return
	}

	if !result.Success && result.Error != "" {
		slog.Error("push delivery failed", "pushy_error", result.Error, "pushy_code", result.Code)
		return
	}

	if len(result.Info.Failed) > 0 {
		slog.Warn("push tokens failed delivery, cleaning up", "failed_tokens", len(result.Info.Failed))
		c.removeInvalidTokens(result.Info.Failed)
	}
}

func (c *Client) getTokensForUser(ctx context.Context, userID uuid.UUID) ([]string, error) {
	rows, err := c.pool.Query(ctx,
		`SELECT token FROM device_tokens WHERE user_id = $1`, userID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var tokens []string
	for rows.Next() {
		var token string
		if err := rows.Scan(&token); err != nil {
			return nil, err
		}
		tokens = append(tokens, token)
	}
	return tokens, rows.Err()
}

func (c *Client) getTokensForUsers(ctx context.Context, userIDs []uuid.UUID) ([]string, error) {
	rows, err := c.pool.Query(ctx,
		`SELECT token FROM device_tokens WHERE user_id = ANY($1)`, userIDs)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var tokens []string
	for rows.Next() {
		var token string
		if err := rows.Scan(&token); err != nil {
			return nil, err
		}
		tokens = append(tokens, token)
	}
	return tokens, rows.Err()
}

// removeInvalidTokens deletes tokens that Pushy reported as invalid.
func (c *Client) removeInvalidTokens(tokens []string) {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	_, _ = c.pool.Exec(ctx,
		`DELETE FROM device_tokens WHERE token = ANY($1)`, tokens)
}
