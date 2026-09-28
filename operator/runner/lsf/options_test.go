package lsf

import (
	"reflect"
	"testing"
)

func TestSplitOptions(t *testing.T) {
	want := []string{"-q", "pdk.q", "-m", "host1 host2", "-R", "select[os==RHEL8] rusage[mem=2048]", "-n", "4"}
	got, err := splitOptions(`-q pdk.q -m 'host1 host2' -R "select[os==RHEL8] rusage[mem=2048]" -n 4`)
	if err != nil || !reflect.DeepEqual(got, want) {
		t.Fatalf("%q %v", got, err)
	}
	got, err = splitOptions(`-R 'select[name==$(touch /tmp/no)]'`)
	if err != nil || got[1] != "select[name==$(touch /tmp/no)]" {
		t.Fatalf("unexpected expansion: %q %v", got, err)
	}
	for _, bad := range []string{`-q "unfinished`, `-q trailing\`, `echo hello`, `-I`, `-K`, `-J replacement`, `-env all`, `-oo other.log`, `--`} {
		if _, err := splitOptions(bad); err == nil {
			t.Fatalf("accepted invalid options %q", bad)
		}
	}
}
