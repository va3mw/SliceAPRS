// Package web serves the SliceAPRS GUI: a single page and a small JSON API.
// The headless program serves it over HTTP and the desktop app shows the
// same handler in its window. The structure follows FlexWSPR.
package web

import (
	"context"
	"embed"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"net"
	"net/http"
	"net/url"
	"os"
	"strconv"
	"sync"
	"time"

	"github.com/va3mw/SliceAPRS/internal/app"
	"github.com/va3mw/SliceAPRS/internal/config"
	"github.com/va3mw/SliceAPRS/internal/flex"
	"github.com/va3mw/SliceAPRS/internal/track"
)

//go:embed static
var static embed.FS

// Options set what the GUI may change.
type Options struct {
	// Local is true for the desktop app's own window. Over HTTP the page
	// cannot change the data folder or the web address; those stay
	// editable in the config file and the desktop app.
	Local bool
}

// Handler returns the GUI and its API for st.
func Handler(st *app.Station, opt Options) http.Handler {
	mux := http.NewServeMux()
	files, _ := fs.Sub(static, "static")
	mux.Handle("GET /", http.FileServerFS(files))
	mux.HandleFunc("GET /tiles/{z}/{x}/{y}", tileHandler(st))

	mux.HandleFunc("GET /api/status", func(w http.ResponseWriter, r *http.Request) {
		writeJSON(w, http.StatusOK, st.Status())
	})
	mux.HandleFunc("GET /api/overview", func(w http.ResponseWriter, r *http.Request) {
		an := st.Analyzer()
		if an == nil {
			writeJSON(w, http.StatusServiceUnavailable, map[string]string{"error": "not started"})
			return
		}
		lat, lon, _ := st.Config().LatLon()
		writeJSON(w, http.StatusOK, an.Overview(lat, lon))
	})
	mux.HandleFunc("GET /api/paths", func(w http.ResponseWriter, r *http.Request) {
		an := st.Analyzer()
		if an == nil {
			writeJSON(w, http.StatusOK, []track.Path{})
			return
		}
		min, _ := strconv.Atoi(r.URL.Query().Get("minutes"))
		if min <= 0 || min > 24*60 {
			min = 60
		}
		ps := an.PathsSince(time.Now().Add(-time.Duration(min) * time.Minute))
		if ps == nil {
			ps = []track.Path{}
		}
		writeJSON(w, http.StatusOK, ps)
	})
	mux.HandleFunc("GET /api/stations", func(w http.ResponseWriter, r *http.Request) {
		an := st.Analyzer()
		out := []track.Station{}
		if an != nil {
			out = an.Stations()
		}
		writeJSON(w, http.StatusOK, out)
	})
	mux.HandleFunc("GET /api/grids", func(w http.ResponseWriter, r *http.Request) {
		an := st.Analyzer()
		out := []track.Grid{}
		if an != nil {
			out = an.Grids()
		}
		lat, lon, _ := st.Config().LatLon()
		writeJSON(w, http.StatusOK, map[string]any{"grids": out, "home_lat": lat, "home_lon": lon, "home": st.Config().Grid})
	})
	mux.HandleFunc("GET /api/packets", func(w http.ResponseWriter, r *http.Request) {
		an := st.Analyzer()
		since, _ := strconv.ParseInt(r.URL.Query().Get("since"), 10, 64)
		out := []track.Heard{}
		if an != nil {
			if h := an.HeardSince(since); h != nil {
				out = h
			}
		}
		writeJSON(w, http.StatusOK, out)
	})
	mux.HandleFunc("GET /api/config", func(w http.ResponseWriter, r *http.Request) {
		writeJSON(w, http.StatusOK, configReply(st, opt))
	})
	mux.HandleFunc("POST /api/config", func(w http.ResponseWriter, r *http.Request) {
		c := config.Default()
		dec := json.NewDecoder(http.MaxBytesReader(w, r.Body, 1<<20))
		if err := dec.Decode(c); err != nil {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": "bad settings: " + err.Error()})
			return
		}
		cur := st.Config()
		if !opt.Local {
			c.DataDir, c.Web.Listen = cur.DataDir, cur.Web.Listen
		}
		keepKeys(&c.Alerts.Pushover, cur.Alerts.Pushover)
		if err := st.Apply(c); err != nil {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": err.Error()})
			return
		}
		writeJSON(w, http.StatusOK, configReply(st, opt))
	})
	mux.HandleFunc("POST /api/alerts/test", func(w http.ResponseWriter, r *http.Request) {
		var req struct {
			What   string        `json:"what"`
			Alerts config.Alerts `json:"alerts"`
		}
		if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 1<<16)).Decode(&req); err != nil {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": "bad request: " + err.Error()})
			return
		}
		keepKeys(&req.Alerts.Pushover, st.Config().Alerts.Pushover)
		ctx, cancel := context.WithTimeout(r.Context(), 30*time.Second)
		defer cancel()
		if err := st.TestAlert(ctx, req.What, req.Alerts); err != nil {
			st.Log().Warn("alert test", "what", req.What, "err", err)
			writeJSON(w, http.StatusBadGateway, map[string]string{"error": err.Error()})
			return
		}
		st.Log().Info("alert test sent", "what", req.What)
		writeJSON(w, http.StatusOK, map[string]string{"ok": "sent"})
	})
	mux.HandleFunc("GET /api/radios", func(w http.ResponseWriter, r *http.Request) {
		radios, err := flex.DiscoverAll(r.Context(), 3*time.Second)
		if err != nil {
			writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
			return
		}
		if radios == nil {
			radios = []flex.RadioInfo{}
		}
		writeJSON(w, http.StatusOK, radios)
	})
	mux.HandleFunc("GET /api/log", func(w http.ResponseWriter, r *http.Request) {
		since, _ := strconv.Atoi(r.URL.Query().Get("since"))
		lines, seq := st.Logs.Since(since)
		if lines == nil {
			lines = []string{}
		}
		writeJSON(w, http.StatusOK, map[string]any{"lines": lines, "seq": seq})
	})
	h := noCache(mux)
	if !opt.Local {
		h = sameOrigin(h)
	}
	return h
}

// configReply is the settings as the page sees them. The Pushover keys are
// never sent back: the page shows whether they are set, and a blank field
// keeps the saved key.
func configReply(st *app.Station, opt Options) map[string]any {
	c := st.Config()
	p := &c.Alerts.Pushover
	set := map[string]bool{"user_key": p.UserKey != "", "token": p.Token != ""}
	p.UserKey, p.Token = "", ""
	return map[string]any{"config": c, "local": opt.Local, "pushover_set": set}
}

// keepKeys fills blank Pushover keys from the saved ones.
func keepKeys(p *config.Pushover, saved config.Pushover) {
	if p.UserKey == "" {
		p.UserKey = saved.UserKey
	}
	if p.Token == "" {
		p.Token = saved.Token
	}
}

// sameOrigin refuses changes sent by other web sites: a page elsewhere could
// otherwise post to http://localhost:8081 from the user's browser.
func sameOrigin(h http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet && r.Method != http.MethodHead {
			if ct := r.Header.Get("Content-Type"); ct != "application/json" {
				http.Error(w, "send JSON", http.StatusUnsupportedMediaType)
				return
			}
			if o := r.Header.Get("Origin"); o != "" {
				u, err := url.Parse(o)
				if err != nil || u.Host != r.Host {
					http.Error(w, "cross-site request refused", http.StatusForbidden)
					return
				}
			}
		}
		h.ServeHTTP(w, r)
	})
}

func writeJSON(w http.ResponseWriter, code int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(code)
	json.NewEncoder(w).Encode(v)
}

func noCache(h http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Cache-Control", "no-store")
		h.ServeHTTP(w, r)
	})
}

// Server is the web page served over HTTP. It moves to a new address when
// web.listen changes in the settings.
type Server struct {
	st   *app.Station
	mu   sync.Mutex
	addr string
	srv  *http.Server
}

// Start serves the page on addr ("off" or "" serves nothing). With follow,
// it moves when web.listen changes in the settings.
func Start(addr string, st *app.Station, follow bool) *Server {
	s := &Server{st: st}
	s.Set(addr)
	if follow {
		st.OnConfig(func(c *config.Config) { s.Set(c.Web.Listen) })
	}
	return s
}

// Set moves the page to addr. On failure (say the port is taken) the page
// stays off and the error is logged.
func (s *Server) Set(addr string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if addr == s.addr && (s.srv != nil || addr == "" || addr == "off") {
		return nil
	}
	if s.srv != nil {
		s.srv.Close()
		s.srv = nil
	}
	s.addr = addr
	if addr == "" || addr == "off" {
		s.st.Log().Info("web page off")
		return nil
	}
	ln, err := net.Listen("tcp", addr)
	if err != nil {
		s.st.Log().Error("web page", "addr", addr, "err", err)
		return err
	}
	srv := &http.Server{Handler: Handler(s.st, Options{}), ReadHeaderTimeout: 10 * time.Second}
	s.srv = srv
	s.st.Log().Info("web page ready", "url", PageURL(ln.Addr()))
	go func() {
		if err := srv.Serve(ln); err != nil && !errors.Is(err, http.ErrServerClosed) {
			s.st.Log().Error("web page", "err", err)
		}
	}()
	return nil
}

// Close stops the page.
func (s *Server) Close() { s.Set("off") }

// PageURL is the address to type in a browser.
func PageURL(a net.Addr) string {
	ta, ok := a.(*net.TCPAddr)
	if !ok {
		return "http://" + a.String() + "/"
	}
	host := ta.IP.String()
	if ta.IP.IsUnspecified() {
		host, _ = os.Hostname()
		if host == "" {
			host = "localhost"
		}
	}
	return fmt.Sprintf("http://%s/", net.JoinHostPort(host, fmt.Sprint(ta.Port)))
}
