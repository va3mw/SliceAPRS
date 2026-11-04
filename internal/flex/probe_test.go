//go:build radio

package flex

import (
	"bufio"
	"fmt"
	"net"
	"os"
	"strings"
	"testing"
	"time"
)

// TestProbe connects as a plain (non-GUI) client and prints the radio's
// transverter, panadapter and stream statuses. Read-only.
func TestProbe(t *testing.T) {
	c, err := net.DialTimeout("tcp", os.Getenv("RADIO")+":4992", 5*time.Second)
	if err != nil {
		t.Skip(err)
	}
	defer c.Close()
	for i, cmd := range []string{"sub xvtr all", "sub pan all", "sub daxiq all", "sub slice all", "info"} {
		fmt.Fprintf(c, "C%d|%s\n", i+1, cmd)
	}
	c.SetReadDeadline(time.Now().Add(4 * time.Second))
	sc := bufio.NewScanner(c)
	for sc.Scan() {
		l := sc.Text()
		if strings.Contains(l, "xvtr") || strings.Contains(l, "display pan") || strings.Contains(l, "dax") || strings.HasPrefix(l, "R") || strings.Contains(l, "slice") {
			if len(l) > 400 {
				l = l[:400]
			}
			fmt.Println(l)
		}
	}
}
