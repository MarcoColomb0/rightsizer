// Package ontap reads capacity, data reduction and performance from NetApp
// ONTAP clusters through the REST API. It only ever sends GET requests.
package ontap

import (
	"context"
	"crypto/sha256"
	"crypto/tls"
	"crypto/x509"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"maps"
	"net"
	"net/http"
	"net/url"
	"regexp"
	"slices"
	"strings"
	"sync"
	"time"
)

// MinMajor is the oldest supported ONTAP 9 release: 9.10.
const MinMajor = 10

type Credentials struct {
	Host        string
	User        string
	Password    string
	Fingerprint string
}

type Client struct {
	base  *url.URL
	http  *http.Client
	creds Credentials
	info  Cluster

	mu      sync.Mutex
	dropped []string
	rec     *[]Exchange
}

// Exchange is one request and its response, kept for diagnostics.
type Exchange struct {
	Path     string
	Status   int
	Millis   int64
	Bytes    int
	Error    string          `json:",omitempty"`
	Body     json.RawMessage `json:",omitempty"`
	Truncate bool            `json:",omitempty"`
}

// maxRecorded caps the body kept per exchange.
const maxRecorded = 2 << 20

// Record keeps every exchange from now on, for a diagnostic bundle.
func (c *Client) Record() {
	c.mu.Lock()
	c.rec = &[]Exchange{}
	c.mu.Unlock()
}

// Recorded returns the exchanges kept since Record.
func (c *Client) Recorded() []Exchange {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.rec == nil {
		return nil
	}
	return slices.Clone(*c.rec)
}

// Dropped lists the fields this cluster refused, as an older release does.
func (c *Client) Dropped() []string {
	c.mu.Lock()
	defer c.mu.Unlock()
	return slices.Clone(c.dropped)
}

func (c *Client) record(path string, status int, start time.Time, body []byte, err error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.rec == nil {
		return
	}
	x := Exchange{Path: path, Status: status, Millis: time.Since(start).Milliseconds(), Bytes: len(body)}
	if err != nil {
		x.Error = err.Error()
	}
	if len(body) > maxRecorded {
		x.Truncate = true
	} else if json.Valid(body) {
		x.Body = append(json.RawMessage(nil), body...)
	}
	*c.rec = append(*c.rec, x)
}

// ReadOnlyError is returned for any request that is not a GET on the
// cluster's REST API: it never leaves the process.
type ReadOnlyError struct{ Method, Path string }

func (e *ReadOnlyError) Error() string {
	return fmt.Sprintf("blocked non read-only ONTAP request %s %s", e.Method, e.Path)
}

// APIError is an error reported by ONTAP.
type APIError struct {
	Status  int
	Code    string
	Message string
}

func (e *APIError) Error() string {
	if e.Status == http.StatusUnauthorized {
		return "login failed: ONTAP rejected the user name or password"
	}
	if e.Message == "" {
		return fmt.Sprintf("ONTAP returned HTTP %d", e.Status)
	}
	return fmt.Sprintf("ONTAP: %s (HTTP %d, code %s)", e.Message, e.Status, e.Code)
}

// IsAuthError reports whether ONTAP refused the credentials.
func IsAuthError(err error) bool {
	var ae *APIError
	return errors.As(err, &ae) && ae.Status == http.StatusUnauthorized
}

// IsForbidden reports whether the user's role cannot read an endpoint.
func IsForbidden(err error) bool {
	var ae *APIError
	return errors.As(err, &ae) && ae.Status == http.StatusForbidden
}

// guard rejects everything but GET requests to the cluster's /api/ tree,
// so a bug or a crafted next link can never change the cluster or reach
// another host.
type guard struct {
	host string
	next http.RoundTripper
}

func (g guard) RoundTrip(r *http.Request) (*http.Response, error) {
	if r.Method != http.MethodGet || r.URL.Scheme != "https" || r.URL.Host != g.host || !strings.HasPrefix(r.URL.Path, "/api/") {
		return nil, &ReadOnlyError{Method: r.Method, Path: r.URL.Scheme + "://" + r.URL.Host + r.URL.Path}
	}
	return g.next.RoundTrip(r)
}

// Thumbprint is the SHA-256 fingerprint of a certificate, in the same
// colon-separated form shown for vCenter certificates.
func Thumbprint(c *x509.Certificate) string {
	sum := sha256.Sum256(c.Raw)
	parts := make([]string, len(sum))
	for i, b := range sum {
		parts[i] = fmt.Sprintf("%02X", b)
	}
	return strings.Join(parts, ":")
}

func baseURL(host string) (*url.URL, error) {
	host = strings.TrimSpace(host)
	host = strings.TrimPrefix(strings.TrimPrefix(host, "https://"), "http://")
	host = strings.TrimSuffix(strings.TrimSuffix(host, "/"), "/api")
	if host == "" || strings.ContainsAny(host, "/?#@ ") {
		return nil, errors.New("invalid ONTAP cluster address")
	}
	if _, _, err := net.SplitHostPort(host); err != nil {
		host = net.JoinHostPort(host, "443")
	}
	return &url.URL{Scheme: "https", Host: host}, nil
}

// Connect checks the credentials and the ONTAP release. With a fingerprint
// the certificate is pinned instead of verified against trusted CAs.
func Connect(ctx context.Context, c Credentials) (*Client, error) {
	u, err := baseURL(c.Host)
	if err != nil {
		return nil, err
	}
	tc := &tls.Config{MinVersion: tls.VersionTLS12}
	if fp := strings.TrimSpace(c.Fingerprint); fp != "" {
		// The pinned fingerprint replaces CA verification, as for vCenter.
		tc.InsecureSkipVerify = true // #nosec G402
		tc.VerifyConnection = func(cs tls.ConnectionState) error {
			if len(cs.PeerCertificates) == 0 || !strings.EqualFold(Thumbprint(cs.PeerCertificates[0]), fp) {
				return errors.New("the ONTAP certificate does not match the pinned fingerprint")
			}
			return nil
		}
	}
	tr := &http.Transport{
		TLSClientConfig:       tc,
		Proxy:                 nil,
		DialContext:           (&net.Dialer{Timeout: 10 * time.Second}).DialContext,
		TLSHandshakeTimeout:   10 * time.Second,
		ResponseHeaderTimeout: 2 * time.Minute,
		MaxIdleConnsPerHost:   4,
		IdleConnTimeout:       90 * time.Second,
	}
	cl := &Client{
		base:  u,
		creds: c,
		http: &http.Client{
			Transport: guard{host: u.Host, next: tr},
			Timeout:   3 * time.Minute,
			CheckRedirect: func(*http.Request, []*http.Request) error {
				return http.ErrUseLastResponse
			},
		},
	}
	if err := cl.get(ctx, "/api/cluster", url.Values{"fields": {"name,uuid,version"}}, &cl.info); err != nil {
		return nil, err
	}
	if v := cl.info.Version; v.Generation == 9 && v.Major < MinMajor || v.Generation < 9 {
		return nil, fmt.Errorf("ONTAP 9.%d or later is required; %s runs %s", MinMajor, cl.info.Name, v.Release())
	}
	return cl, nil
}

func (c *Client) About() string {
	return fmt.Sprintf("NetApp ONTAP %s (cluster %s)", c.info.Version.Release(), c.info.Name)
}

func (c *Client) Cluster() Cluster { return c.info }

// Close releases idle connections. ONTAP REST has no session to log out of.
func (c *Client) Close(context.Context) { c.http.CloseIdleConnections() }

// Now is the local time: ONTAP timestamps carry their zone, so metrics are
// compared on absolute time.
func (c *Client) Now(context.Context) (time.Time, error) { return time.Now(), nil }

func (c *Client) get(ctx context.Context, path string, q url.Values, out any) error {
	u := *c.base
	ref, err := url.Parse(path)
	if err != nil {
		return err
	}
	u.Path, u.RawQuery = ref.Path, ref.RawQuery
	if q != nil {
		u.RawQuery = q.Encode()
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u.String(), nil)
	if err != nil {
		return err
	}
	req.SetBasicAuth(c.creds.User, c.creds.Password)
	req.Header.Set("Accept", "application/json")
	start := time.Now()
	shown := u.Path + "?" + u.RawQuery
	res, err := c.http.Do(req)
	if err != nil {
		c.record(shown, 0, start, nil, err)
		return err
	}
	defer res.Body.Close()
	body, err := io.ReadAll(io.LimitReader(res.Body, 64<<20))
	c.record(shown, res.StatusCode, start, body, err)
	if err != nil {
		return err
	}
	if res.StatusCode != http.StatusOK {
		ae := &APIError{Status: res.StatusCode}
		var e struct {
			Error struct {
				Message string `json:"message"`
				Code    string `json:"code"`
			} `json:"error"`
		}
		if json.Unmarshal(body, &e) == nil {
			ae.Message, ae.Code = e.Error.Message, e.Error.Code
		}
		return ae
	}
	return json.Unmarshal(body, out)
}

type page[T any] struct {
	Records []T `json:"records"`
	Links   struct {
		Next struct {
			Href string `json:"href"`
		} `json:"next"`
	} `json:"_links"`
}

// invalidField finds the field ONTAP refused, as in: The value "x" is
// invalid for field "fields".
var invalidField = regexp.MustCompile(`"([a-z0-9_.]+)" is (?:an )?invalid`)

// list reads every record of a collection, following next links. Fields an
// older release does not know are dropped and the request repeated, so a
// newer counter never makes a whole collection unavailable.
func list[T any](ctx context.Context, c *Client, path string, fields []string, q url.Values) ([]T, error) {
	fields = slices.Clone(fields)
	for {
		out, err := listOnce[T](ctx, c, path, fields, q)
		var ae *APIError
		if err == nil || !errors.As(err, &ae) || ae.Status != http.StatusBadRequest {
			return out, err
		}
		m := invalidField.FindStringSubmatch(ae.Message)
		if m == nil || !slices.Contains(fields, m[1]) {
			return nil, err
		}
		slog.Info("ONTAP field not available on this release", "path", path, "field", m[1])
		fields = slices.DeleteFunc(fields, func(f string) bool { return f == m[1] })
		c.mu.Lock()
		if f := path + " " + m[1]; !slices.Contains(c.dropped, f) {
			c.dropped = append(c.dropped, f)
		}
		c.mu.Unlock()
	}
}

func listOnce[T any](ctx context.Context, c *Client, path string, fields []string, q url.Values) ([]T, error) {
	v := url.Values{}
	maps.Copy(v, q)
	v.Set("fields", strings.Join(fields, ","))
	v.Set("max_records", "1000")
	var out []T
	next := path + "?" + v.Encode()
	for range 10000 {
		var p page[T]
		if err := c.get(ctx, next, nil, &p); err != nil {
			return nil, err
		}
		out = append(out, p.Records...)
		if p.Links.Next.Href == "" {
			return out, nil
		}
		next = p.Links.Next.Href
	}
	return nil, errors.New("ONTAP returned too many pages")
}

// Reconnect opens a second session with the same credentials and pin, so
// diagnostics never disturb collection.
func (c *Client) Reconnect(ctx context.Context) (*Client, error) { return Connect(ctx, c.creds) }
