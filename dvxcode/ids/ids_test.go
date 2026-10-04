package ids

import (
	"strings"
	"testing"
)

func TestFormats(t *testing.T) {
	db := New(map[string]uint32{"2e0lxy": 2351999})
	n, err := db.Load(strings.NewReader("RADIO_ID,CALLSIGN,FIRST_NAME\n2341234,M0ABC,Alice,x\n2341235\tG4XYZ\tBob\n2341236 m1aaa Carol\n2341237,M0ABC,Alice2\n"))
	if err != nil || n != 4 {
		t.Fatalf("n=%d err=%v", n, err)
	}
	if id, _ := db.ID("m0abc"); id != 2341234 {
		t.Fatalf("first ID should win: %d", id)
	}
	if e, ok := db.Callsign(2341235); !ok || e.Callsign != "G4XYZ" || e.Name != "Bob" {
		t.Fatalf("%+v", e)
	}
	if id, _ := db.ID("2E0LXY"); id != 2351999 {
		t.Fatal("override")
	}
}
