// Package daemon provides the client for communicating with the
// obey daemon for agent registration and coordination.
package daemon

// Client communicates with the obey daemon for agent registration.
type Client struct {
	endpoint string
}

// New creates a daemon client targeting the given endpoint.
func New(endpoint string) *Client {
	return &Client{endpoint: endpoint}
}
