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
	"os"
	"time"

	"github.com/marcocolombo/rightsizer/internal/engine"
	"github.com/marcocolombo/rightsizer/internal/report"
	"github.com/marcocolombo/rightsizer/internal/vc"
)

type StartRequest struct {
	Config   engine.Config
	Password string
}

type ProbeRequest struct{ Host string }

type errorBody struct{ Error string }

func Serve(ctx context.Context, sock string, e *engine.Engine) error {
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
	mux.HandleFunc("GET /status", func(w http.ResponseWriter, r *http.Request) {
		reply(w, e.Status(), nil)
	})
	mux.HandleFunc("POST /probe", func(w http.ResponseWriter, r *http.Request) {
		var req ProbeRequest
		if !decode(w, r, &req) {
			return
		}
		ctx, cancel := context.WithTimeout(r.Context(), 15*time.Second)
		defer cancel()
		ci, err := vc.Probe(ctx, req.Host)
		reply(w, ci, err)
	})
	mux.HandleFunc("POST /start", func(w http.ResponseWriter, r *http.Request) {
		var req StartRequest
		if !decode(w, r, &req) {
			return
		}
		ctx, cancel := context.WithTimeout(r.Context(), 60*time.Second)
		defer cancel()
		reply(w, nil, e.Start(ctx, req.Config, req.Password))
	})
	mux.HandleFunc("POST /resume", func(w http.ResponseWriter, r *http.Request) {
		var req StartRequest
		if !decode(w, r, &req) {
			return
		}
		ctx, cancel := context.WithTimeout(r.Context(), 60*time.Second)
		defer cancel()
		reply(w, nil, e.Resume(ctx, req.Password))
	})
	mux.HandleFunc("POST /finish", func(w http.ResponseWriter, r *http.Request) {
		reply(w, nil, e.Finish())
	})
	mux.HandleFunc("POST /cancel", func(w http.ResponseWriter, r *http.Request) {
		reply(w, nil, e.Cancel())
	})
	mux.HandleFunc("POST /publish", func(w http.ResponseWriter, r *http.Request) {
		sh, err := e.Publish()
		reply(w, sh, err)
	})
	mux.HandleFunc("POST /unpublish", func(w http.ResponseWriter, r *http.Request) {
		e.Unpublish()
		reply(w, nil, nil)
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

func (c *Client) Status() (*engine.Status, error) {
	var s engine.Status
	return &s, c.call("GET", "/status", nil, &s)
}

func (c *Client) Probe(host string) (*vc.CertInfo, error) {
	var ci vc.CertInfo
	return &ci, c.call("POST", "/probe", ProbeRequest{host}, &ci)
}

func (c *Client) Start(cfg engine.Config, password string) error {
	return c.call("POST", "/start", StartRequest{cfg, password}, nil)
}

func (c *Client) Resume(password string) error {
	return c.call("POST", "/resume", StartRequest{Password: password}, nil)
}

func (c *Client) Finish() error { return c.call("POST", "/finish", nil, nil) }
func (c *Client) Cancel() error { return c.call("POST", "/cancel", nil, nil) }

func (c *Client) Publish() (*report.Share, error) {
	var s report.Share
	return &s, c.call("POST", "/publish", nil, &s)
}

func (c *Client) Unpublish() error { return c.call("POST", "/unpublish", nil, nil) }
