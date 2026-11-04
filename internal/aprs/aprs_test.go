package aprs

import (
	"math"
	"testing"
)

func near(a, b float64) bool { return math.Abs(a-b) < 0.0005 }

func TestPositions(t *testing.T) {
	for _, c := range []struct {
		info, dest   string
		typ          string
		lat, lon     float64
		amb          int
		own          bool
		course       int
		speed        float64
		hasCourse    bool
		name, symbol string
	}{
		{"!4903.50N/07201.75W-Test 001234", "APRS", TypePosition, 49.058333, -72.029167, 0, true, 0, 0, false, "", "/-"},
		{"=4903.50N/07201.75W>088/036 hello", "APRS", TypePosition, 49.058333, -72.029167, 0, true, 88, 36, true, "", "/>"},
		{"@092345z4903.50N/07201.75W>", "APRS", TypePosition, 49.058333, -72.029167, 0, true, 0, 0, false, "", "/>"},
		{"!4903.  N/07201.  W-", "APRS", TypePosition, 49.058333 + 0.5/60 - 0.5/60*0, -72.025, 2, true, 0, 0, false, "", "/-"},
		{"!/5L!!<*e7>7P[", "APRS", TypePosition, 49.5, -72.75, 0, true, 88, 36.2, true, "", "/>"},
		{";LEADER   *092345z4903.50N/07201.75W>088/036", "APRS", TypeObject, 49.058333, -72.029167, 0, false, 88, 36, true, "LEADER", "/>"},
		{")AID #2!4903.50N/07201.75WA", "APRS", TypeItem, 49.058333, -72.029167, 0, false, 0, 0, false, "AID #2", "/A"},
	} {
		p, err := Parse([]byte(c.info), c.dest)
		if err != nil {
			t.Errorf("%s: %v", c.info, err)
			continue
		}
		if p.Type != c.typ || !p.HasPos || p.OwnPos != c.own || p.Ambiguity != c.amb || p.Name != c.name || p.Symbol != c.symbol {
			t.Errorf("%s: %+v", c.info, p)
		}
		if c.amb == 0 && (!near(p.Lat, c.lat) || !near(p.Lon, c.lon)) {
			t.Errorf("%s: position %.6f %.6f", c.info, p.Lat, p.Lon)
		}
		if p.HasCourse != c.hasCourse || p.Course != c.course || math.Abs(p.SpeedKt-c.speed) > 0.1 {
			t.Errorf("%s: course %v %d %.1f", c.info, p.HasCourse, p.Course, p.SpeedKt)
		}
	}
}

func TestAmbiguity(t *testing.T) {
	p, err := Parse([]byte("!4903.  N/07201.  W-"), "APRS")
	if err != nil {
		t.Fatal(err)
	}
	// Two digits blank: the centre of 49 03' .. 49 04'.
	if p.Ambiguity != 2 || !near(p.Lat, 49+3.5/60) || !near(p.Lon, -(72+1.5/60)) {
		t.Fatalf("%+v", p)
	}
}

func TestMicE(t *testing.T) {
	// Destination S32UPT: 33 25.04 N, longitude offset +100, west; message
	// bits 1,0,0 standard: "Returning". Info: '(' 12+100=112 deg, '_' 67-60=7
	// min, 'f' 74 hundredths: 112 07.74 W. Speed/course "Oj/": SP 'O'=51-28
	// =23, DC 'j'=106-28=78, SE '/'=47-28=19: speed 230+7=237, course
	// 800+19-400=... see below.
	p, err := Parse([]byte("`(_fn\"O>/comment"), "S32UPT")
	if err != nil {
		t.Fatal(err)
	}
	if p.Type != TypeMicE || !p.OwnPos || !near(p.Lat, 33+25.04/60) || !near(p.Lon, -(112+7.74/60)) {
		t.Fatalf("%+v", p)
	}
	if p.MicE != "Returning" || p.Symbol != "/>" {
		t.Fatalf("mic-e %q symbol %q", p.MicE, p.Symbol)
	}
	// SP 'n'=110-28=82, DC '"'=34-28=6, SE 'O'=79-28=51: speed 820+0=820-800
	// =20 kt, course 6*100+51=651-400=251.
	if !p.HasCourse || p.SpeedKt != 20 || p.Course != 251 {
		t.Fatalf("course %d speed %.0f", p.Course, p.SpeedKt)
	}
	if p.Comment != "comment" {
		t.Fatalf("comment %q", p.Comment)
	}
}

func TestMicEAltitude(t *testing.T) {
	// "\"4T}" is 10000+... altitude in base 91 before '}'.
	p, err := Parse([]byte("`(_fn\"O>/\"4T}"), "S32UPT")
	if err != nil {
		t.Fatal(err)
	}
	// '"'=1, '4'=19, 'T'=51: 1*8281+19*91+51 = 10061 m-10000 = 61 m.
	if !p.HasAlt || math.Abs(p.AltFt-61*3.28084) > 0.5 {
		t.Fatalf("alt %v %.1f", p.HasAlt, p.AltFt)
	}
}

func TestStatusMessage(t *testing.T) {
	p, _ := Parse([]byte(">Net tonight 2000"), "APRS")
	if p.Type != TypeStatus || p.Status != "Net tonight 2000" {
		t.Fatalf("%+v", p)
	}
	p, _ = Parse([]byte(":WU2Z     :Testing{003"), "APRS")
	if p.Type != TypeMessage || p.Message.To != "WU2Z" || p.Message.Text != "Testing" || p.Message.ID != "003" {
		t.Fatalf("%+v", p.Message)
	}
	p, _ = Parse([]byte(":BLN1     :Snow"), "APRS")
	if p.Type != TypeMessage || p.Message.To != "BLN1" {
		t.Fatalf("%+v", p.Message)
	}
	if _, err := Parse([]byte(":short"), "APRS"); err == nil {
		t.Fatal("bad message accepted")
	}
}

func TestWeather(t *testing.T) {
	p, err := Parse([]byte("_10090556c220s004g005t077r000p000P000h50b09900wRSW"), "APRS")
	if err != nil {
		t.Fatal(err)
	}
	w := p.Weather
	if p.Type != TypeWeather || *w.WindDir != 220 || *w.WindMPH != 4 || *w.GustMPH != 5 || *w.TempF != 77 || *w.Humidity != 50 || *w.PressureMB != 990 {
		t.Fatalf("%+v", w)
	}
	p, err = Parse([]byte("!4903.50N/07201.75W_220/004g005t-07h00b10132"), "APRS")
	if err != nil {
		t.Fatal(err)
	}
	w = p.Weather
	if !p.HasPos || *w.WindDir != 220 || *w.TempF != -7 || *w.Humidity != 100 || math.Abs(*w.PressureMB-1013.2) > 0.01 {
		t.Fatalf("%+v", w)
	}
}

func TestOthers(t *testing.T) {
	p, _ := Parse([]byte("}VE3ABC>APRS,TCPIP,VA3MW*:!4903.50N/07201.75W-"), "APRS")
	if p.Type != TypeThirdParty || p.HasPos {
		t.Fatalf("%+v", p)
	}
	p, _ = Parse([]byte("T#005,199,000,255,073,123,01101001"), "APRS")
	if p.Type != TypeTelemetry {
		t.Fatalf("%+v", p)
	}
	for _, bad := range []string{"!4903.50X/07201.75W-", "!9903.50N/07201.75W-", "`(_", ";SHORT"} {
		if _, err := Parse([]byte(bad), "S32UPT"); err == nil {
			t.Errorf("%q accepted", bad)
		}
	}
}
