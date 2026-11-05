package app

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/va3mw/SliceAPRS/internal/config"
	"github.com/va3mw/SliceAPRS/internal/track"
)

func TestOpeningAlert(t *testing.T) {
	o := track.Opening{Start: time.Date(2026, 11, 4, 14, 5, 0, 0, time.UTC), Direction: "NNE",
		MaxCall: "VE3ABC-9", MaxKm: 412.4, NormalKm: 180,
		Stations: []track.FarStation{{Call: "VE3ABC-9"}, {Call: "W2XYZ"}}}
	a := OpeningAlert(track.LevelOpen, o)
	if a.Title != "2 m is open" {
		t.Errorf("title %q", a.Title)
	}
	if want := "toward NNE · 2 stations beyond normal · farthest VE3ABC-9 412 km (2.3× normal) · since 14:05 UTC"; a.Text != want {
		t.Errorf("text %q", a.Text)
	}
	if want := "Two metres is open toward the north north east. Farthest station V E 3 A B C dash 9, at 412 kilometres."; a.Speech != want {
		t.Errorf("speech %q", a.Speech)
	}
	o.Direction, o.Stations = "several directions", o.Stations[:1]
	if a := OpeningAlert(track.LevelEnhanced, o); a.Title != "2 m is enhanced" ||
		!strings.HasPrefix(a.Speech, "Two metres is enhanced in several directions. Farthest") ||
		!strings.HasPrefix(a.Text, "toward several directions · 1 station beyond normal ·") {
		t.Errorf("%+v", a)
	}
}

func TestSendPushover(t *testing.T) {
	var got url.Values
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		b, _ := io.ReadAll(r.Body)
		got, _ = url.ParseQuery(string(b))
		if got.Get("token") == "bad" {
			w.WriteHeader(http.StatusBadRequest)
			io.WriteString(w, `{"token":"invalid","errors":["application token is invalid"],"status":0}`)
			return
		}
		io.WriteString(w, `{"status":1,"request":"x"}`)
	}))
	defer srv.Close()
	old := pushoverURL
	pushoverURL = srv.URL
	defer func() { pushoverURL = old }()

	p := config.Pushover{Enabled: true, UserKey: "ukey", Token: "tok", Priority: -1}
	if err := SendPushover(context.Background(), p, Alert{Title: "T", Text: "body"}); err != nil {
		t.Fatal(err)
	}
	if got.Get("user") != "ukey" || got.Get("token") != "tok" || got.Get("title") != "T" || got.Get("message") != "body" || got.Get("priority") != "-1" {
		t.Fatalf("sent %v", got)
	}
	p.Token = "bad"
	if err := SendPushover(context.Background(), p, Alert{Title: "T", Text: "body"}); err == nil || err.Error() != "Pushover: application token is invalid" {
		t.Fatalf("err %v", err)
	}
}
