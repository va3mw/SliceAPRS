package web

import (
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"time"

	"github.com/va3mw/SliceAPRS/internal/app"
)

// Map tiles come from OpenStreetMap through this server rather than
// straight from the page: OSM refuses tiles to pages served from
// 127.0.0.1 or inside the desktop app ("Access blocked"), and its tile
// policy asks for an identifying User-Agent, caching and at most two
// connections. Tiles are kept in <data_dir>/tiles for a week and an old
// copy is served when OSM cannot be reached.
const (
	tileURL    = "https://tile.openstreetmap.org/%d/%d/%d.png"
	tileMaxAge = 7 * 24 * time.Hour
	tileMaxZ   = 13
)

var (
	tileClient = &http.Client{Timeout: 20 * time.Second}
	tileSlots  = make(chan struct{}, 2)
)

func tileHandler(st *app.Station) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		z, err1 := strconv.Atoi(r.PathValue("z"))
		x, err2 := strconv.Atoi(r.PathValue("x"))
		y, err3 := strconv.Atoi(r.PathValue("y"))
		if err1 != nil || err2 != nil || err3 != nil || z < 0 || z > tileMaxZ ||
			x < 0 || y < 0 || x >= 1<<z || y >= 1<<z {
			http.NotFound(w, r)
			return
		}
		file := filepath.Join(st.Config().DataDir, "tiles", strconv.Itoa(z), strconv.Itoa(x), strconv.Itoa(y)+".png")
		fi, err := os.Stat(file)
		if err == nil && time.Since(fi.ModTime()) < tileMaxAge {
			serveTile(w, r, file)
			return
		}
		if err := fetchTile(st, z, x, y, file); err != nil {
			if fi != nil {
				serveTile(w, r, file) // stale beats nothing
				return
			}
			st.Log().Debug("map tile", "z", z, "x", x, "y", y, "err", err)
			http.Error(w, "map tile unavailable", http.StatusBadGateway)
			return
		}
		serveTile(w, r, file)
	}
}

func serveTile(w http.ResponseWriter, r *http.Request, file string) {
	w.Header().Set("Content-Type", "image/png")
	w.Header().Set("Cache-Control", "max-age=86400")
	http.ServeFile(w, r, file)
}

func fetchTile(st *app.Station, z, x, y int, file string) error {
	select {
	case tileSlots <- struct{}{}:
		defer func() { <-tileSlots }()
	case <-time.After(30 * time.Second):
		return fmt.Errorf("busy")
	}
	req, err := http.NewRequest("GET", fmt.Sprintf(tileURL, z, x, y), nil)
	if err != nil {
		return err
	}
	req.Header.Set("User-Agent", "SliceAPRS/"+st.Status().Version+" (+https://github.com/va3mw/SliceAPRS)")
	resp, err := tileClient.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("%s", resp.Status)
	}
	if err := os.MkdirAll(filepath.Dir(file), 0o755); err != nil {
		return err
	}
	tmp := file + ".part"
	f, err := os.Create(tmp)
	if err != nil {
		return err
	}
	_, err = io.Copy(f, io.LimitReader(resp.Body, 1<<20))
	if cerr := f.Close(); err == nil {
		err = cerr
	}
	if err != nil {
		os.Remove(tmp)
		return err
	}
	return os.Rename(tmp, file)
}
