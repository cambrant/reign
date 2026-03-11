// Package cli provides command-line interface functionality for Reign.
package cli

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"
)

// Client is an HTTP client for the Reign API.
type Client struct {
	baseURL    string
	httpClient *http.Client
}

// NewClient creates a new API client.
func NewClient(baseURL string) *Client {
	return &Client{
		baseURL: baseURL,
		httpClient: &http.Client{
			Timeout: 30 * time.Second,
		},
	}
}

// APIError represents an error response from the API.
type APIError struct {
	Error string `json:"error"`
	Code  string `json:"code,omitempty"`
}

// doRequest performs an HTTP request and returns the response body.
func (c *Client) doRequest(method, path string, body interface{}) ([]byte, error) {
	var reqBody io.Reader
	if body != nil {
		jsonData, err := json.Marshal(body)
		if err != nil {
			return nil, fmt.Errorf("failed to marshal request: %w", err)
		}
		reqBody = bytes.NewReader(jsonData)
	}

	url := c.baseURL + path
	req, err := http.NewRequest(method, url, reqBody)
	if err != nil {
		return nil, fmt.Errorf("failed to create request: %w", err)
	}

	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	req.Header.Set("Accept", "application/json")

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("failed to connect to reign server at %s: %w", c.baseURL, err)
	}
	defer resp.Body.Close()

	respBody, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("failed to read response: %w", err)
	}

	if resp.StatusCode >= 400 {
		var apiErr APIError
		if err := json.Unmarshal(respBody, &apiErr); err == nil && apiErr.Error != "" {
			return nil, fmt.Errorf("%s", apiErr.Error)
		}
		return nil, fmt.Errorf("API error: %s", resp.Status)
	}

	return respBody, nil
}

// ServiceRequest represents a request to create or update a service.
type ServiceRequest struct {
	ID             string `json:"id,omitempty"`
	Name           string `json:"name,omitempty"`
	Type           string `json:"type,omitempty"`
	Path           string `json:"path,omitempty"`
	Command        string `json:"command,omitempty"`
	WorkDir        string `json:"work_dir,omitempty"`
	Enabled        *bool  `json:"enabled,omitempty"`
	Infrastructure *bool  `json:"infrastructure,omitempty"`
}

// ServiceListResponse represents the response for listing services.
type ServiceListResponse struct {
	Services []ServiceWithState `json:"services"`
	Total    int                `json:"total"`
}

// ServiceWithState represents a service with its runtime state.
type ServiceWithState struct {
	ID             string       `json:"id"`
	Name           string       `json:"name"`
	Type           string       `json:"type"`
	Path           string       `json:"path"`
	Command        string       `json:"command,omitempty"`
	WorkDir        string       `json:"work_dir,omitempty"`
	Enabled        bool         `json:"enabled"`
	Infrastructure bool         `json:"infrastructure"`
	CreatedAt      time.Time    `json:"created_at"`
	UpdatedAt      time.Time    `json:"updated_at"`
	State          ServiceState `json:"state"`
}

// ServiceState represents the runtime state of a service.
type ServiceState struct {
	ServiceID    string     `json:"service_id"`
	Status       string     `json:"status"`
	PID          *int       `json:"pid,omitempty"`
	StartedAt    *time.Time `json:"started_at,omitempty"`
	StoppedAt    *time.Time `json:"stopped_at,omitempty"`
	RestartCount int        `json:"restart_count"`
	LastError    *string    `json:"last_error,omitempty"`
	UpdatedAt    time.Time  `json:"updated_at"`
}

// ServiceEvent represents a lifecycle event for a service.
type ServiceEvent struct {
	ID        int       `json:"id"`
	ServiceID string    `json:"service_id"`
	EventType string    `json:"event_type"`
	Message   string    `json:"message,omitempty"`
	CreatedAt time.Time `json:"created_at"`
}

// ServiceDetailResponse represents the response for getting a service.
type ServiceDetailResponse struct {
	ServiceWithState
	Events []ServiceEvent `json:"events"`
}

// ListServices retrieves all services with their state.
func (c *Client) ListServices() (*ServiceListResponse, error) {
	data, err := c.doRequest(http.MethodGet, "/services", nil)
	if err != nil {
		return nil, err
	}

	var resp ServiceListResponse
	if err := json.Unmarshal(data, &resp); err != nil {
		return nil, fmt.Errorf("failed to parse response: %w", err)
	}

	return &resp, nil
}

// GetService retrieves a specific service by ID.
func (c *Client) GetService(id string) (*ServiceDetailResponse, error) {
	data, err := c.doRequest(http.MethodGet, "/services/"+id, nil)
	if err != nil {
		return nil, err
	}

	var resp ServiceDetailResponse
	if err := json.Unmarshal(data, &resp); err != nil {
		return nil, fmt.Errorf("failed to parse response: %w", err)
	}

	return &resp, nil
}

// CreateService creates a new service.
func (c *Client) CreateService(req *ServiceRequest) (*ServiceWithState, error) {
	data, err := c.doRequest(http.MethodPost, "/services", req)
	if err != nil {
		return nil, err
	}

	var resp ServiceWithState
	if err := json.Unmarshal(data, &resp); err != nil {
		return nil, fmt.Errorf("failed to parse response: %w", err)
	}

	return &resp, nil
}

// UpdateService updates an existing service.
func (c *Client) UpdateService(id string, req *ServiceRequest) (*ServiceWithState, error) {
	data, err := c.doRequest(http.MethodPut, "/services/"+id, req)
	if err != nil {
		return nil, err
	}

	var resp ServiceWithState
	if err := json.Unmarshal(data, &resp); err != nil {
		return nil, fmt.Errorf("failed to parse response: %w", err)
	}

	return &resp, nil
}

// DeleteService deletes a service.
func (c *Client) DeleteService(id string) error {
	_, err := c.doRequest(http.MethodDelete, "/services/"+id, nil)
	return err
}

// StartService starts a service.
func (c *Client) StartService(id string) (*ServiceWithState, error) {
	data, err := c.doRequest(http.MethodPost, "/services/"+id+"/start", nil)
	if err != nil {
		return nil, err
	}

	var resp ServiceWithState
	if err := json.Unmarshal(data, &resp); err != nil {
		return nil, fmt.Errorf("failed to parse response: %w", err)
	}

	return &resp, nil
}

// StopService stops a service.
func (c *Client) StopService(id string) (*ServiceWithState, error) {
	data, err := c.doRequest(http.MethodPost, "/services/"+id+"/stop", nil)
	if err != nil {
		return nil, err
	}

	var resp ServiceWithState
	if err := json.Unmarshal(data, &resp); err != nil {
		return nil, fmt.Errorf("failed to parse response: %w", err)
	}

	return &resp, nil
}

// RestartService restarts a service.
func (c *Client) RestartService(id string) (*ServiceWithState, error) {
	data, err := c.doRequest(http.MethodPost, "/services/"+id+"/restart", nil)
	if err != nil {
		return nil, err
	}

	var resp ServiceWithState
	if err := json.Unmarshal(data, &resp); err != nil {
		return nil, fmt.Errorf("failed to parse response: %w", err)
	}

	return &resp, nil
}

// EventLogEntry represents an event with service context.
type EventLogEntry struct {
	ID          int       `json:"id"`
	ServiceID   string    `json:"service_id"`
	ServiceName string    `json:"service_name"`
	EventType   string    `json:"event_type"`
	Message     string    `json:"message,omitempty"`
	CreatedAt   time.Time `json:"created_at"`
}

// EventLogResponse represents the response for the event log.
type EventLogResponse struct {
	Events []EventLogEntry `json:"events"`
	Total  int             `json:"total"`
}

// GetEventLog retrieves the unified event log.
func (c *Client) GetEventLog(limit int) (*EventLogResponse, error) {
	path := fmt.Sprintf("/events?limit=%d", limit)
	data, err := c.doRequest(http.MethodGet, path, nil)
	if err != nil {
		return nil, err
	}

	var resp EventLogResponse
	if err := json.Unmarshal(data, &resp); err != nil {
		return nil, fmt.Errorf("failed to parse response: %w", err)
	}

	return &resp, nil
}

// GetServiceLogs retrieves logs for a service.
func (c *Client) GetServiceLogs(id string, lines int) (string, error) {
	path := fmt.Sprintf("/services/%s/logs?lines=%d", id, lines)
	data, err := c.doRequest(http.MethodGet, path, nil)
	if err != nil {
		return "", err
	}

	var resp struct {
		Logs string `json:"logs"`
	}
	if err := json.Unmarshal(data, &resp); err != nil {
		return "", fmt.Errorf("failed to parse response: %w", err)
	}

	return resp.Logs, nil
}

// FollowServiceLogs streams logs for a service, calling onLine for each line.
// Blocks until ctx is cancelled.
func (c *Client) FollowServiceLogs(ctx context.Context, id string, lines int, onLine func(line string)) error {
	path := fmt.Sprintf("/services/%s/logs?follow=true&lines=%d", id, lines)
	return c.streamSSE(ctx, path, onLine)
}

// FollowEvents streams events, calling onData for each event JSON.
// Blocks until ctx is cancelled.
func (c *Client) FollowEvents(ctx context.Context, limit int, onData func(line string)) error {
	path := fmt.Sprintf("/events?follow=true&limit=%d", limit)
	return c.streamSSE(ctx, path, onData)
}

// streamSSE connects to an SSE endpoint and calls onData for each data line.
func (c *Client) streamSSE(ctx context.Context, path string, onData func(line string)) error {
	url := c.baseURL + path
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return fmt.Errorf("failed to create request: %w", err)
	}
	req.Header.Set("Accept", "text/event-stream")

	// Use a client without timeout for streaming
	client := &http.Client{}
	resp, err := client.Do(req)
	if err != nil {
		return fmt.Errorf("failed to connect to reign server at %s: %w", c.baseURL, err)
	}
	defer resp.Body.Close()

	if resp.StatusCode >= 400 {
		body, _ := io.ReadAll(resp.Body)
		var apiErr APIError
		if err := json.Unmarshal(body, &apiErr); err == nil && apiErr.Error != "" {
			return fmt.Errorf("%s", apiErr.Error)
		}
		return fmt.Errorf("API error: %s", resp.Status)
	}

	scanner := bufio.NewScanner(resp.Body)
	for scanner.Scan() {
		line := scanner.Text()
		if strings.HasPrefix(line, "data: ") {
			onData(strings.TrimPrefix(line, "data: "))
		} else if strings.HasPrefix(line, "event: error") {
			// Next data line will be the error message
			if scanner.Scan() {
				errLine := scanner.Text()
				if strings.HasPrefix(errLine, "data: ") {
					return fmt.Errorf("%s", strings.TrimPrefix(errLine, "data: "))
				}
			}
		}
	}

	return scanner.Err()
}
