package vc

import (
	"context"
	"crypto/tls"
	"errors"
	"fmt"
	"net/url"
	"strings"
	"time"

	"github.com/vmware/govmomi/property"
	"github.com/vmware/govmomi/session"
	"github.com/vmware/govmomi/vim25"
	"github.com/vmware/govmomi/vim25/methods"
	"github.com/vmware/govmomi/vim25/soap"
	"github.com/vmware/govmomi/vim25/types"
)

type Credentials struct {
	Host        string
	User        string
	Password    string
	Fingerprint string
}

type Client struct {
	vim   *vim25.Client
	sm    *session.Manager
	creds Credentials
	perf  *perfCounters
}

func Connect(ctx context.Context, c Credentials) (*Client, error) {
	u, err := sdkURL(c.Host)
	if err != nil {
		return nil, err
	}
	sc := soap.NewClient(u, false)
	sc.DefaultTransport().TLSClientConfig.MinVersion = tls.VersionTLS12
	if c.Fingerprint != "" {
		sc.SetThumbprint(u.Host, c.Fingerprint)
	}
	vim, err := vim25.NewClient(ctx, sc)
	if err != nil {
		return nil, err
	}
	vim.RoundTripper = guard{next: sc}
	cl := &Client{vim: vim, sm: session.NewManager(vim), creds: c}
	if err := cl.login(ctx); err != nil {
		return nil, err
	}
	return cl, nil
}

func (c *Client) login(ctx context.Context) error {
	if err := c.sm.Login(ctx, url.UserPassword(c.creds.User, c.creds.Password)); err != nil {
		return fmt.Errorf("login failed: %w", err)
	}
	return nil
}

// Ensure re-establishes the session if vCenter dropped it (restart, timeout).
func (c *Client) Ensure(ctx context.Context) error {
	us, err := c.sm.UserSession(ctx)
	if err == nil && us != nil {
		return nil
	}
	return c.login(ctx)
}

func (c *Client) Close(ctx context.Context) {
	_ = c.sm.Logout(ctx)
}

func (c *Client) About() string {
	a := c.vim.ServiceContent.About
	return fmt.Sprintf("%s (build %s)", a.FullName, a.Build)
}

func (c *Client) IsVCenter() bool {
	return c.vim.IsVC()
}

func sdkURL(host string) (*url.URL, error) {
	host = strings.TrimSpace(host)
	host = strings.TrimPrefix(strings.TrimPrefix(host, "https://"), "http://")
	host = strings.TrimSuffix(strings.TrimSuffix(host, "/"), "/sdk")
	if host == "" || strings.ContainsAny(host, "/?#@ ") {
		return nil, errors.New("invalid vCenter address")
	}
	return &url.URL{Scheme: "https", Host: host, Path: "/sdk"}, nil
}

func (c *Client) Now(ctx context.Context) (time.Time, error) {
	t, err := methods.GetCurrentTime(ctx, c.vim)
	if err != nil {
		return time.Time{}, err
	}
	return *t, nil
}

func IsAuthError(err error) bool {
	return err != nil && (strings.Contains(err.Error(), "login failed") || strings.Contains(err.Error(), "InvalidLogin"))
}

func (c *Client) retrieveOne(ctx context.Context, ref types.ManagedObjectReference, props []string, dst any) error {
	return property.DefaultCollector(c.vim).RetrieveOne(ctx, ref, props, dst)
}
