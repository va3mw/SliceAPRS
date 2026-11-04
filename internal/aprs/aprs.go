// Package aprs parses the information field of APRS packets: plain and
// compressed positions, Mic-E, objects and items, status, weather and
// messages.
package aprs

import (
	"fmt"
	"math"
	"strconv"
	"strings"
)

// Packet types.
const (
	TypePosition   = "position"
	TypeMicE       = "mic-e"
	TypeObject     = "object"
	TypeItem       = "item"
	TypeStatus     = "status"
	TypeMessage    = "message"
	TypeWeather    = "weather"
	TypeThirdParty = "third-party"
	TypeTelemetry  = "telemetry"
	TypeOther      = "other"
)

// Packet is what the information field says.
type Packet struct {
	Type string `json:"type"`

	HasPos     bool    `json:"has_pos"`
	Lat        float64 `json:"lat,omitempty"`
	Lon        float64 `json:"lon,omitempty"`
	Ambiguity  int     `json:"ambiguity,omitempty"` // 0 exact .. 4 to the degree
	Compressed bool    `json:"compressed,omitempty"`
	// OwnPos is true when the position is the sender's own (not an
	// object's or item's).
	OwnPos bool   `json:"own_pos,omitempty"`
	Symbol string `json:"symbol,omitempty"` // table and code, e.g. "/>"

	HasCourse bool    `json:"has_course,omitempty"`
	Course    int     `json:"course,omitempty"` // degrees
	SpeedKt   float64 `json:"speed_kt,omitempty"`
	HasAlt    bool    `json:"has_alt,omitempty"`
	AltFt     float64 `json:"alt_ft,omitempty"`

	Timestamp string `json:"timestamp,omitempty"`
	Messaging bool   `json:"messaging,omitempty"`
	Name      string `json:"name,omitempty"` // object or item
	Killed    bool   `json:"killed,omitempty"`
	MicE      string `json:"mic_e,omitempty"` // Mic-E message, e.g. "En Route"

	Comment string   `json:"comment,omitempty"`
	Status  string   `json:"status,omitempty"`
	Message *Message `json:"message,omitempty"`
	Weather *Weather `json:"weather,omitempty"`
}

// Message is an APRS message, bulletin or acknowledgement.
type Message struct {
	To   string `json:"to"`
	Text string `json:"text"`
	ID   string `json:"id,omitempty"`
}

// Weather holds the fields present in a weather report; nil means absent.
type Weather struct {
	WindDir    *int     `json:"wind_dir,omitempty"` // degrees
	WindMPH    *float64 `json:"wind_mph,omitempty"` // sustained
	GustMPH    *float64 `json:"gust_mph,omitempty"` // peak in last 5 min
	TempF      *float64 `json:"temp_f,omitempty"`   // degrees F
	Rain1h     *float64 `json:"rain_1h,omitempty"`  // inches
	Rain24h    *float64 `json:"rain_24h,omitempty"` // inches
	RainMidn   *float64 `json:"rain_midnight,omitempty"`
	Humidity   *int     `json:"humidity,omitempty"`    // %
	PressureMB *float64 `json:"pressure_mb,omitempty"` // mbar
	Luminosity *int     `json:"luminosity,omitempty"`  // W/m²
}

// Parse decodes an information field. dest is the AX.25 destination
// callsign without SSID, which Mic-E uses for the latitude.
func Parse(info []byte, dest string) (*Packet, error) {
	if len(info) == 0 {
		return nil, fmt.Errorf("empty information field")
	}
	p := &Packet{}
	s := string(info)
	var err error
	switch info[0] {
	case '!', '=':
		p.Type, p.Messaging = TypePosition, info[0] == '='
		err = p.position(s[1:], true)
	case '/', '@':
		p.Type, p.Messaging = TypePosition, info[0] == '@'
		if len(s) < 8 {
			return nil, fmt.Errorf("short timestamped position")
		}
		p.Timestamp = s[1:8]
		err = p.position(s[8:], true)
	case ';':
		p.Type = TypeObject
		err = p.object(s)
	case ')':
		p.Type = TypeItem
		err = p.item(s)
	case '>':
		p.Type, p.Status = TypeStatus, strings.TrimSpace(s[1:])
	case ':':
		if len(s) < 11 || s[10] != ':' {
			return nil, fmt.Errorf("bad message")
		}
		p.Type = TypeMessage
		p.Message = parseMessage(s)
	case '_':
		p.Type = TypeWeather
		if len(s) < 9 {
			return nil, fmt.Errorf("short weather report")
		}
		p.Timestamp = s[1:9]
		p.Weather, p.Comment = parseWeather(s[9:], true)
	case '`', '\'', 0x1C, 0x1D:
		p.Type = TypeMicE
		err = p.micE(info, dest)
	case '}':
		p.Type = TypeThirdParty
		p.Comment = s[1:]
	case 'T':
		if strings.HasPrefix(s, "T#") {
			p.Type = TypeTelemetry
		} else {
			p.Type = TypeOther
		}
		p.Comment = s
	default:
		p.Type, p.Comment = TypeOther, s
	}
	if err != nil {
		return nil, err
	}
	return p, nil
}

func parseMessage(s string) *Message {
	m := &Message{To: strings.TrimSpace(s[1:10]), Text: s[11:]}
	// "text{12" carries a message number; "ack12" and "rej12" have their
	// own. Newer "{MM}AA" reply-ack: keep the first part.
	if i := strings.LastIndexByte(m.Text, '{'); i >= 0 {
		m.ID = strings.TrimRight(m.Text[i+1:], "}")
		if j := strings.IndexByte(m.ID, '}'); j >= 0 {
			m.ID = m.ID[:j]
		}
		m.Text = m.Text[:i]
	}
	return m
}

// position parses a plain or compressed position, the symbol and the data
// after it.
func (p *Packet) position(s string, own bool) error {
	if len(s) < 13 {
		return fmt.Errorf("short position")
	}
	p.OwnPos = own
	if s[0] >= '0' && s[0] <= '9' || s[0] == ' ' {
		return p.plain(s)
	}
	return p.compressed(s)
}

func (p *Packet) plain(s string) error {
	if len(s) < 19 {
		return fmt.Errorf("short position")
	}
	lat, amb, err := parseLat(s[0:8])
	if err != nil {
		return err
	}
	lon, err := parseLon(s[9:18], amb)
	if err != nil {
		return err
	}
	p.Lat, p.Lon, p.Ambiguity, p.HasPos = lat, lon, amb, true
	p.Symbol = string([]byte{s[8], s[18]})
	rest := s[19:]
	if s[18] == '_' { // weather station: wind first, then the other fields
		p.Weather, p.Comment = parseWeather(rest, false)
		return nil
	}
	p.extension(rest)
	return nil
}

// extension reads course/speed and altitude from the comment.
func (p *Packet) extension(rest string) {
	if len(rest) >= 7 && rest[3] == '/' && isDigits(rest[0:3]) && isDigits(rest[4:7]) {
		c, _ := strconv.Atoi(rest[0:3])
		sp, _ := strconv.Atoi(rest[4:7])
		if c <= 360 {
			p.HasCourse, p.Course, p.SpeedKt = true, c%360, float64(sp)
		}
		rest = rest[7:]
	}
	p.Comment = strings.TrimSpace(altitude(p, rest))
}

// altitude takes "/A=001234" (feet) out of a comment.
func altitude(p *Packet, c string) string {
	i := strings.Index(c, "/A=")
	if i < 0 || len(c) < i+9 {
		return c
	}
	v := c[i+3 : i+9]
	if n, err := strconv.Atoi(v); err == nil {
		p.HasAlt, p.AltFt = true, float64(n)
		return c[:i] + c[i+9:]
	}
	return c
}

func isDigits(s string) bool {
	for _, c := range s {
		if c < '0' || c > '9' {
			return false
		}
	}
	return s != ""
}

// parseLat reads "4903.50N", where trailing digits may be spaces for
// position ambiguity. The position is the centre of the ambiguous area.
func parseLat(s string) (float64, int, error) {
	if len(s) != 8 || s[4] != '.' || (s[7] != 'N' && s[7] != 'S') {
		return 0, 0, fmt.Errorf("bad latitude %q", s)
	}
	digits := []byte(s[0:4] + s[5:7])
	amb := 0
	for i := len(digits) - 1; i >= 0 && digits[i] == ' '; i-- {
		amb++
		digits[i] = '0'
	}
	for _, c := range digits {
		if c < '0' || c > '9' {
			return 0, 0, fmt.Errorf("bad latitude %q", s)
		}
	}
	if amb > 4 {
		return 0, 0, fmt.Errorf("bad latitude %q", s)
	}
	deg := float64((digits[0]-'0')*10 + digits[1] - '0')
	min := float64((digits[2]-'0')*10+digits[3]-'0') + float64((digits[4]-'0')*10+digits[5]-'0')/100
	v := deg + (min+ambHalf(amb))/60
	if amb == 4 {
		v = deg + 0.5
	}
	if v > 90 || min >= 60 {
		return 0, 0, fmt.Errorf("bad latitude %q", s)
	}
	if s[7] == 'S' {
		v = -v
	}
	return v, amb, nil
}

// ambHalf is half the ambiguous span in minutes.
func ambHalf(amb int) float64 {
	return [...]float64{0, 0.05, 0.5, 5, 30}[amb]
}

// parseLon reads "07201.75W"; amb comes from the latitude.
func parseLon(s string, amb int) (float64, error) {
	if len(s) != 9 || s[5] != '.' || (s[8] != 'E' && s[8] != 'W') {
		return 0, fmt.Errorf("bad longitude %q", s)
	}
	digits := []byte(s[0:5] + s[6:8])
	for i := range digits {
		if digits[i] == ' ' {
			digits[i] = '0'
		}
	}
	// The latitude sets the ambiguity; blank out the same digits here.
	for k := 0; k < amb && k < 4; k++ {
		digits[len(digits)-1-k] = '0'
	}
	if !isDigits(string(digits)) {
		return 0, fmt.Errorf("bad longitude %q", s)
	}
	n := func(b []byte) float64 { v, _ := strconv.Atoi(string(b)); return float64(v) }
	deg := n(digits[0:3])
	min := n(digits[3:5]) + n(digits[5:7])/100
	v := deg + (min+ambHalf(amb))/60
	if amb == 4 {
		v = deg + 0.5
	}
	if v > 180 || min >= 60 {
		return 0, fmt.Errorf("bad longitude %q", s)
	}
	if s[8] == 'W' {
		v = -v
	}
	return v, nil
}

// compressed reads "/YYYYXXXX$csT".
func (p *Packet) compressed(s string) error {
	tbl := s[0]
	if !(tbl == '/' || tbl == '\\' || tbl >= 'A' && tbl <= 'Z' || tbl >= 'a' && tbl <= 'j') {
		return fmt.Errorf("bad compressed position")
	}
	y, ok1 := base91(s[1:5])
	x, ok2 := base91(s[5:9])
	if !ok1 || !ok2 {
		return fmt.Errorf("bad compressed position")
	}
	p.Lat = 90 - float64(y)/380926
	p.Lon = -180 + float64(x)/190463
	p.HasPos, p.Compressed = true, true
	p.Symbol = string([]byte{tbl, s[9]})
	c, sp, t := s[10], s[11], s[12]
	switch {
	case c == ' ':
	case (t-33)&0x18 == 0x10 && c >= 33 && sp >= 33: // altitude in cs
		p.HasAlt = true
		p.AltFt = math.Pow(1.002, float64(int(c-33)*91+int(sp-33)))
	case c >= '!' && c <= 'z' && sp >= 33:
		p.HasCourse = true
		p.Course = int(c-33) * 4 % 360
		p.SpeedKt = math.Pow(1.08, float64(sp-33)) - 1
	}
	if p.Symbol[1] == '_' {
		p.Weather, p.Comment = parseWeather(s[13:], true)
		return nil
	}
	p.Comment = strings.TrimSpace(altitude(p, s[13:]))
	return nil
}

func base91(s string) (int, bool) {
	v := 0
	for i := 0; i < len(s); i++ {
		c := s[i]
		if c < 33 || c > 124 {
			return 0, false
		}
		v = v*91 + int(c-33)
	}
	return v, true
}

// object reads ";NAME_____*DDHHMMzposition".
func (p *Packet) object(s string) error {
	if len(s) < 18 {
		return fmt.Errorf("short object")
	}
	p.Name = strings.TrimRight(s[1:10], " ")
	p.Killed = s[10] == '_'
	if s[10] != '*' && s[10] != '_' {
		return fmt.Errorf("bad object")
	}
	p.Timestamp = s[11:18]
	return p.position(s[18:], false)
}

// item reads ")NAME!position" (name 3-9 characters).
func (p *Packet) item(s string) error {
	i := strings.IndexAny(s[1:], "!_")
	if i < 3 || i > 9 {
		return fmt.Errorf("bad item")
	}
	p.Name = s[1 : 1+i]
	p.Killed = s[1+i] == '_'
	return p.position(s[2+i:], false)
}

var micEMsgs = [8]string{"Emergency", "Priority", "Special", "Committed", "Returning", "In Service", "En Route", "Off Duty"}
var micECustom = [8]string{"Emergency", "Custom-6", "Custom-5", "Custom-4", "Custom-3", "Custom-2", "Custom-1", "Custom-0"}

// micE decodes a Mic-E position from the destination and info field.
func (p *Packet) micE(info []byte, dest string) error {
	if len(dest) != 6 || len(info) < 9 {
		return fmt.Errorf("bad Mic-E")
	}
	var digits [6]byte
	msg, custom := 0, false
	amb := 0
	for i := 0; i < 6; i++ {
		c := dest[i]
		switch {
		case c >= '0' && c <= '9':
			digits[i] = c - '0'
		case c >= 'A' && c <= 'J':
			digits[i] = c - 'A'
			if i < 3 {
				msg |= 4 >> i
				custom = true
			}
		case c >= 'P' && c <= 'Y':
			digits[i] = c - 'P'
			if i < 3 {
				msg |= 4 >> i
			}
		case c == 'K' || c == 'L' || c == 'Z':
			digits[i] = 0
			amb++
			if i < 3 && c != 'L' {
				msg |= 4 >> i
				custom = custom || c == 'K'
			}
		default:
			return fmt.Errorf("bad Mic-E destination %q", dest)
		}
	}
	if amb > 4 {
		return fmt.Errorf("bad Mic-E destination %q", dest)
	}
	south := !(dest[3] >= 'P' && dest[3] <= 'Z')
	offset := dest[4] >= 'P' && dest[4] <= 'Z'
	west := dest[5] >= 'P' && dest[5] <= 'Z'
	deg := float64(digits[0]*10 + digits[1])
	min := float64(digits[2]*10+digits[3]) + float64(digits[4]*10+digits[5])/100
	lat := deg + (min+ambHalf(amb))/60
	if lat > 90 || min >= 60 {
		return fmt.Errorf("bad Mic-E latitude")
	}
	if south {
		lat = -lat
	}

	d := int(info[1]) - 28
	if offset {
		d += 100
	}
	if d >= 180 && d <= 189 {
		d -= 80
	} else if d >= 190 && d <= 199 {
		d -= 190
	}
	m := int(info[2]) - 28
	if m >= 60 {
		m -= 60
	}
	h := int(info[3]) - 28
	if d < 0 || d > 179 || m < 0 || m > 59 || h < 0 || h > 99 {
		return fmt.Errorf("bad Mic-E longitude")
	}
	lon := float64(d) + (float64(m)+float64(h)/100+ambHalf(amb))/60
	if west {
		lon = -lon
	}
	p.Lat, p.Lon, p.Ambiguity, p.HasPos, p.OwnPos = lat, lon, amb, true, true

	sp := int(info[4]) - 28
	dc := int(info[5]) - 28
	se := int(info[6]) - 28
	speed := sp*10 + dc/10
	if speed >= 800 {
		speed -= 800
	}
	course := (dc%10)*100 + se
	if course >= 400 {
		course -= 400
	}
	if sp >= 0 && dc >= 0 && se >= 0 && course <= 360 {
		p.HasCourse, p.Course, p.SpeedKt = true, course%360, float64(speed)
	}
	p.Symbol = string([]byte{info[8], info[7]})
	if custom {
		p.MicE = micECustom[msg]
	} else {
		p.MicE = micEMsgs[msg]
	}
	rest := string(info[9:])
	// Altitude: three base-91 characters then '}', metres above -10 km.
	if len(rest) >= 4 && rest[3] == '}' {
		if v, ok := base91(rest[0:3]); ok {
			p.HasAlt, p.AltFt = true, (float64(v)-10000)*3.28084
			rest = rest[4:]
		}
	} else if i := strings.Index(rest, "}"); i >= 3 && i <= 4 {
		if v, ok := base91(rest[i-3 : i]); ok {
			p.HasAlt, p.AltFt = true, (float64(v)-10000)*3.28084
			rest = rest[:i-3] + rest[i+1:]
		}
	}
	p.Comment = strings.TrimSpace(rest)
	return nil
}

// parseWeather reads weather fields. Positionless reports start with
// "cDDDsSSS"; position reports with "DDD/SSS". It returns what remains
// as the comment.
func parseWeather(s string, positionless bool) (*Weather, string) {
	w := &Weather{}
	if !positionless && len(s) >= 7 && s[3] == '/' {
		if v, err := strconv.Atoi(s[0:3]); err == nil {
			w.WindDir = &v
		}
		if v, err := strconv.ParseFloat(s[4:7], 64); err == nil {
			w.WindMPH = &v
		}
		s = s[7:]
	}
	for len(s) > 0 {
		k := s[0]
		n := map[byte]int{'c': 3, 's': 3, 'g': 3, 't': 3, 'r': 3, 'p': 3, 'P': 3, 'h': 2, 'b': 5, 'L': 3, 'l': 3}[k]
		if n == 0 || len(s) < 1+n {
			break
		}
		f := strings.TrimSpace(s[1 : 1+n])
		s = s[1+n:]
		if f == "" || strings.Trim(f, ".") == "" {
			continue // field present but unknown ("...")
		}
		v, err := strconv.ParseFloat(f, 64)
		if err != nil {
			continue
		}
		switch k {
		case 'c':
			d := int(v)
			w.WindDir = &d
		case 's':
			w.WindMPH = &v
		case 'g':
			w.GustMPH = &v
		case 't':
			w.TempF = &v
		case 'r':
			x := v / 100
			w.Rain1h = &x
		case 'p':
			x := v / 100
			w.Rain24h = &x
		case 'P':
			x := v / 100
			w.RainMidn = &x
		case 'h':
			h := int(v)
			if h == 0 {
				h = 100
			}
			w.Humidity = &h
		case 'b':
			x := v / 10
			w.PressureMB = &x
		case 'L':
			l := int(v)
			w.Luminosity = &l
		case 'l':
			l := int(v) + 1000
			w.Luminosity = &l
		}
	}
	return w, strings.TrimSpace(s)
}

// Summary is a short human description, for the packet list.
func (p *Packet) Summary() string {
	var parts []string
	switch p.Type {
	case TypeObject, TypeItem:
		k := p.Type
		if p.Killed {
			k += " (killed)"
		}
		parts = append(parts, fmt.Sprintf("%s %s", k, p.Name))
	case TypeMicE:
		parts = append(parts, "Mic-E "+p.MicE)
	case TypeMessage:
		if p.Message != nil {
			s := fmt.Sprintf("message to %s: %s", p.Message.To, p.Message.Text)
			if p.Message.ID != "" {
				s += " {" + p.Message.ID + "}"
			}
			parts = append(parts, s)
		}
	case TypeStatus:
		parts = append(parts, "status: "+p.Status)
	default:
		parts = append(parts, p.Type)
	}
	if p.HasPos {
		parts = append(parts, fmt.Sprintf("%.4f %.4f", p.Lat, p.Lon))
		if p.Ambiguity > 0 {
			parts = append(parts, fmt.Sprintf("±%d", p.Ambiguity))
		}
	}
	if p.HasCourse && p.SpeedKt > 0 {
		parts = append(parts, fmt.Sprintf("%d° %.0f km/h", p.Course, p.SpeedKt*1.852))
	}
	if p.HasAlt {
		parts = append(parts, fmt.Sprintf("%.0f m", p.AltFt*0.3048))
	}
	if w := p.Weather; w != nil {
		if w.TempF != nil {
			parts = append(parts, fmt.Sprintf("%.1f °C", (*w.TempF-32)*5/9))
		}
		if w.WindMPH != nil {
			wd := ""
			if w.WindDir != nil {
				wd = fmt.Sprintf(" from %d°", *w.WindDir)
			}
			parts = append(parts, fmt.Sprintf("wind %.0f km/h%s", *w.WindMPH*1.609, wd))
		}
		if w.Humidity != nil {
			parts = append(parts, fmt.Sprintf("%d%%", *w.Humidity))
		}
		if w.PressureMB != nil {
			parts = append(parts, fmt.Sprintf("%.1f hPa", *w.PressureMB))
		}
	}
	if p.Comment != "" && p.Type != TypeOther && p.Type != TypeThirdParty && p.Type != TypeTelemetry {
		parts = append(parts, p.Comment)
	}
	return strings.Join(parts, " · ")
}
