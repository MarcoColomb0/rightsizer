package ipc

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"os"
	"time"

	"github.com/MarcoColomb0/rightsizer/internal/analysis"
	"github.com/MarcoColomb0/rightsizer/internal/appliance"
	"github.com/MarcoColomb0/rightsizer/internal/engine"
	"github.com/MarcoColomb0/rightsizer/internal/report"
	"github.com/MarcoColomb0/rightsizer/internal/vc"
)

// Backend is everything the console needs from the engine, whether it runs
// in-process (SSH sessions) or over the local socket (docker exec).
type Backend interface {
	Summary() (*engine.Summary, error)
	Source(id string) (*engine.Status, error)
	Probe(host string) (*vc.CertInfo, error)
	Add(cfg engine.Config, password string) (string, error)
	Resume(id, password string) error
	Finish(id string) error
	Remove(id string) error
	Publish(id string) (*report.Share, error)
	StopShare(id string) error
	ChangePassword(old, next string) error
	RequestUpgrade(tag string) error
	RequestReboot() error
	Exclusions() ([]analysis.Exclusion, error)
	Exclude(x analysis.Exclusion) error
	Unexclude(id string) error
	Sizing(id string) (*analysis.Sizing, error)
	SizingParams() (*analysis.SizingParams, error)
	SetSizingParams(p analysis.SizingParams) error
	PublishSizing(id string) (*report.Share, error)
}

// Local serves the console in-process. Host is set on the appliance.
type Local struct {
	E    *engine.Engine
	Host *appliance.Host
}

func (l Local) Summary() (*engine.Summary, error) {
	s := l.E.Summary()
	if l.Host != nil {
		if u := l.Host.Status(); u != nil {
			s.Upgrade = &engine.UpgradeInfo{Version: u.Version, State: u.State, Message: u.Message, Time: u.Time}
		}
		s.Reboot = l.Host.RebootReasons()
	}
	return &s, nil
}

func (l Local) RequestReboot() error {
	if l.Host == nil {
		return errors.New("only the appliance can be restarted from the console")
	}
	return l.Host.RequestReboot()
}

func (l Local) RequestUpgrade(tag string) error {
	if l.Host == nil {
		return errors.New("upgrades are run by the rightsizer launcher on the host")
	}
	return l.Host.RequestUpgrade(tag)
}

func (l Local) Source(id string) (*engine.Status, error) {
	s, err := l.E.Source(id)
	return &s, err
}

func (l Local) Probe(host string) (*vc.CertInfo, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	return vc.Probe(ctx, host)
}

func (l Local) Add(cfg engine.Config, password string) (string, error) {
	ctx, cancel := context.WithTimeout(context.Background(), time.Minute)
	defer cancel()
	return l.E.Add(ctx, cfg, password)
}

func (l Local) Resume(id, password string) error {
	ctx, cancel := context.WithTimeout(context.Background(), time.Minute)
	defer cancel()
	return l.E.Resume(ctx, id, password)
}

func (l Local) Exclusions() ([]analysis.Exclusion, error) { return l.E.Exclusions(), nil }
func (l Local) Exclude(x analysis.Exclusion) error {
	_, err := l.E.Exclude(x)
	return err
}
func (l Local) Unexclude(id string) error { return l.E.Unexclude(id) }

func (l Local) Sizing(id string) (*analysis.Sizing, error) { return l.E.Sizing(id) }
func (l Local) SizingParams() (*analysis.SizingParams, error) {
	p := l.E.SizingParams()
	return &p, nil
}
func (l Local) SetSizingParams(p analysis.SizingParams) error  { return l.E.SetSizingParams(p) }
func (l Local) PublishSizing(id string) (*report.Share, error) { return l.E.PublishSizing(id) }

func (l Local) Finish(id string) error                   { return l.E.Finish(id) }
func (l Local) Remove(id string) error                   { return l.E.Remove(id) }
func (l Local) Publish(id string) (*report.Share, error) { return l.E.Publish(id) }
func (l Local) StopShare(id string) error                { l.E.StopShare(id); return nil }
func (l Local) ChangePassword(old, next string) error    { return l.E.ChangePassword(old, next) }

type addRequest struct {
	Config   engine.Config
	Password string
}

type idRequest struct {
	ID       string
	Password string
	Host     string
	Old      string
}

type errorBody struct{ Error string }

// Serve exposes the backend on a unix socket that only the engine user can
// open. The socket cannot change the administrator password or restart the
// appliance.
func Serve(ctx context.Context, sock string, b Local) error {
	_ = os.Remove(sock)
	ln, err := net.Listen("unix", sock)
	if err != nil {
		return err
	}
	if err := os.Chmod(sock, 0o600); err != nil {
		ln.Close()
		return err
	}
	mux := http.NewServeMux()
	mux.HandleFunc("GET /summary", func(w http.ResponseWriter, r *http.Request) {
		s, err := b.Summary()
		reply(w, s, err)
	})
	mux.HandleFunc("GET /sources/{id}", func(w http.ResponseWriter, r *http.Request) {
		s, err := b.Source(r.PathValue("id"))
		reply(w, s, err)
	})
	mux.HandleFunc("POST /probe", func(w http.ResponseWriter, r *http.Request) {
		var req idRequest
		if decode(w, r, &req) {
			ci, err := b.Probe(req.Host)
			reply(w, ci, err)
		}
	})
	mux.HandleFunc("POST /sources", func(w http.ResponseWriter, r *http.Request) {
		var req addRequest
		if decode(w, r, &req) {
			id, err := b.Add(req.Config, req.Password)
			reply(w, idRequest{ID: id}, err)
		}
	})
	mux.HandleFunc("POST /sources/{id}/resume", func(w http.ResponseWriter, r *http.Request) {
		var req idRequest
		if decode(w, r, &req) {
			reply(w, nil, b.Resume(r.PathValue("id"), req.Password))
		}
	})
	mux.HandleFunc("POST /sources/{id}/finish", func(w http.ResponseWriter, r *http.Request) {
		reply(w, nil, b.Finish(r.PathValue("id")))
	})
	mux.HandleFunc("DELETE /sources/{id}", func(w http.ResponseWriter, r *http.Request) {
		reply(w, nil, b.Remove(r.PathValue("id")))
	})
	mux.HandleFunc("POST /publish", func(w http.ResponseWriter, r *http.Request) {
		var req idRequest
		if decode(w, r, &req) {
			sh, err := b.Publish(req.ID)
			reply(w, sh, err)
		}
	})
	mux.HandleFunc("POST /shares/{id}/stop", func(w http.ResponseWriter, r *http.Request) {
		reply(w, nil, b.StopShare(r.PathValue("id")))
	})
	mux.HandleFunc("GET /exclusions", func(w http.ResponseWriter, r *http.Request) {
		xs, err := b.Exclusions()
		reply(w, xs, err)
	})
	mux.HandleFunc("POST /exclusions", func(w http.ResponseWriter, r *http.Request) {
		var x analysis.Exclusion
		if decode(w, r, &x) {
			reply(w, nil, b.Exclude(x))
		}
	})
	mux.HandleFunc("DELETE /exclusions/{id}", func(w http.ResponseWriter, r *http.Request) {
		reply(w, nil, b.Unexclude(r.PathValue("id")))
	})
	mux.HandleFunc("GET /sizing", func(w http.ResponseWriter, r *http.Request) {
		sz, err := b.Sizing(r.URL.Query().Get("id"))
		reply(w, sz, err)
	})
	mux.HandleFunc("GET /sizing/params", func(w http.ResponseWriter, r *http.Request) {
		p, err := b.SizingParams()
		reply(w, p, err)
	})
	mux.HandleFunc("PUT /sizing/params", func(w http.ResponseWriter, r *http.Request) {
		var p analysis.SizingParams
		if decode(w, r, &p) {
			reply(w, nil, b.SetSizingParams(p))
		}
	})
	mux.HandleFunc("POST /sizing/publish", func(w http.ResponseWriter, r *http.Request) {
		var req idRequest
		if decode(w, r, &req) {
			sh, err := b.PublishSizing(req.ID)
			reply(w, sh, err)
		}
	})
	mux.HandleFunc("GET /latest-report", func(w http.ResponseWriter, r *http.Request) {
		p, err := b.E.LatestReport()
		reply(w, idRequest{ID: p}, err)
	})
	srv := &http.Server{Handler: mux, ReadHeaderTimeout: 5 * time.Second}
	go func() {
		<-ctx.Done()
		sctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		_ = srv.Shutdown(sctx)
	}()
	if err := srv.Serve(ln); err != nil && !errors.Is(err, http.ErrServerClosed) {
		return err
	}
	return nil
}

func decode(w http.ResponseWriter, r *http.Request, v any) bool {
	if err := json.NewDecoder(io.LimitReader(r.Body, 1<<16)).Decode(v); err != nil {
		reply(w, nil, fmt.Errorf("bad request: %w", err))
		return false
	}
	return true
}

func reply(w http.ResponseWriter, v any, err error) {
	w.Header().Set("Content-Type", "application/json")
	if err != nil {
		w.WriteHeader(http.StatusBadRequest)
		_ = json.NewEncoder(w).Encode(errorBody{err.Error()})
		return
	}
	_ = json.NewEncoder(w).Encode(v)
}

type Client struct{ hc *http.Client }

func NewClient(sock string) *Client {
	return &Client{hc: &http.Client{
		Timeout: 90 * time.Second,
		Transport: &http.Transport{DialContext: func(ctx context.Context, _, _ string) (net.Conn, error) {
			var d net.Dialer
			return d.DialContext(ctx, "unix", sock)
		}},
	}}
}

func (c *Client) call(method, path string, in, out any) error {
	var body io.Reader
	if in != nil {
		b, err := json.Marshal(in)
		if err != nil {
			return err
		}
		body = bytes.NewReader(b)
	}
	req, err := http.NewRequest(method, "http://rightsizer"+path, body)
	if err != nil {
		return err
	}
	res, err := c.hc.Do(req)
	if err != nil {
		return fmt.Errorf("cannot reach rightsizer daemon: %w", err)
	}
	defer res.Body.Close()
	if res.StatusCode != http.StatusOK {
		var eb errorBody
		_ = json.NewDecoder(res.Body).Decode(&eb)
		return errors.New(eb.Error)
	}
	if out != nil {
		return json.NewDecoder(res.Body).Decode(out)
	}
	return nil
}

func (c *Client) Summary() (*engine.Summary, error) {
	var s engine.Summary
	return &s, c.call("GET", "/summary", nil, &s)
}

func (c *Client) Source(id string) (*engine.Status, error) {
	var s engine.Status
	return &s, c.call("GET", "/sources/"+id, nil, &s)
}

func (c *Client) Probe(host string) (*vc.CertInfo, error) {
	var ci vc.CertInfo
	return &ci, c.call("POST", "/probe", idRequest{Host: host}, &ci)
}

func (c *Client) Add(cfg engine.Config, password string) (string, error) {
	var r idRequest
	return r.ID, c.call("POST", "/sources", addRequest{cfg, password}, &r)
}

func (c *Client) Resume(id, password string) error {
	return c.call("POST", "/sources/"+id+"/resume", idRequest{Password: password}, nil)
}

func (c *Client) Finish(id string) error { return c.call("POST", "/sources/"+id+"/finish", nil, nil) }
func (c *Client) Remove(id string) error { return c.call("DELETE", "/sources/"+id, nil, nil) }

func (c *Client) Publish(id string) (*report.Share, error) {
	var s report.Share
	return &s, c.call("POST", "/publish", idRequest{ID: id}, &s)
}

func (c *Client) StopShare(id string) error { return c.call("POST", "/shares/"+id+"/stop", nil, nil) }

func (c *Client) Exclusions() ([]analysis.Exclusion, error) {
	var xs []analysis.Exclusion
	return xs, c.call("GET", "/exclusions", nil, &xs)
}

func (c *Client) Exclude(x analysis.Exclusion) error { return c.call("POST", "/exclusions", x, nil) }

func (c *Client) Unexclude(id string) error { return c.call("DELETE", "/exclusions/"+id, nil, nil) }

func (c *Client) Sizing(id string) (*analysis.Sizing, error) {
	var sz analysis.Sizing
	return &sz, c.call("GET", "/sizing?id="+url.QueryEscape(id), nil, &sz)
}

func (c *Client) SizingParams() (*analysis.SizingParams, error) {
	var p analysis.SizingParams
	return &p, c.call("GET", "/sizing/params", nil, &p)
}

func (c *Client) SetSizingParams(p analysis.SizingParams) error {
	return c.call("PUT", "/sizing/params", p, nil)
}

func (c *Client) PublishSizing(id string) (*report.Share, error) {
	var s report.Share
	return &s, c.call("POST", "/sizing/publish", idRequest{ID: id}, &s)
}

func (c *Client) RequestReboot() error {
	return errors.New("only the appliance can be restarted from the console")
}

func (c *Client) RequestUpgrade(string) error {
	return errors.New("upgrades are run by the rightsizer launcher on the host")
}

func (c *Client) ChangePassword(string, string) error {
	return errors.New("not available in this installation")
}

func (c *Client) LatestReport() (string, error) {
	var r idRequest
	return r.ID, c.call("GET", "/latest-report", nil, &r)
}
