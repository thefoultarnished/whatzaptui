package whatsapp

import (
	"context"

	"go.mau.fi/whatsmeow"
	"go.mau.fi/whatsmeow/store"
	waLog "go.mau.fi/whatsmeow/util/log"
)

// Client encapsulates whatsmeow.Client lifecycle, session management, and messaging.
type Client struct {
	*whatsmeow.Client
}

// NewClient initializes a new WhatsApp client wrapper with the configured device store and log level.
func NewClient(device *store.Device, logLevel string) *Client {
	if logLevel == "" {
		logLevel = "INFO"
	}
	logger := waLog.Stdout("client", logLevel, true)
	return &Client{
		Client: whatsmeow.NewClient(device, logger),
	}
}

// WrapClient wraps an existing whatsmeow.Client.
func WrapClient(raw *whatsmeow.Client) *Client {
	return &Client{Client: raw}
}

// IsLoggedIn reports whether the client has an active authenticated session.
func (c *Client) IsLoggedIn() bool {
	if c == nil || c.Client == nil || c.Store == nil {
		return false
	}
	return c.Store.ID != nil
}

// DeleteStore deletes local device credentials and clears the in-memory ID.
func (c *Client) DeleteStore(ctx context.Context) error {
	if c == nil || c.Client == nil || c.Store == nil {
		return nil
	}
	err := c.Store.Delete(ctx)
	c.Store.ID = nil
	return err
}
