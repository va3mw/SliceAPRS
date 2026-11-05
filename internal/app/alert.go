package app

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"time"

	"github.com/va3mw/SliceAPRS/internal/config"
	"github.com/va3mw/SliceAPRS/internal/track"
)

// Alerts: when the level rises to the set level or above, SliceAPRS speaks
// on this computer and/or sends a Pushover message. The same level is not
// alerted again within alertGap, so a band hovering at the edge does not
// keep alerting.
const alertGap = 30 * time.Minute

var pushoverURL = "https://api.pushover.net/1/messages.json" // a variable for the tests

type alerter struct {
	mu    sync.Mutex
	last  map[string]time.Time
	voice sync.Mutex // one spoken alert at a time
}

// Alert is one alert, written for the screen and for speech.
type Alert struct {
	Title  string
	Text   string
	Speech string
}

// levelUp is called by the tracker (under the analyzer's lock) when the
// level rises, so the sending happens elsewhere.
func (s *Station) levelUp(level string, o track.Opening) {
	c := s.Config().Alerts
	if track.LevelRank(level) < track.LevelRank(c.Level) || (!c.Voice && !c.Pushover.Enabled) {
		return
	}
	now := time.Now()
	s.alert.mu.Lock()
	if s.alert.last == nil {
		s.alert.last = map[string]time.Time{}
	}
	if t, ok := s.alert.last[level]; ok && now.Sub(t) < alertGap {
		s.alert.mu.Unlock()
		return
	}
	s.alert.last[level] = now
	s.alert.mu.Unlock()
	go s.send(c, OpeningAlert(level, o))
}

// send speaks and/or pushes an alert, logging the outcome.
func (s *Station) send(c config.Alerts, a Alert) {
	s.log.Info("alert", "title", a.Title, "text", a.Text, "voice", c.Voice, "pushover", c.Pushover.Enabled)
	if c.Pushover.Enabled {
		go func() {
			if err := SendPushover(context.Background(), c.Pushover, a); err != nil {
				s.log.Error("Pushover alert", "err", err)
			} else {
				s.log.Info("Pushover alert sent")
			}
		}()
	}
	if c.Voice {
		s.alert.voice.Lock()
		err := speak(a.Speech)
		s.alert.voice.Unlock()
		if err != nil {
			s.log.Error("voice alert", "err", err)
		}
	}
}

// TestAlert sends a test through one channel, "voice" or "pushover", and
// waits for the result.
func (s *Station) TestAlert(ctx context.Context, what string, c config.Alerts) error {
	a := Alert{Title: "SliceAPRS test", Text: "Alerts from SliceAPRS are working.",
		Speech: "This is a test of the Slice A P R S alerts."}
	switch what {
	case "voice":
		s.alert.voice.Lock()
		defer s.alert.voice.Unlock()
		return speak(a.Speech)
	case "pushover":
		if c.Pushover.UserKey == "" || c.Pushover.Token == "" {
			return fmt.Errorf("enter your user key and API token first")
		}
		return SendPushover(ctx, c.Pushover, a)
	}
	return fmt.Errorf("unknown test %q", what)
}

// OpeningAlert words an alert for an opening that has reached level.
func OpeningAlert(level string, o track.Opening) Alert {
	word := "enhanced"
	if level == track.LevelOpen {
		word = "open"
	}
	n := len(o.Stations)
	text := fmt.Sprintf("toward %s · %d station%s beyond normal", o.Direction, n, plural(n))
	speech := "Two metres is " + word
	switch o.Direction {
	case "":
	case "several directions":
		speech += " in several directions"
	default:
		speech += " toward the " + compassWords(o.Direction)
	}
	speech += "."
	if o.MaxCall != "" {
		text += fmt.Sprintf(" · farthest %s %.0f km", o.MaxCall, o.MaxKm)
		if o.NormalKm > 0 {
			text += fmt.Sprintf(" (%.1f× normal)", o.MaxKm/o.NormalKm)
		}
		speech += fmt.Sprintf(" Farthest station %s, at %.0f kilometres.", spellCall(o.MaxCall), o.MaxKm)
	}
	text += " · since " + o.Start.UTC().Format("15:04") + " UTC"
	return Alert{Title: "2 m is " + word, Text: text, Speech: speech}
}

func plural(n int) string {
	if n == 1 {
		return ""
	}
	return "s"
}

// compassWords turns "NNE" into "north north east".
func compassWords(c string) string {
	w := map[rune]string{'N': "north", 'E': "east", 'S': "south", 'W': "west"}
	var out []string
	for _, r := range c {
		if s, ok := w[r]; ok {
			out = append(out, s)
		}
	}
	return strings.Join(out, " ")
}

// spellCall spaces out a callsign so it is read letter by letter:
// "VE3ABC-9" becomes "V E 3 A B C dash 9". Anything else is dropped.
func spellCall(call string) string {
	var out []string
	for _, r := range strings.ToUpper(call) {
		switch {
		case r == '-':
			out = append(out, "dash")
		case r >= 'A' && r <= 'Z', r >= '0' && r <= '9':
			out = append(out, string(r))
		}
	}
	return strings.Join(out, " ")
}

var pushClient = &http.Client{Timeout: 20 * time.Second}

// SendPushover sends a to the user's devices through pushover.net.
func SendPushover(ctx context.Context, p config.Pushover, a Alert) error {
	v := url.Values{
		"token":    {p.Token},
		"user":     {p.UserKey},
		"title":    {a.Title},
		"message":  {a.Text},
		"priority": {fmt.Sprint(p.Priority)},
	}
	req, err := http.NewRequestWithContext(ctx, "POST", pushoverURL, strings.NewReader(v.Encode()))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	resp, err := pushClient.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	var r struct {
		Status int      `json:"status"`
		Errors []string `json:"errors"`
	}
	json.NewDecoder(resp.Body).Decode(&r)
	if resp.StatusCode == http.StatusOK && r.Status == 1 {
		return nil
	}
	if len(r.Errors) > 0 {
		return fmt.Errorf("Pushover: %s", strings.Join(r.Errors, "; "))
	}
	return fmt.Errorf("Pushover: %s", resp.Status)
}

func speakErr(err error, out []byte) error {
	if msg := strings.TrimSpace(string(out)); msg != "" {
		return fmt.Errorf("%w: %s", err, msg)
	}
	return err
}
